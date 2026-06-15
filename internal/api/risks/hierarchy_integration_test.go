//go:build integration

// Slice 067 — integration tests for the risk-hierarchy backend read
// endpoints. Real Postgres + the assembled platform router so the tests
// exercise the full request path (program-read authz guard, tenancy
// middleware, RLS, the risk.Store aggregation queries).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/risks/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-26  GET /v1/org_units?include_risk_counts=true aggregates by severity
//	ISC-27  riskWire carries org_unit_id/themes/severity; ?theme=&?org_unit=
//	        filters compose with the slice-019 filters
//	ISC-28  GET /v1/risks/theme-heatmap aggregates the grid, built-ins first
//	ISC-29  (risk arm) theme-heatmap + org-unit counts are RLS-isolated
//	        across tenants; both 403 a role without program-read access

package risks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/orgunits"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ----- slice-067 seed helpers -----

// freshHierarchyTenant returns a new tenant id and registers cleanup for
// every slice-052/053 table the hierarchy tests touch. Distinct from the
// slice-020 freshTenant in integration_test.go — that one does not clean
// org_units / org_themes. The cleanup is a pure tenant-scoped DELETE
// returning a string, so it delegates to dbtest.SeedTenant with its own
// table list (slice 435 / 742 drain batch 23).
func freshHierarchyTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"risks",
		"org_units",
		"org_themes",
	)
}

// seedOrgUnit inserts an org_unit and returns its id.
func seedOrgUnit(t *testing.T, admin *pgxpool.Pool, tenant, name, level string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO org_units (id, tenant_id, name, parent_id, level)
		VALUES ($1, $2, $3, NULL, $4)
	`, id, tenant, name, level); err != nil {
		t.Fatalf("seed org_unit: %v", err)
	}
	return id
}

// seedTenantTheme inserts a tenant-private org_theme and returns its name.
func seedTenantTheme(t *testing.T, admin *pgxpool.Pool, tenant, name string) string {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO org_themes (id, tenant_id, theme_name, description)
		VALUES ($1, $2, $3, '')
	`, uuid.New(), tenant, name); err != nil {
		t.Fatalf("seed tenant theme: %v", err)
	}
	return name
}

// seedHierRisk inserts a risk with an explicit org_unit binding, themes,
// and a 5x5 inherent_score (likelihood, impact). severity = likelihood *
// impact. Returns the risk id.
func seedHierRisk(t *testing.T, admin *pgxpool.Pool, tenant string, orgUnit *uuid.UUID, themes []string, likelihood, impact int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	inherent := `{"likelihood":` + itoa(likelihood) + `,"impact":` + itoa(impact) + `}`
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score,
			org_unit_id, themes
		)
		VALUES ($1, $2, 'slice 067 risk', '', 'operational', 'nist_800_30',
		        $3::jsonb, 'mitigate', 'owner', '{}'::jsonb, $4, $5::text[])
	`, id, tenant, inherent, orgUnit, themes); err != nil {
		t.Fatalf("seed hier risk: %v", err)
	}
	return id
}

// itoa is a tiny strconv.Itoa shim kept local so the seed-string helpers
// stay dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// noRoleHierarchyRouter wires the two slice-067 risk read endpoints behind
// a credential carrying NO program-read signal — the v1 representation of
// a viewer-only credential. It exercises the handler-level
// requireProgramRead guard without standing up OPA. The guard runs FIRST
// in every handler — before tenant resolution — so the 403 fires
// regardless of the tenant context.
func noRoleHierarchyRouter(t *testing.T, app *pgxpool.Pool, tenant string) http.Handler {
	t.Helper()
	store := risk.NewStore(app)
	rh := risksapi.New(store)
	oh := orgunits.New(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "viewer-test",
				OwnerRoles: nil,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/risks/theme-heatmap", rh.ThemeHeatmap)
	r.Get("/v1/org_units", oh.List)
	return r
}

// ===== ISC-26: org-unit risk counts aggregate by severity =====

func TestOrgUnitRiskCounts_AggregateBySeverity(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	env := testServer(t, app, tenant)

	ouA := seedOrgUnit(t, admin, tenant, "AppSec", "team")
	ouB := seedOrgUnit(t, admin, tenant, "Cloud", "team")

	// ouA: two risks at severity 20 (4x5, 5x4), one at severity 6 (2x3).
	seedHierRisk(t, admin, tenant, &ouA, []string{"ownership"}, 4, 5)
	seedHierRisk(t, admin, tenant, &ouA, []string{"ownership"}, 5, 4)
	seedHierRisk(t, admin, tenant, &ouA, []string{"tech-debt"}, 2, 3)
	// ouB: one risk at severity 25 (5x5).
	seedHierRisk(t, admin, tenant, &ouB, []string{"availability"}, 5, 5)
	// An unbound risk — must NOT appear in any org_unit's counts.
	seedHierRisk(t, admin, tenant, nil, []string{"monitoring"}, 3, 3)

	resp, body := get(t, env, "/v1/org_units?include_risk_counts=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-26: status %d, want 200", resp.StatusCode)
	}
	units, _ := body["org_units"].([]any)
	if len(units) != 2 {
		t.Fatalf("ISC-26: expected 2 org_units, got %d", len(units))
	}
	byID := map[string]map[string]any{}
	for _, u := range units {
		m := u.(map[string]any)
		byID[m["id"].(string)] = m
	}
	// ouA: {"20": 2, "6": 1}
	aCounts, ok := byID[ouA.String()]["risk_counts"].(map[string]any)
	if !ok {
		t.Fatalf("ISC-26: ouA missing risk_counts; node = %v", byID[ouA.String()])
	}
	if aCounts["20"] != float64(2) {
		t.Fatalf("ISC-26: ouA severity-20 count = %v, want 2", aCounts["20"])
	}
	if aCounts["6"] != float64(1) {
		t.Fatalf("ISC-26: ouA severity-6 count = %v, want 1", aCounts["6"])
	}
	// ouB: {"25": 1}
	bCounts := byID[ouB.String()]["risk_counts"].(map[string]any)
	if bCounts["25"] != float64(1) {
		t.Fatalf("ISC-26: ouB severity-25 count = %v, want 1", bCounts["25"])
	}
}

// ===== ISC-26 (back-compat arm): no param -> shape unchanged =====

func TestOrgUnitList_WithoutParam_NoRiskCounts(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	env := testServer(t, app, tenant)

	ouA := seedOrgUnit(t, admin, tenant, "AppSec", "team")
	seedHierRisk(t, admin, tenant, &ouA, []string{"ownership"}, 4, 4)

	resp, body := get(t, env, "/v1/org_units")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	units, _ := body["org_units"].([]any)
	if len(units) != 1 {
		t.Fatalf("expected 1 org_unit, got %d", len(units))
	}
	// Without ?include_risk_counts=true the risk_counts key is omitted
	// entirely (omitempty) — the slice-053 shape is unchanged.
	if _, present := units[0].(map[string]any)["risk_counts"]; present {
		t.Fatalf("ISC-26 back-compat: risk_counts present without the param")
	}
}

// ===== ISC-27: riskWire carries new fields; theme+org_unit filters compose =====

func TestListRisks_NewFieldsAndComposableFilters(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	env := testServer(t, app, tenant)

	ouA := seedOrgUnit(t, admin, tenant, "AppSec", "team")
	ouB := seedOrgUnit(t, admin, tenant, "Cloud", "team")
	// Risk 1: ouA, themes [ownership, tech-debt], severity 20.
	r1 := seedHierRisk(t, admin, tenant, &ouA, []string{"ownership", "tech-debt"}, 4, 5)
	// Risk 2: ouA, theme [tech-debt], severity 6.
	seedHierRisk(t, admin, tenant, &ouA, []string{"tech-debt"}, 2, 3)
	// Risk 3: ouB, theme [ownership], severity 9.
	seedHierRisk(t, admin, tenant, &ouB, []string{"ownership"}, 3, 3)

	// Unfiltered: all 3, and each row carries org_unit_id/themes/severity.
	resp, body := get(t, env, "/v1/risks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-27: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["risks"].([]any)
	if len(rows) != 3 {
		t.Fatalf("ISC-27: unfiltered expected 3 risks, got %d", len(rows))
	}
	var r1row map[string]any
	for _, r := range rows {
		m := r.(map[string]any)
		if m["id"] == r1.String() {
			r1row = m
		}
	}
	if r1row == nil {
		t.Fatalf("ISC-27: risk 1 missing from list")
	}
	if r1row["org_unit_id"] != ouA.String() {
		t.Fatalf("ISC-27: risk 1 org_unit_id = %v, want %s", r1row["org_unit_id"], ouA)
	}
	if sev, _ := r1row["severity"].(float64); sev != 20 {
		t.Fatalf("ISC-27: risk 1 severity = %v, want 20", r1row["severity"])
	}
	themes, _ := r1row["themes"].([]any)
	if len(themes) != 2 {
		t.Fatalf("ISC-27: risk 1 themes = %v, want 2 entries", r1row["themes"])
	}

	// ?theme=ownership -> risk 1 + risk 3 (both carry ownership).
	resp, body = get(t, env, "/v1/risks?theme=ownership")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-27: theme filter status %d", resp.StatusCode)
	}
	if rows, _ = body["risks"].([]any); len(rows) != 2 {
		t.Fatalf("ISC-27: ?theme=ownership expected 2, got %d", len(rows))
	}

	// ?theme=ownership&org_unit=ouA -> just risk 1 (composes both filters).
	resp, body = get(t, env, "/v1/risks?theme=ownership&org_unit="+ouA.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-27: composed filter status %d", resp.StatusCode)
	}
	rows, _ = body["risks"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != r1.String() {
		t.Fatalf("ISC-27: ?theme=ownership&org_unit=ouA expected [risk1], got %v", rows)
	}

	// ?org_unit composes with the slice-019 ?treatment filter too.
	resp, body = get(t, env, "/v1/risks?org_unit="+ouA.String()+"&treatment=mitigate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-27: org_unit+treatment status %d", resp.StatusCode)
	}
	if rows, _ = body["risks"].([]any); len(rows) != 2 {
		t.Fatalf("ISC-27: ?org_unit=ouA&treatment=mitigate expected 2, got %d", len(rows))
	}

	// A malformed ?org_unit is a 400.
	resp, _ = get(t, env, "/v1/risks?org_unit=not-a-uuid")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ISC-27: malformed org_unit = %d, want 400", resp.StatusCode)
	}
}

// ===== ISC-28: theme-heatmap grid aggregates, built-ins first =====

func TestThemeHeatmap_AggregatesGridBuiltinsFirst(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	env := testServer(t, app, tenant)

	ouA := seedOrgUnit(t, admin, tenant, "AppSec", "team")
	ouB := seedOrgUnit(t, admin, tenant, "Cloud", "team")
	// A tenant-private theme — must sort AFTER the built-in "ownership".
	priv := seedTenantTheme(t, admin, tenant, "zzz-private-theme")

	// (ownership, ouA): two risks, severities 20 and 6 -> count 2, max 20.
	seedHierRisk(t, admin, tenant, &ouA, []string{"ownership"}, 4, 5)
	seedHierRisk(t, admin, tenant, &ouA, []string{"ownership"}, 2, 3)
	// (ownership, ouB): one risk severity 25.
	seedHierRisk(t, admin, tenant, &ouB, []string{"ownership"}, 5, 5)
	// (zzz-private-theme, ouA): one risk severity 9.
	seedHierRisk(t, admin, tenant, &ouA, []string{priv}, 3, 3)
	// An unbound risk with a theme — contributes NO cell (no org_unit axis).
	seedHierRisk(t, admin, tenant, nil, []string{"ownership"}, 5, 5)

	resp, body := get(t, env, "/v1/risks/theme-heatmap")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-28: status %d, want 200", resp.StatusCode)
	}
	cells, _ := body["cells"].([]any)
	if len(cells) != 3 {
		t.Fatalf("ISC-28: expected 3 populated cells, got %d (%v)", len(cells), cells)
	}
	// Built-in "ownership" cells must all sort before the tenant-private
	// "zzz-private-theme" cell.
	lastBuiltinIdx := -1
	firstPrivateIdx := -1
	for i, c := range cells {
		m := c.(map[string]any)
		if m["theme_builtin"] == true {
			lastBuiltinIdx = i
		} else if firstPrivateIdx == -1 {
			firstPrivateIdx = i
		}
	}
	if firstPrivateIdx != -1 && lastBuiltinIdx > firstPrivateIdx {
		t.Fatalf("ISC-28: built-in theme cell sorts after tenant-private cell")
	}
	// Find (ownership, ouA) and assert count 2, aggregate severity 20.
	var found bool
	for _, c := range cells {
		m := c.(map[string]any)
		if m["theme"] == "ownership" && m["org_unit_id"] == ouA.String() {
			found = true
			if m["risk_count"] != float64(2) {
				t.Fatalf("ISC-28: (ownership,ouA) risk_count = %v, want 2", m["risk_count"])
			}
			if m["aggregate_severity"] != float64(20) {
				t.Fatalf("ISC-28: (ownership,ouA) aggregate_severity = %v, want 20", m["aggregate_severity"])
			}
		}
	}
	if !found {
		t.Fatalf("ISC-28: (ownership, ouA) cell missing")
	}
}

// ===== ISC-29 (risk arm): RLS isolation across tenants =====

func TestThemeHeatmap_RLSIsolatedAcrossTenants(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshHierarchyTenant(t, admin)
	tenantB := freshHierarchyTenant(t, admin)

	ouA := seedOrgUnit(t, admin, tenantA, "AppSec-A", "team")
	seedHierRisk(t, admin, tenantA, &ouA, []string{"ownership"}, 4, 5)
	ouB := seedOrgUnit(t, admin, tenantB, "AppSec-B", "team")
	seedHierRisk(t, admin, tenantB, &ouB, []string{"ownership"}, 3, 3)

	// Tenant B's bearer sees only tenant B's cell.
	envB := testServer(t, app, tenantB)
	resp, body := get(t, envB, "/v1/risks/theme-heatmap")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-29: status %d, want 200", resp.StatusCode)
	}
	cells, _ := body["cells"].([]any)
	if len(cells) != 1 {
		t.Fatalf("ISC-29: tenant B expected 1 cell, got %d", len(cells))
	}
	if cells[0].(map[string]any)["org_unit_id"] != ouB.String() {
		t.Fatalf("ISC-29: tenant B saw a foreign org_unit cell")
	}

	// And org-unit risk counts are isolated too.
	resp, body = get(t, envB, "/v1/org_units?include_risk_counts=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-29: org_units status %d", resp.StatusCode)
	}
	if units, _ := body["org_units"].([]any); len(units) != 1 {
		t.Fatalf("ISC-29: tenant B expected 1 org_unit, got %d", len(units))
	}
}

// ===== ISC-29 (risk arm): 403 for a role without program-read access =====

func TestRiskHierarchyEndpoints_403WithoutProgramRead(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshHierarchyTenant(t, admin)
	r := noRoleHierarchyRouter(t, app, tenant)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	for _, path := range []string{"/v1/risks/theme-heatmap", "/v1/org_units"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("ISC-29: GET %s status %d, want 403", path, resp.StatusCode)
		}
	}
}
