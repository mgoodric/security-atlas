//go:build integration

// #1915: a deactivated user loses their API keys and sessions. Two
// deactivation paths (SCIM deprovision, admin revoke-membership) each revoke
// the user's keys (in api_keys and in credstore memory) and sessions; and,
// as defence in depth, a key or session whose owner can no longer sign in is
// refused even when nothing revoked it.
//
// Each test drives a real bearer through httpAuthMiddlewareWithExemptions,
// the production /v1 middleware, so "refused" means a 401 on the wire. Two
// keys cover both auth paths: one rehydrated into credstore memory at boot,
// one issued after the boot so only the DB-backed apikeystore knows it.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (BYPASSRLS).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/admin"
	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/scim"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type deactivationFixture struct {
	admin, app *pgxpool.Pool
	tenant     uuid.UUID
	user       uuid.UUID
	creds      *credstore.Store
	// memToken is user-owned and lives in credstore memory (rehydrated
	// from api_keys at boot). dbToken is user-owned and issued after the
	// boot, so only the apikeystore fallback authenticates it. keylessToken
	// has no owner and must survive every deactivation.
	memToken, dbToken, keylessToken string
	session                         string
	auth                            http.Handler
}

func newDeactivationFixture(t *testing.T) *deactivationFixture {
	t.Helper()
	appURL, adminURL := os.Getenv("DATABASE_URL_APP"), os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		t.Skip("DATABASE_URL_APP and DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(app.Close)
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(admin.Close)

	f := &deactivationFixture{admin: admin, app: app, tenant: uuid.New(), user: uuid.New()}
	bg := context.Background()
	if _, err := admin.Exec(bg, `INSERT INTO tenants (id, name) VALUES ($1, $2)`, f.tenant, "test-deactivation"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"api_keys", "sessions", "user_roles", "me_audit_log", "scim_audit_log", "users"} {
			_, _ = admin.Exec(bg, `DELETE FROM `+table+` WHERE tenant_id = $1`, f.tenant)
		}
		_, _ = admin.Exec(bg, `DELETE FROM tenants WHERE id = $1`, f.tenant)
	})
	f.user = f.seedUser(t, "test-deactivated@example.com")

	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	keys := apikeystore.NewStore(app, admin, hasher, 0)
	tctx := f.tenantCtx(t)
	issue := func(owner *uuid.UUID) string {
		_, tok, ierr := keys.Issue(tctx, f.tenant.String(), apikeystore.IssueInput{IssuedBy: owner})
		if ierr != nil {
			t.Fatalf("issue key: %v", ierr)
		}
		return tok
	}
	f.memToken = issue(&f.user)
	f.keylessToken = issue(nil)

	// Boot: rehydrate the credstore from api_keys, then gate it on the
	// owner's status the way Server.AttachAPIKeyStore does.
	f.creds = credstore.New(0)
	if _, err := f.creds.AttachPersistence(apikeystore.NewPersister(app, admin, hasher)); err != nil {
		t.Fatalf("attach persistence: %v", err)
	}
	f.creds.SetUserGate(keys)
	f.dbToken = issue(&f.user)

	f.session = "sess_" + uuid.NewString()
	if _, err := admin.Exec(bg,
		`INSERT INTO sessions (id, tenant_id, user_id, expires_at) VALUES ($1, $2, $3, now() + interval '7 days')`,
		f.session, f.tenant, f.user); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	f.auth = httpAuthMiddlewareWithExemptions(f.creds, keys)(ok)

	// Precondition: everything works while the user is active.
	f.requireStatus(t, http.StatusOK, f.memToken, f.dbToken, f.keylessToken)
	if err := f.readSession(t); err != nil {
		t.Fatalf("precondition: session read = %v", err)
	}
	return f
}

// otherReplica boots a second credstore over the same api_keys, the way
// another replica would, with the given rotation grace.
func (f *deactivationFixture) otherReplica(t *testing.T, grace time.Duration) *credstore.Store {
	t.Helper()
	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	r := credstore.New(grace)
	if _, err := r.AttachPersistence(apikeystore.NewPersister(f.app, f.admin, hasher)); err != nil {
		t.Fatalf("other replica attach persistence: %v", err)
	}
	return r
}

func (f *deactivationFixture) seedUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, $3, 'active')`,
		id, f.tenant, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func (f *deactivationFixture) tenantCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), f.tenant.String())
	if err != nil {
		t.Fatalf("tenant ctx: %v", err)
	}
	return ctx
}

func (f *deactivationFixture) requireStatus(t *testing.T, want int, tokens ...string) {
	t.Helper()
	for i, tok := range tokens {
		req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		f.auth.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("token %d: status = %d; want %d (body %q)", i, rec.Code, want, rec.Body.String())
		}
	}
}

func (f *deactivationFixture) readSession(t *testing.T) error {
	t.Helper()
	_, err := sessions.NewStore(f.app, 0).Read(f.tenantCtx(t), f.tenant, f.session)
	return err
}

func (f *deactivationFixture) setUser(t *testing.T, status string, active bool) {
	t.Helper()
	if _, err := f.admin.Exec(context.Background(),
		`UPDATE users SET status = $2, active = $3 WHERE id = $1`, f.user, status, active); err != nil {
		t.Fatalf("set user state: %v", err)
	}
}

// requireRevoked asserts the user's keys and session are revoked, durably
// and in memory, and not merely refused by the status gate: with the user
// re-activated behind the revocation's back, they still fail.
func (f *deactivationFixture) requireRevoked(t *testing.T) {
	t.Helper()
	f.requireStatus(t, http.StatusUnauthorized, f.memToken, f.dbToken)
	f.requireStatus(t, http.StatusOK, f.keylessToken)
	if err := f.readSession(t); !errors.Is(err, sessions.ErrRevoked) {
		t.Fatalf("session read = %v; want ErrRevoked", err)
	}

	var liveKeys, liveSessions int
	bg := context.Background()
	if err := f.admin.QueryRow(bg,
		`SELECT count(*) FROM api_keys WHERE tenant_id = $1 AND issued_by = $2 AND revoked_at IS NULL`,
		f.tenant, f.user).Scan(&liveKeys); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if err := f.admin.QueryRow(bg,
		`SELECT count(*) FROM sessions WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		f.tenant, f.user).Scan(&liveSessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if liveKeys != 0 || liveSessions != 0 {
		t.Fatalf("live keys = %d, live sessions = %d; want 0, 0", liveKeys, liveSessions)
	}

	f.setUser(t, "active", true)
	f.requireStatus(t, http.StatusUnauthorized, f.memToken, f.dbToken)
	if err := f.readSession(t); !errors.Is(err, sessions.ErrRevoked) {
		t.Fatalf("re-activated user's session read = %v; want ErrRevoked", err)
	}
}

func TestDeactivation_SCIMDeprovisionRevokesKeysAndSessions(t *testing.T) {
	f := newDeactivationFixture(t)
	store := scim.NewStore(f.app).WithCredentialRevoker(f.creds)
	if _, err := store.Patch(f.tenantCtx(t), uuid.New(), f.tenant.String(), f.user, []scim.PatchOperation{
		{Op: "replace", Path: "active", Value: []byte(`false`)},
	}); err != nil {
		t.Fatalf("deprovision: %v", err)
	}
	f.requireRevoked(t)
}

// #1947: the deprovision runs on another replica, so this replica's
// in-memory record of the key is never revoked. After a SCIM reprovision
// re-activates the user, the durable revoked_at still refuses the key here.
func TestDeactivation_RevokedKeyStaysDeadOnOtherReplicaAfterReprovision(t *testing.T) {
	f := newDeactivationFixture(t)
	otherReplica := credstore.New(0)
	store := scim.NewStore(f.app).WithCredentialRevoker(otherReplica)
	patch := func(active string) {
		t.Helper()
		if _, err := store.Patch(f.tenantCtx(t), uuid.New(), f.tenant.String(), f.user, []scim.PatchOperation{
			{Op: "replace", Path: "active", Value: []byte(active)},
		}); err != nil {
			t.Fatalf("patch active=%s: %v", active, err)
		}
	}
	patch(`false`)
	patch(`true`)

	var status string
	var active bool
	if err := f.admin.QueryRow(context.Background(),
		`SELECT status, active FROM users WHERE id = $1`, f.user).Scan(&status, &active); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if status != "active" || !active {
		t.Fatalf("precondition: user status=%q active=%v; want reprovisioned", status, active)
	}
	f.requireStatus(t, http.StatusUnauthorized, f.memToken, f.dbToken)
	f.requireStatus(t, http.StatusOK, f.keylessToken)
}

// #1964: a machine key (no user, rehydrated into every replica's memory at
// boot) revoked on replica A is refused on replica B from the next request,
// although B's in-memory record of it was never revoked.
func TestDeactivation_RevokedMachineKeyRefusedOnOtherReplica(t *testing.T) {
	f := newDeactivationFixture(t)
	replicaA := f.otherReplica(t, 0)
	machine, err := replicaA.Authenticate(f.keylessToken)
	if err != nil {
		t.Fatalf("replica A authenticate machine key: %v", err)
	}
	if credstore.OwnedByUser(machine) {
		t.Fatalf("precondition: key %s is user-owned; want a machine key", machine.ID)
	}
	if err := replicaA.Revoke(machine.ID); err != nil {
		t.Fatalf("replica A revoke: %v", err)
	}

	// f.auth is replica B: f.creds rehydrated the key at boot and nothing
	// on B revoked it.
	f.requireStatus(t, http.StatusUnauthorized, f.keylessToken)
	f.requireStatus(t, http.StatusOK, f.memToken, f.dbToken)
}

// #1995: a machine key rotated on replica A keeps working on replica B
// through the grace period and is refused there once it ends, although B's
// in-memory record of the predecessor carries no grace deadline.
func TestDeactivation_RotatedKeyRefusedOnOtherReplicaAfterGrace(t *testing.T) {
	f := newDeactivationFixture(t)
	const grace = 2 * time.Second
	replicaA := f.otherReplica(t, grace)
	machine, err := replicaA.Authenticate(f.keylessToken)
	if err != nil {
		t.Fatalf("replica A authenticate machine key: %v", err)
	}
	_, successor, deadline, err := replicaA.Rotate(machine.ID)
	if err != nil {
		t.Fatalf("replica A rotate: %v", err)
	}

	// f.auth is replica B. Within the grace period both bearers work.
	f.requireStatus(t, http.StatusOK, f.keylessToken, successor)
	if time.Now().After(deadline) {
		t.Fatalf("grace period ended before the within-grace check; raise grace")
	}

	time.Sleep(time.Until(deadline) + 100*time.Millisecond)
	f.requireStatus(t, http.StatusUnauthorized, f.keylessToken)
	f.requireStatus(t, http.StatusOK, successor, f.memToken, f.dbToken)
}

// #1995: a key whose api_keys.expires_at has passed is refused on a replica
// whose in-memory record still holds the old (here: no) expiry.
func TestDeactivation_ExpiredKeyRefusedOnOtherReplica(t *testing.T) {
	f := newDeactivationFixture(t)
	machine, err := f.otherReplica(t, 0).Authenticate(f.keylessToken)
	if err != nil {
		t.Fatalf("authenticate machine key: %v", err)
	}
	kid, ok := strings.CutPrefix(machine.ID, "key_")
	if !ok {
		t.Fatalf("credential id %q lacks key_ prefix", machine.ID)
	}
	if _, err := f.admin.Exec(context.Background(),
		`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE tenant_id = $1 AND id = $2`,
		f.tenant, kid); err != nil {
		t.Fatalf("expire key: %v", err)
	}

	f.requireStatus(t, http.StatusUnauthorized, f.keylessToken)
	f.requireStatus(t, http.StatusOK, f.memToken, f.dbToken)
}

// #1947: a key an admin mints through POST /v1/admin/credentials or gRPC
// AdminCredentialsService.Issue is a machine key. The admin is recorded in
// api_keys.minted_by for attribution only, and disabling that admin leaves
// the key working; an operator revokes it deliberately.
func TestDeactivation_AdminMintedKeysOutliveTheMinter(t *testing.T) {
	f := newDeactivationFixture(t)
	minter := credstore.Credential{ID: "jwt:test", TenantID: f.tenant.String(), UserID: "user:" + f.user.String(), IsAdmin: true}

	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	keys := apikeystore.NewStore(f.app, f.admin, hasher, 0)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authctx.WithCredential(req.Context(), minter)))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/credentials", admincreds.New(keys).Issue)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("HTTP issue status = %d; body %q", rec.Code, rec.Body.String())
	}
	var httpKey admincreds.IssueResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &httpKey); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	grpcResp, err := admin.New(f.creds).Issue(authctx.WithCredential(context.Background(), minter),
		&adminv1.IssueRequest{TenantId: f.tenant.String()})
	if err != nil {
		t.Fatalf("gRPC issue: %v", err)
	}

	for name, id := range map[string]string{"http": httpKey.ID, "grpc": grpcResp.GetHandle().GetId()} {
		var mintedBy, issuedBy *uuid.UUID
		if err := f.admin.QueryRow(context.Background(),
			`SELECT minted_by, issued_by FROM api_keys WHERE id = $1`, strings.TrimPrefix(id, "key_")).Scan(&mintedBy, &issuedBy); err != nil {
			t.Fatalf("%s key row: %v", name, err)
		}
		if mintedBy == nil || *mintedBy != f.user {
			t.Errorf("%s key minted_by = %v; want %s", name, mintedBy, f.user)
		}
		if issuedBy != nil {
			t.Errorf("%s key issued_by = %s; want NULL (machine key, not the minter's)", name, *issuedBy)
		}
	}

	store := scim.NewStore(f.app).WithCredentialRevoker(f.creds)
	if _, err := store.Patch(f.tenantCtx(t), uuid.New(), f.tenant.String(), f.user, []scim.PatchOperation{
		{Op: "replace", Path: "active", Value: []byte(`false`)},
	}); err != nil {
		t.Fatalf("deprovision minter: %v", err)
	}
	f.requireStatus(t, http.StatusUnauthorized, f.memToken, f.dbToken)
	f.requireStatus(t, http.StatusOK, httpKey.BearerToken, grpcResp.GetBearerToken())
}

func TestDeactivation_SCIMDeleteRevokesKeysAndSessions(t *testing.T) {
	f := newDeactivationFixture(t)
	store := scim.NewStore(f.app).WithCredentialRevoker(f.creds)
	if err := store.Delete(f.tenantCtx(t), uuid.New(), f.tenant.String(), f.user); err != nil {
		t.Fatalf("delete: %v", err)
	}
	f.requireRevoked(t)
}

func TestDeactivation_AdminRevokeMembershipRevokesKeysAndSessions(t *testing.T) {
	f := newDeactivationFixture(t)
	actor := f.seedUser(t, "test-deactivation-admin@example.com")
	h := adminusers.New(f.app).SetAuthPool(f.admin).SetCredentialRevoker(f.creds)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "jwt:test", TenantID: f.tenant.String(), UserID: actor.String(), IsAdmin: true,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/users/revoke", h.Revoke)

	body, _ := json.Marshal(adminusers.RevokeRequest{
		UserID: f.user.String(), TenantID: f.tenant.String(), RemoveMembership: true,
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/users/revoke", bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d; body %q", rec.Code, rec.Body.String())
	}
	f.requireRevoked(t)
}

// Defence in depth: a user disabled by any write that did NOT revoke their
// credentials is still refused on both columns CanSignIn reads.
func TestDeactivation_DisabledOwnerRefusedWithoutRevocation(t *testing.T) {
	for name, state := range map[string]struct {
		status string
		active bool
	}{
		"status disabled": {"disabled", true},
		"scim inactive":   {"active", false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeactivationFixture(t)
			f.setUser(t, state.status, state.active)
			f.requireStatus(t, http.StatusUnauthorized, f.memToken, f.dbToken)
			f.requireStatus(t, http.StatusOK, f.keylessToken)
			if err := f.readSession(t); !errors.Is(err, sessions.ErrRevoked) {
				t.Fatalf("session read = %v; want ErrRevoked", err)
			}
			// Nothing was revoked: re-activating restores access.
			f.setUser(t, "active", true)
			f.requireStatus(t, http.StatusOK, f.memToken, f.dbToken)
			if err := f.readSession(t); err != nil {
				t.Fatalf("re-activated session read = %v", err)
			}
		})
	}
}
