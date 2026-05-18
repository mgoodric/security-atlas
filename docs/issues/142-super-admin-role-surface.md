# 142 — super_admin role: full schema + management surface (slice 141 follow-on)

**Cluster:** Backend / Multi-tenancy / AuthZ
**Estimate:** 1-2d
**Type:** AFK (one D1 grilled in slice 141; engineer fills the table + management page)
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141. Slice 141 STUBS the `super_admins` table (single-row inserted by the bootstrap path) so the multi-tenant login flow can compile. This slice promotes the stub to the full schema + ships the management surface so additional super_admins can be granted + demoted post-bootstrap.

**What this slice ships:**

- Promote `super_admins(idp_issuer, idp_subject, granted_at, granted_by)` stub to full schema; FK constraints; NON-RLS; granted SELECT/INSERT/DELETE to `atlas_auth` role from slice 141.
- NEW `super_admin_audit_log(audit_id, action, target_idp_issuer, target_idp_subject, actor_idp_issuer, actor_idp_subject, occurred_at, payload_json)` — append-only audit-log; surfaced via slice 124 unified aggregator as new `kind='super_admin_grant'` / `'super_admin_revoke'`.
- NEW OPA policy `policies/authz/super_admin.rego` — single rule `is_super_admin` queried by handlers that gate on the role.
- NEW endpoint `POST /v1/admin/super-admins` (grant; super_admin required).
- NEW endpoint `DELETE /v1/admin/super-admins/{idp_issuer}/{idp_subject}` (demote; super_admin required; last-super_admin safety rail blocks self-demote when count == 1).
- NEW management page `web/app/admin/super-admins/page.tsx` — list current super_admins; grant form; per-row demote button with confirmation modal.
- BFF routes for grant + demote.

**Scope discipline (what is OUT):**

- **Super_admin granting MEMBERSHIP into tenants** (i.e. super_admin self-adds to `user_tenants` for arbitrary tenant) — out of scope for this slice; future slice if needed. v1 super_admin can CREATE tenants (slice 143) and RENAME them (slice 144) but to enter a tenant they need an explicit per-tenant role grant from a tenant admin.
- **Email notifications** on grant/demote — out of scope.
- **Time-bounded super_admin grants** (e.g. "super_admin until 2026-12-31") — out of scope; permanent grants only at v1.

## Threat model

Inherits slice 141's threat model. Per-entity additions:

| STRIDE                       | Threat                                                                                                                                                                            | Mitigation                                                                                                                                                                                                                                                    |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **R** Repudiation            | Super_admin grants are global writes; super_admin demoting another super_admin must leave audit trail. Without it, the "last super_admin standing" scenario has no forensic path. | `super_admin_audit_log` written same-transaction as grant/demote. Slice 124 unified aggregator extends with the 2 new `kind` values. Both grantor + target ID captured.                                                                                       |
| **E** Elevation of privilege | (a) Last-super_admin self-demote → deadlock (nobody can create new super_admins ever again). (b) Race between two concurrent demotes → both succeed → 0 super_admins.             | Last-super_admin safety rail: `DELETE` handler wraps in `SELECT count(*) FROM super_admins FOR UPDATE; if count == 1 → 409 Conflict "Cannot demote the last super_admin"`. FOR UPDATE serializes the race. Integration test asserts both single + concurrent. |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: Promote `super_admins` stub (slice 141 ships PK + INSERT grant to `atlas_auth` only — sufficient for bootstrap) to FULL schema: ADD CHECK constraints (idp_issuer + idp_subject nonempty; granted_by nonempty), ADD index on `(idp_issuer, idp_subject)` for grant-lookup hot path, ADD SELECT grant to `atlas_auth` (for the management page list), ADD DELETE grant to `atlas_auth` (for demote). Migration is an ALTER (additive) — no data backfill needed.
- [ ] AC-2: NEW `super_admin_audit_log` table + 2-policy append-only RLS (slice 036 pattern).
- [ ] AC-3: NEW OPA policy `policies/authz/super_admin.rego` exposing `is_super_admin` rule.
- [ ] AC-4: `POST /v1/admin/super-admins` handler (grant); super_admin-gated; same-transaction audit-log write.
- [ ] AC-5: `DELETE /v1/admin/super-admins/{idp_issuer}/{idp_subject}` handler (demote); super_admin-gated; last-super_admin safety rail (409 if count == 1).
- [ ] AC-6: BFF routes `web/app/api/admin/super-admins/`.
- [ ] AC-7: Management page `web/app/admin/super-admins/page.tsx` — list + grant form + demote modal.
- [ ] AC-8: Slice 124 unified audit-log aggregator extension: 2 new `kind` values (`super_admin_grant`, `super_admin_revoke`).
- [ ] AC-9: Cross-tenant isolation test (super_admin_audit_log is NOT tenant-scoped; visible to any super_admin's audit-log view; verify NO data leak to non-super_admin callers).
- [ ] AC-10: Last-super_admin safety rail integration test (single demote 409s when count == 1; 2 concurrent demotes when count == 2 → 1 succeeds, 1 409s).
- [ ] AC-11: Playwright e2e on `/admin/super-admins` page covering grant + demote happy paths.
- [ ] AC-12: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 141. Adds: **#6 RLS at DB layer** — `super_admins` is non-RLS by design (auth-layer table); `super_admin_audit_log` IS RLS-scoped via the slice-036 append-only pattern.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — OPA + RBAC commitment.
- `Plans/canvas/11-open-questions.md` — super_admin role concept new (not previously in canvas; this slice adds it).

## Dependencies

- **#141** Multi-tenant login + switcher (stubs `super_admins`). **Gate: 141 must be `merged` before 142 flips to `ready`.**
- **#035** OPA middleware (merged) — extends with new policy.
- **#036** Append-only RLS pattern (merged) — `super_admin_audit_log` reuses.
- **#124** Unified audit-log aggregator (merged) — extends with 2 new `kind` values.

## Anti-criteria (P0 — block merge)

- Inherits slice 141 P0-INFO-1 (`atlas_auth` privilege boundary preserved — slice 142 adds INSERT/DELETE grants on `super_admins` to `atlas_auth`; CI test updated accordingly).
- **P0-SA-1** Last-super_admin safety rail is merge-blocking (AC-10 integration test). NO path may DELETE the last super_admin.
- **P0-SA-2** Super_admin grant/demote MUST write `super_admin_audit_log` row SAME-TRANSACTION; no out-of-band writes.
- **P0-SA-3** NO super_admin self-add to `user_tenants` for arbitrary tenant. Per-tenant entry still requires explicit per-tenant role grant.
- **P0-SA-4** NO time-bounded grants at v1.
- **P0-SA-5** NO vendor-prefixed test fixture tokens.

## Skill mix

- slice 141's `internal/auth/userTenants/` package (consume).
- Go integration tests + Playwright e2e — same patterns as slice 141.
- OPA matrix tests for the new `super_admin.rego` policy.

## Notes for the implementing agent

Slice 141 establishes the architectural foundation. This slice is the management-surface follow-on; pickup time ~1-2d.

The last-super_admin safety rail (P0-SA-1 / AC-10) is the load-bearing concern — flag in the engineer's grill against this slice doc.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141 (multi-tenant login). User picked "super_admin role only" for tenant-create auth in the AskUserQuestion before grilling started; this slice ships that role.
