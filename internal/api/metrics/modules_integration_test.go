//go:build integration

// #2173 — Metrics leaves out metrics for a module the caller may not see.
// The catalog is seeded with a core board metric whose child measures
// vendors (category "vendor", Third-party risk) and whose grandchild is
// core: with Third-party risk switched off, the vendor metric is gone from
// the list, reads 404, is left out of its parent's children, and the
// cascade prunes it with its subtree. A views helper that cannot read
// access answers 500, never the unfiltered catalog.

package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	metricsapi "github.com/mgoodric/security-atlas/internal/api/metrics"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type moduleMetrics struct {
	board, vendor, grandchild string
}

// seedModuleCatalog seeds board (core) -> vendor (Third-party risk) ->
// grandchild (core), and removes the three rows and two edges afterwards.
func seedModuleCatalog(t *testing.T, admin *pgxpool.Pool) moduleMetrics {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	m := moduleMetrics{
		board:      "ms.2173.board." + suffix,
		vendor:     "ms.2173.vendor." + suffix,
		grandchild: "ms.2173.grandchild." + suffix,
	}
	for _, r := range []struct{ id, level, category string }{
		{m.board, "board", "test"},
		{m.vendor, "program", "vendor"},
		{m.grandchild, "team", "test"},
	} {
		if _, err := admin.Exec(ctx, `
			INSERT INTO metrics_catalog (
				id, tenant_id, level, category, name, description, unit,
				cadence, compute_strategy, compute_evaluator,
				source_slices, notes
			) VALUES (
				$1, NULL, $2, $3, $1, '#2173 fixture', 'percent',
				'weekly', 'manual_input', NULL, ARRAY[]::TEXT[], ''
			)
		`, r.id, r.level, r.category); err != nil {
			t.Fatalf("seed catalog %q: %v", r.id, err)
		}
	}
	for _, e := range [][2]string{{m.board, m.vendor}, {m.vendor, m.grandchild}} {
		if _, err := admin.Exec(ctx, `
			INSERT INTO metric_cascade_edges (parent_id, child_id, weight, notes)
			VALUES ($1, $2, 1.0, '#2173 fixture edge')
		`, e[0], e[1]); err != nil {
			t.Fatalf("seed edge %v: %v", e, err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		ids := []string{m.board, m.vendor, m.grandchild}
		if _, err := admin.Exec(ctx, `DELETE FROM metric_cascade_edges WHERE parent_id = ANY($1) OR child_id = ANY($1)`, ids); err != nil {
			t.Logf("cleanup edges: %v", err)
		}
		if _, err := admin.Exec(ctx, `DELETE FROM metrics_catalog WHERE id = ANY($1)`, ids); err != nil {
			t.Logf("cleanup catalog: %v", err)
		}
	})
	return m
}

// switchOff stores a module switch as off for tenant.
func switchOff(t *testing.T, admin *pgxpool.Pool, tenant, key string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category, last_changed_by, last_changed_at)
		VALUES ($1, $2, false, '#2173 test', 'controls', 'test-2173', now())
		ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = false
	`, tenant, key); err != nil {
		t.Fatalf("switch off %s: %v", key, err)
	}
}

// moduleRouter serves the metrics read routes through the real handler
// for a viewer of tenant, with module views over pool.
func moduleRouter(t *testing.T, app, viewsPool *pgxpool.Pool, tenant string) http.Handler {
	t.Helper()
	views := moduleaccess.NewViews(moduleaccess.NewStore(viewsPool), featureflag.NewStore(viewsPool))
	h := metricsapi.New(app).WithModules(views)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "metrics-2173", TenantID: tenant,
				UserID: "user:" + uuid.NewString(), TenantRoles: []string{"viewer"},
			})
			ctx, err := tenancy.WithTenant(ctx, tenant)
			if err != nil {
				t.Errorf("WithTenant: %v", err)
				return
			}
			next.ServeHTTP(w, req.WithContext(featureflag.WithCache(ctx)))
		})
	})
	r.Get("/v1/metrics", h.ListCatalog)
	r.Get("/v1/metrics/cascade", h.GetCascade)
	r.Get("/v1/metrics/{id}", h.GetCatalog)
	return r
}

func serve(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func ids(rows any, key string) map[string]bool {
	out := map[string]bool{}
	list, _ := rows.([]any)
	for _, row := range list {
		if m, ok := row.(map[string]any); ok {
			if id, ok := m[key].(string); ok {
				out[id] = true
			}
		}
	}
	return out
}

func TestIntegration_Metrics_HiddenModuleLeftOut(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "feature_flag_audit_log", "feature_flags", "member_module_access")
	m := seedModuleCatalog(t, admin)
	h := moduleRouter(t, app, app, tenant)

	// Switched on: every seeded metric is listed and in the cascade.
	_, list := serve(t, h, "/v1/metrics")
	if got := ids(list["metrics"], "id"); !got[m.vendor] || !got[m.board] {
		t.Fatalf("switched on: list lacks seeded metrics")
	}
	_, cascade := serve(t, h, "/v1/metrics/cascade?level=board&depth=3")
	if got := ids(cascade["nodes"], "metric_id"); !got[m.vendor] || !got[m.grandchild] {
		t.Fatalf("switched on: cascade lacks the vendor subtree")
	}

	switchOff(t, admin, tenant, "vendor.enabled")

	_, list = serve(t, h, "/v1/metrics")
	got := ids(list["metrics"], "id")
	if got[m.vendor] {
		t.Errorf("switched off: list carries the vendor metric")
	}
	if !got[m.board] || !got[m.grandchild] {
		t.Errorf("switched off: list lost a core metric")
	}

	if code, _ := serve(t, h, "/v1/metrics/"+m.vendor); code != http.StatusNotFound {
		t.Errorf("switched off: GET vendor metric = %d; want 404", code)
	}
	code, detail := serve(t, h, "/v1/metrics/"+m.board)
	if code != http.StatusOK {
		t.Fatalf("GET board metric = %d; want 200", code)
	}
	if ids(detail["children"], "id")[m.vendor] {
		t.Errorf("switched off: board's children carry the vendor metric")
	}
	code, detail = serve(t, h, "/v1/metrics/"+m.grandchild)
	if code != http.StatusOK {
		t.Fatalf("GET grandchild metric = %d; want 200", code)
	}
	if ids(detail["parents"], "id")[m.vendor] {
		t.Errorf("switched off: grandchild's parents carry the vendor metric")
	}

	_, cascade = serve(t, h, "/v1/metrics/cascade?level=board&depth=3")
	nodes := ids(cascade["nodes"], "metric_id")
	if !nodes[m.board] {
		t.Errorf("switched off: cascade lost the core root")
	}
	if nodes[m.vendor] || nodes[m.grandchild] {
		t.Errorf("switched off: cascade keeps the vendor subtree (vendor %v, grandchild %v)", nodes[m.vendor], nodes[m.grandchild])
	}
}

// A views helper that cannot read the caller's module access answers 500
// on every metrics read, never the unfiltered catalog.
func TestIntegration_Metrics_ViewsErrorIs500(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin)
	m := seedModuleCatalog(t, admin)

	broken, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(broken.Close)
	h := moduleRouter(t, app, broken, tenant)

	for _, path := range []string{"/v1/metrics", "/v1/metrics/cascade", "/v1/metrics/" + m.board} {
		if code, _ := serve(t, h, path); code != http.StatusInternalServerError {
			t.Errorf("GET %s with failing views = %d; want 500", path, code)
		}
	}
}
