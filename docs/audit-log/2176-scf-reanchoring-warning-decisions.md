# #2176 — SCF re-anchoring: API endpoint and dashboard warning: decisions log

detection_tier_actual: none
detection_tier_target: none

Split from #1999 (#1971 D3, Matt's call of 2026-11-14: flag for re-anchoring). #1999 shipped the rule and `ListControlsOnLegacySCFAnchors`; this adds the tenant read and the dashboard warning on top of it. No migration.

## D1 — One classifier for the upgrade and the endpoint

`scfimport.Classify` applies the D3 rule to one row of `ListControlsOnLegacySCFAnchors` and returns `rebindable`, `needs re-anchoring` or `pinned`, with the remap note for a flagged row. `RebindControls` (the upgrade) now calls it instead of carrying the rule inline, and `scfimport.NeedsReanchoring` (the endpoint) filters on it. The query lists every active control on a legacy anchor, so without a shared classifier the dashboard would count controls the upgrade re-binds or deliberately leaves alone.

## D2 — Pinned controls are excluded, not listed separately

A bundle that declares `scf_release`, or names its anchor by UUID (#1770, `scfimport.IsPinned`), is bound to that release on purpose. Nothing needs doing, so it is not a warning, and the endpoint leaves it out. Rebindable rows (same code, same title) are left out too: the next boot's `RebindControls` or the next re-upload moves them. A control whose stored `control.yaml` does not parse **is** listed, with the "re-upload the bundle" note, because its pin cannot be read and a person has to act.

A separate "pinned" list was not added: no ticket asks for it, and the upgrade output already counts pinned controls.

## D3 — Route, authz and tenancy

- **Route:** `GET /v1/controls/needs-reanchoring`, a literal segment registered next to `/v1/controls/export`, ahead of the `/v1/controls/{id}/...` patterns.
- **Authz:** `BuildInput` derives `read` on `controls`, the same OPA decision as `GET /v1/controls`. `internal/authz/needs_reanchoring_2176_test.go` checks that every role (admin, grc_engineer, auditor, control_owner, viewer, none) gets the same answer for both paths. Auditors and viewers are admitted, and a caller with no role is denied. No new rego.
- **Tenancy:** RLS through `control.Store.inTx` (the tenant GUC), plus the explicit `tenant_id` argument the query already takes. The integration test drives the handler through the non-owner app role. It checks that one tenant's flagged control is not in another tenant's list.
- **Role filter on the count:** the banner's count is the length of the list this same read returns, so it goes through the same gate. A 403 hides the banner (`retry: false`, no error state), the same way a role that cannot read controls sees no control list.

## D4 — The BFF is `/api/controls/needs-reanchoring`, not `/api/dashboard/...`

The dashboard's first load is prefetched server-side, and `dashboard-server-component.spec.ts` asserts it fires no `/api/dashboard/*` request (slice 380). The banner is an optional client read, so it sits under `/api/controls`, and the prefetch contract holds unchanged. It reuses the generic `dashboardProxy` helper.

## D5 — Warning UX

- **Placement and role:** the banner sits at the top of the dashboard's PageShell body, above framework posture. It renders as an `Alert` with `role="status"` rather than the component's default `role="alert"`. It is a standing condition seen on page load, not an interruption, so it is announced politely.
- **Copy:** the title is "N control(s) need re-anchoring". The description says why it matters (no framework credit) and names the target release when there is one. The link reads "Review the control(s)".
- **When it shows:** never during loading, on an error, or at zero.
- **List page:** `/controls/needs-reanchoring` (PageShell) shows the control (linked to its detail page), its bundle, its legacy anchor `code@release` with that anchor's title, and the remap note. It has its own document title for the route sweep.

## D6 — "Cleared once re-anchored" needs no new state

Re-anchoring is a re-upload of the bundle on a current-release anchor. It supersedes the flagged row, and the query lists only `superseded_by IS NULL` rows, so the control leaves the list and the banner count drops. The integration test (`TestNeedsReanchoring_ListsFlaggedControlsAndClearsOnReanchor`) uploads through `control.Store.Upload` and checks the list is empty afterwards, while the other tenant's list is unchanged. The Playwright spec covers the UI side: the empty list shows no banner, and the list page shows its empty state.
