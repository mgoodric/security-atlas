package demoseed

// Removing a demo tenant the super-admin button created before #2043
// (#2153).
//
// Before #2043 the button's seed wrote its demo_seed_apply me_audit_log mark
// into the clicking admin's organisation, not into the demo tenant it
// created. tenantWasSeeded now reads only a tenant's own mark naming its own
// slug (#2043 D2), so such a tenant has no mark Teardown accepts, and
// neither the button nor `demo teardown` can remove it.
//
// TeardownLegacyButtonTenant is the one path that can. It is narrower than
// Teardown on every axis:
//
//   - the actor must be an active super admin, named by email, and is
//     recorded on the teardown audit rows;
//   - the tenant must carry no mark of its own and must not have been seeded
//     in place (Teardown handles both);
//   - another tenant's me_audit_log must hold the legacy demo_seed_apply
//     mark naming this tenant's slug;
//   - the tenant must hold no non-demo data: every user is a demo_only user,
//     and no table outside teardownTables holds a row for it.
//
// The checks and the delete run in one transaction, so data written between
// the check and the delete cannot be swept up.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrLegacyTeardownRefused wraps every refusal of TeardownLegacyButtonTenant,
// so a caller can tell a refusal from a database failure.
var ErrLegacyTeardownRefused = errors.New("demoseed: legacy demo tenant teardown refused")

// TeardownLegacyButtonTenant deletes the demo tenant named by slug, which the
// super-admin demo button created before #2043, acting as the super admin
// whose email is superAdminEmail. See the file comment for every condition
// it refuses on.
func (s *Seeder) TeardownLegacyButtonTenant(ctx context.Context, slug, superAdminEmail string) error {
	if err := validateSlug(slug); err != nil {
		return err
	}
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("demoseed: legacy teardown begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	actorUserID, actorTenantID, err := resolveSuperAdmin(ctx, tx, superAdminEmail)
	if err != nil {
		return err
	}
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE slug = $1 FOR UPDATE`, slug).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: tenant %q not found", ErrLegacyTeardownRefused, slug)
	}
	if err != nil {
		return fmt.Errorf("demoseed: legacy teardown lookup: %w", err)
	}
	if err := assertLegacyButtonTenant(ctx, tx, tenantID, slug); err != nil {
		return err
	}
	if err := assertOnlyDemoData(ctx, tx, tenantID, slug); err != nil {
		return err
	}
	if err := deleteDemoTenant(ctx, tx, tenantID, slug, actorUserID, actorTenantID,
		map[string]any{"legacy_button_tenant": true},
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: legacy teardown commit: %w", err)
	}
	return nil
}

// resolveSuperAdmin returns the user id and tenant of the one active user
// with email who holds a super_admins grant. No match, or more than one, is
// a refusal: the teardown must be attributable to exactly one super admin.
func resolveSuperAdmin(ctx context.Context, tx pgx.Tx, email string) (uuid.UUID, uuid.UUID, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: a super admin email is required", ErrLegacyTeardownRefused)
	}
	rows, err := tx.Query(ctx, `
		SELECT u.id, u.tenant_id
		FROM users u
		JOIN super_admins sa ON sa.user_id = u.id
		WHERE lower(u.email) = lower($1) AND u.status = 'active'
	`, email)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("demoseed: resolve super admin: %w", err)
	}
	type match struct{ user, tenant uuid.UUID }
	matches, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (match, error) {
		var m match
		err := r.Scan(&m.user, &m.tenant)
		return m, err
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("demoseed: resolve super admin: %w", err)
	}
	if len(matches) != 1 {
		return uuid.Nil, uuid.Nil, fmt.Errorf(
			"%w: %q matches %d active super admins, want exactly 1", ErrLegacyTeardownRefused, email, len(matches))
	}
	return matches[0].user, matches[0].tenant, nil
}

// assertLegacyButtonTenant refuses unless tenantID is a pre-#2043 button
// tenant: no in-place rows, no mark of its own, and a legacy mark naming its
// slug in another tenant.
func assertLegacyButtonTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, slug string) error {
	var inPlace, ownMark, legacyMark bool
	err := tx.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM demo_seed_rows WHERE tenant_id = $1),
			EXISTS (
				SELECT 1 FROM me_audit_log
				WHERE tenant_id = $1
				  AND action IN ('demo_seed_apply', 'demo_seed_teardown')
				  AND after->>'slug' = $2
			),
			EXISTS (
				SELECT 1 FROM me_audit_log
				WHERE tenant_id <> $1
				  AND action = 'demo_seed_apply'
				  AND after->>'slug' = $2
				  AND COALESCE(after->>'in_place', '') <> 'true'
			)
	`, tenantID, slug).Scan(&inPlace, &ownMark, &legacyMark)
	if err != nil {
		return fmt.Errorf("demoseed: legacy teardown probe: %w", err)
	}
	switch {
	case inPlace:
		return fmt.Errorf("%w: tenant %q was seeded in place; use `demo teardown`", ErrLegacyTeardownRefused, slug)
	case ownMark:
		return fmt.Errorf("%w: tenant %q carries its own demo seed mark; use `demo teardown --tenant-slug=%s`",
			ErrLegacyTeardownRefused, slug, slug)
	case !legacyMark:
		return fmt.Errorf("%w: no pre-#2043 demo button mark names tenant %q", ErrLegacyTeardownRefused, slug)
	}
	return nil
}

// postSeedDerivedTables are the read models the button's post-seed
// evaluator (EvaluateSeededTenant, slice 671) writes for the demo tenant.
// They are computed from the tenant's controls and evidence ledger, so in a
// tenant whose controls and evidence are all demo they are demo too.
var postSeedDerivedTables = []string{"control_evaluations", "evidence_freshness"}

// assertOnlyDemoData refuses when the tenant holds anything the demo seed
// did not write: a user that is not demo_only, or a row in a tenant-scoped
// table outside teardownTables and postSeedDerivedTables (audit trails
// excepted, as everywhere in this package). A demo tenant holds no real person's account, so every row in a
// teardownTables table was written by the seed or by someone exploring the
// demo as a demo user.
func assertOnlyDemoData(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, slug string) error {
	var realUsers int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND demo_only IS NOT TRUE`, tenantID,
	).Scan(&realUsers); err != nil {
		return fmt.Errorf("demoseed: legacy teardown count users: %w", err)
	}
	if realUsers > 0 {
		return fmt.Errorf("%w: tenant %q has %d users that are not demo users", ErrLegacyTeardownRefused, slug, realUsers)
	}

	tables, err := tenantScopedTables(ctx, tx)
	if err != nil {
		return err
	}
	seeded := make(map[string]bool, len(teardownTables)+len(postSeedDerivedTables))
	for _, t := range teardownTables {
		seeded[t] = true
	}
	for _, t := range postSeedDerivedTables {
		seeded[t] = true
	}
	var foreign []string
	for _, t := range tables {
		if seeded[t.name] {
			continue
		}
		var has bool
		if err := tx.QueryRow(ctx,
			fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE tenant_id = $1)`, pgx.Identifier{t.name}.Sanitize()),
			tenantID,
		).Scan(&has); err != nil {
			return fmt.Errorf("demoseed: legacy teardown probe %s: %w", t.name, err)
		}
		if has {
			foreign = append(foreign, t.name)
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return fmt.Errorf("%w: tenant %q holds rows the demo seed does not write, in: %s",
			ErrLegacyTeardownRefused, slug, strings.Join(foreign, ", "))
	}
	return nil
}
