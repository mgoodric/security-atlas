# 426 — Targeted coverage-lift round: decisions / policies / me / freshnessdrift

**Cluster:** Quality
**Estimate:** 1-2d (M)
**Type:** AFK
**Status:** `ready`
**Priority:** P2

## Narrative

**WHY.** The lowest non-excluded floors in
`cmd/scripts/coverage-thresholds.json` are clustered on user-facing /
eval-adjacent surfaces and sit far under the 70% business-logic tier
target (`$tier_recommendations.api_handlers_and_business_logic`):

- `internal/api/decisions` — **18**
- `internal/freshnessdrift` — **19**
- `internal/policy` — **35** / `internal/api/policies` — **39**
- `internal/api/me` — **43**

These were left low by the enrolment-only drain batches (which enrolled
pre-existing integration suites without adding pure-Go branch tests).
Each is a real surface: `decisions` + `policies` + `me` are user-facing
HTTP handlers; `freshnessdrift` is eval-adjacent (the drift read model).

**WHAT.** A coverage-lift round following the slice-353 Q-2 fast-loop
pattern: per package, add a `helpers_test.go` alongside the existing
suite that exercises the pure-Go branches (validators, normalizers,
formatters, predicate guards, pre-transaction input checks) with fast
`t.Parallel()` table tests — no Postgres, no build tag. Lift each floor
in the **same PR** as the tests (slice-069 monotonic ratchet). Follows
the slice 279 / 312 coverage-round precedent.

**SCOPE DISCIPLINE.** Four packages, pure-Go-first lifts. The target is a
material lift toward the 70% tier — not necessarily _to_ 70% in one PR if
a package's remaining branches genuinely need integration plumbing (those
are noted + spilled). The `policy` and `me` packages touch auth/tenant,
so their new tests assert RLS/role behavior on the branches that have it
— not happy-path-only. No handler behavior change; tests + floor lifts
only.

## Threat model

**S — Spoofing.** `me` resolves the caller's identity; `policies` gates
policy reads.

- Mitigation: new `me` tests assert the identity-resolution branches
  reject missing/invalid auth context; no new auth surface added.

**T — Tampering.** `decisions` + `policies` accept input that drives
queries.

- Mitigation: the pure-Go tests target the input-validation /
  predicate-guard branches (the pre-transaction checks) — proving
  malformed input is rejected before it reaches a query.

**R — Repudiation.** Decision + policy mutations should be auditable.

- Mitigation: no new audit surface; tests cover existing branches. Where
  a handler writes an audit row, that path stays integration-tested.

**I — Information disclosure (relevant — policy / me touch tenant data).**
A coverage test that asserts only happy paths could mask an RLS gap.

- Mitigation: per the brief, `policy` + `me` tests assert RLS/role
  behavior (a wrong-tenant / wrong-role branch denies), not just the
  positive path. The pure-Go tests cover pre-DB guards; any RLS branch
  that needs real Postgres is asserted in the package's integration
  suite, not faked.

**D — Denial of service.** `freshnessdrift` computes over evidence
windows.

- Mitigation: tests cover the bounded-window / stale-exclusion predicate
  branches; no unbounded path is newly exercised.

**E — Elevation of privilege.** `me` + `policies` have role gates.

- Mitigation: the new tests assert the role-guard branches deny the
  unprivileged case where the guard is pure-Go; handler-level role gates
  that need a session stay integration-tested.

**Verdict:** `has-mitigations`. The slice is test-only, but the
policy/me/tenant-touching tests are written to assert
RLS/role/deny branches — not happy-path-only — closing the
"coverage satisfied by happy paths" risk slice 333 Q-4 names.

## Acceptance criteria

- [ ] **AC-1 (test).** `internal/api/decisions` gains a `helpers_test.go`
      (pure-Go, `t.Parallel()`, no build tag) exercising its
      validators / normalizers / predicate guards.
- [ ] **AC-2 (test).** `internal/freshnessdrift` gains pure-Go branch
      tests for its window / stale-exclusion / worst-cell predicate
      branches.
- [ ] **AC-3 (test).** `internal/policy` and `internal/api/policies` gain
      pure-Go branch tests; the tenant/role-touching branches assert
      deny behavior (not happy-path-only).
- [ ] **AC-4 (test).** `internal/api/me` gains pure-Go branch tests; the
      identity-resolution branches assert the missing/invalid-context
      rejection.
- [ ] **AC-5.** Each of the five package floors in
      `cmd/scripts/coverage-thresholds.json` is lifted to
      `max(0, floor(measured - 2pp))` in the SAME PR as its tests —
      monotonic ↑, never above measured.
- [ ] **AC-6.** Each lift is backed by real assertions (no vacuous
      tests); a package whose residual branches need integration plumbing
      documents the residual + the deferred lift in a code comment /
      spillover, rather than over-lifting the floor.
- [ ] **AC-7.** New tests are pure-Go (no Postgres, no `//go:build
integration`) per the slice-353 Q-2 fast-loop convention; they run
      in the existing `Go · build + test` job.
- [ ] **AC-8.** `cmd/scripts/coverage-gate` passes against the lifted
      floors (the gate is the verification).

## Constitutional invariants honored

- **Tenant isolation enforced at the DB layer (invariant #6).** The
  policy / me tests assert deny-on-wrong-tenant/role branches — they
  reinforce RLS, never bypass it.
- **Testing discipline (CLAUDE.md).** Floor lift + tests in one PR;
  monotonic ratchet (slice 069). Pure-Go-first (slice 353 Q-2).
- **Ingestion / evaluation separation (invariant #2).** `freshnessdrift`
  is a read model over the ledger; its tests read, never write
  source-of-truth evidence.

## Canvas references

- `Plans/canvas/07-metrics.md` — freshness/drift read models.
- `Plans/canvas/02-primitives.md` — Policy primitive.
- CLAUDE.md "Test-tier conventions" (Q-2 pure-Go fast loop) +
  `$tier_recommendations` in the thresholds file.

## Dependencies

- **#353** (QA tactical round-1 — Q-2 pure-Go convention) — `merged`. The
  `helpers_test.go` pattern this round follows.
- **#279** / **#312** (prior coverage rounds) — `merged`. The
  coverage-round precedent.
- All five target packages are `merged` on `main`.

## Anti-criteria (P0 — block merge)

- **P0-426-1.** Does NOT raise any floor without writing the tests that
  hit the new bar (slice 069 ratchet — tests + lift in one PR).
- **P0-426-2.** Does NOT lower any existing floor; does NOT over-lift
  above measured coverage.
- **P0-426-3.** Does NOT write happy-path-only tests for the
  policy/me/tenant-touching branches — the RLS/role deny branches MUST be
  asserted where the branch is testable.
- **P0-426-4.** Does NOT mock the DB to fake coverage — pure-Go tests
  target genuinely pure-Go branches; DB-dependent branches stay in the
  integration suite (CLAUDE.md "never mock the DB").
- **P0-426-5.** Does NOT modify `_STATUS.md` from inside this slice's own
  commits.

## Skill mix (3-5)

- `tdd` (pure-Go table tests)
- `engineering-advanced-skills:focused-fix` (per-package branch seam)
- `Security` (RLS/role deny-branch verification)
- `simplify` (pre-PR)

## Notes for the implementing agent

- This is the slice 290 / 297 / 310 / 313 / 315 / 318 / 320 playbook:
  add `helpers_test.go` for the pure-Go branches first (fast feedback, no
  `-p 1` cost), reach for integration only where a branch genuinely needs
  real services.
- For `policy` + `me`: read the package's existing `integration_test.go`
  to see which branches are already DB-covered; target the _pure-Go_
  residue (input validators, role-guard pre-checks, normalizers) and
  assert the deny branch, not just the allow.
- Measure with the merged unit+integration profile (gocovmerge, per slice 279) before setting each floor — the floor is `floor(merged_measured -
2pp)`.
- `decisions` at 18 and `freshnessdrift` at 19 have the most headroom;
  expect the largest lifts there. `me` at 43 and `policies` at 39 are
  closer to the tier and may have a smaller pure-Go residue.
