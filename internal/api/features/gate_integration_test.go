//go:build integration

// Slice 660 — route-gate integration tests. These are the LOAD-BEARING
// proof that a flag-off module's route is actually UNREACHABLE (404),
// not merely nav-hidden. They wire the real featureflag.Gate middleware
// (the same factory httpserver.go applies to the OSCAL + board routes)
// over a sentinel handler against real Postgres, so the per-tenant flag
// read happens under RLS exactly as in production.
//
// Coverage:
//   - flag OFF (Seed default) -> 404 + {"error":"feature disabled"} (NOT
//     200, NOT a 500/blank); the sentinel never runs.
//   - flag ON  (tenant toggled it) -> sentinel runs (200).
//   - no internal-error leak on the gated path.
//   - RLS: tenant A toggling its flag ON does NOT make tenant B reachable.
//
// Requires Postgres reachable via DATABASE_URL_APP (shared TestMain in
// handler_integration_test.go).

package features_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// tenancyCtx builds a tenant-scoped context for direct Store calls.
func tenancyCtx(tenant uuid.UUID) (context.Context, error) {
	return tenancy.WithTenant(context.Background(), tenant.String())
}

// sentinelMarker is what a reachable downstream handler writes. A 404 from
// the gate means the request never reached it, so this marker is absent.
const sentinelMarker = "REACHED_DOWNSTREAM"

// newGatedRouter wires the production middleware stack fragment relevant
// to the gate: a canned credential -> tenancymw (derives tenant + sets
// RLS GUC) -> featureflag.CacheMiddleware -> featureflag.Gate(key) over a
// sentinel handler. This mirrors httpserver.go's OSCAL / board groups.
func newGatedRouter(t *testing.T, tenantID uuid.UUID, key string) http.Handler {
	t.Helper()
	store := featureflag.NewStore(appPool)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test",
				TenantID: tenantID.String(),
				IsAdmin:  true,
				UserID:   "test-user",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Use(featureflag.CacheMiddleware)
	r.Group(func(g chi.Router) {
		g.Use(featureflag.Gate(store, key))
		g.Get("/gated", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sentinelMarker))
		})
	})
	return r
}

func cleanupFlags(t *testing.T, tenant uuid.UUID) {
	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// toggleFlagOn flips a flag to enabled for the tenant via the Store (which
// applies RLS + writes the audit row), so the ON-case test exercises the
// real per-tenant override path rather than a raw INSERT.
func toggleFlagOn(t *testing.T, tenant uuid.UUID, key string) {
	t.Helper()
	store := featureflag.NewStore(appPool)
	ctx, err := tenancyCtx(tenant)
	if err != nil {
		t.Fatalf("tenancyCtx: %v", err)
	}
	if _, err := store.Set(ctx, key, true, "test-admin", "slice 660 gate test"); err != nil {
		t.Fatalf("Set(%q, true): %v", key, err)
	}
}

func TestGate_OSCALExport_OffReturns404(t *testing.T) {
	tenant := uuid.New()
	cleanupFlags(t, tenant)
	r := newGatedRouter(t, tenant, "oscal.export") // OFF by Seed default

	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 when oscal.export is OFF (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Feature-Disabled"); got != "oscal.export" {
		t.Errorf("X-Feature-Disabled = %q; want oscal.export", got)
	}
	// Clean disabled shape; the sentinel must NOT have run.
	if rec.Body.String() == sentinelMarker {
		t.Fatal("downstream handler ran; the gate did not block the request")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v (body=%s)", err, rec.Body.String())
	}
	if body["error"] != "feature disabled" {
		t.Errorf("error = %q; want %q", body["error"], "feature disabled")
	}
	// No internal detail leak (slice 367): the body has exactly the one
	// "error" key, no stack / SQL / internal message.
	if len(body) != 1 {
		t.Errorf("response body has %d keys; want exactly 1 (no leak)", len(body))
	}
}

func TestGate_BoardReporting_OffReturns404(t *testing.T) {
	tenant := uuid.New()
	cleanupFlags(t, tenant)
	r := newGatedRouter(t, tenant, "board.reporting") // OFF by Seed default

	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 when board.reporting is OFF (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == sentinelMarker {
		t.Fatal("downstream handler ran; the gate did not block the request")
	}
}

func TestGate_OSCALExport_OnReachesHandler(t *testing.T) {
	tenant := uuid.New()
	cleanupFlags(t, tenant)
	toggleFlagOn(t, tenant, "oscal.export")
	r := newGatedRouter(t, tenant, "oscal.export")

	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 when oscal.export is ON (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != sentinelMarker {
		t.Errorf("body = %q; want sentinel (downstream should have run)", rec.Body.String())
	}
}

func TestGate_BoardReporting_OnReachesHandler(t *testing.T) {
	tenant := uuid.New()
	cleanupFlags(t, tenant)
	toggleFlagOn(t, tenant, "board.reporting")
	r := newGatedRouter(t, tenant, "board.reporting")

	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 when board.reporting is ON (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestGate_RLSIsolation proves the gate decision is per-tenant: tenant A
// toggling oscal.export ON does NOT make the route reachable for tenant B
// (whose flag stays at the Seed default OFF). This exercises constitutional
// invariant #6 — the flag override row is RLS-scoped, so the gate read for
// tenant B never sees tenant A's row.
func TestGate_RLSIsolation(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupFlags(t, tenantA)
	cleanupFlags(t, tenantB)

	toggleFlagOn(t, tenantA, "oscal.export") // only A toggles ON

	// Tenant A: reachable.
	ra := newGatedRouter(t, tenantA, "oscal.export")
	recA := httptest.NewRecorder()
	ra.ServeHTTP(recA, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if recA.Code != http.StatusOK {
		t.Fatalf("tenant A status = %d; want 200 (A toggled ON)", recA.Code)
	}

	// Tenant B: still gated (404). A's override must not leak across RLS.
	rb := newGatedRouter(t, tenantB, "oscal.export")
	recB := httptest.NewRecorder()
	rb.ServeHTTP(recB, httptest.NewRequest(http.MethodGet, "/gated", nil))
	if recB.Code != http.StatusNotFound {
		t.Fatalf("tenant B status = %d; want 404 (B never toggled; RLS must isolate A's override)", recB.Code)
	}
}
