# 401 — Integration-enrolment drain · batch 1 (security-critical auth/audit)

**Cluster:** infra
**Estimate:** 1-1.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** 390 (drain the 38-package integration-enrolment backlog)

## Narrative

First drain batch of slice 390. Slice 345's enrolment-discovery guard
catalogued 38 packages carrying `//go:build integration` tests that are NOT
in the integration job's package list in `.github/workflows/ci.yml` — their
integration tests have never run in CI. Per 390's stated priority
(constitutional invariant #10 + security surface), this batch enrols the
**security-critical** packages first.

## Packages in this batch (4 — `internal/api/oauth` already enrolled by slice 314)

> **Update 2026-05-30:** `internal/api/oauth` was enrolled + drained by slice
> 314 (#909 — it also lifted oauth coverage to 74.7% and fixed a latent
> device-flow test bug surfaced by the never-run suite). It is already in the
> ci.yml integration list and removed from `KNOWN_UNENROLLED`, so it is
> DROPPED from this batch. This batch now enrols the remaining 4.

```
internal/auth/oidc        # 3 nonce integration tests (slice 365 family)
internal/auth/jwtmw       # JWT middleware
internal/auth/users       # user/identity store
internal/audit/period     # audit-period freezing (invariant #10)
```

## Per-package procedure (390 §Approach)

For EACH package in this batch:

1. Add its `./internal/<pkg>/...` entry to the integration job's package
   list in `.github/workflows/ci.yml` (the "Run integration tests" step).
2. Run its integration suite locally
   (`go test -tags=integration -p 1 ./internal/<pkg>/...`); **fix any test
   that was silently broken by never running in CI** — this is the most
   likely failure class. Fix the test/code correctly; do NOT skip or delete
   to get green. If a test reveals a genuine product bug (not a stale test),
   file a spillover slice and note it rather than masking.
3. Remove the package from `KNOWN_UNENROLLED` in
   `scripts/audit-integration-enrolment.sh` in the SAME PR (the allowlist
   monotonically shrinks; never grows).
4. If the package also sits on `cmd/scripts/coverage-thresholds.json`
   `excludes`, lift it off with a per-package floor (slice 348 track 2).

## COORDINATION NOTE (RESOLVED 2026-05-30)

`internal/api/oauth` was the subject of slice **314** (coverage lift to
74.7%), which landed FIRST (#909) and did the oauth enrolment +
`KNOWN_UNENROLLED` removal. This batch therefore DROPS oauth and enrols the
remaining 4 packages. No further coordination needed.

## Acceptance criteria

- [ ] **AC-1.** All 4 packages (oidc/jwtmw/users/period) enrolled in the
      integration job in ci.yml. (oauth already enrolled by slice 314.)
- [ ] **AC-2.** Each package's integration suite is GREEN in CI (no test left
      silently broken; fixes are real, not skips/deletes).
- [ ] **AC-3.** All 4 removed from `KNOWN_UNENROLLED`; the allowlist shrinks
      by exactly these 4.
- [ ] **AC-4.** Any package also on coverage `excludes` lifted to a per-file
      floor.
- [ ] **AC-5.** `pre-commit run --all-files` + CI green.

## Dependencies

- **#345** (enrolment-discovery guard + allowlist) — `merged`.
- **#390** (parent tracking slice).
- Coordinates with **#314** on the oauth enrolment (see note).

## Anti-criteria (P0)

- **P0-401-1.** Does NOT skip/delete a failing integration test to reach
  green — fix it, or spillover the underlying bug.
- **P0-401-2.** Does NOT grow `KNOWN_UNENROLLED` — monotonic shrink only.
- **P0-401-3.** Enrols ONLY this batch's 5 packages — later batches own the rest.
