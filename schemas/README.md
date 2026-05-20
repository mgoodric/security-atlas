# schemas/ (moved)

The canonical JSON Schemas for every registered `evidence_kind` now live alongside the registry implementation:

```
internal/api/schemaregistry/schemas/<kind>/<semver>.json
```

Slice 014 moved them there so Go's `//go:embed` directive can reach them — `//go:embed` cannot traverse upward out of its package, and the registry needs the bundle at compile time (constitutional: tests run without external file dependencies).

This directory remains as a discovery breadcrumb. Add new platform schemas by writing the JSON file under the canonical path; `go test ./internal/api/schemaregistry/...` will round-trip it through embed-load and Postgres at boot.

## Conventions

- Filename: `<semver>.json` (e.g., `1.0.0.json`)
- Required top-level extension keys: `x-evidence-kind`, `x-semver`, `x-owner`
- Optional: `x-default-scf-anchors` — array of SCF anchor codes (e.g., `["IAC-06"]`)
- Schema dialect: JSON Schema draft 2020-12

See `internal/api/schemaregistry/embed.go` for the loader contract and `Plans/EVIDENCE_SDK.md` §4.5 for the design.

## Deprecation window (slice 179)

Breaking-major bumps (e.g. `1.x.x → 2.0.0`) MUST keep the old version file in the registry for at least 90 days before removal. The CI job `Schema · removal-age (90-day floor)` enforces the floor against the file's introduction commit on `main`. The exact `[deprecation-override]` PR label bypasses the failure (emergency removals only; requires a maintainer's approval and an audit-log entry under `docs/audit-log/`).

Full operator workflow + local-reproduction commands:
[`internal/api/schemaregistry/schemas/README.md`](../internal/api/schemaregistry/schemas/README.md).
