# #2138 — Risk detail layout

Ticket: [#2138](https://github.com/mgoodric/security-atlas/issues/2138)
(parent [#2072](https://github.com/mgoodric/security-atlas/issues/2072)).
Builds on #2135 (the dashboard's section pattern), #2316 (`<Markdown>`),
#2083 (`enumLabel`), #2085 (`<Person>`) and #2556 (viewers read risks).

detection_tier_actual: playwright
detection_tier_target: playwright

The bug the new spec caught: the acceptance card and History printed
people as raw ids (`by 00000000-…`), and `risks-acceptance-2105.spec.ts`
asserted the id. The raw-value scan in `risk-detail-layout-2138.spec.ts`
was run against the id-printing History and failed before it passed.

## D1. A control's live state is the worst cell, read per control

Mitigation shows each linked control's state from
`GET /v1/controls/{id}/state`, one query per control, rolled up
worst-cell-wins with the ranks `worst_per_control` uses in
`internal/db/queries/controls.sql` (fail > inconclusive > excepted >
pass > na; expired > stale > no_evidence > fresh). The alternative, one
`/v1/controls?include=state` page, would cap at 500 controls and read
every control to show two. Per-control queries also isolate failure: a
control whose state does not load says "State unavailable" on its row.
The rollup is a pure helper (`components/risks/control-state.ts`) pinned
by vitest.

## D2. Write affordances follow `is_approver`

Risk writes are admin and grc_engineer (`policies/authz/grc_engineer.rego`;
viewer and control_owner do not write risks). `/v1/me`'s `is_approver` is
exactly that set, by flag or live role, so the page gates Edit, the
lifecycle actions, the owner picker, Link/Unlink, Attach/Detach and Add
action plan on it. It fails closed: nothing shows while `/v1/me` loads or
if it fails. `owner_roles` is not read (#2571's review). The platform
still refuses a write from anyone else; this only stops offering it.

## D3. "System" is resolved in `resolvePerson`

`people.Directory.Name` on the platform reads `system` and
`system:<process>` as "System"; the browser's `resolvePerson` read them
as "Unknown". It now matches, for every `<Person>`, not only this page.

## D4. History keeps ids out of its pure formatter

`historyChanges` marks id-valued fields (`owner_user_id`, `proposed_by`,
`accepter`, `closed_by`, `control_id`, `org_unit_id`) with `ref`, and the
section resolves them: people through `<Person>`, controls through the
tenant control list, org units through the hierarchy's org-unit list.
Each list is fetched only when an entry needs it. An id neither list
knows reads "Unknown control" or "Unknown org unit". An unmapped action
or field key is humanized rather than printed raw.

## D5. Action plans unchanged

`linked-action-plans.tsx` is untouched (#2328 is wiring labels there);
the page passes no `addHref` for a non-approver. The
`GET /v1/risks/{id}/action-plans` gate (#2507) is unchanged.

## D6. The roll-up function reads as a word

`max`, `weighted_max` and `sum` show as "highest", "weighted highest"
and "sum". They are not in `ENUM_LABELS` because they are a page-local
phrasing of a computed field, not a stored enum.
