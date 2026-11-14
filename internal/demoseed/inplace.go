package demoseed

// In-place demo seed (#1765). On a single-tenant install, `atlas-cli demo
// seed` loads the demo dataset into the install's only tenant instead of
// creating a second one, so trying the product never makes an install
// multi-tenant. It does so only while that tenant holds nothing beyond the
// stock bootstrap seed (stock.go), so demo and real data never mix.
//
// That tenant keeps real rows (the bootstrap user, scope, stock controls), so
// teardown cannot delete by tenant_id. Instead ApplyInPlace snapshots the
// primary key of every tenant-scoped row before writing, and after writing
// records each new row in demo_seed_rows. teardownInPlace deletes exactly the
// recorded rows and keeps the tenant.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// ErrTenantHasOrgData — ApplyInPlace refused because the tenant holds
// organisation data beyond the stock bootstrap seed. errors.Is-matchable.
var ErrTenantHasOrgData = errors.New("demoseed: tenant holds organisation data beyond the stock bootstrap seed")

// OrgData counts a tenant's rows that are not part of the stock bootstrap
// seed. Any non-zero count makes ApplyInPlace refuse.
type OrgData struct {
	Controls          int // controls whose bundle_id is not a stock SOC 2 bundle
	Risks             int
	Evidence          int // evidence_records
	Policies          int // policies not created by the stock policy seed
	FrameworkVersions int // tenant-private framework versions
}

// Empty reports whether the tenant holds only the stock bootstrap seed.
func (o OrgData) Empty() bool { return o == OrgData{} }

// String names the non-zero counts, e.g. "3 controls, 1 risk".
func (o OrgData) String() string {
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(o.Controls, "non-stock control", "non-stock controls")
	add(o.Risks, "risk", "risks")
	add(o.Evidence, "evidence record", "evidence records")
	add(o.Policies, "non-stock policy", "non-stock policies")
	add(o.FrameworkVersions, "tenant framework version", "tenant framework versions")
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// ApplyInPlace writes the demo dataset into the existing tenant tenantID and
// records every row it adds, so TeardownTenant can remove exactly those.
//
//   - A tenant already seeded (in place, or a demo tenant made by Apply)
//     returns Result.Idempotent with nothing written.
//   - A tenant holding organisation data beyond the stock bootstrap seed is
//     refused with ErrTenantHasOrgData, and nothing is written.
//
// The tenant row is never created or changed, so the tenant count is
// unchanged.
func (s *Seeder) ApplyInPlace(ctx context.Context, tenantID uuid.UUID, actorUserID uuid.UUID, actorTenantID uuid.UUID) (Result, error) {
	if tenantID == uuid.Nil {
		return Result{}, fmt.Errorf("demoseed: ApplyInPlace: zero tenant id")
	}
	var tenant tenantRow
	err := s.authPool.QueryRow(ctx,
		`SELECT id, name, COALESCE(slug, ''), is_bootstrap_tenant FROM tenants WHERE id = $1`, tenantID,
	).Scan(&tenant.ID, &tenant.Name, &tenant.Slug, &tenant.IsBootstrapTenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("demoseed: tenant %s not found", tenantID)
	}
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: lookup tenant: %w", err)
	}

	inPlace, err := s.tenantSeededInPlace(ctx, tenantID)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: probe seeded state: %w", err)
	}
	seeded, err := s.tenantWasSeeded(ctx, tenantID)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: probe seeded state: %w", err)
	}
	if inPlace || seeded {
		return Result{TenantID: tenantID, TenantSlug: tenant.Slug, Idempotent: true, InPlace: inPlace}, nil
	}

	plaintext, err := GenerateDemoPassword()
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: generate password: %w", err)
	}
	passwordHash, err := password.Hash(plaintext)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: hash password: %w", err)
	}

	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the tenant row so two concurrent in-place seeds serialize: the
	// second sees the first's org data and refuses.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, tenantID); err != nil {
		return Result{}, fmt.Errorf("demoseed: lock tenant: %w", err)
	}
	od, err := countOrgData(ctx, tx, tenantID)
	if err != nil {
		return Result{}, err
	}
	if !od.Empty() {
		return Result{}, fmt.Errorf(
			"refusing to seed: tenant %q holds organisation data beyond the stock bootstrap seed (%s), "+
				"and demo data is never mixed with real data. To try the demo, either opt in to multi-tenant "+
				"(set ATLAS_ALLOW_MULTI_TENANT=true) and seed a separate demo tenant with "+
				"`atlas-cli demo seed --tenant-slug demo`, or use a separate install: %w",
			tenant.Name, od, ErrTenantHasOrgData,
		)
	}

	tables, err := tenantScopedTables(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	before, err := snapshotTenantRows(ctx, tx, tables, tenantID, false)
	if err != nil {
		return Result{}, err
	}

	scope, err := ensureProdScopeCell(ctx, tx, tenantID)
	if err != nil {
		return Result{}, err
	}
	fixtures := s.buildFixturesFor(tenant, scope)
	fixtures.inPlace = true
	res, err := s.writeDataset(ctx, tx, fixtures, ApplyInput{
		Slug:          tenant.Slug,
		ActorUserID:   actorUserID,
		ActorTenantID: actorTenantID,
	}, passwordHash, plaintext)
	if err != nil {
		return Result{}, err
	}

	if s.beforeRecord != nil {
		s.beforeRecord(ctx)
	}
	// Only this transaction's rows: at READ COMMITTED the after-snapshot
	// also sees rows other sessions committed meanwhile, and those are not
	// the seed's to record or to delete.
	after, err := snapshotTenantRows(ctx, tx, tables, tenantID, true)
	if err != nil {
		return Result{}, err
	}
	recorded, err := recordNewRows(ctx, tx, tenantID, before, after)
	if err != nil {
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("demoseed: commit: %w", err)
	}
	res.InPlace = true
	res.RecordedRows = recorded
	return res, nil
}

func countOrgData(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (OrgData, error) {
	var od OrgData
	err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM controls WHERE tenant_id = $1 AND NOT (bundle_id = ANY($2::text[]))),
			(SELECT count(*) FROM risks WHERE tenant_id = $1),
			(SELECT count(*) FROM evidence_records WHERE tenant_id = $1),
			(SELECT count(*) FROM policies WHERE tenant_id = $1 AND created_by IS DISTINCT FROM $3),
			(SELECT count(*) FROM framework_versions WHERE tenant_id = $1)
	`, tenantID, stockControlBundleIDs, stockPolicyCreatedBy).Scan(
		&od.Controls, &od.Risks, &od.Evidence, &od.Policies, &od.FrameworkVersions,
	)
	if err != nil {
		return OrgData{}, fmt.Errorf("demoseed: count organisation data: %w", err)
	}
	return od, nil
}

// tenantSeededInPlace reports whether ApplyInPlace recorded rows for the
// tenant.
func (s *Seeder) tenantSeededInPlace(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var has bool
	err := s.authPool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM demo_seed_rows WHERE tenant_id = $1)`, tenantID,
	).Scan(&has)
	return has, err
}

// ensureProdScopeCell returns the tenant's `environment` dimension and its
// {"environment":"prod"} cell, adding either when missing. The bootstrap
// seed (deploy/docker/bootstrap/seed.sql) normally provides both; reusing
// them keeps the demo evidence in the cell real evidence lands in.
func ensureProdScopeCell(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (scopeRow, error) {
	var sc scopeRow
	err := tx.QueryRow(ctx,
		`SELECT id FROM scope_dimensions WHERE tenant_id = $1 AND name = 'environment'`, tenantID,
	).Scan(&sc.DimensionID)
	if errors.Is(err, pgx.ErrNoRows) {
		sc.DimensionID = uuid.New()
		err = writeScopeDimension(ctx, tx, tenantID, sc.DimensionID)
	}
	if err != nil {
		return scopeRow{}, fmt.Errorf("demoseed: resolve scope dimension: %w", err)
	}
	err = tx.QueryRow(ctx,
		`SELECT id FROM scope_cells WHERE tenant_id = $1 AND dimensions_hash = $2`, tenantID, prodScopeCellHash(),
	).Scan(&sc.CellID)
	if errors.Is(err, pgx.ErrNoRows) {
		sc.CellID = uuid.New()
		err = writeScopeCell(ctx, tx, tenantID, sc.CellID)
	}
	if err != nil {
		return scopeRow{}, fmt.Errorf("demoseed: resolve scope cell: %w", err)
	}
	return sc, nil
}

// tenantTable is a table with a tenant_id column and the SQL expression
// that yields one of its rows' identity as JSON.
type tenantTable struct {
	name    string
	keyExpr string // over alias t
}

// tenantScopedTables lists every table in the current schema with a
// tenant_id column (partitions excluded; their parent covers them). A row's
// key is its primary-key columns, or the whole row when there is no primary
// key. Discovering the tables, rather than listing them, means a writer added
// to the seed later is recorded without anyone remembering to.
func tenantScopedTables(ctx context.Context, tx pgx.Tx) ([]tenantTable, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.relname,
		       COALESCE((
		           SELECT array_agg(a.attname::text ORDER BY k.ord)
		           FROM pg_index i
		           CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		           JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		           WHERE i.indrelid = c.oid AND i.indisprimary
		       ), '{}')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()
		  AND c.relkind IN ('r', 'p')
		  AND NOT c.relispartition
		  AND c.relname <> 'demo_seed_rows'
		  AND EXISTS (
		      SELECT 1 FROM pg_attribute a
		      WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		  )
		ORDER BY c.relname
	`)
	if err != nil {
		return nil, fmt.Errorf("demoseed: list tenant tables: %w", err)
	}
	type raw struct {
		name string
		pk   []string
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var t raw
		err := r.Scan(&t.name, &t.pk)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("demoseed: scan tenant tables: %w", err)
	}
	tables := make([]tenantTable, 0, len(found))
	for _, f := range found {
		tables = append(tables, tenantTable{name: f.name, keyExpr: rowKeyExpr(f.pk)})
	}
	return tables, nil
}

// rowKeyExpr builds jsonb_build_object('col', t."col", ...) over the primary
// key, or to_jsonb(t.*) when pk is empty.
func rowKeyExpr(pk []string) string {
	if len(pk) == 0 {
		return "to_jsonb(t.*)"
	}
	args := make([]string, 0, 2*len(pk))
	for _, col := range pk {
		args = append(args, "'"+strings.ReplaceAll(col, "'", "''")+"'", "t."+pgx.Identifier{col}.Sanitize())
	}
	return "jsonb_build_object(" + strings.Join(args, ", ") + ")"
}

// snapshotTenantRows returns, per table, the key of every row the tenant
// holds; with ownOnly, only rows tx itself last wrote (their xmin is tx's
// transaction id, the test assertFreshDemoTenant uses). The seed opens no
// savepoints, so every row it writes carries that xmin.
func snapshotTenantRows(ctx context.Context, tx pgx.Tx, tables []tenantTable, tenantID uuid.UUID, ownOnly bool) (map[string]map[string]struct{}, error) {
	filter := ""
	if ownOnly {
		filter = ` AND t.xmin::text::bigint = (txid_current() & 4294967295)`
	}
	snap := make(map[string]map[string]struct{}, len(tables))
	for _, t := range tables {
		rows, err := tx.Query(ctx,
			fmt.Sprintf(`SELECT (%s)::text FROM %s t WHERE t.tenant_id::text = $1%s`,
				t.keyExpr, pgx.Identifier{t.name}.Sanitize(), filter),
			tenantID.String(),
		)
		if err != nil {
			return nil, fmt.Errorf("demoseed: snapshot %s: %w", t.name, err)
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, fmt.Errorf("demoseed: snapshot %s: %w", t.name, err)
		}
		set := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			set[k] = struct{}{}
		}
		snap[t.name] = set
	}
	return snap, nil
}

// recordNewRows writes every key in after (the rows tx wrote) but not in
// before (rows that already existed, which tx may have updated) to
// demo_seed_rows and returns how many it wrote.
func recordNewRows(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, before, after map[string]map[string]struct{}) (int, error) {
	var tables, keys []string
	for table, set := range after {
		for k := range set {
			if _, existed := before[table][k]; !existed {
				tables = append(tables, table)
				keys = append(keys, k)
			}
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo_seed_rows (tenant_id, table_name, row_key)
		SELECT $1, u.table_name, u.row_key::jsonb
		FROM unnest($2::text[], $3::text[]) AS u(table_name, row_key)
	`, tenantID, tables, keys); err != nil {
		return 0, fmt.Errorf("demoseed: record seeded rows: %w", err)
	}
	return len(keys), nil
}

// teardownInPlace deletes exactly the rows ApplyInPlace recorded for the
// tenant, then the records themselves, in one transaction. The tenant row and
// every row the seed did not add stay. It writes only a
// super_admin_audit_log row (no tenant_id), so the tenant's rows end up as
// they were before the seed.
//
// Tables are tried in teardownTables order, then alphabetically; a delete
// blocked by a foreign key from a not-yet-deleted demo row is retried on the
// next pass.
func (s *Seeder) teardownInPlace(ctx context.Context, tenantID uuid.UUID, slug string, actorUserID uuid.UUID, actorTenantID uuid.UUID) error {
	tx, err := s.authPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("demoseed: teardown begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT table_name, array_agg(row_key::text)
		FROM demo_seed_rows WHERE tenant_id = $1
		GROUP BY table_name
	`, tenantID)
	if err != nil {
		return fmt.Errorf("demoseed: teardown read records: %w", err)
	}
	remaining := map[string][]string{}
	var table string
	var keys []string
	if _, err := pgx.ForEachRow(rows, []any{&table, &keys}, func() error {
		remaining[table] = keys
		return nil
	}); err != nil {
		return fmt.Errorf("demoseed: teardown read records: %w", err)
	}

	order := inPlaceTeardownOrder(remaining)
	for len(remaining) > 0 {
		progressed := false
		for _, table := range order {
			keys, ok := remaining[table]
			if !ok {
				continue
			}
			blocked, err := deleteRecordedRows(ctx, tx, table, tenantID, keys)
			if err != nil {
				return err
			}
			if !blocked {
				delete(remaining, table)
				progressed = true
			}
		}
		if !progressed {
			left := make([]string, 0, len(remaining))
			for t := range remaining {
				left = append(left, t)
			}
			sort.Strings(left)
			return fmt.Errorf("demoseed: teardown: rows in %s are still referenced by rows the demo seed did not add; refusing to remove them",
				strings.Join(left, ", "))
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM demo_seed_rows WHERE tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("demoseed: teardown clear records: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"slug": slug, "in_place": true, "demo_seed_v": DemoSeedVersion})
	if err != nil {
		return fmt.Errorf("demoseed: marshal audit payload: %w", err)
	}
	if err := writeSuperAdminAuditRow(ctx, tx, "demo_seed_teardown",
		actorUserID, actorTenantID, tenantID, payload,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: teardown commit: %w", err)
	}
	return nil
}

// inPlaceTeardownOrder is teardownTables, then any other recorded table in
// alphabetical order.
func inPlaceTeardownOrder(recorded map[string][]string) []string {
	order := slices.Clone(teardownTables)
	var extra []string
	for t := range recorded {
		if !slices.Contains(order, t) {
			extra = append(extra, t)
		}
	}
	sort.Strings(extra)
	return append(order, extra...)
}

// deleteRecordedRows deletes the tenant's rows of table whose key is in
// keys, inside a savepoint. blocked is true, and nothing is deleted, when a
// foreign key from another (demo) row still references one of them.
func deleteRecordedRows(ctx context.Context, tx pgx.Tx, table string, tenantID uuid.UUID, keys []string) (blocked bool, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("demoseed: teardown savepoint: %w", err)
	}
	_, err = sp.Exec(ctx,
		fmt.Sprintf(`DELETE FROM %s t WHERE t.tenant_id::text = $1 AND to_jsonb(t.*) @> ANY($2::text[]::jsonb[])`,
			pgx.Identifier{table}.Sanitize()),
		tenantID.String(), keys,
	)
	if err != nil {
		_ = sp.Rollback(ctx)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return true, nil
		}
		if pgIsUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("demoseed: teardown delete %s: %w", table, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return false, fmt.Errorf("demoseed: teardown release savepoint: %w", err)
	}
	return false, nil
}
