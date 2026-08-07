//go:build integration

// OE-432 (slice 356a follow-up) — integration coverage for the authz
// middleware's behaviour when the Postgres dependency is unavailable.
//
// Slice 356a's chaos run measured 120/120 authenticated requests
// returning 500 {"error":"authorization engine error"} with ZERO log
// lines while Postgres was stopped. Slice 356a set
// detection_tier_target: integration precisely because no test tier
// reached this path. This file is that tier.
//
// The outage is reproduced by severing the connection, not by stopping
// the shared CI Postgres (which would break every other package on the
// leg, and the OE-432 issue explicitly forbids re-running the chaos
// experiment): the DB-backed roles resolver is pointed at a port with
// no listener, which produces the exact error shape a stopped Postgres
// produced during the recorded run — dial refused on every attempt.
// The steady-state twin below proves the SAME middleware + resolver
// wiring against the REAL RLS-enforced pool returns 200, so the 503 is
// demonstrably outage-specific rather than an artifact of the wiring.
//
// Run with:  go test -tags=integration -race ./internal/api/authzmw/...
//
// Requires (steady-state test only; the outage test needs no services):
//
//	DATABASE_URL_APP  app DSN (atlas_app role, RLS-enforced)
package authzmw_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// severedPool returns a pgxpool whose DSN targets a loopback port with
// no listener: every acquire fails with dial-refused, the same shape a
// stopped Postgres presented during the slice 356a outage window.
// pgxpool.New is lazy, so construction succeeds and the failure
// surfaces on first use — exactly like a warm platform whose database
// just went away.
func severedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve dead port: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()

	dsn := fmt.Sprintf("postgres://atlas_app:unused@127.0.0.1:%d/atlas?sslmode=disable&connect_timeout=2", port)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New(severed): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// outageRequest builds an authenticated GET /v1/anchors with tenant +
// credential context, mirroring the chaos run's read probe.
func outageRequest(t *testing.T, tenant string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	ctx, err := tenancy.WithTenant(req.Context(), tenant)
	if err != nil {
		t.Fatalf("tenancy.WithTenant: %v", err)
	}
	cred := credstore.Credential{
		ID:       "key_chaos_432",
		TenantID: tenant,
		UserID:   "key_chaos_432",
		IsAdmin:  true,
	}
	return req.WithContext(authctx.WithCredential(ctx, cred))
}

// TestMiddleware_Integration_DBUnavailable asserts the three OE-432
// acceptance surfaces on the severed-DB path: status code (503, not
// the pre-fix 500), body shape (database_unavailable + retry_after +
// request_id, no internal detail), and log emission (error level, with
// request context and the real dial error).
//
// NOT t.Parallel: captures slog.Default (process-global), same pattern
// as the unit tier.
func TestMiddleware_Integration_DBUnavailable(t *testing.T) {
	logBuf := captureSlog(t)

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(severedPool(t)))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	reached := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, outageRequest(t, uuid.NewString()))

	if reached {
		t.Fatalf("inner handler reached while database severed")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (pre-OE-432 behaviour was 500); body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After header = %q, want \"5\"", got)
	}

	var body struct {
		Error      string `json:"error"`
		RetryAfter int    `json:"retry_after"`
		RequestID  string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Error != "database_unavailable" {
		t.Fatalf("body.error = %q, want \"database_unavailable\" (names the dependency, not the authz engine)", body.Error)
	}
	if body.RetryAfter != 5 {
		t.Fatalf("body.retry_after = %d, want 5", body.RetryAfter)
	}
	if body.RequestID == "" {
		t.Fatalf("body.request_id empty")
	}
	assertNoLeak(t, rec.Body.String())

	// The slice 356a G-2 kill-shot: the 5xx must have a corresponding
	// server-side error log line carrying request context and the real
	// cause ("connection refused" from the severed dial).
	logged := logBuf.String()
	for _, want := range []string{
		"database dependency unavailable",
		body.RequestID,
		`"path":"/v1/anchors"`,
		"tenant_id",
		"connection refused",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("slog output missing %q: %s", want, logged)
		}
	}
}

// TestMiddleware_Integration_SteadyStateContrast proves the severed
// test's 503 is outage-specific: the identical middleware + DB-backed
// resolver wiring, pointed at the REAL RLS-enforced pool, lets the
// request through. Skips when DATABASE_URL_APP is unset.
func TestMiddleware_Integration_SteadyStateContrast(t *testing.T) {
	appPool := dbtest.NewAppPool(t)

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(appPool))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	reached := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, outageRequest(t, uuid.NewString()))

	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("steady-state request blocked: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
