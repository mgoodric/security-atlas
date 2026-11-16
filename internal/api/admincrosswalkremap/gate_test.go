package admincrosswalkremap_test

// #1777: every edge remap route changes, or reviews changes to, global
// fw_to_scf_edges rows, so each requires the verified JWT's super_admin
// claim (#1757). A tenant admin (per-tenant admin API key, or a JWT without
// super_admin) is refused 403 before the store is touched; a super_admin
// reaches it. The store here is a fake that records the decider.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/admincrosswalkremap"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/crosswalkremap"
)

type caller int

const (
	noCredential caller = iota
	tenantAdminAPIKey
	tenantAdminJWT
	superAdminJWT
)

type fakeStore struct {
	calls   int
	decided crosswalkremap.DecideInput
	err     error
}

func (f *fakeStore) List(context.Context, string, string) ([]crosswalkremap.Proposal, error) {
	f.calls++
	return []crosswalkremap.Proposal{}, f.err
}

func (f *fakeStore) Decide(_ context.Context, in crosswalkremap.DecideInput) (crosswalkremap.Decision, error) {
	f.calls++
	f.decided = in
	return crosswalkremap.Decision{ProposalID: in.ProposalID, Decision: in.Decision, DecidedBy: in.DecidedBy}, f.err
}

func (f *fakeStore) BulkApproveUnchanged(_ context.Context, decidedBy string) (crosswalkremap.BulkResult, error) {
	f.calls++
	f.decided = crosswalkremap.DecideInput{DecidedBy: decidedBy}
	return crosswalkremap.BulkResult{}, f.err
}

const subject = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c1777"

func router(c caller, store admincrosswalkremap.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			tenant := uuid.New()
			switch c {
			case tenantAdminAPIKey:
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID: "key_tenant_admin", TenantID: tenant.String(), IsAdmin: true, UserID: subject,
				})
			case tenantAdminJWT, superAdminJWT:
				ctx = jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
					RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
					CurrentTenantID:  tenant,
					Roles:            map[uuid.UUID][]string{tenant: {"admin"}},
					SuperAdmin:       c == superAdminJWT,
				})
				// IsAdmin is true for the tenant-admin JWT too, so the test
				// proves the gate reads the super_admin claim, not IsAdmin.
				ctx = authctx.WithCredential(ctx, credstore.Credential{
					ID: "jwt:test", TenantID: tenant.String(), IsAdmin: true, UserID: subject,
				})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	admincrosswalkremap.New(store).RegisterRoutes(r)
	return r
}

var routes = []struct{ method, path, body string }{
	{http.MethodGet, "/v1/admin/crosswalk-edge-remap-proposals", ""},
	{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/approve-unchanged", ""},
	{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + subject + "/approve", `{"scf_id":"TRM-20"}`},
	{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + subject + "/reject", `{"reason":"no"}`},
	{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + subject + "/keep-legacy", `{}`},
}

func TestEdgeRemap_SuperAdminGate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		caller caller
		want   int
	}{
		{"no credential", noCredential, http.StatusUnauthorized},
		{"tenant admin API key", tenantAdminAPIKey, http.StatusForbidden},
		{"tenant admin JWT without super_admin", tenantAdminJWT, http.StatusForbidden},
		{"super_admin JWT", superAdminJWT, http.StatusOK},
	} {
		for _, rt := range routes {
			t.Run(c.name+" "+rt.method+" "+rt.path, func(t *testing.T) {
				t.Parallel()
				store := &fakeStore{}
				req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
				rec := httptest.NewRecorder()
				router(c.caller, store).ServeHTTP(rec, req)
				if rec.Code != c.want {
					t.Fatalf("status = %d; want %d (body=%s)", rec.Code, c.want, rec.Body.String())
				}
				if c.want != http.StatusOK && store.calls != 0 {
					t.Fatalf("refused caller reached the store %d times", store.calls)
				}
				if c.want == http.StatusOK && rt.method == http.MethodPost && store.decided.DecidedBy == "" {
					t.Fatalf("super_admin decision recorded no decider")
				}
			})
		}
	}
}

func TestEdgeRemap_ApproveForwardsChosenAnchor(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	req := httptest.NewRequest(http.MethodPost, routes[2].path, strings.NewReader(`{"scf_id":"TRM-31","reason":"logging half"}`))
	rec := httptest.NewRecorder()
	router(superAdminJWT, store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if store.decided.Decision != crosswalkremap.StatusApproved || store.decided.SCFID != "TRM-31" ||
		store.decided.Reason != "logging half" || store.decided.ProposalID != subject {
		t.Fatalf("decision forwarded as %+v", store.decided)
	}
}

func TestEdgeRemap_ErrorStatuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		want int
	}{
		{crosswalkremap.ErrNotFound, http.StatusNotFound},
		{crosswalkremap.ErrDecided, http.StatusConflict},
		{crosswalkremap.ErrEdgeChanged, http.StatusConflict},
		{crosswalkremap.ErrEdgeExists, http.StatusConflict},
		{crosswalkremap.ErrBulkRunning, http.StatusConflict},
		{crosswalkremap.ErrAnchorRequired, http.StatusBadRequest},
		{crosswalkremap.ErrAnchorNotInRelease, http.StatusBadRequest},
		{crosswalkremap.ErrReasonTooLong, http.StatusBadRequest},
		{crosswalkremap.ErrDeciderRequired, http.StatusForbidden},
		{errors.New("boom"), http.StatusInternalServerError},
	} {
		store := &fakeStore{err: c.err}
		req := httptest.NewRequest(http.MethodPost, routes[2].path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		router(superAdminJWT, store).ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%v: status = %d; want %d", c.err, rec.Code, c.want)
		}
	}
}

func TestEdgeRemap_BadInputs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/admin/crosswalk-edge-remap-proposals?class=bogus", ""},
		{http.MethodGet, "/v1/admin/crosswalk-edge-remap-proposals?status=bogus", ""},
		{http.MethodPost, routes[2].path, `{"scf_id":"X","extra":1}`},
		{http.MethodPost, routes[3].path, `{"reason":"a"}{"reason":"b"}`},
	} {
		store := &fakeStore{}
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		router(superAdminJWT, store).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || store.calls != 0 {
			t.Errorf("%s %s %s: status = %d, store calls = %d; want 400 and none", c.method, c.path, c.body, rec.Code, store.calls)
		}
	}
}
