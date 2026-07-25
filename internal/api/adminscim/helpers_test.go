// Pure-Go unit tests for the slice-508 admin SCIM-credential HANDLERS — no
// Postgres, no build tag (the slice 353 "pure-Go pre-DB unit convention").
//
// The Issue / List / Revoke handlers each run a sequence of pure-Go GUARDS
// before they ever touch the *scim.CredentialStore: the admin gate
// (requireAdmin), then per-handler input validation (JSON body decode for
// Issue, chi URL-param UUID parse for Revoke). Those guard branches return
// the request WITHOUT dereferencing the store, so they are exercisable with a
// Handler constructed over a nil store. This lifts the handlers' pure-Go
// branch coverage in the unit tier (the integration suite covers the
// store-touching happy paths). Added in slice 436 to restore the package's
// coverage-floor margin after the god-file split's cross-package attribution
// shift — no adminscim source changed (behavior-preserving).
package adminscim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// adminCtxReq returns a request whose context carries an ADMIN credential for
// a fresh tenant, plus a `user:`-prefixed subject so the Issue path's
// parseUserID branch runs.
func adminCtxReq(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID: uuid.New().String(),
		IsAdmin:  true,
		UserID:   "user:" + uuid.New().String(),
	})
	return r.WithContext(ctx)
}

// nonAdminReq returns a request whose context carries a NON-admin credential.
func nonAdminReq(t *testing.T, method, target string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID: uuid.New().String(),
		IsAdmin:  false,
	})
	return r.WithContext(ctx)
}

// withChiParam wires a chi route param into the request context so
// chi.URLParam(r, key) resolves inside the handler under test.
func withChiParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// newNilStoreHandler returns a Handler over a nil store. Every guard branch
// under test returns BEFORE the store is dereferenced, so the nil is never
// touched — a touch would panic and fail the test loudly.
func newNilStoreHandler() *Handler { return New(nil) }

// newDeadStoreHandler returns a Handler over a CredentialStore backed by a
// non-dialing pool. When a handler reaches its store call, BeginTx fails
// (no connection / a canceled context) and the handler takes its
// httperr.WriteInternal (500) error branch — covering the error-mapping path
// without a live database. The store-touching happy paths (real rows, the
// ListItem mapping loop) stay the integration tier's job.
func newDeadStoreHandler(t *testing.T) *Handler {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://atlas:atlas@127.0.0.1:1/atlas")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	hasher, err := bearer.NewHasher([]byte("adminscim-unit-test-hasher-key-0001-pad-to-32"))
	if err != nil {
		t.Fatalf("bearer.NewHasher: %v", err)
	}
	return New(scim.NewCredentialStore(pool, pool, hasher))
}

// canceledAdminReq is an admin request whose context is already canceled, so
// the first store BeginTx fails immediately and deterministically (no socket
// dial, no timeout wait).
func canceledAdminReq(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	r := adminCtxReq(t, method, target, body)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	return r.WithContext(ctx)
}

func TestIssue_GuardBranches(t *testing.T) {
	t.Parallel()
	h := newNilStoreHandler()

	t.Run("non-admin → 403 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		h.Issue(rec, nonAdminReq(t, http.MethodPost, "/v1/admin/scim-credentials"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403", rec.Code)
		}
	})

	t.Run("missing credential → 401 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials", nil)
		h.Issue(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", rec.Code)
		}
	})

	t.Run("admin + malformed JSON body → 400 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		// A non-EOF decode error: trailing garbage / not an object.
		h.Issue(rec, adminCtxReq(t, http.MethodPost, "/v1/admin/scim-credentials", "{not json"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", rec.Code)
		}
	})
}

func TestList_GuardBranches(t *testing.T) {
	t.Parallel()
	h := newNilStoreHandler()

	t.Run("non-admin → 403 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		h.List(rec, nonAdminReq(t, http.MethodGet, "/v1/admin/scim-credentials"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403", rec.Code)
		}
	})

	t.Run("missing credential → 401 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/scim-credentials", nil)
		h.List(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", rec.Code)
		}
	})
}

func TestRevoke_GuardBranches(t *testing.T) {
	t.Parallel()
	h := newNilStoreHandler()

	t.Run("non-admin → 403 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := withChiParam(
			nonAdminReq(t, http.MethodDelete, "/v1/admin/scim-credentials/"+uuid.NewString()),
			"id", uuid.NewString(),
		)
		h.Revoke(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403", rec.Code)
		}
	})

	t.Run("admin + invalid {id} → 400 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := withChiParam(
			adminCtxReq(t, http.MethodDelete, "/v1/admin/scim-credentials/not-a-uuid", ""),
			"id", "not-a-uuid",
		)
		h.Revoke(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", rec.Code)
		}
	})

	t.Run("admin + empty {id} → 400 (before store)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := withChiParam(
			adminCtxReq(t, http.MethodDelete, "/v1/admin/scim-credentials/", ""),
			"id", "",
		)
		h.Revoke(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", rec.Code)
		}
	})
}

// TestStoreErrorBranches drives each handler past its pure-Go guards to the
// store call with a canceled context, so the store fails and the handler's
// httperr.WriteInternal (500) error branch runs — the cheapest way to cover
// the error-mapping path in the unit tier (the happy path is integration's).
func TestStoreErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("Issue store failure → 500", func(t *testing.T) {
		t.Parallel()
		h := newDeadStoreHandler(t)
		rec := httptest.NewRecorder()
		// Valid JSON body so the decode guard passes and the store is reached.
		h.Issue(rec, canceledAdminReq(t, http.MethodPost, "/v1/admin/scim-credentials", `{"description":"ci"}`))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}
	})

	t.Run("List store failure → 500", func(t *testing.T) {
		t.Parallel()
		h := newDeadStoreHandler(t)
		rec := httptest.NewRecorder()
		h.List(rec, canceledAdminReq(t, http.MethodGet, "/v1/admin/scim-credentials", ""))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}
	})

	t.Run("Revoke store failure → 500", func(t *testing.T) {
		t.Parallel()
		h := newDeadStoreHandler(t)
		rec := httptest.NewRecorder()
		req := withChiParam(
			canceledAdminReq(t, http.MethodDelete, "/v1/admin/scim-credentials/"+uuid.NewString(), ""),
			"id", uuid.NewString(),
		)
		h.Revoke(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}
	})
}

// TestNew_ConstructsHandler exercises the New constructor directly (the
// god-file split removed the only unit-tier call site that ran it via the
// in-test server wiring; covering it here keeps the constructor in the unit
// profile).
func TestNew_ConstructsHandler(t *testing.T) {
	t.Parallel()
	if New(nil) == nil {
		t.Fatal("New(nil) returned nil Handler")
	}
}
