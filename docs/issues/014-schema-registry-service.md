# 014 — Schema registry service (in-tree Go service)

**Cluster:** Evidence pipeline
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Build the in-tree schema registry that holds the JSON Schemas for every registered `evidence_kind`. Schemas live in the repo under `schemas/<kind>/<semver>.json` and are loaded into Postgres at startup. Per-tenant private kinds can be added at runtime via `POST /v1/schemas` (admin-only). Each kind has a stable id, JSON Schema, owner attribution, default SCF anchor mappings, and a semver. The push endpoint (slice 013) calls this service to validate every record before write. Tenants can register their own private kinds without touching the global namespace — the OpenTelemetry-semantic-conventions analog. The slice delivers value because every evidence record now passes through a contract-enforcement point.

## Acceptance criteria

- [ ] AC-1: `GET /v1/schemas` lists registered kinds (global + this tenant's private kinds)
- [ ] AC-2: `GET /v1/schemas/<kind>/<semver>` returns the JSON Schema
- [ ] AC-3: Service ships with ~10 v1 platform schemas: `sast.scan_result.v1`, `access_review.completion.v1`, `manual.attestation.v1`, `aws.s3.bucket_encryption_state.v1`, `github.repo_protection.v1`, `okta.mfa_policy.v1`, `1password.org_policy.v1`, `osquery.host_posture.v1`, `jira.ticket_evidence.v1`, `manual.upload.v1`
- [ ] AC-4: Admin can register a private kind via `POST /v1/schemas` with a JSON Schema + owner; private kinds are tenant-scoped
- [ ] AC-5: Semver enforcement — minor bumps must be additive (new optional fields only); breaking changes require major bump + deprecation window
- [ ] AC-6: Validation hook integrates into slice 013's write path

## Constitutional invariants honored

- **Anti-criterion (schemaless push rejected):** registry is the contract that makes this enforceable
- **Invariant 6 (RLS):** private kinds tenant-scoped

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 (schema registry as OTEL-semantic-conventions analog)
- `Plans/EVIDENCE_SDK.md` §4.5 (schema registry section)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT permit anonymous schema registration
- Does NOT silently auto-bump major versions
- Does NOT leak private kinds across tenants
- Does NOT permit a kind without an owner attribution

## Skill mix (3–5)

- Go HTTP + Postgres
- JSON Schema (draft 2020-12)
- semver parsing/comparison
- sqlc-typed persistence
- Embedded `embed` package for shipping global schemas
