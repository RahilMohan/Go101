# Go101 — Design Document

Date: 2026-08-24
Status: Approved

## Purpose

A 16-project curriculum that teaches Go from beginner to expert through
standalone, mostly-independent projects. The audience is an experienced
programmer new to Go; fundamentals of programming are assumed, Go idioms
are not.

## Design Principles

- **Stack evaluation first.** Each project's documentation opens with an
  evaluation of its tech stack and the core lessons that stack uniquely
  teaches, before any requirements or architecture.
- **Mostly standalone.** Projects share no code. Later projects may reuse
  patterns established earlier.
- **Full-spectrum coverage.** Core language, stdlib, concurrency, HTTP,
  testing/benchmarking, modules/packaging, reflection, cgo, performance
  profiling, distributed systems.
- **Self-contained HTML docs.** One styled HTML file per project plus an
  index. No build step, works offline from `file://`.
- **Learn by implementing.** Learner-facing directories contain stubs and
  failing tests only. Complete idiomatic reference solutions live in a
  separate `solution/` folder per project for study and comparison.

## Repository Layout

```
Go101/
├── AGENTS.md              # Agent workflow rules (this repo's conventions)
├── design.md              # This document — canonical design
├── docs/
│   ├── index.html         # Curriculum overview, links to all 16 project docs
│   └── NN-<slug>.html     # One doc per project (16 files)
└── projects/
    └── NN-<slug>/         # Per-project Go module
        ├── go.mod
        ├── *.go           # Stubs with TODO markers
        ├── *_test.go      # Failing tests defining the contract
        └── solution/      # Complete reference solution (own package)
```

## Curriculum

Difficulty tiers: Beginner (1–4), Intermediate (5–9), Advanced (10–12),
Expert (13–16).

| # | Project | Tech Stack Evaluated | Core Lessons |
|---|---------|---------------------|--------------|
| 1 | wordkit — wc-style text analyzer CLI | Pure stdlib (`os`, `bufio`, `flag`, `io`) | Program anatomy, packages, error values vs exceptions, io.Reader/io.Writer abstraction |
| 2 | taskline — TODO manager with JSON persistence | stdlib (`encoding/json`, `time`, `os`), zero deps | Structs/methods, slices vs maps tradeoffs, JSON tags, file I/O, zero-dependency philosophy |
| 3 | loglens — streaming log analyzer | stdlib (`regexp`, `bufio.Scanner`, errors) | Regex performance costs, streaming vs loading, error wrapping (%w), sentinel errors |
| 4 | moneylib — currency-safe money library | stdlib only; library shape (not app) | Table-driven tests, fuzzing, integer vs float precision, public API design, doc comments |
| 5 | gopherfetch — concurrent link checker | net/http, goroutines, sync, context | Goroutine lifecycle, worker pools, WaitGroup, context cancellation, rate limiting |
| 6 | chatwire — multi-client TCP chat server | net, channels, mutexes | Raw sockets, reader-per-connection, fan-out via channels, race detector, graceful shutdown |
| 7 | shorty — URL shortener REST API | net/http (1.22+ routing), middleware, SQLite via database/sql | Server idioms, middleware chains, SQL injection safety, connection pooling, HTTP testing |
| 8 | pubsubx — generic in-memory pub/sub library | Generics, channels, sync | Type parameters, buffered vs unbounded channels, backpressure, subscriber isolation |
| 9 | metricsd — metrics scraper exposing /metrics | sync/atomic, background loops, HTTP | Atomic operations, lock-free counters, time-series buffers, scrape scheduling |
| 10 | minikv — Redis-like KV store with TTL + WAL | Custom RESP protocol, encoding/binary | Binary protocols, write-ahead logs, fsync tradeoffs, TTL sweeps, eviction policies |
| 11 | tagforge — struct-tag config validator | reflect, strconv, custom tags | Reflection costs/rules, struct tag parsing, validation DSLs, when NOT to reflect |
| 12 | pipeline — DAG workflow orchestrator | Generics, errgroup, channels | DAG execution, cancellation propagation, errgroup patterns, deadlock avoidance |
| 13 | perfhunt — deliberately slow service to optimize | pprof, benchmarks, GODEBUG | CPU/heap/block profiling, allocation reduction, GC tuning, benchmark methodology |
| 14 | fastthumb — image thumbnailer with cgo | cgo, libvips binding, unsafe | cgo call overhead, pointer passing across boundary, build tags, cross-compilation |
| 15 | raftling — mini Raft-replicated KV cluster | Networking, consensus, module publishing | Leader election, log replication, RPC timeouts, semver, publishing modules, CI |
| 16 | gopherpilot — TUI coding agent (capstone) | Bubble Tea-style TUI, streaming LLM APIs, SSE | Streaming JSON parsing, goroutine-driven tool-execution loop, conversation state machine, API key handling via env vars; exercises nearly every prior lesson |

## Documentation Format

Each `docs/NN-<slug>.html` contains, in order:

1. **Stack evaluation** — why this stack, rejected alternatives, which
   lessons the stack uniquely teaches.
2. **Requirements** — functional and non-functional, derived from the tests.
3. **Architecture decisions** — component boundaries, data flow, key
   trade-offs, with rationale.
4. **Concept walkthroughs** — the Go concepts the learner will apply,
   explained against the project's own code shapes.
5. **Testing strategy** — what the provided tests assert and why.
6. **"Go in the Wild" sidebar** — real production systems using this
   project's stack (e.g., Docker/Kubernetes for containers, Terraform for
   infra-as-code, fintech backends for correctness-focused services),
   connecting each project to where Go is used in industry.

### Animation & Visual Style

- Primary animation source: **NudaUI** (nudaui.dev) — pure HTML/CSS,
  zero runtime/build, 1,000+ snippets, glassmorphism/neon/gradient
  aesthetic, prefers-reduced-motion built in.
- Fallback: Uiverse.io when a needed component is absent from NudaUI.
- Snippets are pasted inline so every HTML file stays fully
  self-contained (no CDN dependency, opens offline).
- Rejected: Flowbite (requires Tailwind + JS bundle), Animista
  (generator, not library), motion-components/Vivace (young ecosystems).

## Code Depth Policy

- `projects/NN-<slug>/` root: stub functions returning zero values with
  `// TODO:` markers, plus comprehensive `_test.go` files that fail until
  implemented. Tests are the executable specification.
- `projects/NN-<slug>/solution/`: complete idiomatic implementation in its
  own package, compilable and passing the same tests, for study.
- Secrets (LLM API keys for gopherpilot) are read from environment
  variables only; never committed.

## Tooling

- Go 1.22+ (required for enhanced http.ServeMux routing in shorty).
- Standard tools only: `gofmt`, `go vet`, `go test`. No external lint
  config; formatting is enforced by gofmt.
- Race detector required for all concurrency projects (5, 6, 8, 9, 10,
  12, 15, 16): `go test -race`.

## Agent Orchestration

Each project is built through a five-stage pipeline of specialized agents,
coordinated by an orchestrator (the top-level session). Agents never skip
gates; failed gates loop back to the producing agent with specific findings.

### Roles

| Role | Responsibility |
|------|----------------|
| **Orchestrator** | Extracts the project brief from `design.md`, dispatches agents, enforces gate order, resolves conflicting review findings, declares the project done |
| **Implementation subagent** | Writes stubs, failing `_test.go` files (the executable contract), then the `solution/` reference implementation; runs `gofmt`, `go vet`, `go test`, `-race` where required |
| **Review agent** | Post-implementation quality gate: idiomatic Go, test coverage of stated lessons, API naming, comment quality, solution passes all tests |
| **Documentation agent** | Writes `docs/NN-<slug>.html` from the approved design sections: stack evaluation first, requirements, architecture decisions, concept walkthroughs, testing strategy, "Go in the Wild" sidebar; applies inline NudaUI animation snippets; verifies the file renders standalone offline |
| **Adversarial review agent** | Red-teams both code and docs: hunts untested edge cases, race conditions, resource leaks, injection vectors, secret leakage; attacks docs for pedagogical gaps, contradictions with the code, broken claims, and accessibility regressions |

### Pipeline (per project)

```
Stage 1  Implement    Implementation subagent: stubs + tests, then solution/
                      until all tests pass (incl. -race for concurrency projects)
   │                  GATE: gofmt/vet/test clean
Stage 2  Review       Review agent audits code against the project's
                      core-lessons list and repo conventions
   │                  GATE: no blocking findings
Stage 3  Document     Documentation agent produces the HTML doc
   │                  GATE: renders from file://, zero network calls,
   │                       every required section present
Stage 4  Adversarial  Adversarial review agent attacks code + doc
   │                  GATE: no unresolved critical/high findings
Stage 5  Reconcile    Orchestrator routes findings back to the owning agent;
                      fixes re-enter the gate they failed. Loop terminates
                      when all four gates pass in one pass.
```

### Parallelization Rules

- Within one project: Stages are strictly sequential (docs describe final
  code shape; adversarial review needs both).
- Across projects: projects within the same difficulty tier may be built
  concurrently by separate implementation subagents once their tier's
  first project has established patterns. Tiers remain ordered
  (Beginner → Intermediate → Advanced → Expert) because later tiers reuse
  earlier patterns.
- Review and adversarial agents always receive fresh context: only the
  diff/files under audit plus the relevant `design.md` section — never the
  implementer's reasoning — to avoid anchoring.

### Finding Severity & Loop Policy

- **Critical/High** (correctness, race, leak, secret exposure, false doc
  claim): must be fixed; loop back to producer.
- **Medium** (idiom violations, missing lesson coverage, weak doc
  section): fix unless orchestrator records an explicit deferral reason
  in the project doc's changelog footer.
- **Low/nits**: batched; fixed opportunistically, never block a gate.

## Success Criteria

- All 16 projects have passing stub tests in their `solution/` folders.
- Every HTML doc renders correctly from `file://` with no network calls.
- A learner completing all 16 in order has touched every item in the
  full-spectrum list above at least once.
