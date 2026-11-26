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
// live user_roles, super-admin grant and stored module access at the
// time of the read, so a narrowing or a role change shows up without
// touching the record.
//
// A person can work in an optional module when they are a super-admin
// (exempt, as in ForCaller), their live user_roles hold admin or auditor
// (exempt, as in Gate), or they hold some other live role and an admin
// has not narrowed their module access to leave it out (no stored
// narrowing is "all"). A person with no live role lacks every optional
// module (#2501): user sessions decide on live roles only (#2496), so
// such a person cannot use the module on their own requests either.
// Whether the module is switched on is not asked: a switched-off module's
// records are not shown to anyone.

// memberLacks is the rule above for one person: super is their
// super-admin grant, roles their live user_roles, modules their stored
// access (nil is "all").
func memberLacks(super bool, roles []authz.Role, modules []string, key string) bool {
	if super || exemptRole(roles) {
		return false
	}
	if len(roles) == 0 {
		return true
	}
	return !(Access{Modules: modules}).Has(key)
}

// UserHas reports whether userID (a users.id or user_roles key) can work
// in the optional module key. A core module, or a key the registry does
// not list, answers true. A nil Views (a unit-test server with no
// database) answers true.
func (v *Views) UserHas(ctx context.Context, tenantID uuid.UUID, userID, key string) (bool, error) {
	if v == nil || v.access == nil || !optional(key) || userID == "" {
		return true, nil
	}
	roles, err := v.access.roles.RolesFor(ctx, tenantID.String(), userID)
	if err != nil {
		return false, fmt.Errorf("moduleaccess: owner roles: %w", err)
	}
	if exemptRole(roles) {
		return true, nil
	}
	var modules []string
	if len(roles) > 0 {
		if modules, err = v.access.Get(ctx, tenantID, identity.RolesKey(userID)); err != nil {
			return false, err
		}
		if (Access{Modules: modules}).Has(key) {
			return true, nil
		}
	}
	super, err := v.access.superAdmin(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	return !memberLacks(super, roles, modules, key), nil
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

// MembersWithout returns the users.id of the tenant's users who cannot
// work in the optional module key, by memberLacks: a user with no live
// role (none granted, or deactivated) is among them unless a super-admin.
// A core module, an unlisted key, or a nil Views returns none.
func (v *Views) MembersWithout(ctx context.Context, tenantID uuid.UUID, key string) (map[string]bool, error) {
	out := map[string]bool{}
	if v == nil || v.access == nil || !optional(key) {
		return out, nil
	}
	users, err := v.access.tenantUsers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	stored, err := v.access.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	roles, err := v.access.liveRoles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for user, super := range users {
		if memberLacks(super, roles[user], stored[user], key) {
			out[user] = true
		}
	}
	return out, nil
}

// superAdmin reports whether userID names an active user of the tenant
// holding the platform super-admin grant. A key that names no user (an
// API key) is not one.
func (s *Store) superAdmin(ctx context.Context, tenantID uuid.UUID, userID string) (bool, error) {
	id, ok := identity.UserID(identity.RolesKey(userID))
	if !ok {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("moduleaccess: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return false, err
	}
	var super bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM super_admins s
		      JOIN users u ON u.id = s.user_id
		     WHERE s.user_id = $1 AND u.tenant_id = $2 AND u.status = 'active')`,
		id, tenantID).Scan(&super); err != nil {
		return false, fmt.Errorf("moduleaccess: query super_admins: %w", err)
	}
	return super, nil
}

// tenantUsers returns every user of the tenant by users.id, each with
// whether they are an active super-admin.
func (s *Store) tenantUsers(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error) {
	out := map[string]bool{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT u.id::text,
		       u.status = 'active' AND EXISTS (SELECT 1 FROM super_admins s WHERE s.user_id = u.id)
		  FROM users u
		 WHERE u.tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("moduleaccess: query users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			user  string
			super bool
		)
		if err := rows.Scan(&user, &super); err != nil {
			return nil, fmt.Errorf("moduleaccess: scan users: %w", err)
		}
		out[user] = super
	}
	return out, rows.Err()
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
