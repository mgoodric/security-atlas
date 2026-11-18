# #2392 — anchor history on control detail, legacy-anchor flag on the controls list: decisions log

detection_tier_actual: none
detection_tier_target: none

Split out of #1778 (its D1). No migration: both features read `scf_remap_decisions` (#1776) and `scf_remap_proposals` (#1775) as they are.

## D1 — The read lives in `controldetail`, beside the evaluation history

`GET /v1/controls/{id}/anchor-history` is a control-detail read like `/history`, so it sits in `internal/api/controldetail` and reuses that package's pieces:

- the live-role gate (`requireControlRead`, `authz.HasAnyTenantRole`, #2507);
- the opaque `(timestamp, id)` keyset cursor and the 1..200 limit (default 50).

The route has a row in `routes2428` and in the controldetail site of `gateSites2447`, so the handler gate is checked against the real OPA bundle. OPA maps the path to `controls` / `read`, which viewer.rego admits. The integration test reads it as a live viewer.

## D2 — The whole bundle, across revisions

An approval supersedes the control: the decision's `legacy_control_id` is the old revision and `new_control_id` the new one. The query resolves the path id to its `bundle_id`, then returns every decision whose legacy revision is in that bundle. The history therefore reads the same from the old revision's id and from the new one's, which the integration test checks from both. `legacy_version` and `new_version` come from joining the two revisions, so the page can say "revision 2" without showing an id.

## D3 — Another tenant's control is a 404, not an empty list

The evaluation-history read answers 200 with no rows for an id it cannot see. This read looks up the bundle first, under RLS and with the credential's tenant id. No row means 404, which is what the dispatcher's rules asked for. An unknown id gets the same answer, so a 404 says nothing about whether the id exists in another tenant.

## D4 — The list flag is a column of the paged list query, with a filter

`legacy_anchor` is an `EXISTS` over `scf_remap_proposals` on the active control row with status `open` or `rejected`, computed in `ListActiveControlsWithState`. No per-row fetch. The rule is the review page's `isPending`. A pending proposal on a superseded revision is stale (#2325), so only the active row counts. `?legacy_anchor=true` narrows both the page and `CountActiveControls` with the same predicate, and the BFF passes it through. The page has no filter pill for it yet. The badge links to `/controls/remap`, where the reviewer settles it.

## D5 — The Playwright spec shapes the BFF with page.route

This keeps #1778 D7's call. That log suggested a real-stack seed harness here, one that imports two releases. The spec does not add one. Seeding the platform side is covered by the Go integration tests, which seed a catalog, two revisions, a proposal and two decisions directly and run on real Postgres:

- the field and its filter;
- the read's bundle scope;
- the system actor;
- paging;
- the cross-tenant 404;
- the viewer read.

The spec makes one decision through the real `/controls/remap` page, against an in-memory fixture that also backs the list and the History tab. It shows the page-level behaviour:

- the entry appears after the decision;
- the reviewer is shown by name, and a `system:` principal reads "System";
- codes read as `code@release`, with no UUID in the section;
- the list flag clears for the decided control and stays on the undecided one.

A two-release seed harness is still worth having, for this spec and for #1778's. It is a separate piece of work.
