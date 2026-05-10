# 025 — Auditor role + scoped read-only access

**Cluster:** Audit workflow
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the dedicated `auditor` role with strictly scoped read-only access. An auditor user has read access to evidence, controls, scopes, exceptions, policies within a specific `AuditPeriod` (slice 028) — not live state. They have their own workspace for testing notes (not visible to auditees). They cannot mutate any tenant data. Auth/role checks land in OPA policy (slice 035). The slice delivers value because real auditors can do real work in the platform — Drata-style "Audit Hub" usefulness depends on this role being first-class, not bolted on.

## Acceptance criteria

- [ ] AC-1: `auditor` role exists in RBAC roles (slice 035) and OPA Rego policies
- [ ] AC-2: An auditor session sees data filtered to their assigned `audit_period` and scope predicate
- [ ] AC-3: All mutating API endpoints reject auditor role with 403
- [ ] AC-4: Auditor's testing notes (`POST /v1/audit-notes`) are persisted scoped to the auditor + audit_period; not visible to auditees
- [ ] AC-5: `GET /v1/me/audit-period` returns the auditor's active period + scope assignment
- [ ] AC-6: Switching audit periods (for engagements covering multiple historical periods) is supported

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** auditor sees state as of `audit_period_end`, not live
- **Invariant 6 (RLS):** RLS policies enforce per-period scoping at the database layer
- **Replacement-grade criterion (auditor must be first-class):** the audit hub workflow depends on this slice

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.1 (auditor role)
- `Plans/canvas/01-vision.md` §1.4 (auditor persona)

## Dependencies

- #033, #035

## Anti-criteria (P0)

- Does NOT permit any mutation by auditor role
- Does NOT show auditees the auditor's testing notes
- Does NOT permit auditor to see data outside their audit_period scope

## Skill mix (3–5)

- OPA Rego (role policy)
- Go API middleware
- Postgres RLS-aware queries
- Session management with period context
- API design for read-only enforcement
