package moduleaccess

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/identity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #2174: who can work in a module, asked about a person who is not the
// caller: the owner of a record (for the "owner can't access this module"
// flag) and the members an assignee picker lists. Both read the person's
// live user_roles and stored module access at the time of the read, so a
// narrowing or a role change shows up without touching the record.
//
// A person lacks a module only when an admin narrowed their module access
// to leave it out and their live user_roles hold neither admin nor
// auditor (both exempt, as in Gate). A person with no stored narrowing
// has "all". Roles held only in a credential (a test token, an IdP claim)
// cannot be read for someone who is not the caller, so the absence of a
// user_roles row is not taken as lost access: the flag and the picker are
// advisory, and Gate still refuses the person on their own requests.
// Whether the module is switched on is not asked: a switched-off module's
// records are not shown to anyone.

// UserHas reports whether userID (a users.id or user_roles key) can work
// in the optional module key. A core module, or a key the registry does
// not list, answers true. A nil Views (a unit-test server with no
// database) answers true.
func (v *Views) UserHas(ctx context.Context, tenantID uuid.UUID, userID, key string) (bool, error) {
	if v == nil || v.access == nil || !optional(key) || userID == "" {
		return true, nil
	}
	modules, err := v.access.Get(ctx, tenantID, identity.RolesKey(userID))
	if err != nil {
		return false, err
	}
	if (Access{Modules: modules}).Has(key) {
		return true, nil
	}
	roles, err := v.access.roles.RolesFor(ctx, tenantID.String(), userID)
	if err != nil {
		return false, fmt.Errorf("moduleaccess: owner roles: %w", err)
	}
	return exemptRole(roles), nil
}

// exemptRole reports whether roles hold admin or auditor, whom module
// access does not apply to.
func exemptRole(roles []authz.Role) bool {
	for _, role := range roles {
		if role == authz.RoleAdmin || role == authz.RoleAuditor {
			return true
		}
	}
	return false
}

// OwnerCheck answers the "owner can't access this module" flag for the
// records of one read, resolving each distinct owner once.
type OwnerCheck struct {
	v      *Views
	ctx    context.Context
	tenant uuid.UUID
	key    string
	seen   map[string]bool
}

// Owners starts the flag check for records of the optional module key in
// r's tenant. It fails closed: a request with no tenant is an error.
func (v *Views) Owners(r *http.Request, key string) (*OwnerCheck, error) {
	raw, err := tenancy.TenantFromContext(r.Context())
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: owner check: %w", err)
	}
	tenantID, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: owner check tenant: %w", err)
	}
	return &OwnerCheck{v: v, ctx: r.Context(), tenant: tenantID, key: key, seen: map[string]bool{}}, nil
}

// Lacks reports whether owner (a users.id) cannot work in the module. A
// record with no owner is never flagged.
func (o *OwnerCheck) Lacks(owner string) (bool, error) {
	if owner == "" {
		return false, nil
	}
	if lacks, ok := o.seen[owner]; ok {
		return lacks, nil
	}
	has, err := o.v.UserHas(o.ctx, o.tenant, owner, o.key)
	if err != nil {
		return false, err
	}
	o.seen[owner] = !has
	return !has, nil
}

// MembersWithout returns the user_roles keys of the tenant's members who
// cannot work in the optional module key: narrowed to leave it out, and
// holding neither admin nor auditor live. A core module, an unlisted key,
// or a nil Views returns none.
func (v *Views) MembersWithout(ctx context.Context, tenantID uuid.UUID, key string) (map[string]bool, error) {
	out := map[string]bool{}
	if v == nil || v.access == nil || !optional(key) {
		return out, nil
	}
	stored, err := v.access.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	narrowedOut := false
	for user, modules := range stored {
		if !(Access{Modules: modules}).Has(key) {
			out[user] = true
			narrowedOut = true
		}
	}
	if !narrowedOut {
		return out, nil
	}
	roles, err := v.access.liveRoles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for user := range out {
		if exemptRole(roles[user]) {
			delete(out, user)
		}
	}
	return out, nil
}

// liveRoles returns every live role in the tenant keyed by user_roles key,
// leaving out a user whose users row is not active: the rows
// authz.DBRolesResolver would answer for each person.
func (s *Store) liveRoles(ctx context.Context, tenantID uuid.UUID) (map[string][]authz.Role, error) {
	out := map[string][]authz.Role{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT ur.user_id, ur.role
		  FROM user_roles ur
		 WHERE ur.tenant_id = $1
		   AND NOT EXISTS (
		       SELECT 1 FROM users u
		        WHERE u.tenant_id = ur.tenant_id
		          AND u.id::text = ur.user_id
		          AND u.status <> 'active')`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: query user_roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var user, role string
		if err := rows.Scan(&user, &role); err != nil {
			return nil, fmt.Errorf("moduleaccess: scan user_roles: %w", err)
		}
		if r := authz.Role(role); authz.IsCanonical(r) {
			out[user] = append(out[user], r)
		}
	}
	return out, rows.Err()
}

// optional reports whether key is an optional module in the registry.
func optional(key string) bool {
	for _, m := range featureflag.OptionalModules() {
		if m.Key == key {
			return true
		}
	}
	return false
}
