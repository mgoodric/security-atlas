# 104 — `GET /v1/anchors?include=state` extension for `/controls` list view

**Cluster:** Backend / API
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 098, captured as follow-up per continuous-batch policy.

Slice 098 ships the `/controls` list view per the design captured in slice 093 (mockup `Plans/mockups/controls.html` + design doc `Plans/canvas/12-ui-fill-in-design-decisions.md` §7). Design doc §7 binds the table columns to `anchorWire` + `stateWire`:

| column           | wire source                                                  |
| ---------------- | ------------------------------------------------------------ |
| scf_id           | `anchorWire.scf_id` (`internal/api/anchors/handlers.go`)     |
| name             | `anchorWire.name`                                            |
| family           | `anchorWire.family`                                          |
| result           | `stateWire.result` (`internal/api/controlstate/handlers.go`) |
| freshness_status | `stateWire.freshness_status`                                 |
| last_observed_at | `stateWire.last_observed_at`                                 |

On `main`, `GET /v1/anchors` returns the SCF catalog (~1,400 rows per tenant) keyed by SCF-anchor UUID. `GET /v1/controls/{id}/state` returns per-control state keyed by **control_id** (the tenant's instantiated control), not by anchor_id. There is no joined endpoint. Slice 098 explicitly rejected per-row fan-out (1,400 anchors × per-row state call) per the slice text itself:

> if `GET /v1/anchors` returns ~1,400 anchors per tenant and each state call adds latency, surface the `?include=state` extension as a backend follow-on slice rather than making 1,400 calls.

Slice 098 therefore ships with state cells rendered as `—` (slice 041 precedent — labeled empty when the endpoint doesn't exist; no fabrication, anti-criterion P0-A1). This slice closes that gap by adding the joined endpoint so the `/controls` page can render real state for every anchor with one round-trip.

## Acceptance criteria

- [ ] AC-1: `GET /v1/anchors?include=state` returns the anchor list joined to the tenant's current control state per anchor; when the tenant has no control instantiated for an anchor, the `state` field is `null`.
- [ ] AC-2: The joined response shape is `{ anchors: [{ ...anchorWire, state: stateWire | null }] }` — additive to `anchorWire`, no breaking change to `?include=` omitted callers.
- [ ] AC-3: `?include=state` resolves the state column via the same `eval.Engine.ControlState` path as `GET /v1/controls/{id}/state` — no parallel evaluation path.
- [ ] AC-4: The handler runs a single CTE / join query — NOT a per-anchor loop calling the engine (the whole point of the extension).
- [ ] AC-5: RLS enforces tenant isolation on the joined query — the integration test asserts that Tenant A's request never sees Tenant B's state rows (canvas §5.4).
- [ ] AC-6: When multiple controls satisfy one anchor (rare but legal), the joined state aggregates per the canvas §6 rollup rule — slice 098 only needs a single representative row, so the "worst-state cell" wins (fail > insufficient_evidence > pass > not_applicable).
- [ ] AC-7: Unit tests for the per-anchor join (single anchor, single control: state passes through; single anchor, no control: state is null; single anchor, two controls with conflicting state: worst-state wins).
- [ ] AC-8: Integration test covering the full RLS round-trip (Tenant A vs Tenant B) plus the empty-state branch.
- [ ] AC-9: `web/lib/api.ts` `fetchControlsList` updated to call `?include=state` and parse the joined shape — `web/app/(authed)/controls/page.tsx` removes the `state: null` placeholder and renders real state cells.
- [ ] AC-10: `web/app/(authed)/controls/filters.ts` `applyFilters` already accepts the joined state shape (slice 098 sketched the type); the only change is the BFF now populates it.
- [ ] AC-11: CHANGELOG entry under `[Unreleased]` announcing the new `?include=state` query parameter.

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** the join query respects the tenant GUC. The handler does not source tenant from any caller-supplied field.
- **Invariant 1 (one control, N framework satisfactions):** the join derives state per **anchor** (the catalog spine node), not per framework satisfaction; this preserves the graph shape.
- **Anti-criteria honored:** the join is one query, not 1,400 — the slice exists specifically to avoid the per-row fan-out anti-pattern.

## Canvas references

- `Plans/canvas/03-ucf.md` (anchor as spine node)
- `Plans/canvas/06-risk.md` §6.2 (operational_score / state rollup rule)
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §7 (binding the column set to anchorWire + stateWire)
- `internal/api/anchors/handlers.go` (`anchorWire`, `listAnchors`)
- `internal/api/controlstate/handlers.go` (`stateWire`)
- `internal/eval/engine.go` (`Engine.ControlState`)

## Dependencies

- **098** (`/controls` list view) — `ready`/`in-review` at time of writing — provides the BFF + page consumer that will pick up the joined shape
- **012** (control evaluation engine) — merged — provides `eval.Engine.ControlState`
- **033** (RLS + tenancymw) — merged — provides the tenant GUC the join trusts

## Anti-criteria (P0 — block merge)

- Does NOT add a per-anchor loop in the handler (the slice exists to avoid it).
- Does NOT change the existing `?include=` omitted response shape — additive only.
- Does NOT route the join through application code that would bypass RLS — the query runs under the tenant GUC the middleware sets.
- Does NOT fabricate state for anchors with no tenant control attached — those rows return `state: null` and the UI renders `—` honestly (slice 098 P0-A1 precedent).

## Skill mix

- sqlc + Atlas (joined CTE query, recursive if the state aggregation needs to walk the control → evaluations ledger)
- Postgres RLS (per-tenant join verification)
- Negative tests (tenant isolation under join shape)

## Notes

The other four list-view slices (099 evidence, 100 risks, 101 policies, 102 audits) face an analogous fan-out question. Each will file its own `?include=` extension slice if it surfaces — the pattern is the same: ship the page against the v1 endpoint, file a backend slice to lift the per-row fan-out anti-pattern.
