//go:build integration

// Integration tests for the slice 034 admin credentials HTTP API. They drive
// the handlers through httptest, requiring a real Postgres reachable via
// DATABASE_URL_APP. The DB-backed apikeystore.Store is wired with the test
// hash key.
package admincreds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

const testHashKey = "atlas-slice-034-test-hash-key!!1"

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping admincreds HTTP integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New(app): %v\n", err)
		os.Exit(1)
	}
	appPool = p
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		ap, err := pgxpool.New(ctx, adminURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New(admin): %v\n", err)
			os.Exit(1)
		}
		adminPool = ap
	}
	code := m.Run()
	p.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

func newHandler(t *testing.T, tenantID uuid.UUID) (*admincreds.Handler, *apikeystore.Store, http.Handler) {
	t.Helper()
	hasher, err := bearer.NewHasher([]byte(testHashKey))
	if err != nil {
		t.Fatalf("bearer.NewHasher: %v", err)
	}
	store := apikeystore.NewStore(appPool, adminPool, hasher, 7*24*time.Hour)
	store.SetPrefix(bearer.PrefixTest)
	h := admincreds.New(store)

	r := chi.NewRouter()
	// Inject an admin credential into every request so requireAdmin passes.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_admin",
				TenantID: tenantID.String(),
				IsAdmin:  true,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/admin/credentials", h.Issue)
	r.Get("/v1/admin/credentials", h.List)
	r.Post("/v1/admin/credentials/{id}/rotate", h.Rotate)
	r.Post("/v1/admin/credentials/{id}/revoke", h.Revoke)
	return h, store, r
}

// === ISC-34: Issue returns 201 with bearer_token exactly once ===

func TestIssueReturnsBearerExactlyOnce(t *testing.T) {
	tenantID := uuid.New()
	_, _, handler := newHandler(t, tenantID)
	defer cleanupTenant(tenantID)

	body, _ := json.Marshal(admincreds.IssueRequest{TenantID: tenantID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Issue: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp admincreds.IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BearerToken == "" {
		t.Fatalf("bearer_token missing in response")
	}
	if !strings.HasPrefix(resp.BearerToken, bearer.PrefixTest) {
		t.Fatalf("bearer_token prefix mismatch: %s", resp.BearerToken)
	}
	if resp.Last4 != resp.BearerToken[len(resp.BearerToken)-4:] {
		t.Fatalf("last4 does not match bearer suffix")
	}
}

// === ISC-35: List never includes bearer_token; filters to caller's tenant ===

func TestListExcludesBearerToken(t *testing.T) {
	tenantID := uuid.New()
	_, _, handler := newHandler(t, tenantID)
	defer cleanupTenant(tenantID)

	body, _ := json.Marshal(admincreds.IssueRequest{TenantID: tenantID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Issue: want 201, got %d", w.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/admin/credentials?tenant_id="+tenantID.String(), nil)
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d: %s", listW.Code, listW.Body.String())
	}
	if strings.Contains(listW.Body.String(), "bearer_token") {
		t.Fatalf("List response leaked bearer_token: %s", listW.Body.String())
	}
}

// === ISC-37: Revoke returns 204; subsequent Authenticate returns 401 ===

func TestRevokeInvalidates(t *testing.T) {
	tenantID := uuid.New()
	_, store, handler := newHandler(t, tenantID)
	defer cleanupTenant(tenantID)

	body, _ := json.Marshal(admincreds.IssueRequest{TenantID: tenantID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var resp admincreds.IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rev := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials/"+resp.ID+"/revoke", nil)
	revW := httptest.NewRecorder()
	handler.ServeHTTP(revW, rev)
	if revW.Code != http.StatusNoContent {
		t.Fatalf("Revoke: want 204, got %d: %s", revW.Code, revW.Body.String())
	}

	if _, err := store.Authenticate(context.Background(), resp.BearerToken); err == nil {
		t.Fatalf("Authenticate(revoked) returned nil error; expected ErrUnknownKey")
	}
}

// === ISC-36: Rotate returns successor + predecessor_expires_at ===

func TestRotateGivesSuccessor(t *testing.T) {
	tenantID := uuid.New()
	_, _, handler := newHandler(t, tenantID)
	defer cleanupTenant(tenantID)

	body, _ := json.Marshal(admincreds.IssueRequest{TenantID: tenantID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var orig admincreds.IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &orig); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rot := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials/"+orig.ID+"/rotate", nil)
	rotW := httptest.NewRecorder()
	handler.ServeHTTP(rotW, rot)
	if rotW.Code != http.StatusOK {
		t.Fatalf("Rotate: want 200, got %d: %s", rotW.Code, rotW.Body.String())
	}
	var rotResp admincreds.RotateResponse
	if err := json.Unmarshal(rotW.Body.Bytes(), &rotResp); err != nil {
		t.Fatalf("decode rotate: %v", err)
	}
	if rotResp.BearerToken == orig.BearerToken {
		t.Fatalf("Rotate returned same bearer as predecessor")
	}
	if rotResp.PredecessorExpiresAt.IsZero() {
		t.Fatalf("predecessor_expires_at is zero")
	}
	if rotResp.ID == orig.ID {
		t.Fatalf("Rotate returned same id as predecessor")
	}
}

// === ISC-9 (proxy): non-admin caller gets 403 from Issue ===

func TestIssueRequiresAdmin(t *testing.T) {
	tenantID := uuid.New()
	hasher, _ := bearer.NewHasher([]byte(testHashKey))
	store := apikeystore.NewStore(appPool, adminPool, hasher, 0)
	h := admincreds.New(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Non-admin credential.
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_non_admin",
				TenantID: tenantID.String(),
				IsAdmin:  false,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/admin/credentials", h.Issue)

	body, _ := json.Marshal(admincreds.IssueRequest{TenantID: tenantID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("Issue(non-admin): want 403, got %d", w.Code)
	}
}

func cleanupTenant(tenantID uuid.UUID) {
	if adminPool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = adminPool.Exec(ctx, "DELETE FROM api_keys WHERE tenant_id = $1", tenantID)
}
