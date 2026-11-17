//go:build integration

// #2428: role matrix with real tenant-role JWTs. Each principal is a users
// row with one user_roles grant, signed in with an ES256 atlas JWT that
// carries no super_admin claim. The request runs through jwtmw, authzmw
// (with the DB-backed roles resolver on the RLS-enforced pool) and a
// handler that applies the route's real gate, so it shows what a person
// who signed in normally gets:
//   - a tenant admin passes every switched gate;
//   - a grc_engineer passes the approver gates and nothing admin-only;
//   - control_owner, auditor and viewer pass none;
//   - a revoked grant is refused while the token that carried it is live.
//
// Run with:  go test -tags=integration -race -run 2428 ./internal/authz/...

package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const (
	issuer2428   = "https://atlas.example.test"
	audience2428 = "https://atlas.example.test"
)

// stack2428 is jwtmw -> authzmw -> a handler that answers 204 when the
// route's gate admits the credential and 403 when it does not. reached
// reports whether the last request got past authzmw.
type stack2428 struct {
	signer  *tokensign.Signer
	handler http.Handler
	reached *bool
}

func newStack2428(t *testing.T, appPool *pgxpool.Pool) stack2428 {
	t.Helper()
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(store)
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(appPool))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	gates := map[string]gate2428{}
	for _, rt := range routes2428 {
		gates[rt.method+" "+rt.path] = rt.gate
	}
	reached := new(bool)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		cred, ok := authctx.CredentialFromContext(r.Context())
		// A row may name a query (#2134: GET /v1/needs-attention?for=all,
		// whose view is a query parameter), so the key keeps it.
		key := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		g, known := gates[key]
		if !ok || !known || !g.fn(cred) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   issuer2428,
		ExpectedAudience: audience2428,
	})(authzmw.Middleware(engine, nil)(inner))
	return stack2428{signer: signer, handler: h, reached: reached}
}

// mint signs a normal sign-in JWT for user in tenant, carrying role in the
// tenant role claim and no super_admin.
func (s stack2428) mint(t *testing.T, tenant string, user uuid.UUID, role authz.Role) string {
	t.Helper()
	tid := uuid.MustParse(tenant)
	now := time.Now()
	tok, err := s.signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer2428,
			Subject:   "user:" + user.String(),
			Audience:  []string{audience2428},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "local",
		CurrentTenantID:  tid,
		AvailableTenants: []uuid.UUID{tid},
		Roles:            map[uuid.UUID][]string{tid: {string(role)}},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// do sends one request with bearer and returns the status and whether the
// handler was reached (authzmw allowed it).
func (s stack2428) do(bearer, method, path string) (int, bool) {
	*s.reached = false
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w.Code, *s.reached
}

func seedUser2428(t *testing.T, admin *pgxpool.Pool, tenant string, role authz.Role) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2428', 'active')`,
		id, tenant, "u2428-"+id.String()+"@example.invalid"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2428')`,
		tenant, id.String(), string(role)); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}
	return id
}

func TestTenantGates2428_RealJWTRoleMatrix(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	s := newStack2428(t, appPool)

	for _, role := range tenantRoles2428 {
		bearer := s.mint(t, tenant, seedUser2428(t, adminPool, tenant, role), role)
		for _, rt := range routes2428 {
			code, reached := s.do(bearer, rt.method, rt.path)
			want := http.StatusForbidden
			if wantAdmit2428(rt.gate, role) {
				want = http.StatusNoContent
			}
			if code != want {
				t.Errorf("%s JWT: %s %s = %d (reached handler=%v), want %d", role, rt.method, rt.path, code, reached, want)
			}
		}
	}
}

// TestTenantGates2428_RevokedGrantRefusedWhileTokenLive: the token still
// says admin after the grant is deleted, and is still valid, but every
// gate refuses it, because the gates read the live user_roles and not the
// claim. A walkthrough write is refused by OPA before the handler runs:
// since #2496 the claim no longer derives control_owner.
func TestTenantGates2428_RevokedGrantRefusedWhileTokenLive(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	s := newStack2428(t, appPool)

	user := seedUser2428(t, adminPool, tenant, authz.RoleAdmin)
	bearer := s.mint(t, tenant, user, authz.RoleAdmin)
	const population = "/v1/audit-periods/" + id2428 + "/populations/" + id2428
	if code, _ := s.do(bearer, http.MethodPost, population); code != http.StatusNoContent {
		t.Fatalf("granted admin: POST %s = %d, want 204", population, code)
	}
	if code, _ := s.do(bearer, http.MethodPost, "/v1/walkthroughs"); code != http.StatusNoContent {
		t.Fatalf("granted admin: POST /v1/walkthroughs = %d, want 204", code)
	}

	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for _, rt := range routes2428 {
		if code, _ := s.do(bearer, rt.method, rt.path); code != http.StatusForbidden {
			t.Errorf("revoked admin, live token: %s %s = %d, want 403", rt.method, rt.path, code)
		}
	}
	code, reached := s.do(bearer, http.MethodPost, "/v1/walkthroughs")
	if reached || code != http.StatusForbidden {
		t.Errorf("revoked admin: POST /v1/walkthroughs = %d reached=%v, want OPA's 403", code, reached)
	}
}
