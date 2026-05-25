// Unit tests for the slice 278 admindemo handler.
//
// These tests cover the gate logic WITHOUT a Postgres dependency:
//
//   - Status route shape (enabled / disabled)
//   - Env-var gate returns 503 when unset
//   - Admin gate returns 401 when no credential, 403 when not admin
//   - Rate limiter returns 429 on second call inside window
//
// The DB-bound paths (audit-row write + seeder Apply / Teardown)
// are covered by integration_test.go which requires a real Postgres.

package admindemo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// --- helpers ---

// requestWithAdmin returns a fresh request whose context carries an
// admin credential (cred.IsAdmin=true). The credential's TenantID is
// a valid UUID so resolveActor can construct it.
func requestWithAdmin(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	cred := credstore.Credential{
		ID:       "test-admin",
		UserID:   uuid.NewString(),
		TenantID: uuid.NewString(),
		IsAdmin:  true,
	}
	ctx := authctx.WithCredential(r.Context(), cred)
	return r.WithContext(ctx)
}

// requestWithViewer returns a request with a non-admin credential.
func requestWithViewer(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	cred := credstore.Credential{
		ID:       "test-viewer",
		UserID:   uuid.NewString(),
		TenantID: uuid.NewString(),
		IsAdmin:  false,
	}
	ctx := authctx.WithCredential(r.Context(), cred)
	return r.WithContext(ctx)
}

// requestWithOwnerRoleAdmin returns a request whose credential carries
// the "admin" role via OwnerRoles (slice 192+ JWT path).
func requestWithOwnerRoleAdmin(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	cred := credstore.Credential{
		ID:         "test-owner-admin",
		UserID:     uuid.NewString(),
		TenantID:   uuid.NewString(),
		IsAdmin:    false,
		OwnerRoles: []string{"admin"},
	}
	ctx := authctx.WithCredential(r.Context(), cred)
	return r.WithContext(ctx)
}

// --- Status ---

// ISC-11: Status returns enabled=true when the gate is satisfied.
func TestStatus_EnabledTrue(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return true })
	w := httptest.NewRecorder()
	h.Status(w, requestWithAdmin(http.MethodGet, "/v1/admin/demo/status"))

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if !got.Enabled {
		t.Fatalf("enabled = false; want true")
	}
}

// ISC-11: Status returns enabled=false when the env-var is unset.
func TestStatus_EnabledFalse(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return false })
	w := httptest.NewRecorder()
	h.Status(w, requestWithAdmin(http.MethodGet, "/v1/admin/demo/status"))

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	var got statusResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Enabled {
		t.Fatalf("enabled = true; want false")
	}
}

// --- Admin gate ---

// ISC-4: non-admin caller (viewer) gets 403.
func TestSeed_NonAdminGets403(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return true })
	w := httptest.NewRecorder()
	h.Seed(w, requestWithViewer(http.MethodPost, "/v1/admin/demo/seed"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// ISC-4: missing credential gets 401.
func TestSeed_MissingCredentialGets401(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return true })
	r := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	w := httptest.NewRecorder()
	h.Seed(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

// ISC-4: admin role via OwnerRoles (JWT path) is accepted by the gate.
// Reaches the next gate (env-var) which returns 503 because the test
// passes an empty pool. Verifies the admin path doesn't 403.
func TestSeed_OwnerRoleAdminPasses(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return true })
	w := httptest.NewRecorder()
	h.Seed(w, requestWithOwnerRoleAdmin(http.MethodPost, "/v1/admin/demo/seed"))

	// Past the admin gate; will fail on the next gate (authPool=nil
	// → 503 "no auth pool"). Anything that is NOT 403/401 confirms
	// admin acceptance.
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Fatalf("admin via OwnerRoles rejected; status=%d body=%s", w.Code, w.Body.String())
	}
}

// --- Env-var gate ---

// ISC-3: env-var unset → 503 with documented error message.
func TestSeed_EnvDisabledGets503(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return false })
	w := httptest.NewRecorder()
	h.Seed(w, requestWithAdmin(http.MethodPost, "/v1/admin/demo/seed"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "demo seed not enabled on this deployment" {
		t.Fatalf("error message = %q; want exact phrase", body["error"])
	}
}

// ISC-9 parallel: env unset for teardown also gets 503.
func TestTeardown_EnvDisabledGets503(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return false })
	w := httptest.NewRecorder()
	h.Teardown(w, requestWithAdmin(http.MethodPost, "/v1/admin/demo/teardown"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503; body=%s", w.Code, w.Body.String())
	}
}

// --- Rate limiter ---

// ISC-5: second invocation within 60s from the same IP → 429.
func TestSeed_RateLimit429(t *testing.T) {
	t.Parallel()

	// Frozen clock so the limiter window is deterministic.
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	h := New(nil, func() bool { return true }).WithClock(func() time.Time { return now })

	r1 := requestWithAdmin(http.MethodPost, "/v1/admin/demo/seed")
	r1.RemoteAddr = "192.0.2.10:12345"
	w1 := httptest.NewRecorder()
	h.Seed(w1, r1)
	// First call passes the rate limit; will 503 on nil-pool gate.
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first call should not be rate-limited; got 429")
	}

	r2 := requestWithAdmin(http.MethodPost, "/v1/admin/demo/seed")
	r2.RemoteAddr = "192.0.2.10:54321" // same IP, different port
	w2 := httptest.NewRecorder()
	h.Seed(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d, want 429; body=%s", w2.Code, w2.Body.String())
	}
	if w2.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After header = %q; want 60", w2.Header().Get("Retry-After"))
	}
}

// ISC-5: independent IPs are NOT shared (different IPs each get one token).
func TestSeed_RateLimitPerIPIndependent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	h := New(nil, func() bool { return true }).WithClock(func() time.Time { return now })

	r1 := requestWithAdmin(http.MethodPost, "/v1/admin/demo/seed")
	r1.RemoteAddr = "192.0.2.10:12345"
	w1 := httptest.NewRecorder()
	h.Seed(w1, r1)

	r2 := requestWithAdmin(http.MethodPost, "/v1/admin/demo/seed")
	r2.RemoteAddr = "192.0.2.20:12345" // DIFFERENT IP
	w2 := httptest.NewRecorder()
	h.Seed(w2, r2)

	if w2.Code == http.StatusTooManyRequests {
		t.Fatalf("second call from different IP should not be rate-limited; got 429")
	}
}

// ISC-11: Status route is NOT rate-limited.
func TestStatus_NotRateLimited(t *testing.T) {
	t.Parallel()

	h := New(nil, func() bool { return true })
	r1 := requestWithAdmin(http.MethodGet, "/v1/admin/demo/status")
	r1.RemoteAddr = "192.0.2.30:12345"
	w1 := httptest.NewRecorder()
	h.Status(w1, r1)

	r2 := requestWithAdmin(http.MethodGet, "/v1/admin/demo/status")
	r2.RemoteAddr = "192.0.2.30:54321"
	w2 := httptest.NewRecorder()
	h.Status(w2, r2)

	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("status calls should not be rate-limited; got %d then %d", w1.Code, w2.Code)
	}
}

// --- clientIP edge cases ---

// X-Forwarded-For is HONORED when TRUST_FORWARDED_HEADERS=1.
//
// NOTE: t.Setenv serializes with other tests that depend on the env
// var; this test is intentionally NOT t.Parallel().
func TestClientIP_XFFHonoredWhenOptedIn(t *testing.T) {
	t.Setenv(trustForwardedHeadersEnv, "1")

	r := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	r.RemoteAddr = "192.0.2.99:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 192.0.2.99")
	if got := clientIP(r); got != "198.51.100.1" {
		t.Fatalf("clientIP = %q; want 198.51.100.1", got)
	}
}

// X-Forwarded-For is IGNORED by default (defense against spoofing).
func TestClientIP_XFFIgnoredByDefault(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	r.RemoteAddr = "192.0.2.99:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 192.0.2.99")
	if got := clientIP(r); got != "192.0.2.99" {
		t.Fatalf("clientIP = %q; want 192.0.2.99 (XFF should be ignored without TRUST_FORWARDED_HEADERS=1)", got)
	}
}

// RemoteAddr port suffix is stripped.
func TestClientIP_StripsPort(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	r.RemoteAddr = "192.0.2.99:1234"
	if got := clientIP(r); got != "192.0.2.99" {
		t.Fatalf("clientIP = %q; want 192.0.2.99", got)
	}
}
