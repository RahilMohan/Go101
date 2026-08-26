package raft

import (
	"context"
	"time"
)

// replica owns all Raft state for one node and runs as a single
// goroutine (the actor pattern): messages, client requests, election
// timers, and heartbeats are serialized through one select loop, so
// no fine-grained locking of Raft state is needed.
type replica struct {
	n *Node

	state    State
	term     uint64
	votedFor string
	leaderID string

	log         []Entry // log[0] is a sentinel; real entries start at 1
	commitIndex uint64
	lastApplied uint64
	kv          map[string]string

	next  map[string]uint64
	match map[string]uint64

	votes map[string]bool
	pend  []pendingReq

	election Timer
	hbTimer  Timer
	hbC      <-chan time.Time
}

func (n *Node) run(ctx context.Context) {
	r := &replica{
		n:   n,
		log: []Entry{{}},
		kv:  map[string]string{},
	}
	defer func() {
		n.markStopped()
		r.failPending(ErrStopped)
	}()
	r.stepDown(0)
	r.election = n.cfg.clock.NewTimer(n.cfg.electionTimeout())
	defer r.election.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-n.tr.Recv():
			if !ok {
				return
			}
			r.handleMessage(m)
		case req := <-n.reqCh:
			r.handleRequest(req)
		case <-r.election.C():
			if r.state == Leader {
				r.election.Reset(n.cfg.electionTimeout())
				continue
			}
			r.startElection()
		case <-r.hbC:
			r.broadcastAppend()
			r.hbTimer.Reset(n.cfg.heartbeat)
		}
	}
}

func (r *replica) majority() int { return (len(r.n.peerIDs)+1)/2 + 1 }

func (r *replica) lastIndex() uint64 { return uint64(len(r.log) - 1) }

func (r *replica) lastTerm() uint64 { return r.log[len(r.log)-1].Term }

func (r *replica) publish() {
	r.n.publish(r.state, r.term, r.leaderID)
}

// stepDown reverts to follower status. term 0 means "initial boot".
func (r *replica) stepDown(term uint64) {
	if term > r.term {
		r.term = term
		r.votedFor = ""
	}
	r.state = Follower
	r.leaderID = ""
	r.stopHeartbeats()
	for i := range r.pend {
		r.reply(&r.pend[i], "", false, ErrNotLeader)
	}
	r.pend = nil
	r.publish()
}

func (r *replica) stopHeartbeats() {
	if r.hbTimer != nil {
		r.hbTimer.Stop()
		r.hbTimer = nil
		r.hbC = nil
	}
}

func (r *replica) startElection() {
	r.term++
	r.state = Candidate
	r.votedFor = r.n.id
	r.leaderID = ""
	r.votes = map[string]bool{r.n.id: true}
	r.publish()
	r.election.Reset(r.n.cfg.electionTimeout())
	m := Message{
		Type:         MsgRequestVote,
		From:         r.n.id,
		Term:         r.term,
		CandidateID:  r.n.id,
		LastLogIndex: r.lastIndex(),
		LastLogTerm:  r.lastTerm(),
	}
	r.broadcast(m)
	if len(r.votes) >= r.majority() {
		r.becomeLeader()
	}
}

func (r *replica) becomeLeader() {
	r.state = Leader
	r.leaderID = r.n.id
	next := r.lastIndex() + 1
	if r.next == nil {
		r.next = make(map[string]uint64, len(r.n.peerIDs))
	}
	if r.match == nil {
		r.match = make(map[string]uint64, len(r.n.peerIDs))
	}
	for _, p := range r.n.peerIDs {
		r.next[p] = next
		r.match[p] = 0
	}
	r.publish()
	r.stopHeartbeats()
	r.hbTimer = r.n.cfg.clock.NewTimer(r.n.cfg.heartbeat)
	r.hbC = r.hbTimer.C()
	r.broadcastAppend()
}

func (r *replica) broadcast(tmpl Message) {
	for _, p := range r.n.peerIDs {
		m := tmpl
		m.To = p
		r.send(m)
	}
}

func (r *replica) send(m Message) { r.n.tr.Send(m) }

func (r *replica) handleMessage(m Message) {
	if m.Term > r.term {
		r.stepDown(m.Term)
	}
	switch m.Type {
	case MsgRequestVote:
		r.handleRequestVote(m)
	case MsgRequestVoteResp:
		r.handleVoteResp(m)
	case MsgAppendEntries:
		r.handleAppendEntries(m)
	case MsgAppendEntriesResp:
		r.handleAppendResp(m)
	}
}

func (r *replica) handleRequestVote(m Message) {
	resp := Message{
		Type: MsgRequestVoteResp,
		From: r.n.id,
		To:   m.From,
		Term: r.term,
	}
	if m.Term < r.term {
		r.send(resp)
		return
	}
	upToDate := m.LastLogTerm > r.lastTerm() ||
		(m.LastLogTerm == r.lastTerm() && m.LastLogIndex >= r.lastIndex())
	canVote := r.votedFor == "" || r.votedFor == m.CandidateID
	if canVote && upToDate {
		r.votedFor = m.CandidateID
		r.election.Reset(r.n.cfg.electionTimeout())
		resp.VoteGranted = true
	}
	r.send(resp)
}

func (r *replica) handleVoteResp(m Message) {
	if r.state != Candidate || m.Term != r.term || !m.VoteGranted {
		return
	}
	r.votes[m.From] = true
	if len(r.votes) >= r.majority() {
		r.becomeLeader()
	}
}

func (r *replica) handleAppendEntries(m Message) {
	resp := Message{Type: MsgAppendEntriesResp, From: r.n.id, To: m.From, Term: r.term}
	if m.Term < r.term {
		r.send(resp)
		return
	}
	r.stepDown(m.Term)
	r.leaderID = m.From
	r.publish()
	r.election.Reset(r.n.cfg.electionTimeout())

	lastIdx := r.lastIndex()
	if m.PrevLogIndex > lastIdx || r.log[m.PrevLogIndex].Term != m.PrevLogTerm {
		resp.MatchIndex = r.conflictHint(m.PrevLogIndex)
		r.send(resp)
		return
	}
	for i, e := range m.Entries {
		idx := m.PrevLogIndex + 1 + uint64(i)
		if idx <= r.lastIndex() {
			if r.log[idx].Term == e.Term {
				continue
			}
			r.log = r.log[:idx]
		}
		r.log = append(r.log, m.Entries[i:]...)
		break
	}
	if m.LeaderCommit > r.commitIndex {
		r.commitIndex = min64(m.LeaderCommit, r.lastIndex())
		r.applyCommitted()
	}
	resp.Success = true
	resp.MatchIndex = m.PrevLogIndex + uint64(len(m.Entries))
	r.send(resp)
}

// conflictHint suggests where the leader should resume sending:
// just past our log if we are too short, else the first index of the
// conflicting term.
func (r *replica) conflictHint(prev uint64) uint64 {
	if prev > r.lastIndex() {
		return min64(r.lastIndex()+1, prev) // we are short; probe lower
	}
	ct := r.log[prev].Term
	i := prev
	for i > 1 && r.log[i-1].Term == ct {
		i--
	}
	return i
}

func (r *replica) handleAppendResp(m Message) {
	if r.state != Leader || m.Term != r.term {
		return
	}
	if !m.Success {
		hint := m.MatchIndex
		if hint < 1 {
			hint = 1
		}
		if hint > r.next[m.From] {
			hint = r.next[m.From]
		}
		r.next[m.From] = hint
		r.sendAppend(m.From)
		return
	}
	if m.MatchIndex > r.match[m.From] {
		r.match[m.From] = m.MatchIndex
	}
	if m.MatchIndex+1 > r.next[m.From] {
		r.next[m.From] = m.MatchIndex + 1
	}
	r.advanceCommit()
}

func (r *replica) advanceCommit() {
	for idx := r.lastIndex(); idx > r.commitIndex; idx-- {
		if r.log[idx].Term != r.term {
			break
		}
		count := 1
		for _, p := range r.n.peerIDs {
			if r.match[p] >= idx {
				count++
			}
		}
		if count >= r.majority() {
			r.commitIndex = idx
			r.applyCommitted()
			r.broadcastAppend()
			break
		}
	}
}

func (r *replica) applyCommitted() {
	for r.lastApplied < r.commitIndex {
		r.lastApplied++
		e := r.log[r.lastApplied]
		if e.Op == OpSet {
			r.kv[e.Key] = e.Value
		}
	}
	r.n.publishKV(r.kv)
	r.resolvePending(nil)
}

func (r *replica) broadcastAppend() {
	for _, p := range r.n.peerIDs {
		r.sendAppend(p)
	}
}

func (r *replica) sendAppend(peer string) {
	next := r.next[peer]
	if next < 1 {
		next = 1
	}
	prev := next - 1
	entries := make([]Entry, len(r.log[next:]))
	copy(entries, r.log[next:])
	r.send(Message{
		Type:         MsgAppendEntries,
		From:         r.n.id,
		To:           peer,
		Term:         r.term,
		PrevLogIndex: prev,
		PrevLogTerm:  r.log[prev].Term,
		LeaderCommit: r.commitIndex,
		Entries:      entries,
	})
}

func (r *replica) handleRequest(req *request) {
	if r.state != Leader {
		req.resp <- reqResult{err: ErrNotLeader}
		return
	}
	switch req.kind {
	case reqSet:
		e := Entry{
			Index: r.lastIndex() + 1,
			Term:  r.term,
			Op:    OpSet,
			Key:   req.key,
			Value: req.val,
		}
		r.log = append(r.log, e)
		r.pend = append(r.pend, pendingReq{idx: e.Index, kind: req.kind, ch: req.resp})
		r.broadcastAppend()
		r.advanceCommit()
	case reqGet:
		e := Entry{
			Index: r.lastIndex() + 1,
			Term:  r.term,
			Op:    OpRead,
			Key:   req.key,
		}
		r.log = append(r.log, e)
		r.pend = append(r.pend, pendingReq{idx: e.Index, kind: req.kind, key: req.key, ch: req.resp})
		r.broadcastAppend()
		r.advanceCommit()
	}
}

// resolvePending replies to every pending request whose log index is
// now committed. If reason is non-empty it fails all pending requests
// instead (used when leadership is lost).
func (r *replica) resolvePending(reason error) {
	kept := r.pend[:0]
	for _, p := range r.pend {
		if reason != nil {
			r.reply(&p, "", false, reason)
			continue
		}
		if p.idx <= r.commitIndex {
			if p.kind == reqGet {
				v, ok := r.kv[p.key]
				r.reply(&p, v, ok, nil)
			} else {
				r.reply(&p, "", true, nil)
			}
			continue
		}
		kept = append(kept, p)
	}
	r.pend = kept
}

func (r *replica) failPending(reason error) { r.resolvePending(reason) }

func (r *replica) reply(p *pendingReq, val string, ok bool, err error) {
	p.ch <- reqResult{val: val, ok: ok, err: err}
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
