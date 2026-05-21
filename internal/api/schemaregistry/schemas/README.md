# `evidence_kind` schemas — registry source-of-truth

This directory holds the JSON Schemas that define every `evidence_kind`
the platform accepts via the Evidence SDK. The directory is the
embedded source-of-truth: `internal/api/schemaregistry/embed.go` walks
the tree at compile time via `//go:embed` and loads every file into
the registry at boot.

## File layout

```
internal/api/schemaregistry/schemas/<kind>/<semver>.json
```

- `<kind>` — dotted lowercase namespace (e.g. `aws.s3.bucket_encryption_state`).
- `<semver>` — exact SemVer (`MAJOR.MINOR.PATCH`, e.g. `1.0.0`, `1.1.0`).

Each file is a single JSON Schema 2020-12 document plus the platform
extension keys:

| Key                     | Required | Example                             | Notes                                        |
| ----------------------- | -------- | ----------------------------------- | -------------------------------------------- |
| `x-evidence-kind`       | yes      | `aws.s3.bucket_encryption_state.v1` | Dotted name + major-version suffix.          |
| `x-semver`              | yes      | `1.0.0`                             | Must match the filename (without `.json`).   |
| `x-owner`               | yes      | `platform`                          | Team / connector that authored the schema.   |
| `x-default-scf-anchors` | no       | `["IAC-06"]`                        | Optional default control mapping for ingest. |

## Version-bump discipline (slice 014)

The registry enforces SemVer compatibility on insert. Two boundaries
matter:

- **Additive minor / patch (`1.0.0 → 1.1.0`)** — pure-additive
  changes (new optional fields) are accepted automatically by
  `additive.go`. Existing records keep validating.
- **Breaking major (`1.x.x → 2.0.0`)** — a new top-level version
  is required when the schema removes a required field, narrows a
  type, or changes semantic meaning. The OLD version (`1.x.x`) stays
  in the registry — old records continue to validate against their
  recorded schema_version. The append-only evidence ledger
  (constitutional invariant #2) means historical records remain
  queryable forever even after a major bump.

## Deprecation window (slice 179)

When a breaking-major bump lands, the OLD `<kind>/1.x.x.json` file
MUST stay in this directory for **at least 90 days** before a PR
removes it. The clock starts at the file's first commit on `main` and
is enforced structurally by the `Schema · removal-age (90-day floor)`
CI check (`.github/workflows/ci.yml`, slice 179).

The window exists because connectors and downstream pushers carry a
schema-version pin against the registry; removing a version too soon
strands those clients. 90 days matches the slowest realistic
quarterly release cadence (canvas open questions #9 + #17, resolved
2026-05-20).

### Operator workflow

1. Land the new major (e.g. `2.0.0.json`) in a normal PR. The OLD
   file stays.
2. Update the connector / pusher to target `2.0.0`. Cut a release.
3. Wait at least 90 days from `1.x.x.json`'s introduction-on-`main`
   commit.
4. Open a follow-up PR that removes `1.x.x.json`. The CI check
   `Schema · removal-age (90-day floor)` reads each removed file's
   introduction date from `git log --diff-filter=A` on `main` and
   passes only when the file is at least 90 days old.

### Emergency removal — `[deprecation-override]` label

A schema published with a security-sensitive defect must be
unpublishable immediately. The escape hatch:

1. Apply the **exact** `[deprecation-override]` label to the PR.
2. Add an audit-log entry under `docs/audit-log/<NNN>-<kebab>.md`
   documenting the rationale (which schema, why early removal is
   necessary, downstream-impact assessment).
3. A maintainer's approval gates the override structurally — only
   maintainers can apply repo labels.

The CI job exports `SCHEMA_REMOVAL_OVERRIDE=1` to the script when the
label is present. The script still prints the violation to stderr for
the audit trail, then exits 0.

### Local reproduction

```bash
# Against your local PR branch:
git fetch origin main:main
git diff --diff-filter=D --name-only main...HEAD \
  -- internal/api/schemaregistry/schemas/ \
  | bash scripts/check-schema-removal-age.sh

# Or pass paths explicitly (useful for what-if checks):
bash scripts/check-schema-removal-age.sh \
  internal/api/schemaregistry/schemas/<kind>/<old-semver>.json
```

### Trust root

The 90-day age is computed from `git log --diff-filter=A --format=%cI`
on `main` — the trust root cannot be forged by a PR because a PR
cannot rewrite `main`'s history. The file's introduction date is NOT
read from any PR-mutable source (filename, frontmatter, or commit
message in the PR branch).

## Authoring a new schema (slice 014 + slice 067 walkthrough)

See `docs-site/docs/connector-authoring.md` for the full Evidence-SDK
authoring flow. The schema-file ergonomics:

1. Drop `<kind>/<semver>.json` here.
2. Run `go test ./internal/api/schemaregistry/...` — the embed-load
   round-trip validates the file at compile time AND against
   Postgres at boot.
3. Add a fixture record under
   `internal/api/schemaregistry/embed_load_test.go` if the schema
   introduces a new validation pattern worth pinning.

## References

- `internal/api/schemaregistry/embed.go` — embed-load contract.
- `internal/api/schemaregistry/additive.go` + `semver.go` — slice 014
  compatibility-enforcement implementation.
- `Plans/EVIDENCE_SDK.md` §4.5 — schema-registry design.
- `Plans/canvas/04-evidence-engine.md` §4 — evidence engine + registry.
- `Plans/canvas/11-open-questions.md` items 9 + 17 — deprecation-window
  resolution (2026-05-20).
- `scripts/check-schema-removal-age.sh` — slice 179 enforcer script.
- `.github/workflows/ci.yml::schema-removal-age` — slice 179 CI job.
