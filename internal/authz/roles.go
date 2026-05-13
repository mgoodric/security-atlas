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
//   #6 Tenant isolation. The authz layer reads tenant_id from the
//      authenticated credential's context (slice 033 tenancymw) -- it
//      does NOT trust the request body. RLS in Postgres is the second
//      leg of defense in depth.
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

// Role is the canonical role enum from canvas §9.5. The CHECK
// constraint on user_roles.role mirrors this set; updates to one MUST
// update the other and add a .rego file under policies/authz/.
type Role string

const (
	RoleAdmin         Role = "admin"
	RoleGRCEngineer   Role = "grc_engineer"
	RoleControlOwner  Role = "control_owner"
	RoleAuditor       Role = "auditor"
	RoleViewer        Role = "viewer"
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
