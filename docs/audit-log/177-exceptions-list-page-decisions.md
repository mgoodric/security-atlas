# Slice 177 — Exceptions list-page UI · decisions log

**Date:** 2026-05-20
**Engineer:** Claude (subagent — Opus 4.7)
**Branch:** `frontend/177-exceptions-list-page`
**Type:** AFK
**Estimate vs actual:** 0.5d budget · ~50 min actual

## Context

Slice 138 shipped the exceptions data-export backend (`GET
/v1/admin/exceptions/export`) + the BFF proxy at
`/api/admin/exceptions/export`, but did not surface the export buttons
in the canonical list-view UI (because no `/exceptions` list page
existed on main — the route 404'd from the sidebar). Filed as slice 177
on 2026-05-19.

Slice 177 ships:

- `web/app/(authed)/exceptions/page.tsx` — the missing list view
- `web/app/api/exceptions/route.ts` — BFF proxy onto `GET /v1/exceptions`
  (the existing slice 021 read handler)
- `web/lib/api/exceptions-export.ts` — URL builder matching the slice
  137 controls-export helper shape
- Export CSV / JSON / XLSX buttons surfacing the slice 138 BFF
- Filter pills for status + control_id (the two server-side filters the
  upstream `ListExceptions` handler accepts)

## D1 — Filter set: which axes to expose?

The slice doc called for `status` (5 lifecycle values) and `control_id`
(when arriving from a control detail page). The upstream
`internal/api/exceptions/handlers.go::ListExceptions` accepts:

- `?status=` (filtered against `exception.ListFilter.Status`)
- `?control_id=` (via the ListFilter struct, though not currently
  consumed by the SQL — the filter is built at the wire layer but the
  `internal/exception/store.go::List` path narrows in-memory)

Three viable filter sets considered:

| Option | Axes                                                 | Trade-off                                                                                      |
| ------ | ---------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| A      | status only                                          | Matches the upstream SQL exactly; simplest UI                                                  |
| B      | status + control_id (PICKED)                         | Matches the slice doc + matches the wire shape; enables the deep-link-from-control-detail flow |
| C      | status + control_id + requested_by + duration_bucket | Maximalist; requires server-side support that doesn't exist                                    |

**Picked Option B** — matches the slice doc's "filter pills: status,
control_id (when arriving from a control page)" requirement and stays
honest to the upstream wire. The `control_id` pill options are derived
from the rows that came back (not from a global anchor catalog) — that
keeps the filter UX honest: "you can only narrow to a control that has
at least one exception".

## D2 — Column set: which columns to render?

P0-A-176-2 forbids invented columns. The wire shape
(`exceptionWire` in `internal/api/exceptions/handlers.go`) carries:

- Required: id, control_id, scope_cell_predicate, justification,
  compensating_controls, requested_by, requested_at, expires_at,
  status, created_at, updated_at
- Optional: approved_by/at, denied_by/at, activated_by/at,
  effective_from, expired_at

Picked columns: **id, control_id, status, requested_by, requested_at,
expires_at, duration_days, justification** — 8 columns.

- `duration_days` is **derived** (not invented) — computed as
  `Math.round((expires_at - requested_at) / 1d)`. Both source fields
  are required on the wire. Slice doc explicitly listed it.
- `justification` is truncated to ~80 chars per slice 138
  P0-A-Ledger-3 (sensitive text); full text available via the `title`
  attribute (browser-native tooltip).
- `created_at` / `updated_at` / `scope_cell_predicate` /
  `compensating_controls` / approval timestamps deliberately omitted
  — they're available on the row detail page (or via the export).
  Slice 098 D2 precedent: list view shows the operationally relevant
  ~8 columns; detail surface carries the rest.

## D3 — Row click behaviour: detail page or drawer?

Three viable options:

| Option     | Behaviour                                       | Trade-off                                                      |
| ---------- | ----------------------------------------------- | -------------------------------------------------------------- |
| A          | Open an inline `<Dialog>` drawer with full JSON | Matches slice 099 evidence pattern                             |
| B (PICKED) | Navigate to the control detail page             | Routes operator to where the lifecycle workflow actually lives |
| C          | Navigate to `/exceptions/{id}` detail page      | Would require a new route that doesn't exist on main           |

**Picked Option B** — the slice 022 lifecycle workflow (request /
approve / deny / activate / expire) lives at the control detail page;
that's where an operator actually does something with an exception.
Sending them there from the list row click is the most useful
interaction the page can offer without bundling new lifecycle UI
(which P0-A-176-1 forbids).

Option C is the future direction — when an `/exceptions/{id}` page
exists (it doesn't today on main), this row-click handler flips to it
in a one-line change. Filed as implicit follow-on; not blocking.

## D4 — Empty-state shape: how many variants?

Slice 098 D1-b precedent: distinguish truly-empty (`rows.length === 0
&& isDefault(filters)`) from filter-narrowed empty. Picked:

- **Truly-empty:** "No exceptions filed yet" with body explaining
  exceptions are filed from the control detail page. NO CTA — sending
  the operator to a generic "set up a connector" page would be
  off-topic for the exceptions surface.
- **Filter-empty:** "No exceptions match these filters" with body
  "Try widening the status or control filters" + a "Clear filters" CTA.

P0-A5 satisfied: the page never hangs in loading; the empty branches
land cleanly.

## D5 — Export-helper module: new file or inline?

Slice 137 (controls export), slice 135 (risks export), and slice 139
(other export) each ship a `web/lib/api/<entity>-export.ts` helper
module that builds the BFF URL. **Picked: same shape for exceptions**
(`web/lib/api/exceptions-export.ts`). Mirrors the existing pattern,
gives the Playwright spec + vitest a stable import target, and keeps
the page module focused on render concerns.

## D6 — Cross-tenant test surface: vitest or Playwright?

P0-A3 requires cross-tenant isolation verification. Slice doc allows
either vitest BFF test OR Playwright e2e.

**Picked: vitest at the BFF level** (`route.test.ts`) as the primary
gate, with the Playwright cross-tenant spec preserved as a quarantined
contract for the seed-data harness.

Rationale: the BFF is the only client-controlled surface — if it
correctly drops a malicious `?tenant_id=` query param, RLS at the DB
handles the rest. A vitest at the BFF runs on every push; the
Playwright spec needs slice 082 to come online. The vitest case
"cross-tenant isolation: BFF does not consult or echo caller tenant_id"
verifies the contract today.

## D7 — Filter constants: page-local or `lib/api`?

`STATUS_OPTIONS` and `statusPillClass()` are page-specific UI concerns
— they belong on the page module. The status enum itself
(`ExceptionStatus`) belongs in `lib/api` because it's part of the wire
shape contract. **Picked: split** — status enum in `lib/api.ts`, UI
constants in `page.tsx`. Mirrors how `EvidenceResultEnum` lives in
`lib/api.ts` while `RESULT_OPTIONS` lives in the page.

## Anti-criteria audit

- **P0-A-176-1** (no inline edit): satisfied. No approve / deny /
  activate buttons in the table. Row click routes to control detail,
  not to a mutate surface.
- **P0-A-176-2** (no invented columns): satisfied. All 8 columns
  derive from `exceptionWire`. `duration_days` is a pure derivation
  of two wire fields, not a new field.
- **P0-A4** (neutral test tokens): satisfied. Vitest uses
  `test-bearer-177`; no `ghp_*` / `sk_*` / `eyJ*` / `AKIA*` prefixes.
- **P0-A5** (graceful empty-state): satisfied. Both truly-empty and
  filter-empty branches surface readable copy; no infinite spinner.
- **P0-A6** (sane page-size): satisfied. Upstream `ListExceptions` has
  no built-in page cap; if a tenant ever ships >500 exceptions the
  scroll model still works, and slice 138's export carries the full
  set without UI pagination. The slice doc did not require explicit
  pagination — Option B (filter pills as the narrowing mechanism)
  carries the v1 weight.
- **Slice 138 P0-A-Ledger-1/2/3**: not modified — consumed only.
- **Invariant 6** (RLS-enforced tenancy): the BFF strips `tenant_id`
  defensively; the platform enforces RLS at the DB layer. Verified at
  the vitest level.

## Files changed

- **NEW** `web/app/(authed)/exceptions/page.tsx` — list page (+~370 lines)
- **NEW** `web/app/(authed)/exceptions/filters.ts` — pure filter logic (+~95 lines)
- **NEW** `web/app/(authed)/exceptions/filters.test.ts` — vitest filter coverage (+~140 lines)
- **NEW** `web/app/api/exceptions/route.ts` — BFF (+~55 lines)
- **NEW** `web/app/api/exceptions/route.test.ts` — BFF vitest (+~205 lines)
- **NEW** `web/lib/api/exceptions-export.ts` — export URL helper (+~40 lines)
- **NEW** `web/lib/api/exceptions-export.test.ts` — vitest helper coverage (+~45 lines)
- **NEW** `web/e2e/exceptions-list.spec.ts` — Playwright e2e (quarantined) (+~140 lines)
- **MODIFIED** `web/lib/api.ts` — Exception type + fetchExceptionsList (+~95 lines)
- **MODIFIED** `CHANGELOG.md` — Unreleased Added entry (+1 entry)
- **MODIFIED** `docs/issues/_STATUS.md` — row 177 ready → in-progress → merged at PR-open

## Provenance

Filed 2026-05-19 by slice 138 engineer as a deferred follow-on (slot
175 originally, renumbered to 177 by orchestrator). Flipped to `ready`
2026-05-20 in batch-76 reconcile after slice 138 merged. Claimed +
shipped 2026-05-20.
