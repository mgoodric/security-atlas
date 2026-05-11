# internal/

Private Go packages organized by domain. Nothing here is importable from outside this module.

Planned subpackages (filled by subsequent slices per `docs/issues/_INDEX.md`):

| Package                                                                 | Purpose                                                                      | First slice        |
| ----------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ------------------ |
| `internal/catalog/`                                                     | SCF + framework versioning                                                   | 006                |
| `internal/evidence/ingest/`                                             | Ingestion stage (canonicalize, redact, hash, scope-tag, write)               | 013                |
| `internal/evidence/ledger/`                                             | Append-only ledger reads                                                     | 013                |
| `internal/eval/`                                                        | Control state evaluation engine                                              | 012                |
| `internal/ucf/`                                                         | UCF graph traversal queries                                                  | 008                |
| `internal/scope/`                                                       | Scope dimensions + `applicability_expr` engine + FrameworkScope intersection | 017, 018           |
| `internal/risk/` `internal/policy/` `internal/audit/` `internal/board/` | Domain handlers                                                              | 019, 022, 025, 031 |
| `internal/auth/`                                                        | OIDC RP + RBAC + ABAC via OPA                                                | 034, 035           |
| `internal/tenancy/`                                                     | RLS context plumbing                                                         | 002, 033           |
| `internal/api/`                                                         | HTTP + gRPC handlers                                                         | 013, 008+          |

Empty in slice 001.
