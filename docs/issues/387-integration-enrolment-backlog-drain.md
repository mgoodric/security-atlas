# 387 — Drain the integration-job enrolment backlog (38 packages)

**Cluster:** infra
**Estimate:** 2-3d (likely 6-10 sub-slices)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 345, captured per continuous-batch policy.

Slice 345 added the enrolment-discovery guard
(`scripts/audit-integration-enrolment.sh`). Building it surfaced the
empirical state the guard was designed to detect: **38 packages carry a
`//go:build integration` build tag but are NOT enumerated in the
integration job's package list** in `.github/workflows/ci.yml`. Their
integration tests — including real `Test...` functions against the
Postgres / NATS / MinIO harness — do not run in CI today. Examples:

- `internal/api/oauth` — 28 integration test functions, unrun.
- `internal/exception` — 22 integration test functions, unrun.
- `internal/auth/oidc` — 3 nonce integration tests (slice 365 family), unrun.
- `internal/oscal`, `internal/api/search`, `internal/api/board`, … — all unrun.

This is exactly the I-1 gap class slice 334's audit named and slice
348's `excludes` audit independently catalogued
(`docs/audits/348-coverage-excludes-audit.md` category (c) TEST_PRESENT,
27 entries — a subset of this 38).

Slice 345 honors its own anti-criterion P0-345-1 ("does NOT retroactively
enrol forgotten packages — separate slice if any surface") by seeding the
38 into a **documented, dated known-gaps allowlist** baked into the guard
script (`KNOWN_UNENROLLED`). The guard fails on the 39th forgotten
package while the 38 drain through this slice. This slice IS that drain.

## The backlog (38 packages, as of 2026-05-29)

```
internal/api
internal/api/adminauditlog
internal/api/adminauthzbundle
internal/api/admincreds
internal/api/admindemo
internal/api/adminsso
internal/api/adminusers
internal/api/aggregationrules
internal/api/board
internal/api/calendar
internal/api/decisions
internal/api/emptyset
internal/api/freshnessdrift
internal/api/me
internal/api/oauth
internal/api/policies
internal/api/questionnaires
internal/api/search
internal/api/ucfcoverage
internal/api/vendors
internal/audit/notes
internal/audit/period
internal/auth
internal/auth/jwtmw
internal/auth/keystore/fsstore
internal/auth/oidc
internal/auth/users
internal/catalog/metrics
internal/drift
internal/exception
internal/freshness
internal/freshnessdrift
internal/mcp
internal/observability/otel
internal/oscal
internal/policy/pdf
internal/policy/seed
internal/risk/aggrule
```

## Approach

For each package (or small batch), in its own PR:

1. Add the `./internal/<pkg>/...` entry to the integration job's package
   list in `.github/workflows/ci.yml`.
2. Run the integration suite for it locally; fix any test that was
   silently broken by never running in CI (the most likely failure
   class — these tests have not run since they were written).
3. Remove the package from the `KNOWN_UNENROLLED` allowlist in
   `scripts/audit-integration-enrolment.sh` in the SAME PR (the allowlist
   monotonically shrinks; never grows — same ratchet discipline as slice
   069's coverage `excludes`).
4. If the package also sits on `cmd/scripts/coverage-thresholds.json`
   `excludes`, lift it off with a per-package floor (per slice 348
   recommendation track 2).

**Prioritize security-critical first:** `internal/api/oauth`,
`internal/auth/oidc`, `internal/auth/jwtmw`, `internal/auth/users`,
`internal/audit/period` (constitutional invariant 10).

## Acceptance criteria

- [ ] All 38 packages enrolled in the integration job (across N PRs).
- [ ] `KNOWN_UNENROLLED` in the guard shrinks to empty.
- [ ] No integration test left silently broken; each enrolled package's
      suite is green in CI.
- [ ] Slice 348 category (c) re-audited; TEST_PRESENT entries retired
      where enrolment closes them.

## Dependencies

- **#345** (enrolment-discovery guard) — the guard whose allowlist this
  slice drains. Must land first.
- **#348** (coverage excludes audit) — `merged`. The independent catalog
  of the overlapping gap.

## Notes

This is deliberately filed as ONE tracking slice; the implementer should
split it into batched sub-slices (security-critical packages first, then
3-5 packages per follow-up PR) to keep each PR's CI green and reviewable.
