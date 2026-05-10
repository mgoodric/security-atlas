# 018 — FrameworkScope predicate + intersection compute + scope-versioning workflow

**Cluster:** Scope + FrameworkScope
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the per-framework scope predicate that intersects with `Control.applicability_expr` to produce `effective_scope(control, framework)` — the cells where a control's evidence actually counts for a given framework. For v1, SOC 2 is auditor-defined; the org enters a predicate via a small UI form. FrameworkScope is versioned in its own right: `effective_from`, `effective_to`, `approved_by`, `approval_evidence` capture when the scope changed and who locked it. The compute path: for any `(control, framework)` query, intersect `control.applicability_expr` with `framework_scope.predicate` and return the resulting cells. The slice delivers value because PCI/HIPAA in phase 2 will inherit this infrastructure without rework.

## Acceptance criteria

- [ ] AC-1: `POST /v1/framework-scopes` creates a `FrameworkScope` row (predicate, effective_from, status=draft)
- [ ] AC-2: `PATCH /v1/framework-scopes/:id` to status=approved requires `approved_by` + `approval_evidence` (e.g., a doc reference)
- [ ] AC-3: `GET /v1/controls/:id/effective-scope?framework_version=SOC2:2017` returns the intersection of control applicability and framework scope
- [ ] AC-4: SOC 2 default FrameworkScope (auditor-defined predicate) seedable via config
- [ ] AC-5: FrameworkScope changes are versioned — historical queries return historical scope
- [ ] AC-6: An out-of-scope control returns empty effective_scope; coverage compute then yields `n/a` (not fail)

## Constitutional invariants honored

- **Invariant 5 (FrameworkScope intersection):** the entire premise of this slice
- **Invariant 8 (OSCAL wire format — partial):** scope structure exportable to OSCAL implementation statements (used in slice 030)

## Canvas references

- `Plans/canvas/05-scopes.md` §5.5 (FrameworkScope entity + intersection model)

## Dependencies

- #017

## Anti-criteria (P0)

- Does NOT permit FrameworkScope mutation once `status=approved` (only new versions)
- Does NOT auto-approve scope changes
- Does NOT compute coverage over cells outside the framework's scope predicate

## Skill mix (3–5)

- Go predicate engine
- Postgres versioned-row patterns
- API design with workflow states
- Compliance domain modeling
- Next.js form (predicate editor UI)
