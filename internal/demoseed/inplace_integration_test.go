//go:build integration

// Integration tests for #1765: demo seed into the sole tenant on a fresh
// single-tenant install.
//
//	AC-1 a fresh single-tenant install is seeded in place; tenant count unchanged
//	AC-2 teardown leaves exactly the pre-seed rows (no orphans, none removed)
//	AC-3 a non-stock control makes seed refuse with the explanation, writing nothing
//	AC-4 with ATLAS_ALLOW_MULTI_TENANT=true, seed still creates its own demo tenant
//
// The shared integration database always holds many tenants, so these tests
// stand in for "the install's only tenant" with WithTenantResolver, which is
// exactly the answer tenancy.ResolveImplicitTenant gives on a single-tenant
// install.
//
// Run with: go test -tags=integration -p 1 ./internal/demoseed/...

package demoseed_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// freshInstallTenant creates a tenant shaped like the one
// deploy/docker/bootstrap/seed.sql + bootstrap.sh leave behind: the
// environment dimension, the prod scope cell, the default admin with local
// credentials and the admin role, and one stock SOC 2 control. It returns
// the tenant's ref and removes everything under it when the test ends.
func freshInstallTenant(t *testing.T) tenancy.TenantRef {
	t.Helper()
	ctx := context.Background()
	ref := tenancy.TenantRef{
		ID:   uuid.New(),
		Slug: "demo-solo-" + uuid.NewString()[:8],
		Name: "Solo Org",
	}
	userID := uuid.New()
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`, []any{ref.ID, ref.Name, ref.Slug}},
		{`INSERT INTO scope_dimensions (id, tenant_id, name, value_type, allowed_values, is_required, is_builtin)
		  VALUES ($1, $2, 'environment', 'string', '["prod", "staging", "dev"]'::jsonb, FALSE, TRUE)`,
			[]any{uuid.New(), ref.ID}},
		{`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		  VALUES ($1, $2, 'Default (prod)', '{"environment": "prod"}'::jsonb,
		          encode(digest('{"environment":"prod"}', 'sha256'), 'hex'))`,
			[]any{uuid.New(), ref.ID}},
		{`INSERT INTO users (id, tenant_id, email, display_name, status)
		  VALUES ($1, $2, 'admin@solo.example', 'Default Administrator', 'active')`,
			[]any{userID, ref.ID}},
		{`INSERT INTO local_credentials (user_id, tenant_id, password_hash, algo, params)
		  VALUES ($1, $2, 'not-a-real-hash', 'argon2id', '{}'::jsonb)`,
			[]any{userID, ref.ID}},
		{`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
		  VALUES ($1, $2, 'admin', 'system:bootstrap_seed')`,
			[]any{ref.ID, userID.String()}},
	}
	t.Cleanup(func() { removeTenant(t, ref.ID) })
	for _, st := range stmts {
		if _, err := adminPool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("seed fresh-install tenant: %v\n%s", err, st.sql)
		}
	}
	insertControl(t, ref.ID, "soc2_cc6_1_logical_access_baseline")
	return ref
}

// insertControl adds an active control with the given bundle_id.
func insertControl(t *testing.T, tenantID uuid.UUID, bundleID string) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO controls
		 (id, tenant_id, title, description, control_family,
		  implementation_type, owner_role, lifecycle_state, applicability_expr, bundle_id)
		 VALUES ($1, $2, $3, 'A control.', 'IAC',
		         'automated'::control_implementation_type, 'security', 'active'::control_lifecycle_state, 'true', $4)`,
		uuid.New(), tenantID, "Control "+bundleID, bundleID,
	); err != nil {
		t.Fatalf("insert control %s: %v", bundleID, err)
	}
}

// removeTenant deletes every row under tenantID, then the tenant, retrying
// tables a foreign key still blocks.
func removeTenant(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tables := tenantTables(t)
	for pass := 0; pass < 10 && len(tables) > 0; pass++ {
		var blocked []string
		for _, table := range tables {
			if _, err := adminPool.Exec(ctx,
				fmt.Sprintf(`DELETE FROM %q WHERE tenant_id::text = $1`, table), tenantID.String(),
			); err != nil {
				blocked = append(blocked, table)
			}
		}
		tables = blocked
	}
	if _, err := adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
		t.Logf("cleanup tenant %s: %v (tables left: %v)", tenantID, err, tables)
	}
}

// tenantTables lists every table with a tenant_id column (partitions
// excluded), independently of the seeder's own discovery.
func tenantTables(t *testing.T) []string {
	t.Helper()
	rows, err := adminPool.Query(context.Background(), `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, name)
	}
	return out
}

// tenantRows returns every row the tenant holds, as full-row JSON sorted per
// table, plus the tenant row itself under "tenants".
func tenantRows(t *testing.T, tenantID uuid.UUID) map[string][]string {
	t.Helper()
	ctx := context.Background()
	out := map[string][]string{}
	read := func(table, sql string) {
		rows, err := adminPool.Query(ctx, sql, tenantID.String())
		if err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			out[table] = append(out[table], r)
		}
		sort.Strings(out[table])
	}
	for _, table := range tenantTables(t) {
		read(table, fmt.Sprintf(`SELECT to_jsonb(t.*)::text FROM %q t WHERE t.tenant_id::text = $1`, table))
	}
	read("tenants", `SELECT to_jsonb(t.*)::text FROM tenants t WHERE t.id::text = $1`)
	return out
}

// withoutAuditTables drops the tenant's audit-log tables from a tenantRows
// snapshot. Matt's M2 call (#2039): the seed's audit rows are a trace and
// survive teardown, so "exactly the pre-seed rows" excludes them.
func withoutAuditTables(rows map[string][]string) map[string][]string {
	out := map[string][]string{}
	for table, r := range rows {
		if demoseed.IsTenantAuditTable(table) {
			continue
		}
		out[table] = r
	}
	return out
}

// seedAuditTrail returns the tenant's me_audit_log actions written by the
// in-place seed and its teardown, oldest first, with the teardown's
// removed_rows count.
func seedAuditTrail(t *testing.T, tenantID uuid.UUID) (actions []string, removedRows int) {
	t.Helper()
	rows, err := adminPool.Query(context.Background(), `
		SELECT action, COALESCE((after->>'removed_rows')::int, 0)
		FROM me_audit_log
		WHERE tenant_id = $1 AND action IN ('demo_seed_apply', 'demo_seed_teardown')
		  AND after->>'in_place' = 'true'
		ORDER BY occurred_at, action`, tenantID)
	if err != nil {
		t.Fatalf("read me_audit_log: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var n int
		if err := rows.Scan(&a, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		actions = append(actions, a)
		removedRows += n
	}
	return actions, removedRows
}

func countTenants(t *testing.T) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(), `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	return n
}

// soleTenantSeeder returns a seeder for which ref is the install's only
// tenant.
func soleTenantSeeder(t *testing.T, ref tenancy.TenantRef) *demoseed.Seeder {
	t.Helper()
	seeder, err := demoseed.NewSeeder(adminPool, 0.2)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	return seeder.WithTenantResolver(func(context.Context) (tenancy.TenantRef, error) { return ref, nil })
}

func diffRows(before, after map[string][]string) []string {
	var diffs []string
	tables := map[string]bool{}
	for k := range before {
		tables[k] = true
	}
	for k := range after {
		tables[k] = true
	}
	for table := range tables {
		b, a := before[table], after[table]
		inB := map[string]bool{}
		for _, r := range b {
			inB[r] = true
		}
		inA := map[string]bool{}
		for _, r := range a {
			inA[r] = true
		}
		for _, r := range a {
			if !inB[r] {
				diffs = append(diffs, table+": added "+r)
			}
		}
		for _, r := range b {
			if !inA[r] {
				diffs = append(diffs, table+": removed "+r)
			}
		}
	}
	sort.Strings(diffs)
	return diffs
}

// AC-1: seeded into the sole tenant, and the tenant count stays the same.
func TestSeedInPlace_FreshSingleTenantInstall_LoadsIntoSoleTenant(t *testing.T) {
	ref := freshInstallTenant(t)
	seeder := soleTenantSeeder(t, ref)
	ctx := context.Background()
	tenantsBefore := countTenants(t)

	res, err := seeder.Seed(ctx, demoseed.SeedInput{})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if !res.InPlace || res.TenantID != ref.ID {
		t.Fatalf("Seed result = {InPlace:%v TenantID:%s}, want in place into %s", res.InPlace, res.TenantID, ref.ID)
	}
	if got := countTenants(t); got != tenantsBefore {
		t.Errorf("tenant count = %d after seed, want %d (seed must not create a tenant)", got, tenantsBefore)
	}
	var controls, risks, evidence int
	if err := adminPool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM controls WHERE tenant_id = $1),
		       (SELECT count(*) FROM risks WHERE tenant_id = $1),
		       (SELECT count(*) FROM evidence_records WHERE tenant_id = $1)`, ref.ID,
	).Scan(&controls, &risks, &evidence); err != nil {
		t.Fatalf("count demo rows: %v", err)
	}
	if controls != res.Controls+1 || risks != res.Risks || evidence != res.Evidence || res.Controls == 0 {
		t.Errorf("tenant holds controls=%d risks=%d evidence=%d, want %d (+1 stock), %d, %d",
			controls, risks, evidence, res.Controls, res.Risks, res.Evidence)
	}
	if res.RecordedRows == 0 {
		t.Error("RecordedRows = 0, want the seeded rows recorded for teardown")
	}

	again, err := seeder.Seed(ctx, demoseed.SeedInput{})
	if err != nil || !again.Idempotent {
		t.Errorf("re-seed = (Idempotent:%v, %v), want idempotent no-op", again.Idempotent, err)
	}
}

// AC-2: teardown leaves exactly the pre-seed rows, and keeps the tenant.
func TestSeedInPlace_Teardown_LeavesExactlyPreSeedRows(t *testing.T) {
	ctx := context.Background()
	for name, teardown := range map[string]func(*demoseed.Seeder, tenancy.TenantRef) error{
		"no slug": func(s *demoseed.Seeder, _ tenancy.TenantRef) error {
			_, err := s.TeardownImplicit(ctx, uuid.Nil, uuid.Nil)
			return err
		},
		// A slug that names a tenant seeded in place must not drop it.
		"by slug": func(s *demoseed.Seeder, ref tenancy.TenantRef) error {
			return s.Teardown(ctx, ref.Slug, uuid.Nil, uuid.Nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ref := freshInstallTenant(t)
			seeder := soleTenantSeeder(t, ref)
			before := withoutAuditTables(tenantRows(t, ref.ID))

			res, err := seeder.Seed(ctx, demoseed.SeedInput{})
			if err != nil {
				t.Fatalf("Seed: %v", err)
			}
			if d := diffRows(before, withoutAuditTables(tenantRows(t, ref.ID))); len(d) == 0 {
				t.Fatal("seed changed nothing; the teardown check would prove nothing")
			}
			if err := teardown(seeder, ref); err != nil {
				t.Fatalf("teardown: %v", err)
			}
			// Every demo row outside the audit logs is gone, and nothing else moved.
			if d := diffRows(before, withoutAuditTables(tenantRows(t, ref.ID))); len(d) > 0 {
				t.Errorf("after teardown the tenant differs from before the seed in %d rows; first: %s", len(d), d[0])
			}
			// M2: the tenant's audit log keeps the apply and records the teardown.
			actions, removed := seedAuditTrail(t, ref.ID)
			if !slices.Equal(actions, []string{"demo_seed_apply", "demo_seed_teardown"}) {
				t.Errorf("me_audit_log seed trail = %v, want [demo_seed_apply demo_seed_teardown]", actions)
			}
			if removed != res.RecordedRows {
				t.Errorf("teardown audit row says %d rows removed, want the %d recorded", removed, res.RecordedRows)
			}
			var records int
			if err := adminPool.QueryRow(ctx,
				`SELECT count(*) FROM demo_seed_rows WHERE tenant_id = $1`, ref.ID,
			).Scan(&records); err != nil || records != 0 {
				t.Errorf("demo_seed_rows left = %d (%v), want 0", records, err)
			}

			// The tenant can be seeded again after teardown.
			if res, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil || res.Idempotent {
				t.Errorf("re-seed after teardown = (Idempotent:%v, %v), want a fresh seed", res.Idempotent, err)
			}
		})
	}
}

// AC-3: a non-stock control makes seed refuse with the explanation, and
// nothing is written.
func TestSeedInPlace_NonStockControl_RefusesAndWritesNothing(t *testing.T) {
	ref := freshInstallTenant(t)
	insertControl(t, ref.ID, "acme-custom-mfa")
	seeder := soleTenantSeeder(t, ref)
	before := tenantRows(t, ref.ID)
	tenantsBefore := countTenants(t)

	_, err := seeder.Seed(context.Background(), demoseed.SeedInput{})
	if !errors.Is(err, demoseed.ErrTenantHasOrgData) {
		t.Fatalf("Seed err = %v, want ErrTenantHasOrgData", err)
	}
	for _, want := range []string{"1 non-stock control", "ATLAS_ALLOW_MULTI_TENANT=true", "--tenant-slug", "separate install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if d := diffRows(before, tenantRows(t, ref.ID)); len(d) > 0 {
		t.Errorf("refused seed wrote %d rows; first: %s", len(d), d[0])
	}
	if got := countTenants(t); got != tenantsBefore {
		t.Errorf("tenant count = %d, want %d", got, tenantsBefore)
	}
}

// AC-4: with the multi-tenant opt-in, a slug still creates a separate demo
// tenant as before; without it, the slug is refused and no tenant created.
func TestSeed_TenantSlug_NeedsMultiTenantOptIn(t *testing.T) {
	ctx := context.Background()
	seeder, err := demoseed.NewSeeder(adminPool, 0.2)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}

	t.Run("refused without opt-in", func(t *testing.T) {
		freshInstallTenant(t) // the install already has a tenant
		slug := "demo-noopt-" + uuid.NewString()[:8]
		cleanupTenant(t, slug)
		tenantsBefore := countTenants(t)
		_, err := seeder.Seed(ctx, demoseed.SeedInput{Slug: slug})
		if !errors.Is(err, demoseed.ErrMultiTenantNotEnabled) {
			t.Fatalf("Seed err = %v, want ErrMultiTenantNotEnabled", err)
		}
		if !strings.Contains(err.Error(), "ATLAS_ALLOW_MULTI_TENANT=true") {
			t.Errorf("refusal %q does not name the opt-in", err)
		}
		if got := countTenants(t); got != tenantsBefore {
			t.Errorf("tenant count = %d, want %d", got, tenantsBefore)
		}
	})

	t.Run("creates its own tenant with opt-in", func(t *testing.T) {
		slug := "demo-optin-" + uuid.NewString()[:8]
		cleanupTenant(t, slug)
		tenantsBefore := countTenants(t)
		res, err := seeder.Seed(ctx, demoseed.SeedInput{Slug: slug, AllowMultiTenant: true})
		if err != nil {
			t.Fatalf("Seed: %v", err)
		}
		if res.InPlace || res.TenantSlug != slug || res.Controls == 0 {
			t.Errorf("Seed result = {InPlace:%v Slug:%q Controls:%d}, want a new demo tenant", res.InPlace, res.TenantSlug, res.Controls)
		}
		if got := countTenants(t); got != tenantsBefore+1 {
			t.Errorf("tenant count = %d, want %d", got, tenantsBefore+1)
		}
	})
}

// A tenant missing the stock scope rows still seeds in place: the seed adds
// the environment dimension and prod cell, and teardown removes them again.
func TestSeedInPlace_NoStockScope_AddsAndRemovesIt(t *testing.T) {
	ref := freshInstallTenant(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM scope_cells WHERE tenant_id = $1`,
		`DELETE FROM scope_dimensions WHERE tenant_id = $1`,
	} {
		if _, err := adminPool.Exec(ctx, stmt, ref.ID); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	seeder := soleTenantSeeder(t, ref)
	before := withoutAuditTables(tenantRows(t, ref.ID))
	if _, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := len(tenantRows(t, ref.ID)["scope_cells"]); got != 1 {
		t.Errorf("scope_cells after seed = %d, want 1", got)
	}
	if _, err := seeder.TeardownImplicit(ctx, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if d := diffRows(before, withoutAuditTables(tenantRows(t, ref.ID))); len(d) > 0 {
		t.Errorf("after teardown %d rows differ; first: %s", len(d), d[0])
	}
}

// A row added after the seed that a foreign key with ON DELETE RESTRICT
// ties to a demo row blocks teardown: it refuses, names the table, and
// removes nothing, rather than deleting a row the seed did not add.
func TestSeedInPlace_Teardown_RefusesWhenRealRowReferencesDemoRow(t *testing.T) {
	ref := freshInstallTenant(t)
	ctx := context.Background()
	seeder := soleTenantSeeder(t, ref)
	if _, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	// An operator files an exception against a demo control.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO exceptions
		SELECT (jsonb_populate_record(NULL::exceptions,
		        to_jsonb(e.*) || jsonb_build_object('id', gen_random_uuid()))).*
		FROM exceptions e WHERE e.tenant_id = $1 LIMIT 1`, ref.ID,
	); err != nil {
		t.Fatalf("insert real exception: %v", err)
	}
	before := tenantRows(t, ref.ID)

	_, err := seeder.TeardownImplicit(ctx, uuid.Nil, uuid.Nil)
	if err == nil || !strings.Contains(err.Error(), "still referenced") {
		t.Fatalf("teardown err = %v, want a refusal naming the referenced rows", err)
	}
	if d := diffRows(before, tenantRows(t, ref.ID)); len(d) > 0 {
		t.Errorf("refused teardown changed %d rows; first: %s", len(d), d[0])
	}
}

// B1 (#2039 review): the seed runs at READ COMMITTED, so a row another
// session commits for the tenant mid-seed is visible to the after-snapshot.
// It is not the seed's, so it must not be recorded, and teardown keeps it.
func TestSeedInPlace_RowCommittedMidSeedBySomeoneElse_IsKept(t *testing.T) {
	ref := freshInstallTenant(t)
	ctx := context.Background()
	realID := uuid.New()
	seeder := soleTenantSeeder(t, ref).WithBeforeRecordHook(func(context.Context) {
		// A separate connection, committed while the seed transaction is open.
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO scope_dimensions (id, tenant_id, name, value_type, allowed_values, is_required, is_builtin)
			 VALUES ($1, $2, 'region', 'string', '["us", "eu"]'::jsonb, FALSE, FALSE)`,
			realID, ref.ID,
		); err != nil {
			t.Errorf("commit real row mid-seed: %v", err)
		}
	})
	if _, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	var recorded bool
	if err := adminPool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM demo_seed_rows
		 WHERE tenant_id = $1 AND table_name = 'scope_dimensions' AND row_key->>'id' = $2)`,
		ref.ID, realID.String(),
	).Scan(&recorded); err != nil {
		t.Fatalf("probe demo_seed_rows: %v", err)
	}
	if recorded {
		t.Error("a row another session committed mid-seed was recorded as a demo row")
	}
	if _, err := seeder.TeardownImplicit(ctx, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	var kept bool
	if err := adminPool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM scope_dimensions WHERE id = $1)`, realID,
	).Scan(&kept); err != nil || !kept {
		t.Errorf("teardown removed the row committed mid-seed by another session (kept=%v, err=%v)", kept, err)
	}
}

// The apply and teardown marks stay in the organisation's audit log (M2), so
// they must never make it look like a whole demo tenant: a later teardown by
// slug refuses and the organisation survives.
func TestSeedInPlace_AfterTeardown_SlugTeardownNeverDropsTheTenant(t *testing.T) {
	ref := freshInstallTenant(t)
	ctx := context.Background()
	seeder := soleTenantSeeder(t, ref)
	if _, err := seeder.Seed(ctx, demoseed.SeedInput{}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := seeder.TeardownImplicit(ctx, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	err := seeder.Teardown(ctx, ref.Slug, uuid.Nil, uuid.Nil)
	if err == nil || !strings.Contains(err.Error(), "does not carry the slice-205 forensic mark") {
		t.Errorf("second teardown by slug err = %v, want a refusal", err)
	}
	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, ref.ID).Scan(&exists); err != nil || !exists {
		t.Fatalf("tenant gone after second teardown (exists=%v, err=%v)", exists, err)
	}
}
