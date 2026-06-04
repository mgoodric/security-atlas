# 448 — Bulk operations + saved filter-views (operator ergonomics, controls list)

**Cluster:** Frontend
**Estimate:** M (1-2d)
**Type:** JUDGMENT (UX shape of multi-select + saved-view persistence)
**Status:** `ready`

## Narrative

The list views (controls / evidence / risks / policies) gained filter pills via
slices 224 / 234 / 238 / 244 — an operator can narrow a list, but then has to
**act on each row one at a time**, and the filter they painstakingly built is
**gone the moment they navigate away**. A solo security leader triaging 50+
controls hits this immediately: "assign all these 12 unowned controls to me" is
12 clicks, and "show me my weekly triage view" has to be re-filtered every time.

This slice ships operator ergonomics on **ONE list surface — controls** (the
`web/app/(authed)/controls` page): **multi-select + one bulk action**
(bulk-assign owner) **+ save-current-filter-as-view** persisted per user. It is
the tracer bullet that proves the bulk-op + saved-view pattern; the other three
list surfaces are explicit follow-ons.

**Scope discipline.** **Controls list only, one bulk action (assign owner), one
saved-view capability.** It does **not** add bulk delete / bulk status-change /
bulk archive (each is a separate action with its own authorization surface —
follow-ons), does **not** generalize to evidence/risks/policies in this slice,
and does **not** add shared/team views (per-user persistence only). **Follow-on
slices:** bulk actions on evidence/risks/policies; additional bulk actions
(status, archive); shared/team saved views.

## Threat model (STRIDE)

A bulk operation is an **authorization amplifier**: the classic failure is
checking the caller's permission against the _first_ item and then applying to
_all_ selected items — letting a caller mutate rows they cannot individually
touch, or rows in another tenant. Saved views persist user-entered filter state.

**S — Spoofing.** The bulk-assign + saved-view endpoints are authenticated +
role-gated (reuse the controls-list authz; assign-owner requires the role that
can edit a control's owner today).
**Mitigation:** reuse the existing controls authz + OAuth-AS JWT validation; no
new ingress scheme.

**T — Tampering.** The bulk action takes a set of control IDs + a target owner.
A caller could submit IDs they did not see / do not own, or an invalid owner.
**Mitigation:** the bulk action validates the target owner is a real user in the
tenant; every submitted control ID is validated to exist + be in the caller's
tenant before mutation; the saved-view filter payload is validated against the
known filter schema (no arbitrary JSON that becomes a query).

**R — Repudiation (PRIMARY for bulk).** A bulk change touches many rows at once;
the audit trail must record the bulk action, not silently mutate N rows.
**Mitigation:** the bulk-assign writes an audit-log entry capturing the action,
the actor, the target owner, and the **set** of affected control IDs (one bulk
event referencing N items, or N per-item entries — either, but auditable). A
later "who reassigned all these?" question is answerable.

**I — Information disclosure.** Saved views persist per user; one user's saved
view must not be readable by another user / tenant.
**Mitigation:** saved views are RLS-scoped to (tenant, user); a saved view's
filter payload carries no cross-tenant IDs (it is filter criteria, not data).

**E — Elevation of privilege (PRIMARY for bulk).** The amplifier risk: the
authz check must be applied **per item**, not once for the batch.
**Mitigation:** the bulk-assign **re-checks role + tenant for every control in the
set** (not just the first); an item the caller cannot mutate is rejected (the
whole batch fails, or that item is skipped + reported — decide + document), never
silently applied. An integration test proves a caller cannot bulk-assign a
control outside their tenant/role via the bulk path even if the single-item path
would also reject it.

**D — Denial of service.** An unbounded selection (select-all across thousands)
could make one request mutate an unbounded set.
**Mitigation:** the bulk action caps the selection size per request (paginate /
chunk above the cap); the mutation runs in a bounded transaction.

## Acceptance criteria

**Frontend — multi-select + bulk action**

- [ ] **AC-1.** The controls list (`web/app/(authed)/controls`) gains row
      multi-select (checkbox per row + select-all-in-view) that composes with the
      existing slice-224 filter pills.
- [ ] **AC-2.** A bulk-assign-owner action operates on the selected set: pick a
      target owner, apply to all selected controls.
- [ ] **AC-3.** The selection size is capped per request (chunked above the cap);
      the UI communicates the cap.

**Frontend — saved filter-views**

- [ ] **AC-4.** A "save current filter as view" affordance persists the active
      filter-pill state as a named, per-user saved view.
- [ ] **AC-5.** Saved views are listed + selectable; selecting one re-applies its
      filter state to the list.

**Backend**

- [ ] **AC-6.** A bulk-assign endpoint validates every submitted control ID
      (exists + caller's tenant) and the target owner (real tenant user) before
      mutation.
- [ ] **AC-7.** The endpoint **re-checks role + tenant per control** in the set
      (authorization is not checked once for the batch — threat-model E).
- [ ] **AC-8.** The bulk action writes an audit-log entry capturing actor +
      target owner + the set of affected control IDs (threat-model R).
- [ ] **AC-9.** A saved-views endpoint persists + lists views RLS-scoped to
      (tenant, user); the filter payload is validated against the known filter
      schema (threat-model T).

**Tests**

- [ ] **AC-10.** Integration test: bulk-assign applies to all selected controls +
      writes the audit-log entry.
- [ ] **AC-11.** **Per-item authz test:** a caller cannot bulk-assign a control
      outside their tenant/role via the bulk path; the bulk path is not weaker
      than the single-item path (threat-model E — load-bearing).
- [ ] **AC-12.** Integration test: a saved view persists + re-applies; another
      user cannot read it (threat-model I).
- [ ] **AC-13.** Integration test: an over-cap selection is rejected/chunked
      (threat-model D).
- [ ] **AC-14.** Frontend test (vitest/Playwright): multi-select + bulk-assign +
      save-view flows render and call the right endpoints.

**Docs**

- [ ] **AC-15.** A changelog entry; the bulk-op + saved-view pattern documented
      for the follow-on list surfaces.

## Constitutional invariants honored

- **#6 — Tenant isolation via RLS.** Bulk mutations + saved views are tenant
  (and, for views, user) scoped; per-item tenant re-check on the bulk path.
- **Repudiation discipline.** Bulk actions are audited (the bulk event references
  the affected set).
- **No anti-pattern violation.** Pure operator ergonomics — no scope creep into a
  trust center or any deferred surface.

## Canvas references

- `Plans/canvas/02-primitives.md` — Control primitive (owner, lifecycle).
- `Plans/canvas/07-metrics.md` / mockups — list-view + filter surfaces.
- (Filter-pill lineage: slices 224/234/238/244 — the surface this extends.)

## Dependencies

- **#224** (controls-list scope filter pills) — `merged`. The filter state the
  saved view persists + the surface multi-select composes with.
- **#190** (OAuth-AS JWT validation) — `merged`. The role gate.
- The single-item assign-owner path (existing control-edit authz) — reused as
  the per-item authorization the bulk path re-checks.

## Anti-criteria (P0 — block merge)

- **P0-448-1.** Does NOT check authorization once for the batch — per-item
  role + tenant re-check (threat-model E — proven by AC-11).
- **P0-448-2.** Does NOT silently apply a bulk change to a row the caller cannot
  individually mutate.
- **P0-448-3.** Does NOT mutate an unbounded selection — capped/chunked per
  request (threat-model D).
- **P0-448-4.** Does NOT skip the audit-log entry for the bulk action
  (threat-model R).
- **P0-448-5.** Does NOT let one user's saved view be readable by another
  user/tenant (threat-model I).
- **P0-448-6.** Does NOT add bulk delete / status-change / archive — assign-owner
  only; follow-ons.
- **P0-448-7.** Does NOT generalize to evidence/risks/policies or add shared/team
  views — controls + per-user only.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; per-item-authz test is
load-bearing) · `security-review` (bulk authorization amplifier + saved-view
RLS) · `database-designer` (saved-views table + bulk-mutation transaction) ·
`simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** the load-bearing risk is the authorization amplifier
  — the bulk path MUST be exactly as strict as the single-item path, re-checked
  per item. Do NOT write a bulk endpoint that authorizes the batch once. AC-11
  is the test that proves it.
- **JUDGMENT calls you own:** the selection cap value, whether an
  unauthorized-item-in-batch fails the whole batch or skips+reports (document the
  choice), and the saved-view naming UX. Record in the decisions log.
- Reuse the existing single-item assign-owner authz as the per-item check — do
  not re-implement a parallel authz path that could drift.
- Detection-tier: an authz-amplifier gap caught in integration is the desired
  `target=integration, actual=integration`.
