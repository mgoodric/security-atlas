# 409 — Contract-tier rollout: dashboard + high-traffic e2e routes (unblocks 394)

**Cluster:** Quality
**Estimate:** 2-3d (gated by the DB-seam decision below)
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** 392 (contract-tier rollout) · unblocks 394 (e2e mocks load from goldens)

## Narrative

Slice 349 piloted and slice 392 rolled out the golden-file contract tier
(provider records the real handler's response body to a shared
`web/lib/contracts/*.golden.json`; the BFF consumer asserts against the same
golden). It currently covers four endpoints: `version`, `me`,
`admin/demo/status`, `install-state`.

Slice **394** (teach `/e2e/` `route.fulfill` mocks to load from the goldens)
is `blocked` until the golden tier spans the **high-traffic routes the e2e
suite actually traverses** — the dashboard panels (`web/app/api/dashboard/**`:
activity, drift, freshness, framework-posture, risks, upcoming) plus
controls, risks, and audit. Doing 394 against only the 4 thin endpoints is
premature (394 D5). This slice records those goldens, which is 394's
unblocking precondition.

## THE LOAD-BEARING CONSTRAINT (design this first)

ADR-0007 mandates that contract **recorders run on the plain
`go test ./...` unit surface (no DB)** — that is what keeps the tier a
zero-new-CI-job, zero-new-gate layer riding the existing Go-unit + vitest
surfaces. But the dashboard-panel upstream handlers
(`/v1/activity`, `/v1/controls/drift`, `/v1/freshness`, `/v1/frameworks/posture`,
`/v1/risks`, `/v1/upcoming`, …) **read tenant data through `dbx.New(h.pool)`
against a real Postgres pool** — exactly the situation slice 392 hit with
`GET /v1/metrics` and **deferred**, because there is no interface seam to
record them on the unit surface (392 decisions log).

So the first job of this slice is to PICK the recording strategy and record
the decision, not to mechanically record goldens:

- **Option A — add an injectable query-interface seam** to each panel
  handler so its response shape can be recorded on the unit surface with a
  fake/stub data source (the ADR-0007-faithful path). Real but bounded
  refactor per handler; this is what 392 named as the proper fix for the
  metrics deferral.
- **Option B — a no-DB construction trick per handler** (cf. 392's `me`
  recorder: a non-UUID credential forced the synthetic-profile path with a
  nil resolver, yielding the shape with no pool). Works only where a handler
  has such a degenerate-but-representative path; not all panels will.
- **Option C — record a representative subset now** (the panels that already
  have a unit-surface-reachable path) and DEFER the rest with documented
  rationale (like 392 did for metrics), filing follow-ups. 394 can then
  unblock against whatever coverage is sufficient for its e2e routes.

The engineer evaluates per-endpoint which of A/B/C applies, records it in
`docs/audit-log/409-...-decisions.md`, and does NOT silently move a recorder
onto the integration surface (that violates ADR-0007 and couples golden
regeneration to a DB bring-up — call it out as a spillover refactor instead).

## What ships

1. Per-endpoint contract goldens under `web/lib/contracts/` for the
   dashboard panel routes (and controls/risks/audit if reachable), each with
   a provider recorder (`*_contract_test.go`) reusing the slice-392 shared
   recorder helper pattern, and a consumer vitest assert.
2. Prove drift sensitivity on at least one new endpoint (rename a field →
   both halves fail → restore), per the 349/392 discipline.
3. A decisions log capturing the A/B/C choice per endpoint + any deferrals
   with rationale (mirrors 392's metrics-defer note).
4. Update slice 394's Dependencies section: flip it `not-ready` → `ready`
   once enough dashboard/controls/risks/audit goldens exist for its e2e
   `fulfillFromGolden` helper to be non-premature.

## Acceptance criteria

- [ ] **AC-1.** Recording strategy (A/B/C) chosen + documented per target endpoint.
- [ ] **AC-2.** Goldens + recorders + consumer asserts land for the
      unit-recordable dashboard/controls/risks/audit routes; deferrals
      documented with rationale (no recorder moved onto the integration
      surface — P0 below).
- [ ] **AC-3.** Drift sensitivity proven on ≥1 new endpoint.
- [ ] **AC-4.** No new CI job / no new gate (ADR-0007); rides Go-unit + vitest.
- [ ] **AC-5.** Slice 394's blocker reassessed; flipped to `ready` if covered,
      else its remaining gap re-stated.
- [ ] **AC-6.** `pre-commit run --all-files` + CI green.

## Dependencies

- **#392** (contract-tier rollout) — `merged`. The shared recorder pattern.
- **ADR-0007** — the zero-new-gate constraint this slice must honor.

## Anti-criteria (P0 — block merge)

- **P0-409-1.** Does NOT move a contract recorder onto the Go **integration**
  surface to dodge the DB-seam problem — that breaks ADR-0007's "rides the
  unit surface" intent. Add a seam (Option A), use a no-DB path (Option B),
  or defer with rationale (Option C).
- **P0-409-2.** Does NOT widen any handler's public API beyond what recording
  requires; an injectable query interface (Option A) stays internal.
- **P0-409-3.** Does NOT modify `_INDEX.md` / `_STATUS.md`.

## Notes

This is the unblocking precondition slice 394 names. It is deliberately
JUDGMENT (not AFK) because the DB-seam strategy is a real per-endpoint design
call, not a mechanical golden-record. Surfaced while dispositioning the
post-loop backlog 2026-05-30.
