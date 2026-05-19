# 151 — Risk creation form missing control-link UI (slice 105 incomplete)

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0:

> "If I try to create a risk, I get this message but there is nowhere for me to link a control on the add risk page: 'Could not create risk · risk: treatment validation: treatment=mitigate requires at least one linked_control_id'"

Slice 019 (risk CRUD backend) validates `treatment=mitigate` requires ≥1 linked control (correct per canvas §6 residual-calculation requirement). Slice 105 (Risk creation form) shipped the form but omitted the control-link picker UI.

**What this slice ships:**

- Add control-link multi-select to `/risks/new` form.
- Bind to existing `GET /v1/controls` list endpoint (filter by tenant, paginated).
- Form validation: require ≥1 control link when `treatment=mitigate`.
- Form submission: POST with `linked_control_ids` array.

## Acceptance criteria

- [ ] AC-1: New `<ControlMultiSelect>` component in `web/components/` using shadcn `<Command>` + `<Popover>`.
- [ ] AC-2: Component fetches controls via existing `GET /v1/controls` (paginated; first 50 + search-as-you-type).
- [ ] AC-3: Component integrated into `web/app/(authed)/risks/new/page.tsx`; renders when `treatment === 'mitigate'`.
- [ ] AC-4: Form-side validation: cannot submit with `treatment=mitigate` + 0 selected controls; field-level error.
- [ ] AC-5: Form submission posts `linked_control_ids` to `POST /v1/risks` body.
- [ ] AC-6: Playwright e2e: create a risk with `treatment=mitigate` + linked control; assert it appears in the risk list.
- [ ] AC-7: vitest unit tests for the ControlMultiSelect component.
- [ ] AC-8: CHANGELOG entry: "Risk creation form: control-link selector for mitigate treatments (#151; slice 105 follow-on)".

## Dependencies

- **#019** Risk CRUD backend (merged) — accepts `linked_control_ids`.
- **#020** Risk-control linkage (merged) — backend processes the array.
- **#105** Risk create UI (merged, incomplete) — extends.

## Anti-criteria (P0 — block merge)

- **P0-RISK-1** UI MUST enforce the validation client-side (don't rely on backend error display); user experience is "field-level error before submit", not "submit then see error".
- **P0-RISK-2** NO scope creep into other risk fields or hierarchy linkage.
- **P0-RISK-3** NO vendor-prefixed test fixture tokens.

## Notes for the implementing agent

Frontend-only. Pickup time ~3 hours.

Provenance: filed 2026-05-18 from operator v1.10.0 report.
