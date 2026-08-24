# Agent Instructions

## Canonical Design
All project specs, curriculum, doc format, and the agent-orchestration
pipeline live in `design.md`. Read the relevant section before starting
any task. Do not restate it here.

## Package Manager / Toolchain
Go 1.22+. Standard toolchain only — no external linters.
Format with `gofmt -w`, vet with `go vet ./...`.

## File-Scoped Commands
| Task | Command |
|------|---------|
| Test one project | `go test ./projects/NN-slug/...` |
| Race check | `go test -race ./projects/NN-slug/...` |
| Vet one project | `go vet ./projects/NN-slug/...` |
| Format | `gofmt -w <file>` |

## Repo Layout
```
docs/NN-<slug>.html      # per-project docs (self-contained HTML)
projects/NN-<slug>/      # stubs + failing tests
projects/NN-<slug>/solution/  # reference solution, own package
```

## Key Conventions
- Stubs return zero values with `// TODO:` markers; `_test.go` files are
  the executable specification and must fail until implemented.
- Solutions go in `solution/` subpackage only — never edit stubs to cheat
  tests.
- Secrets (API keys) come from env vars only; never hardcode or commit.
- Docs must render offline from `file://`: no CDN links; animation
  snippets pasted inline per `design.md` → Documentation Format.

## Agent Orchestration
Follow the five-stage pipeline in `design.md` → Agent Orchestration:
Implement → Review → Document → Adversarial → Reconcile. Gates are
mandatory; route failed-gate findings back to the producing agent with
specific findings. Review/adversarial agents get fresh context (files +
design excerpt only), never the implementer's reasoning.

## Commit Attribution
AI commits MUST include:
```
Co-Authored-By: opencode <noreply@opencode.ai>
```
