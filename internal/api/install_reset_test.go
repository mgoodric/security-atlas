package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// fakeResetter records ResetBootstrap calls and reports a fixed tenant count.
type fakeResetter struct {
	tenants    int
	resetCalls int
}

func (f *fakeResetter) ResetBootstrap(context.Context, bool) error {
	f.resetCalls++
	return nil
}

func (f *fakeResetter) TenantCount(context.Context) (int, error) { return f.tenants, nil }

// resetFixture is a Server with a fake resetter, a bootstrap-token path in a
// temp dir, and one live credential whose bearer is the valid reset token.
type resetFixture struct {
	srv       *Server
	resetter  *fakeResetter
	tokenPath string
	liveToken string
}

func newResetFixture(t *testing.T, tenants int) resetFixture {
	t.Helper()
	srv := New(Config{})
	r := &fakeResetter{tenants: tenants}
	srv.AttachPlatformResetter(r)
	path := filepath.Join(t.TempDir(), "bootstrap-token")
	srv.AttachBootstrapTokenPath(path)
	_, tok, err := srv.IssueBootstrapCredential(uuid.NewString())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return resetFixture{srv: srv, resetter: r, tokenPath: path, liveToken: tok}
}

// tenantAdminKey is what a per-tenant admin API key produces: IsAdmin, no JWT.
func tenantAdminKey(ctx context.Context) context.Context {
	return authctx.WithCredential(ctx, credstore.Credential{
		ID: "key_admin", TenantID: uuid.NewString(), IsAdmin: true,
	})
}

func superAdminJWT(ctx context.Context) context.Context {
	userID := uuid.NewString()
	ctx = authctx.WithCredential(ctx, credstore.Credential{
		ID: "jwt:test", UserID: "user:" + userID, TenantID: uuid.NewString(),
	})
	return jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user:" + userID},
		SuperAdmin:       true,
	})
}

func (f resetFixture) post(t *testing.T, as func(context.Context) context.Context, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(resetBootstrapRequest{Token: token, Force: true})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/install/reset-bootstrap", strings.NewReader(string(body)))
	if as != nil {
		req = req.WithContext(as(req.Context()))
	}
	rec := httptest.NewRecorder()
	f.srv.handleResetBootstrap(rec, req)
	return rec
}

// #1851: on a multi-tenant install a tenant admin key is refused, and
// nothing is reset or written.
func TestResetBootstrap_TenantAdminKeyOnMultiTenantGets403(t *testing.T) {
	f := newResetFixture(t, 2)
	rec := f.post(t, tenantAdminKey, f.liveToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if f.resetter.resetCalls != 0 {
		t.Fatalf("ResetBootstrap called %d times; want 0", f.resetter.resetCalls)
	}
	if _, err := os.Stat(f.tokenPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap-token file written on a refused reset (stat err=%v)", err)
	}
}

// #1851: the allowed paths, a super_admin JWT on any install and the admin
// key fallback on a single-tenant install, reset and write the token.
func TestResetBootstrap_AllowedPaths(t *testing.T) {
	cases := map[string]struct {
		tenants int
		as      func(context.Context) context.Context
	}{
		"super_admin on multi-tenant":  {2, superAdminJWT},
		"super_admin on single-tenant": {1, superAdminJWT},
		"admin key on single-tenant":   {1, tenantAdminKey},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, tc.tenants)
			rec := f.post(t, tc.as, f.liveToken)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if f.resetter.resetCalls != 1 {
				t.Fatalf("ResetBootstrap called %d times; want 1", f.resetter.resetCalls)
			}
			got, err := os.ReadFile(f.tokenPath)
			if err != nil {
				t.Fatalf("read token file: %v", err)
			}
			if string(got) != f.liveToken {
				t.Fatalf("token file = %q, want the issued token", got)
			}
		})
	}
}

// A non-admin credential and a missing credential are refused.
func TestResetBootstrap_NonAdminRefused(t *testing.T) {
	f := newResetFixture(t, 1)
	viewer := func(ctx context.Context) context.Context {
		return authctx.WithCredential(ctx, credstore.Credential{ID: "key_viewer", TenantID: uuid.NewString()})
	}
	if rec := f.post(t, viewer, f.liveToken); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rec.Code)
	}
	if rec := f.post(t, nil, f.liveToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential status = %d, want 401", rec.Code)
	}
	if f.resetter.resetCalls != 0 {
		t.Fatalf("ResetBootstrap called %d times; want 0", f.resetter.resetCalls)
	}
}

// #1851: a caller-chosen string, or a revoked credential's token, is refused
// before anything is reset or written, even for a super_admin.
func TestResetBootstrap_RejectsTokenThatIsNotALiveCredential(t *testing.T) {
	f := newResetFixture(t, 1)
	cred, revoked, err := f.srv.IssueBootstrapCredential(uuid.NewString())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := f.srv.credStore.Revoke(cred.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for name, tok := range map[string]string{
		"caller-chosen": "hunter2",
		"revoked":       revoked,
	} {
		rec := f.post(t, superAdminJWT, tok)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400; body=%s", name, rec.Code, rec.Body.String())
		}
	}
	if f.resetter.resetCalls != 0 {
		t.Fatalf("ResetBootstrap called %d times; want 0", f.resetter.resetCalls)
	}
	if _, err := os.Stat(f.tokenPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap-token file written for an invalid token (stat err=%v)", err)
	}
}
