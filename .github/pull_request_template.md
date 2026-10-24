# Pull Request Checklist

This project follows the **Go101 Agent Orchestration Pipeline** (see `AGENTS.md`).  
All PRs must pass the five gates before merge.

---

## 🚦 Gate Status

| Gate | Description | Status |
|------|-------------|--------|
| **1. Implement** | `gofmt` / `go vet` / `go test -race` clean on `solution/` | ☐ |
| **2. Review** | Code reviewed against project's core lessons & repo conventions | ☐ |
| **3. Document** | `docs/NN-<slug>.html` updated, renders offline from `file://` | ☐ |
| **4. Adversarial** | Fleet review: Correctness Hunter / Doc Skeptic / Abuse Attacker | ☐ |
| **5. Reconcile** | All findings fixed or deferred with rationale in doc changelog | ☐ |

---

## 📋 PR Requirements

- [ ] **Branch naming**: `feat/NN-slug`, `fix/NN-slug`, `docs/NN-slug`, or `chore/...`
- [ ] **Single project scope**: Changes limited to one `NN-<slug>` project (per `design.md` parallelization rules)
- [ ] **Tests pass**: `go test -race ./...` in the project's `solution/` directory
- [ ] **No secrets**: API keys, tokens, credentials only via environment variables
- [ ] **Documentation updated**: If code changed, corresponding `docs/NN-<slug>.html` updated
- [ ] **Attribution trailer**: Commits include `Co-Authored-By: opencode <noreply@opencode.ai>`

---

## 🔍 Review Focus Areas

### Code (Implementation Agent)
- [ ] Idiomatic Go (error values, not exceptions; interfaces over generics where appropriate)
- [ ] Test coverage matches stated core lessons in `design.md`
- [ ] Race detector clean for concurrency projects (5, 6, 8, 9, 10, 12, 15, 16)
- [ ] Solution in `solution/` package only — stubs unchanged

### Documentation (Documentation Agent)
- [ ] All 6 sections present: Stack Evaluation → Requirements → Architecture → Concept Walkthroughs → Testing Strategy → Go in the Wild
- [ ] Self-contained HTML: zero network calls, renders from `file://`
- [ ] NudaUI animation snippets pasted inline (no CDN)
- [ ] Tier badge matches `design.md` (Beginner=green, Intermediate=yellow, Advanced=purple, Expert=red/Capstone)

### Security (Adversarial Fleet)
- [ ] No injection vectors (SQL, command, path traversal)
- [ ] No resource exhaustion (unbounded channels, goroutine leaks, memory bombs)
- [ ] No secret leakage in code or docs
- [ ] Input validation at boundaries

---

## 📝 Additional Notes

<!-- Add any context reviewers should know -->