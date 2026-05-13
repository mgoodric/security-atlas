# 055 — Decision Log CRUD + linkage

**Cluster:** Risk register
**Estimate:** 2d
**Type:** AFK

## Narrative

Decision Log primitive per canvas §6.7. Distinct from exceptions (§6.3): exceptions are formal control bypasses; Decisions capture broader operational tradeoffs ("shipping MVP, deferring SAML to v1.2", "skipping IaC because the tool sunsets Q3"). Both are linkable; together they form the audit narrative chain alongside Risks and Controls.

Four endpoint groups:

1. **Decision CRUD** — create, read, update, supersede, expire.
2. **Linkage** — M:N to risks, controls, exceptions, scope_predicates.
3. **Revisit calendar** — query decisions due for revisit (revisit_by ≤ today + N days).
4. **Audit narrative emission** — decisions show up in OSCAL SSP export (slice 030) as context — not as compliance artifacts.

The implementation is mostly data shape + RLS + linkage management. The audit-narrative integration is the meaningful integration point with the OSCAL pipeline.

## Acceptance criteria

- [ ] AC-1: `POST /v1/decisions` accepts the canvas §6.7 schema. Required fields: `title`, `narrative`, `decision_maker`, `decided_at`. Returns the created decision with a generated `decision_id` (format: `DL-YYYY-MM-DD-NNNN`).
- [ ] AC-2: `GET /v1/decisions/{id}` returns decision plus all linked entities (risks, controls, exceptions, scope_predicates) in one response. `GET /v1/decisions?status=active&revisit_due_within_days=30` lists with filters.
- [ ] AC-3: `PATCH /v1/decisions/{id}` updates mutable fields; changes captured in an append-only audit log (`decisions_audit`).
- [ ] AC-4: Supersession workflow — `POST /v1/decisions/{id}/supersede` accepts `{superseded_by: <new_decision_id>}`. Sets `status: superseded` on the old, links the new decision. The old decision is never deleted; auditor trail preserved.
- [ ] AC-5: Linkage CRUD on each link type: `POST /v1/decisions/{id}/links/risks` (add), `DELETE /v1/decisions/{id}/links/risks/{risk_id}` (remove). Same pattern for controls, exceptions, scope_predicates. Idempotent.
- [ ] AC-6: `revisit_by` enforcement — decisions with `revisit_by < today AND status == active` surface in `GET /v1/decisions/overdue`. Daily background job emits a notification per overdue decision to its `decision_maker` (one notification per overdue, not repeated).
- [ ] AC-7: OSCAL audit-narrative integration — when slice 030 runs OSCAL export, decisions linked to in-scope controls appear in the SSP narrative as `<remarks>` blocks with a structured format: `[DL-id] {title} ({decision_maker}, {decided_at}) — Linked risks: {ids}. Revisit: {revisit_by or "n/a"}.`
- [ ] AC-8: Integration test: create a decision linked to a risk + control + exception; verify `GET` returns all linkage; supersede with a new decision; confirm old becomes `superseded` and is reachable via `superseded_by` from the new one; verify OSCAL export includes the decision narrative.
- [ ] AC-9: Cross-tenant denial: linking to a risk/control/exception in another tenant returns 404 (existence-leak prevention). Audit log records the failed attempt.
- [ ] AC-10: Decision Log appears in `_INDEX.md` style dashboard exports (post-slice-055): list view filterable by `status`, `constraints[]`, `decision_maker`, `revisit_by_range`.

## Constitutional invariants honored

- **Invariant 6** (tenant isolation) — every endpoint scoped via RLS; cross-tenant linkage attempts return 404
- **Invariant 8** (OSCAL as wire format) — decisions surface in SSP narrative export (not in OSCAL daily model)
- **Invariant 9** (manual evidence is first-class) — Decision Log is the explicit operational counterpart to manual evidence; both have the same UI surface and lifecycle weight
- **AI-assist boundary** — decisions can be AI-suggested (draft narrative, suggest constraints tags) but require human `decision_maker` field and human-set `decided_at`; no auto-creation

## Canvas references

- `Plans/canvas/06-risk.md §6.3` — Exception/waiver workflow (contrast partner; decisions vs exceptions)
- `Plans/canvas/06-risk.md §6.7` — Decision Log primitive (full schema and audit role)

## Dependencies

- **052** (schema + migrations) — `decisions` and link tables must exist
- **020** (risk → control linkage) — for `linked_risks` linkage to attach meaningfully
- **021** (exception/waiver workflow) — for `linked_exceptions` linkage to attach meaningfully

## Anti-criteria (P0)

- Do NOT collapse Decision Log into Exception. Different audit roles per canvas §6.7.
- Do NOT delete decisions on supersession — append-only history is required for audit trail.
- Do NOT auto-create decisions from AI suggestions without explicit human `decision_maker` field set.
- Do NOT spam overdue notifications — one notification per overdue decision per `decision_maker`, not repeated until status changes.
- Do NOT permit cross-tenant linkage references; respond with 404 (existence-leak prevention).
- Do NOT expose decision narratives in OSCAL export for tenants that have opted decisions out of audit-narrative emission (config flag).

## Skill mix (3–5)

- `tdd` (M:N linkage logic + supersession workflow benefit from test-first design)
- `engineering-advanced-skills:api-design-reviewer` (CRUD shape, linkage semantics, idempotency)
- `engineering-advanced-skills:database-designer` (M:N join table indexing; audit-log append-only enforcement)
- `security-review` (cross-tenant denial; AI-assist boundary on narrative drafting)
- `changelog-generator` (Decision Log itself feeds the project's CHANGELOG narrative for v1.x communication)
