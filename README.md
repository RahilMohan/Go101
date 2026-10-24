# Go101 — Learn Go in 16 Projects

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Tests](https://github.com/baldur10/Go101/actions/workflows/ci.yml/badge.svg)](https://github.com/baldur10/Go101/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Zero External Dependencies](https://img.shields.io/badge/Deps-Zero%20External-brightgreen)](design.md)
[![Projects](https://img.shields.io/badge/Projects-16-blueviolet)](docs/index.html)

> A hands-on curriculum teaching Go from beginner to expert through 16 standalone, mostly-independent projects. Designed for experienced programmers new to Go.

---

## 🎯 Curriculum Overview

| Tier | Projects | Focus |
|------|----------|-------|
| **Beginner** (1–4) | [wordkit](docs/01-wordkit.html) · [taskline](docs/02-taskline.html) · [loglens](docs/03-loglens.html) · [moneylib](docs/04-moneylib.html) | Program anatomy, structs/JSON, streaming, precision arithmetic |
| **Intermediate** (5–9) | [gopherfetch](docs/05-gopherfetch.html) · [chatwire](docs/06-chatwire.html) · [shorty](docs/07-shorty.html) · [pubsubx](docs/08-pubsubx.html) · [metricsd](docs/09-metricsd.html) | Concurrency, TCP, HTTP/REST, generics, atomics |
| **Advanced** (10–12) | [minikv](docs/10-minikv.html) · [tagforge](docs/11-tagforge.html) · [pipeline](docs/12-pipeline.html) | Binary protocols, reflection, DAG orchestration |
| **Expert** (13–16) | [perfhunt](docs/13-perfhunt.html) · [fastthumb](docs/14-fastthumb.html) · [raftling](docs/15-raftling.html) · [gopherpilot](docs/16-gopherpilot.html) | Profiling, cgo, Raft consensus, LLM agent capstone |

📖 **Full curriculum:** [`docs/index.html`](docs/index.html) — self-contained, works offline from `file://`

---

## 🚀 Quick Start

```bash
# Clone and explore
git clone https://github.com/baldur10/Go101.git
cd Go101

# Pick a project (e.g., project 01)
cd projects/01-wordkit

# Run the failing tests (stubs return zero values)
go test ./...

# Implement the solution in *.go files
# When tests pass, compare with reference solution:
cd ../01-wordkit/solution
go test ./...
```

### Prerequisites
- **Go 1.22+** (required for enhanced `http.ServeMux` routing in Project 7)
- No external dependencies — pure stdlib (except Project 7 SQLite, Project 12 `golang.org/x/sync`, Project 14 optional cgo/libvips)

---

## 📁 Repository Structure

```
Go101/
├── AGENTS.md              # Agent workflow rules & conventions
├── design.md              # Canonical design document
├── LICENSE                # MIT License
├── README.md              # This file
├── .github/
│   ├── workflows/ci.yml   # CI: fmt, vet, test -race
│   └── pull_request_template.md
├── CODEOWNERS             # Auto-request reviews
├── docs/
│   ├── index.html         # Curriculum overview (start here!)
│   └── NN-<slug>.html     # 16 project docs (self-contained, offline)
└── projects/
    └── NN-<slug>/
        ├── go.mod         # Each project is its own module
        ├── *_test.go      # Executable specification (failing until implemented)
        ├── *.go           # Stubs with // TODO: markers
        └── solution/      # Reference implementation (own package)
            ├── go.mod
            ├── *_test.go  # Same tests, passing
            └── *.go       # Complete idiomatic solution
```

---

## 🧪 Testing

```bash
# Test a single project's solution
cd projects/01-wordkit/solution && go test ./...

# Race detector (required for concurrency projects: 5,6,8,9,10,12,15,16)
cd projects/06-chatwire/solution && go test -race ./...

# Format & vet
gofmt -w <file>
go vet ./...
```

---

## 📚 Documentation

Each project has a **self-contained HTML document** with 6 sections:
1. **Stack Evaluation** — Why this stack, rejected alternatives, unique lessons
2. **Requirements** — Derived from the test contract
3. **Architecture Decisions** — Component boundaries, data flow, trade-offs
4. **Concept Walkthroughs** — Go concepts applied to this project's code
5. **Testing Strategy** — What tests assert and why
6. **Go in the Wild** — Production systems using this stack

Open any `docs/NN-<slug>.html` directly in a browser — **no build step, works offline**.

---

## 🤝 Contributing

See [`AGENTS.md`](AGENTS.md) for the full agent orchestration pipeline:

1. **Branch** — `git checkout -b feat/NN-slug` (never commit to `main` directly)
2. **Implement** — Stubs + failing tests → `solution/` reference implementation
3. **Review** — Code audited against core lessons
4. **Document** — HTML doc produced, renders offline
5. **Adversarial** — Fleet review (Correctness / Doc Skeptic / Abuse Attacker)
6. **Reconcile** — Findings fixed, gates re-checked

All PRs must pass the CI workflow and follow the PR template checklist.

---

## 📄 License

[MIT](LICENSE) — Copyright (c) 2024 Go101 Contributors

---

## 🙏 Acknowledgments

- **NudaUI** — Animation snippets (nudaui.dev)
- **Go Team** — The language and standard library
- **Every project** teaches patterns used in production: Docker, Kubernetes, Terraform, Prometheus, etcd, CockroachDB, and more