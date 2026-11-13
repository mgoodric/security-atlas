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
	"github.com/mgoodric/security-atlas/internal/platform"
)

// fakeResetter records ResetBootstrap calls and reports a fixed tenant count.
// resetErr, when set, is returned by ResetBootstrap.
type fakeResetter struct {
	tenants    int
	resetCalls int
	resetErr   error
}

func (f *fakeResetter) ResetBootstrap(context.Context, bool) error {
	f.resetCalls++
	return f.resetErr
}

func (f *fakeResetter) TenantCount(context.Context) (int, error) { return f.tenants, nil }

// resetFixture is a Server with a fake resetter and a bootstrap-token path
// in a temp dir. tenantID is the caller's tenant for every credential the
// fixture's callers present.
type resetFixture struct {
	srv       *Server
	resetter  *fakeResetter
	tokenPath string
	tenantID  string
}

func newResetFixture(t *testing.T, tenants int) resetFixture {
	t.Helper()
	srv := New(Config{})
	r := &fakeResetter{tenants: tenants}
	srv.AttachPlatformResetter(r)
	path := filepath.Join(t.TempDir(), "bootstrap-token")
	srv.AttachBootstrapTokenPath(path)
	return resetFixture{srv: srv, resetter: r, tokenPath: path, tenantID: uuid.NewString()}
}

// tenantAdminKey is what a per-tenant admin API key produces: IsAdmin, no JWT.
func (f resetFixture) tenantAdminKey(ctx context.Context) context.Context {
	return authctx.WithCredential(ctx, credstore.Credential{
		ID: "key_admin", TenantID: f.tenantID, IsAdmin: true,
	})
}

func (f resetFixture) superAdminJWT(ctx context.Context) context.Context {
	userID := uuid.NewString()
	ctx = authctx.WithCredential(ctx, credstore.Credential{
		ID: "jwt:test", UserID: "user:" + userID, TenantID: f.tenantID,
	})
	return jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user:" + userID},
		SuperAdmin:       true,
	})
}

func (f resetFixture) viewer(ctx context.Context) context.Context {
	return authctx.WithCredential(ctx, credstore.Credential{ID: "key_viewer", TenantID: f.tenantID})
}

func (f resetFixture) postBody(t *testing.T, as func(context.Context) context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/install/reset-bootstrap", strings.NewReader(body))
	if as != nil {
		req = req.WithContext(as(req.Context()))
	}
	rec := httptest.NewRecorder()
	f.srv.handleResetBootstrap(rec, req)
	return rec
}

func (f resetFixture) post(t *testing.T, as func(context.Context) context.Context) *httptest.ResponseRecorder {
	t.Helper()
	return f.postBody(t, as, `{"force":true}`)
}

// assertNothingWritten checks a refused reset left no trace: no status
// reset, no bootstrap-token file, and no credential minted for the tenant.
func (f resetFixture) assertNothingWritten(t *testing.T, wantResetCalls int) {
	t.Helper()
	if f.resetter.resetCalls != wantResetCalls {
		t.Fatalf("ResetBootstrap called %d times; want %d", f.resetter.resetCalls, wantResetCalls)
	}
	if _, err := os.Stat(f.tokenPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap-token file written on a refused reset (stat err=%v)", err)
	}
	if got := f.srv.credStore.List(f.tenantID); len(got) != 0 {
		t.Fatalf("refused reset minted %d credential(s) for the tenant; want 0", len(got))
	}
}

// #1851: on a multi-tenant install a tenant admin key is refused, and
// nothing is reset or written.
func TestResetBootstrap_TenantAdminKeyOnMultiTenantGets403(t *testing.T) {
	f := newResetFixture(t, 2)
	rec := f.post(t, f.tenantAdminKey)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	f.assertNothingWritten(t, 0)
}

// #1851, #1885: the allowed paths, a super_admin JWT on any install and the
// admin key fallback on a single-tenant install, reset the status and write
// a freshly minted ADMIN credential for the caller's tenant to the token
// file, so signing in with it after recovery gives an admin session.
func TestResetBootstrap_AllowedPathsMintAdminCredential(t *testing.T) {
	cases := map[string]struct {
		tenants  int
		superJWT bool
	}{
		"super_admin on multi-tenant":  {2, true},
		"super_admin on single-tenant": {1, true},
		"admin key on single-tenant":   {1, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, tc.tenants)
			as := f.tenantAdminKey
			if tc.superJWT {
				as = f.superAdminJWT
			}
			rec := f.post(t, as)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if f.resetter.resetCalls != 1 {
				t.Fatalf("ResetBootstrap called %d times; want 1", f.resetter.resetCalls)
			}
			var resp resetBootstrapResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !resp.StatusReset || !resp.FileWritten || resp.BearerToken == "" {
				t.Fatalf("response = %+v; want status_reset, file_written and a bearer", resp)
			}
			got, err := os.ReadFile(f.tokenPath)
			if err != nil {
				t.Fatalf("read token file: %v", err)
			}
			if string(got) != resp.BearerToken {
				t.Fatalf("token file = %q, want the returned bearer", got)
			}
			cred, err := f.srv.credStore.Authenticate(resp.BearerToken)
			if err != nil {
				t.Fatalf("re-issued bearer does not authenticate: %v", err)
			}
			if !cred.IsAdmin {
				t.Fatalf("re-issued bootstrap credential IsAdmin = false; want an admin credential")
			}
			if cred.TenantID != f.tenantID || resp.TenantID != f.tenantID || resp.CredentialID != cred.ID {
				t.Fatalf("re-issued credential %+v / response %+v; want tenant %s", cred, resp, f.tenantID)
			}
		})
	}
}

// #1885: a caller that does not pass the gate gets no admin credential. A
// non-admin key and a missing credential are refused before anything is
// minted, reset or written.
func TestResetBootstrap_NonGatedCallerGetsNoAdminCredential(t *testing.T) {
	f := newResetFixture(t, 1)
	if rec := f.post(t, f.viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rec.Code)
	}
	if rec := f.post(t, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential status = %d, want 401", rec.Code)
	}
	f.assertNothingWritten(t, 0)
}

// #1885: a reset refused by the foot-gun gate (first sign-in happened, no
// --force) mints no credential and writes no file.
func TestResetBootstrap_RefusedResetMintsNothing(t *testing.T) {
	f := newResetFixture(t, 1)
	f.resetter.resetErr = platform.ErrResetForbidden
	rec := f.postBody(t, f.superAdminJWT, `{"force":false}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	f.assertNothingWritten(t, 1)
}

// #1885: tenant_id naming another tenant is refused before anything is
// minted, reset or written; the caller's own tenant id is accepted.
func TestResetBootstrap_TenantIDMustBeCallersTenant(t *testing.T) {
	f := newResetFixture(t, 2)
	rec := f.postBody(t, f.superAdminJWT, `{"force":true,"tenant_id":"`+uuid.NewString()+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("other tenant: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	f.assertNothingWritten(t, 0)

	rec = f.postBody(t, f.superAdminJWT, `{"force":true,"tenant_id":"`+f.tenantID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own tenant: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// #1885: a caller-supplied token (what an older CLI sends) is ignored. The
// file never holds a caller-chosen string; it holds the minted credential.
func TestResetBootstrap_IgnoresCallerSuppliedToken(t *testing.T) {
	f := newResetFixture(t, 1)
	rec := f.postBody(t, f.superAdminJWT, `{"token":"hunter2","force":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(f.tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(got) == "hunter2" {
		t.Fatalf("token file holds the caller-supplied token")
	}
	if _, err := f.srv.credStore.Authenticate(string(got)); err != nil {
		t.Fatalf("token file does not hold a live credential: %v", err)
	}
}
