package raft

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

const (
	testWait    = 5 * time.Second
	testPollGap = 5 * time.Millisecond
)

// eventually polls cond until it holds or the deadline expires. Tests
// never assume fixed sleeps: every assertion is bounded-time polling.
func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(testPollGap)
	}
	t.Fatalf("timed out after %v waiting for: %s", d, what)
}

// newCluster wires nodeIDs over a shared Hub; extra IDs get hub inboxes
// without becoming nodes (e.g. a ghost voter). Every node's transport is
// wrapped in an individually addressable FaultTransport.
func newCluster(t *testing.T, nodeIDs, extraIDs []string, opts ...Option) ([]*Node, *Hub, map[string]*FaultTransport) {
	t.Helper()
	hub := NewHub(append(append([]string{}, nodeIDs...), extraIDs...)...)
	t.Cleanup(hub.Close)
	nodes := make([]*Node, 0, len(nodeIDs))
	faults := make(map[string]*FaultTransport, len(nodeIDs))
	for _, id := range nodeIDs {
		ft := WrapTransport(hub.Transport(id), &Policy{})
		faults[id] = ft
		n := New(id, nodeIDs, ft, opts...)
		n.Start(context.Background())
		nodes = append(nodes, n)
	}
	return nodes, hub, faults
}

func leaders(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		if n.Status().State == Leader {
			out = append(out, n)
		}
	}
	return out
}

func soleLeader(nodes []*Node) *Node {
	ls := leaders(nodes)
	if len(ls) == 1 {
		return ls[0]
	}
	return nil
}

// mustSet sets k=v via whichever node is currently sole leader,
// retrying across leadership changes caused by injected faults.
func mustSet(t *testing.T, nodes []*Node, key, val string) {
	t.Helper()
	eventually(t, testWait, fmt.Sprintf("Set(%q=%q) committed", key, val), func() bool {
		l := soleLeader(nodes)
		if l == nil {
			return false
		}
		return l.Set(key, val) == nil
	})
}

func TestSingleNodeElectsItselfAndServesKV(t *testing.T) {
	nodes, _, _ := newCluster(t, []string{"only"}, nil)
	l := nodes[0]
	eventually(t, testWait, "single-node leader", func() bool {
		s := l.Status()
		return s.State == Leader && s.LeaderID == "only"
	})
	if err := l.Set("a", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := l.Get("a")
	if err != nil || !ok || v != "1" {
		t.Fatalf("Get = (%q,%v,%v), want (1,true,nil)", v, ok, err)
	}
}

func TestSingleLeaderAmongThree(t *testing.T) {
	nodes, _, _ := newCluster(t, []string{"n0", "n1", "n2"}, nil)
	var leader *Node
	eventually(t, testWait, "exactly one leader among three", func() bool {
		leader = soleLeader(nodes)
		return leader != nil
	})
	term := leader.Status().Term
	// Leadership must be stable: same sole leader, unchanged term.
	eventually(t, 500*time.Millisecond, "leader stable", func() bool {
		l := soleLeader(nodes)
		return l == leader && l.Status().Term == term
	})
	// Followers must agree on who the leader is.
	for _, f := range nodes {
		if f == leader {
			continue
		}
		id := leader.ID()
		eventually(t, 2*time.Second, "follower "+f.ID()+" acknowledges leader", func() bool {
			return f.Status().LeaderID == id && f.Status().Term >= term
		})
	}
}

func TestDuplicateVotesRejectedInSameTerm(t *testing.T) {
	nodes, hub, _ := newCluster(t, []string{"n0", "n1", "n2"}, []string{"ghost"})
	var leader *Node
	eventually(t, testWait, "leader elected", func() bool {
		leader = soleLeader(nodes)
		return leader != nil
	})
	term := leader.Status().Term

	ghostTr := hub.Transport("ghost")
	for _, f := range nodes {
		if f == leader {
			continue
		}
		ghostTr.Send(Message{
			Type:         MsgRequestVote,
			From:         "ghost",
			To:           f.ID(),
			Term:         term,
			CandidateID:  "ghost",
			LastLogIndex: 10000,
			LastLogTerm:  term + 10, // maximally up-to-date log
		})
	}

	denied, granted := 0, 0
	deadline := time.Now().Add(2 * time.Second)
	for denied+granted < len(nodes)-1 && time.Now().Before(deadline) {
		select {
		case m := <-ghostTr.Recv():
			if m.Type == MsgRequestVoteResp {
				if m.VoteGranted {
					granted++
				} else {
					denied++
				}
			}
		case <-time.After(testWait):
			t.Fatal("no vote responses from ghost candidate")
		}
	}
	if granted != 0 {
		t.Fatalf("followers granted %d votes to second candidate in term %d; each node votes at most once per term", granted, term)
	}
	if denied != len(nodes)-1 {
		t.Fatalf("got %d/%d responses", denied+granted, len(nodes)-1)
	}
}

func TestQuorumCommitAndReadYourWrites(t *testing.T) {
	nodes, _, _ := newCluster(t, []string{"n0", "n1", "n2"}, nil)
	var leader *Node
	eventually(t, testWait, "leader elected", func() bool {
		leader = soleLeader(nodes)
		return leader != nil
	})
	if err := leader.Set("color", "blue"); err != nil {
		t.Fatalf("Set on leader: %v", err)
	}
	v, ok, err := leader.Get("color")
	if err != nil || !ok || v != "blue" {
		t.Fatalf("Get = (%q,%v,%v), want (blue,true,nil)", v, ok, err)
	}
	var follower *Node
	for _, n := range nodes {
		if n != leader {
			follower = n
			break
		}
	}
	if _, _, err := follower.Get("color"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Get on follower = %v, want ErrNotLeader", err)
	}
}

func TestPartitionedMinorityUnavailableNoSplitBrain(t *testing.T) {
	nodes, _, faults := newCluster(
		t, []string{"n0", "n1", "n2"}, nil,
		WithTiming(defaultHeartbeat, defaultElectionMin, defaultElectionMax, 800*time.Millisecond),
	)
	var oldL *Node
	eventually(t, testWait, "initial leader", func() bool {
		oldL = soleLeader(nodes)
		return oldL != nil
	})
	oldTerm := oldL.Status().Term

	// Partition: isolate the current leader entirely.
	faults[oldL.ID()].Mutate(func(p *Policy) { p.DropAll = true })

	// Majority side elects a new leader with a higher term.
	var newL *Node
	var rest []*Node
	for _, n := range nodes {
		if n != oldL {
			rest = append(rest, n)
		}
	}
	eventually(t, testWait, "majority side elected replacement leader", func() bool {
		newL = soleLeader(rest)
		return newL != nil && newL.Status().Term > oldTerm
	})

	// Old (minority-side) leader must be unavailable for both writes
	// and reads: no quorum, so no stale serving — no split brain.
	if err := oldL.Set("x", "y"); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("Set on isolated leader = %v, want ErrNoQuorum", err)
	}
	if _, _, err := oldL.Get("x"); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("Get on isolated leader = %v, want ErrNoQuorum", err)
	}

	// The quorum side keeps working.
	if err := newL.Set("during", "partition"); err != nil {
		t.Fatalf("Set on new leader: %v", err)
	}
	v, ok, err := newL.Get("during")
	if err != nil || !ok || v != "partition" {
		t.Fatalf("Get on new leader = (%q,%v,%v)", v, ok, err)
	}

	// Heal the partition: old leader steps down and catches up.
	faults[oldL.ID()].Mutate(func(p *Policy) { p.DropAll = false })
	newTerm := newL.Status().Term
	eventually(t, testWait, "old leader stepped down", func() bool {
		s := oldL.Status()
		return s.State == Follower && s.Term >= newTerm
	})
	eventually(t, testWait, "old leader caught up on replicated state", func() bool {
		return oldL.Snapshot()["during"] == "partition"
	})
	eventually(t, testWait, "cluster has exactly one leader again", func() bool {
		return soleLeader(nodes) != nil && len(leaders(nodes)) == 1
	})
}

func TestDropsAndReorderDelayButPreserveCommitOrder(t *testing.T) {
	nodeIDs := []string{"n0", "n1", "n2"}
	nodes, _, faults := newCluster(t, nodeIDs, nil)
	for i, id := range nodeIDs {
		id := id
		faults[id].Mutate(func(p *Policy) {
			p.DropProb = 0.35
			p.Reorder = true
			p.Rand = rand.New(rand.NewSource(int64(42 + i))) // deterministic per-link stream
		})
	}
	want := map[string]string{}
	for i := 0; i < 8; i++ {
		k, v := fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)
		mustSet(t, nodes, k, v) // sequential: each commit precedes the next Set
		want[k] = v
	}
	// All replicas must converge to exactly the expected state; exact
	// equality proves every entry was applied, in order, despite drops
	// and reordering.
	eventually(t, testWait, "all replicas converge to committed sequence", func() bool {
		for _, n := range nodes {
			got := n.Snapshot()
			if len(got) != len(want) {
				return false
			}
			for k, v := range want {
				if got[k] != v {
					return false
				}
			}
		}
		return true
	})
}

func TestRejoinedFollowerCatchesUp(t *testing.T) {
	nodes, _, faults := newCluster(t, []string{"n0", "n1", "n2"}, nil)
	var leader *Node
	eventually(t, testWait, "leader elected", func() bool {
		leader = soleLeader(nodes)
		return leader != nil
	})
	var lagging *Node
	for _, n := range nodes {
		if n != leader {
			lagging = n
			break
		}
	}
	faults[lagging.ID()].Mutate(func(p *Policy) { p.DropAll = true })
	want := map[string]string{}
	for _, kv := range [][2]string{{"k1", "v1"}, {"k2", "v2"}, {"k3", "v3"}} {
		mustSet(t, nodes, kv[0], kv[1])
		want[kv[0]] = kv[1]
	}
	faults[lagging.ID()].Mutate(func(p *Policy) { p.DropAll = false })
	eventually(t, testWait, "rejoined follower caught up", func() bool {
		got := lagging.Snapshot()
		if len(got) != len(want) {
			return false
		}
		for k, v := range want {
			if got[k] != v {
				return false
			}
		}
		return true
	})
}

// F1: toggling DropProb at runtime without a Rand must fall back to a
// deterministic internal source — never panic inside the library-owned
// pump goroutine, which would kill the whole process.
func TestDropProbWithoutRandDoesNotKillProcess(t *testing.T) {
	nodeIDs := []string{"n0", "n1", "n2"}
	nodes, _, faults := newCluster(t, nodeIDs, nil)
	for _, id := range nodeIDs {
		id := id
		faults[id].Mutate(func(p *Policy) { p.DropProb = 0.4 }) // Rand deliberately nil
	}
	mustSet(t, nodes, "survives", "misconfig")
	eventually(t, testWait, "all replicas converge despite nil-Rand drops", func() bool {
		for _, n := range nodes {
			if n.Snapshot()["survives"] != "misconfig" {
				return false
			}
		}
		return true
	})
}

// F2: a leader receiving an AppendEntries rejection bearing a higher
// term must step down immediately (Raft §5.1).
func TestLeaderStepsDownOnHigherTermAppendResp(t *testing.T) {
	nodes, hub, _ := newCluster(t, []string{"n0", "n1", "n2"}, []string{"ghost"})
	var leader *Node
	eventually(t, testWait, "leader elected", func() bool {
		leader = soleLeader(nodes)
		return leader != nil
	})
	term := leader.Status().Term
	hub.Transport("ghost").Send(Message{
		Type:    MsgAppendEntriesResp,
		From:    "ghost",
		To:      leader.ID(),
		Term:    term + 7,
		Success: false,
	})
	eventually(t, testWait, "leader stepped down on higher-term rejection", func() bool {
		s := leader.Status()
		return s.State == Follower && s.Term >= term+7
	})
}

// LOW-1: WithTiming must normalize inverted election bounds instead of
// producing a negative jitter span (rand.Int63n panic), and equal
// bounds must not panic either.
func TestWithTimingNormalizesInvertedElectionBounds(t *testing.T) {
	cg := config{
		heartbeat:   defaultHeartbeat,
		electionMin: defaultElectionMin,
		electionMax: defaultElectionMax,
		rpcTimeout:  defaultRPCTimeout,
	}
	WithTiming(5*time.Millisecond, 400*time.Millisecond, 300*time.Millisecond, time.Second)(&cg)
	if cg.heartbeat != 5*time.Millisecond || cg.rpcTimeout != time.Second {
		t.Fatalf("WithTiming ignored overrides: hb=%v rpc=%v", cg.heartbeat, cg.rpcTimeout)
	}
	if cg.electionMin > cg.electionMax {
		t.Fatalf("inverted bounds not normalized: min=%v max=%v", cg.electionMin, cg.electionMax)
	}
	if cg.electionMin != 300*time.Millisecond || cg.electionMax != 400*time.Millisecond {
		t.Fatalf("bounds = min=%v max=%v, want swapped 300ms/400ms", cg.electionMin, cg.electionMax)
	}
	nodes, _, _ := newCluster(
		t, []string{"only"}, nil,
		WithTiming(defaultHeartbeat, 120*time.Millisecond, 120*time.Millisecond, time.Second),
	)
	eventually(t, testWait, "leader elected with equal election bounds", func() bool {
		return nodes[0].Status().State == Leader
	})
}

// LOW-2: a full request channel is local backpressure and must be
// reported as ErrBusy, not ErrNoQuorum.
func TestFullRequestChannelReturnsErrBusy(t *testing.T) {
	hub := NewHub("solo")
	t.Cleanup(hub.Close)
	n := New("solo", []string{"solo"}, hub.Transport("solo"),
		WithTiming(defaultHeartbeat, defaultElectionMin, defaultElectionMax, 80*time.Millisecond))
	for i := 0; i < cap(n.reqCh); i++ {
		n.reqCh <- &request{resp: make(chan reqResult, 1)}
	}
	err := n.Set("k", "v")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Set with full reqCh = %v, want ErrBusy", err)
	}
}
