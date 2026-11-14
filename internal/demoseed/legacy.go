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
//     mark naming this tenant's slug, written no earlier than this tenant
//     was created, with no demo_seed_teardown naming the slug since then. A
//     slug match alone would also accept a later tenant that reused the slug
//     after the legacy demo tenant was gone;
//   - the tenant must hold demo data and nothing else: at least one user,
//     every user demo_only, no OAuth client bound to it, and no row in a
//     table outside teardownTables.
//
// The checks and the delete run in one SERIALIZABLE transaction. The app
// writes at READ COMMITTED, so what that buys is snapshot isolation, not
// full serializability against those writers:
//
//   - the delete removes only rows visible in the snapshot the checks read,
//     so a row committed after the checks is never deleted;
//   - a concurrent update or delete of a row the teardown deletes makes it
//     fail with 40001 (serialization_failure), and nothing is deleted;
//   - a row inserted concurrently into a table with an FK to tenants blocks
//     on the FOR UPDATE lock on the tenants row; one inserted into a table
//     with no FK to tenants is left behind as an orphan, not deleted.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrLegacyTeardownRefused wraps every refusal of TeardownLegacyButtonTenant,
// so a caller can tell a refusal from a database failure.
var ErrLegacyTeardownRefused = errors.New("demoseed: legacy demo tenant teardown refused")

// LegacyTeardownResult reports what a legacy teardown deleted, or with
// DryRun what it would have deleted: rows per table, tenants included.
type LegacyTeardownResult struct {
	TenantID uuid.UUID
	DryRun   bool
	Deleted  map[string]int64
}

// TeardownLegacyButtonTenant deletes the demo tenant named by slug, which the
// super-admin demo button created before #2043, acting as the super admin
// whose email is superAdminEmail. See the file comment for every condition
// it refuses on. With dryRun it runs every check and the delete, reports the
// counts, and rolls back.
func (s *Seeder) TeardownLegacyButtonTenant(ctx context.Context, slug, superAdminEmail string, dryRun bool) (LegacyTeardownResult, error) {
	res := LegacyTeardownResult{DryRun: dryRun}
	if err := validateSlug(slug); err != nil {
		return res, err
	}
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return res, fmt.Errorf("demoseed: legacy teardown begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	actorUserID, actorTenantID, err := resolveSuperAdmin(ctx, tx, superAdminEmail)
	if err != nil {
		return res, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE slug = $1 FOR UPDATE`, slug).Scan(&res.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, fmt.Errorf("%w: tenant %q not found", ErrLegacyTeardownRefused, slug)
	}
	if err != nil {
		return res, fmt.Errorf("demoseed: legacy teardown lookup: %w", err)
	}
	if err := assertLegacyButtonTenant(ctx, tx, res.TenantID, slug); err != nil {
		return res, err
	}
	if err := assertOnlyDemoData(ctx, tx, res.TenantID, slug); err != nil {
		return res, err
	}
	res.Deleted, err = deleteDemoTenant(ctx, tx, res.TenantID, slug, actorUserID, actorTenantID,
		map[string]any{"legacy_button_tenant": true},
	)
	if err != nil {
		return res, concurrentWriteErr(err)
	}
	if dryRun {
		return res, nil // the deferred Rollback undoes the delete
	}
	if err := tx.Commit(ctx); err != nil {
		return res, concurrentWriteErr(fmt.Errorf("demoseed: legacy teardown commit: %w", err))
	}
	return res, nil
}

// ErrConcurrentWrite reports that a write by another transaction conflicted
// with the legacy teardown (SQLSTATE 40001). The transaction rolled back, so
// nothing was deleted, and a rerun re-checks against the new state.
var ErrConcurrentWrite = errors.New("demoseed: concurrent write detected; nothing was deleted; rerun")

// concurrentWriteErr maps a serialization failure to ErrConcurrentWrite and
// returns any other error unchanged.
func concurrentWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return fmt.Errorf("%w (%v)", ErrConcurrentWrite, err)
	}
	return err
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
	// The mark is tied to this tenant by time: the button created the tenant
	// and wrote the mark in one transaction, so the mark is never older than
	// the tenant row. A teardown naming the slug since then means the slug
	// has changed hands, and the mark may belong to a tenant already gone.
	var inPlace, ownMark, legacyMark, laterTeardown bool
	err := tx.QueryRow(ctx, `
		WITH t AS (SELECT created_at FROM tenants WHERE id = $1)
		SELECT
			EXISTS (SELECT 1 FROM demo_seed_rows WHERE tenant_id = $1),
			EXISTS (
				SELECT 1 FROM me_audit_log
				WHERE tenant_id = $1
				  AND action IN ('demo_seed_apply', 'demo_seed_teardown')
				  AND after->>'slug' = $2
			),
			EXISTS (
				SELECT 1 FROM me_audit_log m, t
				WHERE m.tenant_id <> $1
				  AND m.action = 'demo_seed_apply'
				  AND m.after->>'slug' = $2
				  AND COALESCE(m.after->>'in_place', '') <> 'true'
				  AND m.occurred_at >= t.created_at
			),
			EXISTS (
				SELECT 1 FROM me_audit_log m, t
				WHERE m.action = 'demo_seed_teardown'
				  AND m.after->>'slug' = $2
				  AND m.occurred_at >= t.created_at
			) OR EXISTS (
				SELECT 1 FROM super_admin_audit_log a, t
				WHERE a.action = 'demo_seed_teardown'
				  AND a.payload_json->>'slug' = $2
				  AND a.occurred_at >= t.created_at
			)
	`, tenantID, slug).Scan(&inPlace, &ownMark, &legacyMark, &laterTeardown)
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
		return fmt.Errorf("%w: no pre-#2043 demo button mark names tenant %q at or after its creation",
			ErrLegacyTeardownRefused, slug)
	case laterTeardown:
		return fmt.Errorf("%w: a demo teardown has named slug %q since tenant creation; the mark may belong to an earlier tenant",
			ErrLegacyTeardownRefused, slug)
	}
	return nil
}

// postSeedDerivedTables are the read models the button's post-seed
// evaluator (EvaluateSeededTenant, slice 671) writes for the demo tenant.
// They are computed from the tenant's controls and evidence ledger, so in a
// tenant whose controls and evidence are all demo they are demo too.
var postSeedDerivedTables = []string{"control_evaluations", "evidence_freshness"}

// assertOnlyDemoData refuses unless the tenant looks like the seed's output
// and nothing more:
//   - at least one user, and every user demo_only (the seed always writes
//     demo users; a tenant with none is not a demo tenant);
//   - no OAuth client bound to it (oauth_clients carries bound_tenant_id,
//     not tenant_id, so the table scan below cannot see it; a machine-fed
//     tenant needs no human user);
//   - no row in a tenant-scoped table outside teardownTables and
//     postSeedDerivedTables (audit trails excepted, as everywhere in this
//     package).
//
// A demo tenant holds no real person's account, so every row in a
// teardownTables table was written by the seed or by someone exploring the
// demo as a demo user.
func assertOnlyDemoData(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, slug string) error {
	var demoUsers, realUsers, boundClients int
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM users WHERE tenant_id = $1 AND demo_only IS TRUE),
			(SELECT count(*) FROM users WHERE tenant_id = $1 AND demo_only IS NOT TRUE),
			(SELECT count(*) FROM oauth_clients WHERE bound_tenant_id = $1)
	`, tenantID).Scan(&demoUsers, &realUsers, &boundClients); err != nil {
		return fmt.Errorf("demoseed: legacy teardown count users: %w", err)
	}
	switch {
	case realUsers > 0:
		return fmt.Errorf("%w: tenant %q has %d users that are not demo users", ErrLegacyTeardownRefused, slug, realUsers)
	case demoUsers == 0:
		return fmt.Errorf("%w: tenant %q has no demo users; the demo seed always writes them", ErrLegacyTeardownRefused, slug)
	case boundClients > 0:
		return fmt.Errorf("%w: tenant %q has %d OAuth clients bound to it", ErrLegacyTeardownRefused, slug, boundClients)
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
