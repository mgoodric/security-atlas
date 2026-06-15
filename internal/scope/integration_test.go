//go:build integration

// Integration tests for slice 017: scope dimensions, scope cells, seed,
// applicability_expr evaluation. Real Postgres only — RLS cannot be tested
// against a fake DB (memory rule: "Never mock the DB").

package scope_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// Slice 435: the pool/DSN/tenant-seed/context boilerplate this file used to
// re-derive (appDSN/adminDSN/openPool/inline tenancy.WithTenant) now lives in
// the shared internal/dbtest harness. dbtest.NewMigratePool / NewAppPool open
// the two pools; dbtest.WithTenantCtx tags the tenant context;
// dbtest.SeedTenant (via freshTenant below) seeds + cleans up.

// freshTenant returns a brand-new tenant id and registers a cleanup that wipes
// every row written under it through the privileged (migrate) pool — the
// append-only evidence_records is among them, so the migrate pool is required.
// Each test owns its own tenant so RLS guarantees isolation and tests can run
// in any order.
func freshTenant(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, migrate,
		"evidence_records",
		"scope_cells",
		"scope_dimensions",
		"scopes",
		"controls",
	)
}

// TestSeedTenant_IsIdempotent — AC-5: fresh deploys seed a single default cell
// with sane defaults (bu=default, env=prod, data_classification=internal).
// Calling SeedTenant twice returns the same cell and never creates duplicates.
func TestSeedTenant_IsIdempotent(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)

	first, err := store.SeedTenant(ctx)
	if err != nil {
		t.Fatalf("SeedTenant first: %v", err)
	}
	if first.Dimensions["business_unit"] != "default" ||
		first.Dimensions["environment"] != "prod" ||
		first.Dimensions["data_classification"] != "internal" {
		t.Fatalf("default cell dimensions wrong: %v", first.Dimensions)
	}

	second, err := store.SeedTenant(ctx)
	if err != nil {
		t.Fatalf("SeedTenant second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("second SeedTenant returned different cell id: %s vs %s", first.ID, second.ID)
	}

	cells, err := store.ListCells(ctx)
	if err != nil {
		t.Fatalf("ListCells: %v", err)
	}
	if len(cells) != 1 {
		t.Fatalf("expected exactly 1 cell after re-seed; got %d", len(cells))
	}

	dims, err := store.ListDimensions(ctx)
	if err != nil {
		t.Fatalf("ListDimensions: %v", err)
	}
	// The builtin set must be seeded.
	expected := []string{"business_unit", "environment", "geography", "cloud_account", "data_classification", "product_line"}
	have := map[string]bool{}
	for _, d := range dims {
		have[d.Name] = true
	}
	for _, n := range expected {
		if !have[n] {
			t.Fatalf("builtin dimension %q missing after SeedTenant", n)
		}
	}
}

// TestCreateCell_RejectsUndeclaredDimension — anti-criterion: do NOT silently
// drop cells whose dimensions don't match the schema. The store must reject
// with ErrInvalidDimension.
func TestCreateCell_RejectsUndeclaredDimension(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := store.CreateCell(ctx, "bad", map[string]string{
		"environment":         "prod",
		"data_classification": "internal",
		"made_up_dimension":   "nope",
	})
	if err == nil || !errors.Is(err, scope.ErrInvalidDimension) {
		t.Fatalf("want ErrInvalidDimension; got %v", err)
	}
}

// TestCreateCell_RejectsValueOutsideAllowedValues — same anti-criterion: when
// a dimension declares allowed_values, writes with other values are rejected.
func TestCreateCell_RejectsValueOutsideAllowedValues(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := store.CreateCell(ctx, "bad-env", map[string]string{
		"environment":         "production",
		"data_classification": "internal",
	})
	if err == nil || !errors.Is(err, scope.ErrInvalidDimension) {
		t.Fatalf("want ErrInvalidDimension for unknown env value; got %v", err)
	}
}

// TestCreateCell_DuplicateReturnsCellExists — UNIQUE on (tenant_id,
// dimensions_hash) prevents duplicates.
func TestCreateCell_DuplicateReturnsCellExists(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	dims := map[string]string{
		"business_unit":       "platform",
		"environment":         "staging",
		"data_classification": "confidential",
	}
	if _, err := store.CreateCell(ctx, "platform-staging", dims); err != nil {
		t.Fatalf("first CreateCell: %v", err)
	}
	_, err := store.CreateCell(ctx, "platform-staging-dup", dims)
	if !errors.Is(err, scope.ErrCellExists) {
		t.Fatalf("want ErrCellExists; got %v", err)
	}
}

// TestCreateCell_DimensionOrderDoesNotMatter — canonical hash dedupes regardless
// of map iteration order. This is what makes the UNIQUE constraint trustworthy.
func TestCreateCell_DimensionOrderDoesNotMatter(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := map[string]string{"environment": "staging", "data_classification": "confidential", "business_unit": "x"}
	b := map[string]string{"business_unit": "x", "data_classification": "confidential", "environment": "staging"}
	if _, err := store.CreateCell(ctx, "a", a); err != nil {
		t.Fatalf("CreateCell a: %v", err)
	}
	if _, err := store.CreateCell(ctx, "b", b); !errors.Is(err, scope.ErrCellExists) {
		t.Fatalf("expected ErrCellExists across reordered keys; got %v", err)
	}
}

// TestControlApplicability_AppliesExpression — AC-3 and AC-6. Insert a few
// scope cells; insert a control whose applicability_expr is a JSON-AST that
// selects only prod+restricted+confidential; ControlApplicability returns
// exactly those cells.
func TestControlApplicability_AppliesExpression(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, dims := range []map[string]string{
		{"business_unit": "platform", "environment": "prod", "data_classification": "restricted"},
		{"business_unit": "platform", "environment": "staging", "data_classification": "confidential"},
		{"business_unit": "platform", "environment": "dev", "data_classification": "internal"},
		{"business_unit": "platform", "environment": "prod", "data_classification": "public"},
	} {
		if _, err := store.CreateCell(ctx, "", dims); err != nil {
			t.Fatalf("CreateCell %v: %v", dims, err)
		}
	}

	// Insert a control with a JSON-AST applicability_expr. We bypass the
	// Go API for controls (not in this slice) and write directly via the
	// admin pool with tenant scope applied. bundle_id is NOT NULL on
	// controls (added by a later slice); each test owns a unique bundle
	// so the per-tenant UNIQUE(bundle_id) constraint is honored.
	controlID := uuid.NewString()
	bundleID := "scope-it-" + controlID
	exprJSON := `{"op":"and","args":[
		{"op":"in","dim":"environment","values":["prod","staging"]},
		{"op":"in","dim":"data_classification","values":["restricted","confidential"]}
	]}`
	if err := withAdminTenant(migrate, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, applicability_expr, bundle_id)
			VALUES ($1, $2, 'IAC-06', 'MFA', 'IAC', 'automated', $3, $4)
		`, controlID, tenant, exprJSON, bundleID)
		return err
	}); err != nil {
		t.Fatalf("insert control: %v", err)
	}

	cells, err := store.ControlApplicability(ctx, uuid.MustParse(controlID))
	if err != nil {
		t.Fatalf("ControlApplicability: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("want 2 applicable cells; got %d (%v)", len(cells), cells)
	}
	for _, c := range cells {
		env := c.Dimensions["environment"]
		dc := c.Dimensions["data_classification"]
		if env != "prod" && env != "staging" {
			t.Fatalf("unexpected env %q", env)
		}
		if dc != "restricted" && dc != "confidential" {
			t.Fatalf("unexpected dc %q", dc)
		}
	}
}

// TestControlApplicability_LegacyTrueReturnsAllCells — AC-4 + back-compat:
// slice-002 sets applicability_expr default to the literal string "true",
// which must mean "match every cell".
func TestControlApplicability_LegacyTrueReturnsAllCells(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := store.CreateCell(ctx, "x", map[string]string{
		"business_unit": "platform", "environment": "staging", "data_classification": "confidential",
	})
	if err != nil {
		t.Fatalf("CreateCell: %v", err)
	}

	controlID := uuid.NewString()
	bundleID := "scope-it-legacy-" + controlID
	if err := withAdminTenant(migrate, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, applicability_expr, bundle_id)
			VALUES ($1, $2, 'IAC-99', 'all', 'IAC', 'automated', 'true', $3)
		`, controlID, tenant, bundleID)
		return err
	}); err != nil {
		t.Fatalf("insert control: %v", err)
	}

	cells, err := store.ControlApplicability(ctx, uuid.MustParse(controlID))
	if err != nil {
		t.Fatalf("ControlApplicability: %v", err)
	}
	if len(cells) != 2 { // default cell + the one we added
		t.Fatalf("want 2; got %d", len(cells))
	}
}

// TestRLS_OtherTenantCannotSeeCells — Invariant 6. Tenant A creates cells;
// Tenant B opens its own context and sees zero of them.
func TestRLS_OtherTenantCannotSeeCells(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, migrate)
	tenantB := freshTenant(t, migrate)
	store := scope.NewStore(app)

	ctxA := dbtest.WithTenantCtx(t, tenantA)
	if _, err := store.SeedTenant(ctxA); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, err := store.CreateCell(ctxA, "private-A", map[string]string{
		"business_unit": "secret", "environment": "prod", "data_classification": "restricted",
	}); err != nil {
		t.Fatalf("CreateCell A: %v", err)
	}

	ctxB := dbtest.WithTenantCtx(t, tenantB)
	cells, err := store.ListCells(ctxB)
	if err != nil {
		t.Fatalf("ListCells B: %v", err)
	}
	if len(cells) != 0 {
		t.Fatalf("tenant B saw %d cells from tenant A; RLS bypassed", len(cells))
	}
}

// ---- HTTP-level smoke tests (AC-2, AC-7) ----

func setupHTTPServer(t *testing.T, tenant string) (*httptest.Server, string) {
	t.Helper()
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path.
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests nil; AttachDB ineffective")
	}
	ts := httptest.NewServer(handler)
	// The app pool is closed by dbtest.NewAppPool's own t.Cleanup.
	t.Cleanup(ts.Close)
	return ts, bearer
}

func doJSON(t *testing.T, method, url, bearer, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nilOr(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bb, _ := io.ReadAll(resp.Body)
	return resp, bb
}

// nilOr returns nil for an empty string, otherwise a strings.Reader
// wrapping it. Slice 284 removed a previously-local `strings` shim that
// collided with the stdlib package once unit tests in this same _test
// package imported `strings` for real.
func nilOr(s string) io.Reader {
	if s == "" {
		return nil
	}
	return strings.NewReader(s)
}

// TestHTTP_CreateAndListCells — AC-2 + AC-7.
func TestHTTP_CreateAndListCells(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)

	// Pre-seed so the dimension schema exists.
	app := dbtest.NewAppPool(t)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := scope.NewStore(app).SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ts, bearer := setupHTTPServer(t, tenant)

	body := `{
		"label": "platform-staging",
		"dimensions": {
			"business_unit": "platform",
			"environment": "staging",
			"data_classification": "confidential"
		}
	}`
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/scopes/cells", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/scopes/cells status = %d; body = %s", resp.StatusCode, payload)
	}

	resp, payload = doJSON(t, http.MethodGet, ts.URL+"/v1/scopes/cells", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/scopes/cells status = %d; body = %s", resp.StatusCode, payload)
	}
	var got struct {
		Cells []map[string]any `json:"cells"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Default + one we just created.
	if len(got.Cells) != 2 {
		t.Fatalf("want 2 cells; got %d (%v)", len(got.Cells), got.Cells)
	}
}

// TestHTTP_PostRejectsBadDimensions — anti-criterion enforcement at HTTP layer.
func TestHTTP_PostRejectsBadDimensions(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)

	app := dbtest.NewAppPool(t)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := scope.NewStore(app).SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ts, bearer := setupHTTPServer(t, tenant)
	body := `{"label":"x","dimensions":{"made_up":"value"}}`
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/scopes/cells", bearer, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", resp.StatusCode, payload)
	}
}

// TestHTTP_ControlApplicability_EndToEnd — AC-7. Insert a control + a few cells,
// GET /v1/controls/:id/applicability, confirm the wire shape.
func TestHTTP_ControlApplicability_EndToEnd(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)

	app := dbtest.NewAppPool(t)
	store := scope.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, dims := range []map[string]string{
		{"business_unit": "x", "environment": "prod", "data_classification": "restricted"},
		{"business_unit": "x", "environment": "dev", "data_classification": "public"},
	} {
		if _, err := store.CreateCell(ctx, "", dims); err != nil {
			t.Fatalf("CreateCell: %v", err)
		}
	}
	controlID := uuid.NewString()
	bundleID := "scope-it-http-" + controlID
	exprJSON := `{"op":"eq","dim":"environment","value":"prod"}`
	if err := withAdminTenant(migrate, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, applicability_expr, bundle_id)
			VALUES ($1, $2, 'IAC-06', 'MFA', 'IAC', 'automated', $3, $4)
		`, controlID, tenant, exprJSON, bundleID)
		return err
	}); err != nil {
		t.Fatalf("insert control: %v", err)
	}

	ts, bearer := setupHTTPServer(t, tenant)
	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/controls/"+controlID+"/applicability", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, payload)
	}
	var got struct {
		ControlID       string           `json:"control_id"`
		Applicable      []map[string]any `json:"applicable"`
		ApplicableCount int              `json:"applicable_count"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Universe: default cell (env=prod) + the two we inserted (env=prod, env=dev).
	// Expression `env = prod` matches the default + the prod/restricted cell.
	if got.ApplicableCount != 2 {
		t.Fatalf("applicable_count = %d; want 2", got.ApplicableCount)
	}
}

// ---- helpers ----

// withAdminTenant runs fn inside an admin-pool transaction with the tenant GUC
// applied. We use the admin pool for direct DDL/DML setup that the slice's own
// store doesn't expose (e.g., inserting a row in `controls`).
func withAdminTenant(admin *pgxpool.Pool, tenant string, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := admin.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// admin pool is BYPASSRLS but set the GUC anyway for consistency.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		return fmt.Errorf("set_config: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
