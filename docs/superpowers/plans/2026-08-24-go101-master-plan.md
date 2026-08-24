# Go101 Master Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete Go101 repo — 16 learning projects (stubs + failing tests + reference solutions) and 16 self-contained HTML docs — via the five-stage agent pipeline in `design.md`.

**Architecture:** Phase-ordered build following the difficulty tiers. Each project runs through Implement → Review → Document → Adversarial → Reconcile. This document is the master plan; each project's bite-sized task plan is generated at its phase start by the orchestrator from `design.md` → Curriculum (its row) + Documentation Format + Code Depth Policy.

**Tech Stack:** Go 1.22+ stdlib toolchain only (`gofmt`, `go vet`, `go test`, `-race`). Docs are hand-written HTML with inline NudaUI animation snippets.

**Spec:** `design.md` (canonical). `AGENTS.md` (conventions). Both travel with every dispatch.

## Global Constraints

- Go version floor: **1.22** (`shorty` requires enhanced `http.ServeMux` routing).
- Stubs return zero values with `// TODO:` markers; tests fail until implemented.
- Solutions live only in `projects/NN-slug/solution/` (own package); never edit stubs to pass.
- Race detector mandatory for concurrency projects: 5, 6, 8, 9, 10, 12, 15, 16.
- Secrets from env vars only; never hardcoded or committed (applies to #16 gopherpilot).
- Docs must render offline from `file://`: zero CDN/network references; animation snippets pasted inline; every doc contains all six sections from `design.md` → Documentation Format, stack evaluation first.
- All commits include `Co-Authored-By: opencode <noreply@opencode.ai>`.
- Review/adversarial agents receive fresh context only: files under audit + relevant `design.md` excerpt. Never implementer reasoning.

---

## Phase 0: Repository Bootstrap

**Files:**
- Create: `.gitignore` (Go: binaries, `*.test`, `.env`)
- Create: `docs/index.html` (curriculum overview page linking 16 placeholder cards)
- Create: `go.work` (workspace spanning all `projects/*` modules)
- Init: git repository

- [ ] **Step 1:** `git init`; write `.gitignore`
- [ ] **Step 2:** Write `docs/index.html` — self-contained, NudaUI-styled, 16 project cards (title, tier, one-line lesson summary), links to `NN-<slug>.html`
- [ ] **Step 3:** Verify `file://` render offline (no network calls)
- [ ] **Step 4:** Commit: `chore: bootstrap Go101 repo`

**Gate:** index renders offline; git history clean.

---

## Phase 1: Beginner Tier (Projects 1–4)

Projects: wordkit, taskline, loglens, moneylib. No race requirement. Sequential within tier for first project, then parallelizable after wordkit establishes patterns.

Per project (repeat pipeline):

- [ ] **Step 1 (Orchestrator):** Generate the project's bite-sized task plan from its curriculum row; define public API signatures (Consumes/Produces blocks) before any dispatch.
- [ ] **Step 2 (Implementation subagent):** Scaffold `go.mod` (module `go101.dev/projects/NN-slug`), write `_test.go` contract files (failing), stubs, then `solution/` implementation until green. Run `gofmt -w`, `go vet ./...`, `go test ./...`.
- [ ] **Step 3 (Gate):** `go test ./projects/NN-slug/...` passes; stubs still fail without solution (verify stub-only build fails tests); vet clean.
- [ ] **Step 4 (Review subagent):** Fresh-context audit vs core lessons + conventions. Blocking findings → back to Step 2.
- [ ] **Step 5 (Documentation subagent):** Write `docs/NN-<slug>.html` per six-section format; paste NudaUI snippets inline; add "Go in the Wild" sidebar; update index.html card link.
- [ ] **Step 6 (Adversarial subagent):** Attack code (edge cases, leaks) + doc (false claims, gaps, a11y). Critical/high findings loop back; low nits batched.
- [ ] **Step 7 (Orchestrator):** Reconcile until one clean pass through Steps 3–6. Commit per project: `feat(NN): <slug> — stubs, solution, docs`.

**Gate:** all four projects green; docs offline-render; moneylib additionally demonstrates fuzzing (`go test -fuzz`) and table-driven tests in its test suite.

---

## Phase 2: Intermediate Tier (Projects 5–9)

Projects: gopherfetch, chatwire, shorty, pubsubx, metricsd. Race detector required on all five. shorty introduces external dep (SQLite driver via `database/sql`) — first non-zero-dep module.

- [ ] **Step 0 (Tier setup):** Orchestrator verifies SQLite driver choice works with pure-Go builds (modernc.org/sqlite or mattn/go-sqlite3 with cgo note in doc) and records decision in shorty's stack-evaluation section.
- [ ] **Steps 1–7:** Same pipeline as Phase 1, plus:
  - Every test run uses `-race`.
  - Concurrency projects' tests must include a determinism strategy (timeouts, channels-as-signals) so CI isn't flaky.
  - shorty's HTTP handler tests use `httptest` exclusively (no live ports).

**Gate:** five projects green with `-race`; no flaky tests across 3 consecutive `go test -count=3 -race ./projects/{05..09}-*/...` runs.

---

## Phase 3: Advanced Tier (Projects 10–12)

Projects: minikv, tagforge, pipeline. minikv needs WAL crash-consistency tests (kill/restart simulation); pipeline needs deadlock-freedom proof via timeout-bounded integration test.

- [ ] **Steps 1–7:** Same pipeline as Phase 2, plus:
  - minikv: tests cover TTL expiry sweep, WAL replay after simulated crash (`os.Remove` mid-sequence), eviction policy boundaries.
  - tagforge: reflection paths benchmarked (`testing.B`) with results quoted in the doc's concept walkthrough.
  - pipeline: errgroup cancellation propagates within bounded time (asserted in test, not documented hope).

**Gate:** three projects green with `-race`; adversarial review specifically signed off on WAL/cancellation correctness claims in docs.

---

## Phase 4: Expert Tier (Projects 13–16)

Projects: perfhunt, fastthumb, raftling, gopherpilot. Highest risk tier — capstone last.

- [ ] **Step 0 (Tier setup):** Orchestrator confirms cgo toolchain availability for fastthumb (libvips headers) and documents fallback (pure-Go `image` package comparison mode) if unavailable. Decision recorded before dispatch.
- [ ] **Steps 1–7:** Same pipeline as Phase 3, plus:
  - perfhunt ships with an intentionally slow baseline commit; optimization work happens in `solution/` with before/after pprof profiles referenced in the doc.
  - raftling includes a deterministic fault-injection harness (message drop/delay via channel wrapper) — no sleep-based tests.
  - gopherpilot: API key strictly via env var; LLM client behind an interface so tests run against a scripted fake provider; SSE/streaming parser tested against recorded chunk fixtures. Doc includes setup section for obtaining keys.
  - gopherpilot gets a final end-to-end adversarial pass: prompt-injection surface review of the tool-execution loop.

**Gate:** four projects green with `-race`; capstone demo command in doc verified working against fake provider (no real API call needed for CI).

---

## Phase 5: Final Integration & Sweep

- [ ] **Step 1:** Full-suite verification:
  ```bash
  gofmt -l .            # expect empty
  go vet ./...
  go test -race ./...
  ```
- [ ] **Step 2:** Offline audit — grep all HTML for `https?://` asset references (external links in prose are fine; loaded resources are not). Expect zero loaded resources.
- [ ] **Step 3:** Cross-doc consistency sweep — adversarial agent checks curriculum coverage claim in `design.md` → Success Criteria: every full-spectrum item appears in ≥1 project's lessons/docs.
- [ ] **Step 4:** Update `docs/index.html` cards to final state; fix any dead links.
- [ ] **Step 5:** Final commit: `feat: complete Go101 curriculum`.

**Gate:** everything above green. Done.

---

## Execution Notes for the Orchestrator

- Dispatch one implementation subagent per project; never two agents editing one project concurrently.
- Generate each project's bite-sized plan immediately before its dispatch so it can reference the previous project's established patterns (Interfaces blocks).
- Batch low-severity findings per tier, not per project, to reduce loop churn.
- If a gate fails twice with the same finding, escalate to the human rather than looping again.
