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
// revoke until the token expires (#2381). An unbound API key's owner_roles
// label still does, as it did before #2428: it names no user, so the label
// is its grant. A key bound to a user counts its owner's live roles alone
// (#2541): its label is a copy taken when the key was minted, and keeping it
// would let a revoked admin act as one through the key.
func IsTenantAdmin(cred credstore.Credential) bool {
	if cred.IsAdmin || hasLabel(cred.TenantRoles, RoleAdmin) {
		return true
	}
	return roleLabelIsGrant(cred) && hasLabel(cred.OwnerRoles, RoleAdmin)
}

// IsTenantApprover reports whether cred may approve: a tenant admin, the
// approver flag, or the live tenant grc_engineer role (#2428). It matches
// grc_engineer.rego, which grants grc_engineer the approve action, so a
// handler gate and the OPA decision admit the same callers. As in
// IsTenantAdmin, an unbound API key's owner_roles label counts, and neither a
// JWT role claim nor a bound key's label does.
func IsTenantApprover(cred credstore.Credential) bool {
	if IsTenantAdmin(cred) || cred.IsApprover || hasLabel(cred.TenantRoles, RoleGRCEngineer) {
		return true
	}
	return roleLabelIsGrant(cred) && hasLabel(cred.OwnerRoles, RoleGRCEngineer)
}

// IsTenantAuditReader reports whether cred may read what OPA admits an
// auditor to alongside admin and grc_engineer, the dashboard export
// (#2350): a tenant approver (IsTenantApprover) or the live auditor role,
// or, for a credential naming no user, its auditor label
// (TenantRoleLabels). control_owner and viewer are refused, as OPA refuses
// them there. As in IsTenantApprover, a JWT session's role claim does not
// count (#2381).
func IsTenantAuditReader(cred credstore.Credential) bool {
	return IsTenantApprover(cred) || containsLabel(TenantRoleLabels(cred), string(RoleAuditor))
}

// IsTenantRiskReader reports whether cred may read the risk register, the
// decision log and the org units the risk hierarchy draws (#2556): a tenant
// approver (IsTenantApprover), or a viewer by its live tenant role or, for a
// credential naming no user, its viewer label (TenantRoleLabels). It matches
// OPA on `risks`, `decisions` and `org_units` reads: admin.rego admits
// everything, grc_engineer.rego reads everything, viewer.rego lists the
// three. control_owner and auditor are refused, as OPA refuses them there.
//
// It replaces the `IsAdmin || IsApprover || len(OwnerRoles) > 0` read gate,
// which counted a session's role claim (#2381, #2507) and admitted any label.
func IsTenantRiskReader(cred credstore.Credential) bool {
	return IsTenantApprover(cred) || containsLabel(TenantRoleLabels(cred), string(RoleViewer))
}

// HasAnyTenantRole reports whether cred holds any canonical role in its
// tenant (#2507): a tenant approver (IsTenantApprover, which covers the admin
// and approver flags), or any canonical role among TenantRoleLabels: a live
// user_roles row, or, for a credential naming no user, its own label. A label
// outside the five roles does not count, by decision (Matt, 2026-11-17):
// a free-text label grants nothing. Since #2596 OPA agrees: derivedRolesFor
// derives nothing from free text either (free_text_label_2596_test.go). On a
// key bound to a user a free-text label is still a control_owner ceiling on
// the owner's live roles (#2431), which both layers apply.
//
// It replaces the `IsAdmin || IsApprover || len(OwnerRoles) > 0` read gate,
// which counted a session's role claim (#2381) and admitted any label. Use a
// narrower helper (IsTenantRiskReader) where OPA admits fewer roles.
func HasAnyTenantRole(cred credstore.Credential) bool {
	if IsTenantApprover(cred) {
		return true
	}
	for _, l := range TenantRoleLabels(cred) {
		if isCanonicalRole(l) {
			return true
		}
	}
	return false
}

// IsTenantControlOwner reports whether cred holds the live tenant
// control_owner role, or, for an unbound API key, the control_owner
// owner_roles label. As in IsTenantAdmin, neither a JWT role claim nor a
// bound key's label counts.
func IsTenantControlOwner(cred credstore.Credential) bool {
	if hasLabel(cred.TenantRoles, RoleControlOwner) {
		return true
	}
	return roleLabelIsGrant(cred) && hasLabel(cred.OwnerRoles, RoleControlOwner)
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
// can be legacy free text ("employee", "security-engineering"), and the only
// place a user holds one is an api_keys.owner_roles label. The ack roster
// counts live user_roles only (#2586), so such a policy's ack by label is
// recorded but not counted in its rate. The label is set by an admin
// when the key is minted and goes when the key is revoked; it is not a copy
// of a revocable tenant role, and an ack grants nothing.
//
// A bound key's label that names a canonical role (control_owner,
// grc_engineer, ...) is the exception (#2541): that role is revocable, and
// the owner's live user_roles already carry it while it is held. So it counts
// only through TenantRoles, and a revoked control_owner cannot attest through
// a key minted while they held the role.
func AckRoleLabels(cred credstore.Credential) []string {
	out := append([]string(nil), cred.TenantRoles...)
	if jwtSession(cred) && !machineJWT(cred) {
		return out
	}
	bound := boundKey(cred)
	for _, l := range cred.OwnerRoles {
		if bound && isCanonicalRole(l) {
			continue
		}
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
// than a copy of a user's roles: true unless cred is a user's JWT session,
// whose role claim outlives a revoke (#2381), or an API key bound to a user
// (issued_by set, so UserID is the owner, not the key id), whose label is a
// copy of the owner's roles (#2541). A key naming no user at all (empty
// UserID) is unbound, as boundKey and is_machine_actor treat it.
//
// Every reader of the label uses it (#2596): the tenant gates (IsTenantAdmin,
// IsTenantApprover, IsTenantControlOwner), TenantRoleLabels, and, through
// derivedRolesFor, OPA. Before #2596 the tenant gates had their own rule,
// which refused a client_credentials machine token's label, and
// TenantRoleLabels refused the label of a key naming no user.
func roleLabelIsGrant(cred credstore.Credential) bool {
	switch {
	case jwtSession(cred):
		return machineJWT(cred)
	case apiKey(cred):
		return !boundKey(cred)
	default:
		return true
	}
}

// boundKey reports whether cred is an API key bound to a user: issued_by is
// set, so UserID is the owner's id rather than the key's own. A key naming no
// user at all (empty UserID) is not bound.
func boundKey(cred credstore.Credential) bool {
	return apiKey(cred) && cred.UserID != "" && cred.UserID != cred.ID
}

func isCanonicalRole(l string) bool {
	for _, r := range CanonicalRoles() {
		if l == string(r) {
			return true
		}
	}
	return false
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
