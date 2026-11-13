//go:build integration

// #1850: a super_admin token minted by the test-mode issuer is backed by
// a super_admins row, so the JWT middleware's live re-check keeps the
// claim. Without the grant, every Playwright spec that relies on the
// default super_admin test token would lose admin access.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
)

func TestIntegrationIssueTestJWT_SuperAdminBackedByTable(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL (BYPASSRLS admin pool) not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	t.Setenv("ATLAS_TEST_MODE", "1")
	const issuer = "https://atlas.test"
	srv, _ := testServerWithJWT(t, issuer)
	srv.AttachAuthPool(pool)

	tenant := uuid.New()
	subject := testSubjectFor(tenant)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM super_admins WHERE user_id = $1`, subject)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt",
		bytes.NewReader([]byte(`{"tenant_id":"`+tenant.String()+`"}`)))
	rec := httptest.NewRecorder()
	srv.handleIssueTestJWT(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	// The token now survives the production re-check with the claim.
	var claims *jwt.AtlasClaims
	mw := jwtmw.Middleware(srv.jwtSigner, nil, jwtmw.Options{
		ExpectedIssuer:   issuer,
		ExpectedAudience: issuer,
		SuperAdmins:      superadmin.NewDBChecker(pool),
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims = jwtmw.FromContext(r.Context())
	}))
	call := httptest.NewRequest(http.MethodGet, "/v1/admin/super-admins", nil)
	call.Header.Set("Authorization", "Bearer "+resp.Token)
	h.ServeHTTP(httptest.NewRecorder(), call)
	if claims == nil {
		t.Fatal("token did not authenticate")
	}
	if !claims.SuperAdmin {
		t.Error("test-mode super_admin token lost its claim; issuer did not record super_admins")
	}
}
