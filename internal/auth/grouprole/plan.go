// Package grouprole is the slice 509 IdP group-to-role derivation surface: it
// maps a validated identity's IdP group membership to atlas RBAC roles via the
// per-tenant oidc_idp_group_mappings table, then reconciles the user's
// group-derived role rows.
//
// It is the authorization-derivation sibling of slice 508 (SCIM user
// lifecycle): 508 provisions the user, 509 maps their groups to roles. Because
// this surface ASSIGNS ROLES (the thing 508 deferred, P0-508-3), the security
// model is the load-bearing part. See docs/issues/509-*.md STRIDE.
//
// ONE resolver, TWO validated sources (AC-2). Both the OIDC-claim path and the
// SCIM-Group path call Resolver.Derive with an ALREADY-VALIDATED group set.
// The resolver NEVER accepts raw, unvalidated group input (P0-509-2): the
// caller (a verified-JWT claim reader or an authenticated SCIM-group handler)
// is responsible for validation; the resolver only maps + reconciles.
//
// This file holds the PURE-Go reconciliation logic (the fast, exhaustive
// unit-test surface — slice 353 Q-2). The DB-backed store lives in
// resolver.go.
package grouprole

import "sort"

// Source identifies which validated channel supplied the group set. Recorded
// in the audit log so "why does this user have this role?" is answerable
// (STRIDE-R).
type Source string

const (
	// SourceOIDC is the OIDC `groups` claim path (roles derived at login from a
	// verified ID token's groups claim).
	SourceOIDC Source = "oidc"
	// SourceSCIM is the SCIM Group path (roles re-derived when an authenticated
	// SCIM Group membership changes).
	SourceSCIM Source = "scim"
)

// Valid reports whether s is a recognized derivation source.
func (s Source) Valid() bool { return s == SourceOIDC || s == SourceSCIM }

// roleAdmin is the canonical admin role string. Kept local so the pure-Go plan
// has no import on the authz package (it stays a leaf, trivially testable).
const roleAdmin = "admin"

// reconcileState is the pure input to planReconcile: everything the
// reconciliation decision needs, with no DB or I/O.
type reconcileState struct {
	// target is the set of roles the mappings resolve for this identity's
	// validated group set (the DESIRED group-derived role set). Already
	// de-duplicated.
	target map[string]struct{}
	// current is the user's EXISTING group-derived roles (origin='group-derived').
	current map[string]struct{}
	// manual is the set of roles the user ALSO holds via a manual admin
	// assignment (origin='manual'). A revoke must never delete a role the user
	// holds manually (AC-4); a grant must not duplicate one.
	manual map[string]struct{}
	// tenantAdminCount is the number of DISTINCT users currently holding the
	// admin role in the tenant (any origin). The last-admin guard reads it.
	tenantAdminCount int
	// userHoldsAdminElsewhere is true when this user holds admin via a path
	// that survives this reconciliation — i.e. a manual admin grant. If the
	// user keeps admin manually, revoking the group-derived admin can never
	// strand the tenant, so the guard does not fire.
	userHoldsManualAdmin bool
}

// reconcilePlan is the pure output: the exact grants + revokes to apply, plus
// any guard suppressions (a revoke the last-admin guard refused to perform).
type reconcilePlan struct {
	grants  []string // roles to INSERT as origin='group-derived'
	revokes []string // roles to DELETE (origin='group-derived' only)
	// suppressedRevokes are group-derived roles whose revoke was BLOCKED by the
	// last-admin guard (AC-5 / P0-509-3). They remain granted. Surfaced for the
	// audit trail + tests.
	suppressedRevokes []string
}

// planReconcile computes the grant/revoke plan from the pure state. This is the
// heart of the slice 509 JUDGMENT — the precedence + conflict-resolution rules
// live HERE, deterministically, exhaustively unit-tested:
//
//   - GRANT a target role the user does not already hold as group-derived. If
//     the user already holds it manually we still record a group-derived row so
//     the role survives a later manual revoke independently (the two origins are
//     tracked separately); the INSERT is idempotent on the PK so this is safe.
//   - REVOKE a current group-derived role no longer in the target set
//     (fail-closed: an unmapped group contributes nothing, so its role drops
//     out of target and is revoked) — UNLESS it is the admin role and revoking
//     it would remove the tenant's last admin (AC-5). The guard fires only when
//     ALL of: the role is admin, the user does not also hold admin manually,
//     and the tenant has exactly one admin user (this user). In that case the
//     revoke is suppressed and the user keeps the group-derived admin role, so
//     the tenant can never be locked out by a group re-derivation.
//
// The result is sorted for deterministic application + audit ordering.
func planReconcile(st reconcileState) reconcilePlan {
	var p reconcilePlan

	// Grants: target \ current.
	for role := range st.target {
		if _, have := st.current[role]; !have {
			p.grants = append(p.grants, role)
		}
	}

	// Revokes: current \ target, with the last-admin guard on admin.
	for role := range st.current {
		if _, keep := st.target[role]; keep {
			continue
		}
		if role == roleAdmin && wouldStrandLastAdmin(st) {
			p.suppressedRevokes = append(p.suppressedRevokes, role)
			continue
		}
		p.revokes = append(p.revokes, role)
	}

	sort.Strings(p.grants)
	sort.Strings(p.revokes)
	sort.Strings(p.suppressedRevokes)
	return p
}

// wouldStrandLastAdmin reports whether revoking THIS user's group-derived admin
// role would remove the tenant's final admin (AC-5 / P0-509-3). It returns true
// only when the user does not also hold admin manually (a manual admin grant
// survives the re-derivation, so the tenant is not stranded) AND the tenant has
// at most one admin user. A count of 1 means this user is the only admin;
// 0 is defensive (should not happen if the user currently holds group-derived
// admin, but treating <=1 as "stranding" is the conservative fail-safe).
func wouldStrandLastAdmin(st reconcileState) bool {
	if st.userHoldsManualAdmin {
		return false
	}
	return st.tenantAdminCount <= 1
}

// setOf builds a set from a slice, dropping empties.
func setOf(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it == "" {
			continue
		}
		out[it] = struct{}{}
	}
	return out
}
