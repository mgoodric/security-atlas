# Slice 748 — Deep audit-period snapshot integration for ActionPlan: decisions log

**Type:** JUDGMENT (follow-up to slice 384, D5)
**Status at build:** the slice doc's `Status:` said `not-ready — blocked on
slice 384`. Ground truth at build time: slice 384 is MERGED on `main`
(`internal/actionplan` package + `action_plans` migration + `ListSnapshot`
present), so the blocker is cleared and the slice is READY. Built.

This is a JUDGMENT slice: the subjective build-time calls below were made by the
implementing engineer and recorded here rather than blocking the merge on a
human sign-off. The maintainer iterates post-deployment.

---

## What this slice does (one paragraph)

Wires the slice-384 read-side foundation (`actionplan.Store.ListSnapshot(frozenAt)`)
into the slice-028 audit-period freeze primitive (`internal/audit/period`). A new
`period.Store.Snapshot(ctx, periodID, lister)` resolves a period's freeze horizon
(`frozen_at` when frozen, wall-clock now when still open) and assembles a
`period.FrozenView` whose `ActionPlans` are drawn through the injected lister at
that horizon. It is a **read-side wiring slice + tests**: no migration, no new
HTTP route, no record mutation.

---

## Decisions made

### D1 — Injected function-typed seam, NOT a direct import (layering / import-cycle)

**The load-bearing call.** AC-1 says wire `actionplan.Store.ListSnapshot` into the
period frozen-view read path, but flags that `internal/audit/period` may not be
allowed to import `internal/actionplan` directly.

Ground-truth dependency check before deciding:

- `internal/actionplan` does NOT import `internal/audit/period` (grep clean).
- `internal/audit/period` imports only `internal/audit/sink`,
  `internal/audit/unifiedlog`, `internal/db/dbx`, `internal/tenancy` — all leaf
  infra. It is itself a near-leaf of the audit subtree.

A _direct_ `period -> actionplan` import would NOT create an immediate cycle
today (actionplan doesn't import period), but it would (a) make `period` depend
on a peer _domain_ package (an inversion — `period` is a foundational primitive,
`actionplan` is a later feature built atop the same infra), and (b) be a latent
cycle the moment `actionplan` ever needs anything from `period`.

**Decision:** keep `period` free of any `actionplan` import. `period` declares
its own minimal projection (`ActionPlanRef`) and a function-typed seam
(`ActionPlanSnapshotLister func(ctx, horizon) ([]ActionPlanRef, error)`).
`period.Store.Snapshot` takes that seam as a parameter (dependency injection),
mirroring how `auditperiods.Handler` already injects narrow read seams
(`periodLister` / `periodReader`, slices 411/687) into the same package. The
adapter that bridges `actionplan.ActionPlan -> period.ActionPlanRef` lives in
`internal/actionplan` (`Store.PeriodSnapshotLister()`), where the dependency
points the **safe** direction (`actionplan -> period`). The caller (a future
HTTP/export consumer, or — today — the integration test) wires
`apStore.PeriodSnapshotLister()` into `perStore.Snapshot(...)`.

**Confidence: high.** `go build ./...` + `go vet -tags=integration ./...` clean;
no cycle. This is the established pattern in the same file family.

### D2 — Add a `FrozenView` assembly point rather than overloading `ControlState`

The slice-028 `period` package had no method producing "remediation commitments
in scope at freeze time" — `ControlState` is a per-control evidence-horizon read,
and the freeze hash inputs are evidence-ids + control-ids only. Rather than
contort an existing method, I added a dedicated `Snapshot` returning a
`FrozenView` struct that today carries `ActionPlans` and a `Frozen`/`Horizon`
pair. The struct is the explicit extension point a future slice can grow
(e.g. an evidence/control-state participant) without re-plumbing the read.
**Confidence: high.**

### D3 — AC-5 asserts horizon-MEMBERSHIP invariance, not per-field row reconstruction (honest scope)

P0-384-5 (per the slice-384 narrative + D5) is precisely: "a live edit today
never mutates _what a past frozen snapshot would return_, because the snapshot
read is `created_at`-horizoned and the live `UPDATE` path is independent." That
guarantee is about **which plans are in scope at `frozen_at`** (set membership),
not about reconstructing each plan's field values _as they were at `frozen_at`_.
`actionplan.Store.ListSnapshot` returns the CURRENT row of plans created on/before
the horizon — it does not (and slice 384 did not build) a temporal
point-in-time row reconstruction; the `action_plan_audit_log` before/after trail
is where per-field history lives.

So the AC-5 integration test asserts the honest, load-bearing invariant: after a
live `draft -> in_progress` edit of a pre-freeze plan, the frozen view's plan
**set** is unchanged (same membership, same count, same horizon, no post-freeze
leak), and the live `Get` reflects the edit independently. Full per-field
point-in-time reconstruction of a frozen action-plan row is a deferred concern
(it would need a temporal/as-of read the foundation doesn't provide); it is NOT
in this slice's scope and is noted here rather than silently implied by a
stronger-looking assertion. **Confidence: high** that this matches the documented
P0-384-5 contract; **medium** that a maintainer may later want true as-of field
reconstruction — that would be its own slice (temporal action-plan read).

### D4 — AC-3 (OSCAL AP/AR) is N/A: nothing to re-source

Inspected the OSCAL aggregate path (`internal/oscal/aggregate.go`,
`cmd/atlas-oscal`, `oscal-bridge`). The POA&M aggregate (`aggregate.poamInput()`)
derives POA&M items from **failing control evaluations** as of the frozen
horizon (`a.failingEvals` + `defaultRemediationWindow`), with an explicit code
comment that this stands "until the real remediation-tracking slice lands." No
OSCAL file imports `internal/actionplan` or calls `ListSnapshot`; action plans
are NOT a participant in the OSCAL AP/AR aggregate at all.

Therefore AC-3 is **N/A for this slice**: there is no live-vs-frozen action-plan
source in the OSCAL path to redirect. Sourcing OSCAL POA&M items from the frozen
action-plan snapshot is exactly the slice-384 named spillover #1 ("OSCAL POA&M
export"), which is its own slice and explicitly out of scope here. Building it
now would be scope creep into a deferred slice. I did NOT touch `aggregate.go`.
**Confidence: high.** (Per the spillover policy I did not re-file: slice 384's
decisions log + slice doc already record OSCAL POA&M export as the named future
item; this slice references it rather than duplicating the file.)

### D5 — No schema change (confirmed, as the spec predicted)

`action_plans.audit_period_id` (NULL FK to `audit_periods(id)`) and
`ListActionPlansSnapshot` (the `created_at <= frozen_at` query) already exist on
`main` from slice 384's migration `20260612070000_action_plans.sql`. This slice
is pure read-side wiring; it adds Go code only. No new migration, no slice-002
fixture patch. A migration here would have been a smell, exactly as the slice
doc warned. **Confidence: high.**

### D6 — Tests live in `actionplan_test`, not `period_test`

The new integration test exercises the cross-package wiring
(`actionplan.Store.PeriodSnapshotLister()` injected into `period.Store.Snapshot`).
`actionplan_test` can import both concrete packages with no cycle; it also reuses
the slice-384 actionplan suite's `seedUser` / `validCreate` / `ctxFor` helpers.
The pure-Go branch coverage (nil-lister guard, struct shape) lives in
`internal/audit/period/snapshot_test.go` (no build tag, `t.Parallel()`), per the
slice-353 Q-2 pure-Go-pre-DB convention. **Confidence: high.**

---

## Acceptance criteria — disposition

| AC   | Status | How                                                                                                                                                                                                                          |
| ---- | ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AC-1 | PASS   | `period.Store.Snapshot` draws action plans via the injected `ListSnapshot(frozen_at)` seam; `TestPeriodSnapshot_IncludesPreFreezeExcludesPostFreeze` asserts the pre-freeze plan appears and the horizon equals `frozen_at`. |
| AC-2 | PASS   | Same test: a plan created AFTER the freeze is excluded from the snapshot; live state continues independently.                                                                                                                |
| AC-3 | N/A    | OSCAL AP/AR POA&M derives items from failing control evaluations, not action plans (D4). No live action-plan source exists to re-point; sourcing it is slice-384 spillover #1, out of scope.                                 |
| AC-4 | PASS   | `TestPeriodSnapshot_IncludesPreFreezeExcludesPostFreeze` — freeze, create-after, assert snapshot excludes the new plan and includes the pre-freeze plan.                                                                     |
| AC-5 | PASS   | `TestPeriodSnapshot_LiveEditAfterFreezeDoesNotChangeSnapshot` — a `draft -> in_progress` edit after freeze leaves the frozen view's plan membership + horizon unchanged while the live `Get` reflects the edit (D3 framing). |

---

## Verification

- `gofmt -l` clean on all touched files; `go build ./...` exit 0; `go vet ./...`
  exit 0; `go vet -tags=integration ./internal/audit/period/... ./internal/actionplan/...`
  clean.
- **Live-tested.** Spun up `postgres:16-alpine` in Docker, applied
  `migrations/bootstrap/01-roles.sql` + every `migrations/sql/*.sql` (the CI
  bring-up), set the `atlas_app` password, and ran:
  - `go test -tags=integration -p 1 ./internal/actionplan/... ./internal/audit/period/...`
    → both packages `ok` (new slice-748 tests + the full slice-384 + slice-028
    suites, no regression).
  - The slice-384 `TestListSnapshot_FreezeHorizon` (the foundation) still passes.
- Pure-Go unit: `go test ./internal/audit/period/...` → `ok`.
- Coverage: new functions measured at `Snapshot` 68.2%, `PeriodSnapshotLister`
  87.5%, `ListSnapshot` 90.0% (merged unit+integration); per-package totals stay
  above floors (`internal/actionplan` ≥ 66, `internal/audit/period` ≥ 70). No
  floor lowered.
- `scripts/check-openapi-drift.sh` → exit 0 (280 routes; no route added).

---

## Detection-tier classification (slice 353, Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the slice. The wiring
  was built test-alongside; the integration suite ran green live on the first
  complete run against real Postgres.
- `detection_tier_target`: `integration` — the load-bearing risks (freeze-horizon
  membership, post-freeze exclusion, live-edit invariance, RLS-scoped period
  resolution) are exactly the time-window/RLS class the Go integration tier is
  built to catch, and they are covered there. The nil-lister guard + struct shape
  are additionally covered at the fast `unit` tier.

Companion fix-forward note: zero fix-forward commits — everything verified before
the PR opened.

---

## Spillover slices filed

None. The one adjacent future item — sourcing OSCAL POA&M items from the frozen
action-plan snapshot — is the already-named slice-384 spillover #1 ("OSCAL POA&M
export"); per the continuous-batch spillover policy I reference it rather than
re-filing a duplicate. No new out-of-scope finding emerged.
