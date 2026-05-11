# security-atlas

Open-source GRC platform — a control-graph + evidence-pipeline for security programs running against many frameworks (SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, GDPR).

**Pre-implementation status.** Slice 001 ships the monorepo skeleton + CI green build only. See [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) for the design canvas and [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) for the v1 roadmap (49 slices · ~94d critical path).

---

## Prerequisites

- Go 1.22+
- Node.js 20+
- Python 3.11+ (for `oscal-bridge` and ruff)
- [`just`](https://just.systems) — `brew install just`
- [`pre-commit`](https://pre-commit.com) — `pip install pre-commit`
- [`golangci-lint`](https://golangci-lint.run) — `brew install golangci-lint`
- [`uv`](https://docs.astral.sh/uv/) (optional, for Python workspace) — `brew install uv`

---

## Quick start

```sh
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas
just install-hooks   # one-time: installs pre-commit hooks
just build           # build Go binaries + frontend
just test            # run all tests
```

After `just install-hooks`, commits with malformed Go (or unformatted YAML / JSON / Markdown) are rejected locally before they reach the remote.

---

## Task surface (`just`)

| Recipe                | What it does                                                          |
| --------------------- | --------------------------------------------------------------------- |
| `just`                | List all recipes                                                      |
| `just build`          | Build all components (Go + frontend)                                  |
| `just build-go`       | Build Go binaries only                                                |
| `just build-frontend` | Build the `web/` workspace                                            |
| `just test`           | Run all tests                                                         |
| `just test-go`        | Run Go tests (`go test -race ./...` in CI)                            |
| `just test-frontend`  | Run frontend tests                                                    |
| `just lint`           | Run all linters (Go + frontend + Python)                              |
| `just lint-go`        | `golangci-lint run ./...`                                             |
| `just lint-frontend`  | `npm run lint` in `web/`                                              |
| `just lint-python`    | `ruff check .`                                                        |
| `just fmt`            | Format all code (in-place)                                            |
| `just fmt-go`         | `gofmt -w` + `goimports -w -local github.com/mgoodric/security-atlas` |
| `just fmt-python`     | `ruff format .`                                                       |
| `just install-hooks`  | Install pre-commit hooks (one-time)                                   |
| `just hooks-run`      | Run pre-commit against the whole tree                                 |
| `just tidy`           | `go mod tidy` and fail if `go.mod`/`go.sum` change                    |
| `just ci`             | Run what CI runs (lint + test + build)                                |

---

## Repository layout

| Path                                        | What it is                                                                                                | First slice that fills it           |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------- | ----------------------------------- |
| [`Plans/`](./Plans)                         | Design canvas, mockups, deep-dive docs (pre-implementation)                                               | (already populated)                 |
| [`docs/issues/`](./docs/issues)             | v1 backlog (49 issues + index + dep graph + review)                                                       | (already populated)                 |
| [`CLAUDE.md`](./CLAUDE.md)                  | Constitutional principles + AI-assist boundary + tech stack lock                                          | (already populated)                 |
| `cmd/atlas/`                                | Platform server binary                                                                                    | slice 013 + ongoing                 |
| `cmd/atlas-cli/`                            | CLI binary                                                                                                | slice 003                           |
| `cmd/atlas-oscal/`                          | OSCAL bridge binary (Python via gRPC)                                                                     | slice 030                           |
| `internal/`                                 | Private Go packages (catalog, evidence, eval, ucf, scope, risk, policy, audit, board, auth, tenancy, api) | slices 002+                         |
| `pkg/sdk-go/`                               | Public Go SDK (evidence push)                                                                             | slice 003                           |
| `connectors/`                               | Per-connector implementations (AWS, GitHub, Okta, 1Password, osquery, Jira/Linear, manual-upload)         | slices 004, 044–049                 |
| `sdk/python/` `sdk/typescript/` `sdk/java/` | Non-Go SDKs                                                                                               | slice 003 (Python + TS); Java in v2 |
| `web/`                                      | Next.js 15 frontend                                                                                       | slice 005                           |
| `oscal-bridge/`                             | Python service wrapping `compliance-trestle`                                                              | slice 030                           |
| `proto/`                                    | gRPC protobuf definitions                                                                                 | slice 003                           |
| `schemas/`                                  | JSON Schemas for `evidence_kind`                                                                          | slice 014                           |
| `migrations/`                               | Atlas declarative migrations (`schema.hcl`)                                                               | slice 002                           |
| `policies/`                                 | OPA Rego (authz + control policies)                                                                       | slice 035                           |
| `deploy/docker/` `deploy/helm/`             | Deployment artifacts                                                                                      | slices 037, 038                     |

---

## Contributing

Read [`CLAUDE.md`](./CLAUDE.md) first. It documents:

- 10 constitutional invariants (architecture commitments — non-negotiable)
- 7 anti-patterns explicitly rejected (policy template theater, AI-generated audit responses, proprietary collector agents, etc.)
- The hard AI-assist boundary (citations required, human approval before publish)
- Licensing constraints (SCF/CCM/CAIQ/SIG/HECVAT — what we can and cannot bundle)
- Working norms (Conventional Commits, no emojis in code/docs, default to `Plans/` edits pre-code)

---

## License

**Open decision (Plans/canvas/11-open-questions.md #03).** Apache 2.0 vs AGPL is unresolved. Until the project license is chosen and a `LICENSE` file lands, this repository is shared for design review and contribution conversations only — **not licensed for redistribution.** The placeholder `LICENSE` file at the repo root states the same.

---

## Status

- **Slice 001** — Monorepo skeleton + CI green build (this slice)
- See [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) for the 49-slice v1 roadmap

[GitHub](https://github.com/mgoodric/security-atlas)
