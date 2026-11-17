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

// HasTenantRole reports whether the caller holds any role in its current
// tenant, given its live user_roles there (#2494). The web shows a "no role
// in this organisation" page in place of the dashboard when it is false.
//
// A user's session (a "jwt:" credential naming a user) counts only its live
// roles or the super-admin bit (jwtmw sets IsAdmin from the re-checked
// atlas:super_admin claim). Its role claim does not count: it outlives a
// revoke (#2381), and it is empty for a user with no role (#2401). Any
// other credential, an API key or a client_credentials token, counts the
// roles BuildInput derives for it, so it never sees the page while the
// platform admits it.
func HasTenantRole(cred credstore.Credential, liveRoles []string) bool {
	if cred.IsAdmin || len(liveRoles) > 0 {
		return true
	}
	if jwtSession(cred) && !machineJWT(cred) {
		return false
	}
	return len(derivedRolesFor(cred)) > 0
}

// TenantRoleLabels returns the tenant roles a handler gate counts for cred
// (#2505). Gates that admit a named role (an export, a role-targeted policy
// ack) read it rather than cred.OwnerRoles.
//
// It is the live user_roles authzmw resolved (cred.TenantRoles), plus the
// credential's own role label only when the credential names no user: an
// unbound API key, a client_credentials token, or an in-memory credential.
// Those have no user_roles to resolve, so the label is their grant. A user's
// session and a key bound to a user count their live roles alone: the
// session's role claim outlives a revoke until the token expires (#2381),
// and a bound key's label is a copy taken when the key was minted (#2427).
func TenantRoleLabels(cred credstore.Credential) []string {
	out := append([]string(nil), cred.TenantRoles...)
	if !roleLabelIsGrant(cred) {
		return out
	}
	for _, l := range cred.OwnerRoles {
		if !containsLabel(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// AckRoleLabels returns the roles a policy acknowledgment matches against a
// policy's acknowledgment_required_roles (#2505): the live user_roles authzmw
// resolved, plus the credential's OwnerRoles unless it is a user's JWT
// session. A session's OwnerRoles is its role claim, which outlives a revoke.
//
// It differs from TenantRoleLabels for a key bound to a user. Required roles
// are free text ("employee", "security-engineering"), and the only place a
// user holds one is an api_keys.owner_roles label: the ack roster counts its
// denominator from that column. Dropping a bound key's label would leave a
// user on the roster who can never acknowledge. The label is set by an admin
// when the key is minted and goes when the key is revoked; it is not a copy
// of a revocable tenant role, and an ack grants nothing.
func AckRoleLabels(cred credstore.Credential) []string {
	out := append([]string(nil), cred.TenantRoles...)
	if jwtSession(cred) && !machineJWT(cred) {
		return out
	}
	for _, l := range cred.OwnerRoles {
		if !containsLabel(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// HasOwnerRole reports whether cred may act as the owner of a control whose
// free-text owner_role is role: the slice-011 attestation gate, and the CSF
// assessment edit gate (#2523). The admin flag is a wildcard. An empty role
// admits only the admin flag, so a control bundle that declared no
// owner_role cannot be attested by anyone else.
//
// It matches role against AckRoleLabels, not TenantRoleLabels. owner_role is
// free text set by the control bundle, like a policy's required ack roles,
// and an api_keys.owner_roles label is the only place a user holds a
// non-canonical one; that label is set by an admin and goes when the key is
// revoked. A user's JWT session counts its live user_roles alone: its role
// claim outlives a revoke until the token expires (#2381), so a revoked
// owner could otherwise keep attesting.
func HasOwnerRole(cred credstore.Credential, role string) bool {
	if cred.IsAdmin {
		return true
	}
	return role != "" && containsLabel(AckRoleLabels(cred), role)
}

// roleLabelIsGrant reports whether cred's OwnerRoles is its own grant rather
// than a copy of a user's roles: true unless cred is a user's JWT session or
// an API key bound to a user (issued_by set, so UserID is the owner, not the
// key id).
func roleLabelIsGrant(cred credstore.Credential) bool {
	switch {
	case jwtSession(cred):
		return machineJWT(cred)
	case apiKey(cred):
		return cred.UserID == cred.ID
	default:
		return true
	}
}

func containsLabel(labels []string, l string) bool {
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
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
