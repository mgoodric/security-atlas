# 401 — Integration-enrolment drain · batch 1 (security-critical auth/audit)

**Cluster:** infra
**Estimate:** 1-1.5d
**Type:** AFK
**Status:** `ready`
**Parent:** 390 (drain the 38-package integration-enrolment backlog)

## Narrative

First drain batch of slice 390. Slice 345's enrolment-discovery guard
catalogued 38 packages carrying `//go:build integration` tests that are NOT
in the integration job's package list in `.github/workflows/ci.yml` — their
integration tests have never run in CI. Per 390's stated priority
(constitutional invariant #10 + security surface), this batch enrols the
**security-critical** packages first.

## Packages in this batch (5)

```
internal/api/oauth        # 28 integration test functions, unrun (largest)
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

## COORDINATION NOTE (read before starting)

`internal/api/oauth` is ALSO the subject of slice **314** (coverage lift to
70%), whose step 1 is "enrol oauth in the integration job." Whichever of
314/401 lands first does the oauth enrolment; the other rebases and drops
the duplicate. Do NOT run 314 and 401 in the same parallel batch — they
both edit ci.yml's integration list + touch oauth. Sequence them.

## Acceptance criteria

- [ ] **AC-1.** All 5 packages enrolled in the integration job in ci.yml.
- [ ] **AC-2.** Each package's integration suite is GREEN in CI (no test left
      silently broken; fixes are real, not skips/deletes).
- [ ] **AC-3.** All 5 removed from `KNOWN_UNENROLLED`; the allowlist shrinks
      by exactly these 5.
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
