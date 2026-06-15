# 384 — ActionPlan primitive: schema + CRUD + risk/control linkage

**Cluster:** Risk register
**Estimate:** 3d
**Type:** JUDGMENT
**Status:** `ready` (corrected 2026-06-14: the prior `merged` header was a bad reconcile — the slice was unbuilt on `main`; built fresh in the slice-384 PR)

## Narrative

Customers running third-party risk evaluations on the security-atlas operator (the solo CISO / vCISO at a 50-150-person company — canvas §1.5) frequently issue findings that require a written remediation commitment. Today the operator has nowhere inside security-atlas to track those commitments. They could shoe-horn into Exception (§6.3) but Exception's semantic is _backward-looking acknowledged non-compliance with compensating controls until a fixed expiry_ — different from a _forward-looking remediation commitment with milestones_. They could shoe-horn into DecisionLog (§6.7) but DecisionLog's semantic is _operational/architectural tradeoff with rationale_ — different again. The operator ends up tracking these in a spreadsheet, which violates the v1 binary success test (canvas §1.5: "does the operator reach for a Google Sheet to fill a gap?").

**The slice ships a fourth first-class risk-register primitive: ActionPlan.** Distinct semantic: a forward-looking commitment to close a gap, with owner + due date + linkage to the risks and controls the gap touches. Lifecycle is its own state machine (`draft → in_progress → blocked → completed → verified`) — not Exception's `requested → approved → active → expired` and not DecisionLog's `active → revisited → superseded → expired`. Triggering event captured (free-text, e.g. "Customer X Q2 2026 TPRM finding #4") so the chain back to the originating eval is legible without a separate table for ExternalEvaluation (deferred).

**Scope discipline.** This is the FOUNDATION slice: schema + minimal lifecycle + CRUD + M2M linkages to Risk and Control + listing/detail UI. The following are explicitly DEFERRED to spillover slots:

- OSCAL POA&M export — `oscal-bridge` work; spillover slice (≈1d AFK).
- Reminder cadence / due-date nudges — spillover slice (≈0.5d AFK).
- Multi-stage approval workflow gates (`requested → approved → in_progress`) — spillover slice (≈1d AFK).
- Board-narrative integration (slice 182 dependency) — spillover slice.
- External-evaluation primitive (TPRM event as a first-class record) — spillover slice (likely 2d JUDGMENT once needed).

**Why now.** Canvas §1.5 v1 binary criterion: the operator's customers will diligence the diligence tool itself; the diligence-response surface needs an in-product home before the operator's first customer TPRM. Filed via /idea-to-slice 2026-05-29 from maintainer's vCISO-customer-feedback channel.

**Terminology decision.** Primitive named `ActionPlan` (user-facing) and serializes to OSCAL `POA&M` on export (the wire format per canvas §3.4). NIST IR 8477 / OSCAL terminology is "Plan of Action and Milestones"; the user-facing UI uses "Action Plan" because that's the term operators use in vendor-diligence conversations. Mapping documented in the spillover OSCAL export slice.

## Threat model

STRIDE pass — new authenticated CRUD + M2M linkage surface on tenant-scoped primitive. No new auth role; reuses existing RBAC + ABAC via OPA per canvas §9.

**S — Spoofing.** New authenticated endpoints (5 CRUD + 4 linkage). All require existing session + tenant context. No unauthenticated surface added. CLEAN.

**T — Tampering.** User-input fields require explicit validation: `title` ≤200ch, `description` ≤4000ch, `triggering_event` ≤500ch, `due_date` ≤5 years out. `owner_id` validated against tenant's users. `status` enum-validated at DB layer (CHECK constraint) AND at handler layer. M2M linkage validates `risk_id` and `control_id` exist within the caller's tenant scope before INSERT. CLEAN with explicit ACs.

**R — Repudiation.** Append-only `action_plan_audit_log` table records every state transition + every linkage add/remove + every mutation. Audit log row carries `actor_id`, `tenant_id`, `action_plan_id`, `action_type`, `before_state`, `after_state`, `created_at`. CLEAN.

**I — Information disclosure.** Tenant-scoped primitive. Postgres RLS on `action_plans`, `action_plan_risks`, `action_plan_controls`, `action_plan_audit_log` — four policies each (SELECT / INSERT / UPDATE / DELETE) per slice 002's established four-policy convention. M2M linkage tables require `action_plan_id`'s tenant_id to match `app.current_tenant` GUC (subquery-based RLS). CLEAN.

**D — Denial of service.** Per-plan caps: ≤50 linked risks + ≤50 linked controls. Pagination on list endpoints (default 25, max 100). Title/description/triggering_event length bounds enforced at handler AND DB layer. CLEAN.

**E — Elevation of privilege.** Reuses existing RBAC + ABAC. No new role introduced. Caller must have `risk_register:write` for mutations and `risk_register:read` for reads (existing roles from slice 056). Linkage endpoints require write on action plan + read on the linkage target (risk/control). CLEAN.

**Threat-model verdict:** CLEAN.

## Acceptance criteria

### Schema

- [ ] **AC-1.** Migration creates `action_plans` table: `id UUID PK`, `tenant_id UUID NOT NULL`, `title TEXT NOT NULL CHECK (length <= 200)`, `description TEXT CHECK (length <= 4000)`, `triggering_event TEXT CHECK (length <= 500)`, `owner_id UUID NOT NULL REFERENCES users(id)`, `due_date DATE`, `status TEXT NOT NULL CHECK (status IN ('draft','in_progress','blocked','completed','verified'))`, `audit_period_id UUID NULL REFERENCES audit_periods(id)`, `tombstoned_at TIMESTAMPTZ`, `created_at`, `updated_at`.
- [ ] **AC-2.** Migration creates `action_plan_risks` M2M table: `action_plan_id UUID`, `risk_id UUID`, `linked_at TIMESTAMPTZ`, `linked_by UUID`, PRIMARY KEY (`action_plan_id`, `risk_id`).
- [ ] **AC-3.** Migration creates `action_plan_controls` M2M table: `action_plan_id UUID`, `control_id UUID`, `linked_at`, `linked_by`, PRIMARY KEY (`action_plan_id`, `control_id`).
- [ ] **AC-4.** Migration creates `action_plan_audit_log` append-only table: `id UUID PK`, `tenant_id UUID NOT NULL`, `action_plan_id UUID NOT NULL`, `actor_id UUID NOT NULL`, `action_type TEXT NOT NULL` (`created`/`updated`/`status_changed`/`risk_linked`/`risk_unlinked`/`control_linked`/`control_unlinked`/`tombstoned`), `before_state JSONB`, `after_state JSONB`, `created_at TIMESTAMPTZ`.
- [ ] **AC-5.** RLS policies (SELECT + INSERT + UPDATE + DELETE) on `action_plans` per slice 002 four-policy convention; FORCE ROW LEVEL SECURITY.
- [ ] **AC-6.** RLS policies on `action_plan_risks` via subquery against `action_plans.tenant_id`; FORCE ROW LEVEL SECURITY.
- [ ] **AC-7.** RLS policies on `action_plan_controls` via subquery against `action_plans.tenant_id`; FORCE ROW LEVEL SECURITY.
- [ ] **AC-8.** RLS policies on `action_plan_audit_log` per slice 002 four-policy convention; FORCE ROW LEVEL SECURITY.
- [ ] **AC-9.** DB-layer trigger denies UPDATE on `action_plan_audit_log` (append-only invariant).

### Backend CRUD

- [ ] **AC-10.** `POST /v1/action-plans` creates record; rejects missing required fields with 400; rejects `due_date` > now + 5 years with 400.
- [ ] **AC-11.** `GET /v1/action-plans` lists tenant's action plans with pagination (`?limit=25&cursor=...`); default 25, max 100.
- [ ] **AC-12.** `GET /v1/action-plans/{id}` returns single plan with `linked_risks[]` + `linked_controls[]` populated (single round-trip).
- [ ] **AC-13.** `PATCH /v1/action-plans/{id}` updates editable fields; rejects status transition not allowed by state machine with 422.
- [ ] **AC-14.** `DELETE /v1/action-plans/{id}` sets `tombstoned_at` (soft-delete per canvas invariant #2); subsequent GET returns 404.
- [ ] **AC-15.** State-machine validation rejects: `draft → completed` (must pass `in_progress`); `verified → draft` (terminal); `* → draft` (except creation).
- [ ] **AC-16.** Every mutation writes a row to `action_plan_audit_log` in the same transaction.

### M2M Linkage

- [ ] **AC-17.** `POST /v1/action-plans/{id}/risks/{risk_id}` creates link; returns 409 if already linked; returns 404 if either id missing in tenant scope.
- [ ] **AC-18.** `DELETE /v1/action-plans/{id}/risks/{risk_id}` removes link; returns 404 if not linked.
- [ ] **AC-19.** `POST /v1/action-plans/{id}/controls/{control_id}` creates link; same 409/404 semantics.
- [ ] **AC-20.** `DELETE /v1/action-plans/{id}/controls/{control_id}` removes link; same 404 semantic.
- [ ] **AC-21.** Per-plan cap: ≤50 linked risks AND ≤50 linked controls (separate caps); 51st link returns 422 with `limit_exceeded`.

### Frontend

- [ ] **AC-22.** New `/action-plans` listing page with status filter pills + paginated table (title / status / owner / due_date).
- [ ] **AC-23.** New `/action-plans/[id]` detail page showing all fields + linked risks + linked controls.
- [ ] **AC-24.** Action plan creation form with searchable multi-select for linked risks (max 50) + linked controls (max 50).
- [ ] **AC-25.** Existing `/risks/[id]` detail page gains "Linked Action Plans" section (read-only list).
- [ ] **AC-26.** Existing `/controls/[id]` detail page gains "Linked Action Plans" section (read-only list).

### Audit-period freezing

- [ ] **AC-27.** When an AuditPeriod is frozen (slice TBD wiring), action plans created/updated after `frozen_at` are NOT included in the period's snapshot; the period's snapshot includes only action plans with `created_at <= frozen_at` and reflects their state at `frozen_at`.

### Tests

- [ ] **AC-28.** Go integration test: cross-tenant SELECT on `action_plans` returns zero rows (RLS verification).
- [ ] **AC-29.** Go integration test: cross-tenant linkage attempt (POST .../risks/{rid} where rid is in Tenant B) returns 404 (cross-tenant deny).
- [ ] **AC-30.** Go integration test: state-machine valid + invalid transitions (parametric, ≥6 cases).

## Constitutional invariants honored

- **Invariant #2 (ingestion ≠ evaluation; append-only ledger):** action_plan_audit_log is append-only via DB trigger (AC-9); soft-delete via tombstoned_at preserves the record (AC-14).
- **Invariant #4 (multidimensional scope):** ActionPlan inherits applicability via linked controls' `applicability_expr` — no separate `applicability_expr` field on ActionPlan itself (kept simple in foundation slice).
- **Invariant #6 (RLS at DB layer):** All 4 new tables enforce RLS via FORCE + four-policy pattern (AC-5/-6/-7/-8); cross-tenant access denied at DB layer.
- **Invariant #9 (manual evidence first-class):** ActionPlan is a manual-evidence-adjacent primitive — first-class lifecycle, owner, audit-log, dashboard surface.
- **Invariant #10 (audit-period freezing):** AC-27 honors freezing semantics.
- **AI-assist boundary:** unchanged — no AI-generated action plan text in foundation slice.

## Canvas references

- `Plans/canvas/06-risk.md` §6.3 Exception/waiver workflow (sibling primitive; distinguish semantic)
- `Plans/canvas/06-risk.md` §6.7 Decision Log (sibling primitive; distinguish semantic)
- `Plans/canvas/01-vision.md` §1.5 v1 binary success test ("does the operator reach for a Google Sheet?")
- `Plans/canvas/08-audit-workflow.md` §8.4 audit-period freezing
- `Plans/canvas/03-ucf.md` (Control linkage M2M target)

## Dependencies

- #002 (merged) — schema + four-policy RLS convention
- #021 (merged) — Exception primitive (sibling pattern reference)
- #052 (merged) — Risk hierarchy schema (provides `risks.id` target for M2M)
- #056 (merged) — Risk register write/read roles (reused, NOT extended)
- #055 (merged) — DecisionLog primitive (sibling pattern reference)
- audit-period freezing primitive (slice TBD) — AC-27 may need a follow-up wiring slice once the freezing primitive ships

## Anti-criteria (P0 — block merge)

- **P0-384-1.** Does NOT ship OSCAL POA&M export (deferred to spillover; OSCAL bridge work).
- **P0-384-2.** Does NOT ship reminder/nudge cadence (deferred to spillover).
- **P0-384-3.** Does NOT ship multi-stage approval workflow gates (deferred to spillover; foundation lifecycle is operator-controlled state transitions, no approval role).
- **P0-384-4.** Does NOT permit cross-tenant linkage (AC-17/-19 reject; AC-29 verifies via integration test).
- **P0-384-5.** Does NOT permit retroactive editing of records inside a frozen AuditPeriod's window (AC-27 freezing-snapshot honored).
- **P0-384-6.** Does NOT hard-delete — soft-delete via `tombstoned_at` only (canvas invariant #2 append-only).
- **P0-384-7.** Does NOT exceed 50-risk + 50-control M2M caps per plan (AC-21).
- **P0-384-8.** Does NOT permit `due_date` > 5 years out (AC-10).
- **P0-384-9.** Does NOT introduce new authz role — reuses `risk_register:read/write` from slice 056.
- **P0-384-10.** Does NOT extend Exception primitive — shape decision (2026-05-29 via /idea-to-slice AskUserQuestion) locks NEW primitive.
- **P0-384-11.** Does NOT permit `triggering_event` injection into structured tables. Foundation slice keeps `triggering_event` as free-text. ExternalEvaluation primitive (spillover) is the future home for structured triggering-event linkage.
- **P0-384-12.** Does NOT bundle AI-assist features (suggest action plan text from risk + finding). Foundation slice is human-only; AI-assist is a separate slice respecting canvas AI-assist boundary.

## Skill mix (3-5)

- sqlc + Atlas migration authoring (4 new tables + RLS policies + append-only trigger)
- Go chi handler authoring (5 CRUD + 4 M2M endpoints)
- Next.js App Router page authoring (1 list + 1 detail + 1 form + 2 existing-page sections)
- Go integration testing with Postgres RLS (slice 002 pattern reuse)
- State-machine validation discipline

## Notes for the implementing agent

### Phase-0 normalize output

- **Cluster:** Risk register (consistent with slices 021, 055, 056)
- **Scope:** 3d JUDGMENT (new primitive + schema + multi-surface)
- **Trigger:** Product/vCISO use case — canvas §1.5 v1 binary criterion alignment
- **Shape decision:** NEW ActionPlan primitive (NOT Exception extension, NOT Risk UI-only). User-confirmed 2026-05-29 via /idea-to-slice AskUserQuestion preview.

### Phase-1 context recovery

- Read canvas §6.3 (Exception) for sibling-primitive pattern: state machine + DB enforcement + linkage discipline
- Read canvas §6.7 (DecisionLog) for sibling-primitive pattern: M:N linkage shape
- Read slice 002 for four-policy RLS pattern (SELECT/INSERT/UPDATE/DELETE)
- Read slice 021 (Exception) for state-machine validation + audit-log pattern
- Read slice 055 (DecisionLog) for M:N linkage table pattern
- Recent slice docs (382, 383, 364) for current voice + format

### Phase-2 grill output

- **Terminology drift check:** "Action Plan" vs "Remediation Plan" vs "POA&M". User-facing is "Action Plan"; OSCAL export serializes to "POA&M" (canvas §3.4 wire format). Slice docs internally call it `ActionPlan`. Go package: `internal/actionplan/`. DB table: `action_plans`. URL path: `/v1/action-plans` + `/action-plans` (frontend).
- **Scope creep check:** Foundation slice deliberately defers: OSCAL POA&M export, reminders, approval workflow, board-narrative integration, ExternalEvaluation primitive. Each is a separate spillover slot.
- **Already-built check:** No prior slice. Closest sibling = slice 021 (Exception); semantic distinct.
- **Hidden finding:** ActionPlan does NOT carry its own `applicability_expr` field in the foundation. It inherits applicability via linked controls. This is a deliberate simplification — if operators need standalone-applicability-without-control-linkage, that's a follow-up.

### Phase-3 threat model

CLEAN — 5 categories surveyed, 12 explicit P0 anti-criteria capture the threats.

### Phase-5 pressure-test output

Reviewed all 30 ACs against the Splitting Test. AC-15 (state machine) is parametric — 3 explicit invalid transitions named; engineer enumerates remaining cases in test AC-30. AC-16 (audit-log on every mutation) is a single transactional invariant; AC-9 (DB trigger for append-only) backstops at storage layer.

### Spillover candidates (file as separate slices after foundation merges)

1. **OSCAL POA&M export** — `oscal-bridge` (Python) work to serialize ActionPlan + linked risks/controls to NIST OSCAL POA&M format. ≈1d AFK.
2. **ActionPlan reminder cadence** — daily job surfaces action plans approaching `due_date` (configurable: 30d / 14d / 7d / 1d). Powers dashboard "Upcoming items" alongside slice 021 exception expiry. ≈0.5d AFK.
3. **ActionPlan approval workflow gates** — adds `requested → approved → in_progress` upfront states for orgs requiring approval before commit. Distinct authz role(s) may be needed. ≈1d JUDGMENT.
4. **ActionPlan board-narrative integration** — adds ActionPlan summaries to board pack (slice 182 dependency). Auto-generated text MUST follow canvas AI-assist boundary (mandatory citations + per-section approval).
5. **ExternalEvaluation primitive** — structured representation of the triggering event (TPRM, customer audit, security review) with M:N linkage to ActionPlans. Today's `triggering_event TEXT` field is the bridge. ≈2d JUDGMENT.

### Why this slice now

vCISO customer feedback channel surfaced the gap on 2026-05-29: customers running TPRM evaluations on the security-atlas operator's company issue findings that need a written remediation commitment, and the operator has nowhere in security-atlas to track it. Filing into the diligence-response surface before the operator's next customer TPRM (canvas §1.5 v1 binary criterion).
