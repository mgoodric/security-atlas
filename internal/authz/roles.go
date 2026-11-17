// Package authz wraps an embedded Open Policy Agent (OPA) engine for
// authorization decisions across the platform. It implements canvas
// §9.5: 5-role RBAC plus ABAC for the "fine cuts that matter" (e.g.
// auditor X can only see scope cells within audit_period Y).
//
// The package exposes three entry points:
//
//   - Decide(ctx, input) -> Decision        evaluate a single input
//   - BuildInput(...)                       construct the canonical Input
//   - NewAuditWriter(pool) -> AuditWriter   write Decisions to the DB
//
// Policies live under policies/authz/*.rego and are loaded once at
// startup via NewEngine. Reloading at runtime is intentionally not
// supported in v1 -- the slice's HITL gate (docs/audit-log/authz-review.md)
// is the change-management surface.
//
// Constitutional invariants honored:
//
//	#6 Tenant isolation. The authz layer reads tenant_id from the
//	   authenticated credential's context (slice 033 tenancymw) -- it
//	   does NOT trust the request body. RLS in Postgres is the second
//	   leg of defense in depth.
//
// Anti-criteria honored (P0):
//
//   - NO admin emergency-bypass: the 5 roles are the universe. There is
//     no special-case shortcut around Decide.
//   - NO endpoint without explicit Decide call: authzmw.Middleware
//     wraps every route on the root chi router.
//   - NO decision skips audit log: AuditWriter is called from inside
//     Middleware on every allow OR deny.
package authz

import "github.com/mgoodric/security-atlas/internal/api/credstore"

// IsTenantAdmin reports whether cred administers its tenant: the admin
// flag, or the live tenant admin role authzmw resolved from user_roles
// (cred.TenantRoles, #2428). Tenant-scoped admin gates (LLM routing,
// control import settings, audit-period populations) use it; the
// install-wide and /v1/admin/* gates stay on cred.IsAdmin (#2216 D2).
//
// A JWT session's role claim (OwnerRoles) does not count: it outlives a
// revoke until the token expires (#2381). An API key's owner_roles label
// still does, as it did before #2428, so no key loses access.
func IsTenantAdmin(cred credstore.Credential) bool {
	if cred.IsAdmin || hasLabel(cred.TenantRoles, RoleAdmin) {
		return true
	}
	return !jwtSession(cred) && hasLabel(cred.OwnerRoles, RoleAdmin)
}

// IsTenantApprover reports whether cred may approve: a tenant admin, the
// approver flag, or the live tenant grc_engineer role (#2428). It matches
// grc_engineer.rego, which grants grc_engineer the approve action, so a
// handler gate and the OPA decision admit the same callers. As in
// IsTenantAdmin, an API key's owner_roles label counts and a JWT role
// claim does not.
func IsTenantApprover(cred credstore.Credential) bool {
	if IsTenantAdmin(cred) || cred.IsApprover || hasLabel(cred.TenantRoles, RoleGRCEngineer) {
		return true
	}
	return !jwtSession(cred) && hasLabel(cred.OwnerRoles, RoleGRCEngineer)
}

// IsTenantControlOwner reports whether cred holds the live tenant
// control_owner role, or, for an API key, the control_owner owner_roles
// label. As in IsTenantAdmin, a JWT role claim does not count.
func IsTenantControlOwner(cred credstore.Credential) bool {
	if hasLabel(cred.TenantRoles, RoleControlOwner) {
		return true
	}
	return !jwtSession(cred) && hasLabel(cred.OwnerRoles, RoleControlOwner)
}

func hasLabel(labels []string, role Role) bool {
	for _, l := range labels {
		if l == string(role) {
			return true
		}
	}
	return false
}

// Role is the canonical role enum from canvas §9.5. The CHECK
// constraint on user_roles.role mirrors this set; updates to one MUST
// update the other and add a .rego file under policies/authz/.
type Role string

const (
	RoleAdmin        Role = "admin"
	RoleGRCEngineer  Role = "grc_engineer"
	RoleControlOwner Role = "control_owner"
	RoleAuditor      Role = "auditor"
	RoleViewer       Role = "viewer"
)

// CanonicalRoles returns the 5 canonical roles in canvas order. Used by
// the integration tests and the role × endpoint matrix.
func CanonicalRoles() []Role {
	return []Role{
		RoleAdmin,
		RoleGRCEngineer,
		RoleControlOwner,
		RoleAuditor,
		RoleViewer,
	}
}

// IsCanonical reports whether r is one of the 5 canvas roles. Used by
// BuildInput to drop unknown roles silently rather than passing them
// to OPA (where they'd never match a rule anyway).
func IsCanonical(r Role) bool {
	switch r {
	case RoleAdmin, RoleGRCEngineer, RoleControlOwner, RoleAuditor, RoleViewer:
		return true
	}
	return false
}
