# 035 — RBAC roles (5) + ABAC via OPA embedded library

**Cluster:** Multi-tenancy / auth
**Estimate:** 2d
**Type:** HITL

## Narrative

Implement coarse-grained RBAC (5 roles: `admin`, `grc_engineer`, `control_owner`, `auditor`, `viewer`) backed by fine-grained ABAC decisions in OPA. OPA runs as an embedded Go library (Open Policy Agent SDK), not a sidecar. Rego policies in `policies/authz/` define the cuts: e.g., `auditor X can only see scope cells within audit_period Y for client Z`. Every API endpoint enforces authorization via a single middleware that calls `opa.Decide(input)`. Decisions are logged for audit. HITL: a security practitioner should review the role definitions and the seed Rego policies before merge — getting role boundaries wrong is the path to "the CI key can push anything for any tenant." The slice delivers value because every API path has consistent, auditable authorization decisions.

## Acceptance criteria

- [ ] AC-1: 5 RBAC roles defined; users assignable to one or more roles per tenant
- [ ] AC-2: OPA embedded library loaded from `policies/authz/`; ~10 seed Rego policies covering the role boundaries
- [ ] AC-3: Authorization middleware applied to every mutating endpoint; reads `(user, action, resource)` and calls OPA
- [ ] AC-4: Decision audit log: every policy decision (allow/deny) recorded with `decision_id`, `user_id`, `action`, `resource`, `result`
- [ ] AC-5: Test matrix: each role × representative endpoint × expected outcome documented
- [ ] AC-6: ABAC example test: an auditor's session is denied access to a scope cell outside their assigned audit_period
- [ ] AC-7: HITL: role + policy review log at `docs/audit-log/authz-review.md`

## Constitutional invariants honored

- **Invariant 6 (RLS):** RBAC/ABAC layered on top of RLS (defense in depth)
- **Tech-stack lock:** OPA embedded; same engine for control policies and authz decisions

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.5 (Auth model)
- `CLAUDE.md` (planned repo layout — `policies/` Rego)

## Dependencies

- #033, #034

## Anti-criteria (P0)

- Does NOT permit endpoint without explicit authz decision (default deny)
- Does NOT skip the decision audit log
- Does NOT ship roles without HITL review

## Skill mix (3–5)

- OPA Go SDK + Rego authoring
- Go HTTP middleware
- RBAC + ABAC composition
- Audit-log discipline
- HITL review coordination
