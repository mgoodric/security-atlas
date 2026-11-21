//go:build integration

// #2849: a policy-sync API key is refused by every existing route. Over
// HTTP the real router answers 403 on a sample of read, write and admin
// routes and on evidence push, also when a session cookie rides along;
// an ordinary API key keeps getting the 401 /v1 gives every non-JWT
// bearer. Over gRPC, where API keys authenticate, the key is refused with
// PermissionDenied on evidence push and the admin credentials service.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (BYPASSRLS).
package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestPolicySyncKey_RefusedEverywhere(t *testing.T) {
	appURL, adminURL := os.Getenv("DATABASE_URL_APP"), os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		t.Skip("DATABASE_URL_APP and DATABASE_URL required")
	}
	ctx := context.Background()
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

	tenant := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'test-policy-sync-2849')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = admin.Exec(bg, `DELETE FROM me_audit_log WHERE tenant_id = $1`, tenant)
		_, _ = admin.Exec(bg, `DELETE FROM api_keys WHERE tenant_id = $1`, tenant)
		_, _ = admin.Exec(bg, `DELETE FROM tenants WHERE id = $1`, tenant)
	})

	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	keys := apikeystore.NewStore(app, admin, hasher, 0)
	tctx, err := tenancy.WithTenant(ctx, tenant.String())
	if err != nil {
		t.Fatalf("tenant ctx: %v", err)
	}
	issue := func(in apikeystore.IssueInput) string {
		t.Helper()
		_, tok, ierr := keys.Issue(tctx, tenant.String(), in, apikeystore.Actor{})
		if ierr != nil {
			t.Fatalf("issue key: %v", ierr)
		}
		return tok
	}
	syncKey := issue(apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	ordinaryKey := issue(apikeystore.IssueInput{IsAdmin: true})

	srv := New(Config{})
	srv.AttachDB(app)
	srv.AttachAPIKeyStore(keys)
	// Rehydrate the credstore from api_keys, as cmd/atlas does at boot, so
	// both keys authenticate on gRPC.
	if _, err := srv.AttachCredstorePersistence(apikeystore.NewPersister(app, admin, hasher)); err != nil {
		t.Fatalf("attach persistence: %v", err)
	}
	session := srv.IssueTestJWT(t, testjwt.AdminFor(tenant))

	httpSrv := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(httpSrv.Close)
	do := func(method, path, token, cookie string) int {
		t.Helper()
		var body *strings.Reader
		if method == http.MethodGet {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(`{}`)
		}
		req, _ := http.NewRequest(method, httpSrv.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: jwtmw.DefaultCookieName, Value: cookie})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	routes := []struct{ method, path string }{
		// reads
		{http.MethodGet, "/v1/controls"},
		{http.MethodGet, "/v1/policies"},
		{http.MethodGet, "/v1/risks"},
		{http.MethodGet, "/v1/me"},
		// writes
		{http.MethodPost, "/v1/policies"},
		{http.MethodPost, "/v1/risks"},
		{http.MethodPost, "/v1/exceptions"},
		// admin
		{http.MethodGet, "/v1/admin/credentials"},
		{http.MethodPost, "/v1/admin/credentials"},
		{http.MethodGet, "/v1/admin/users"},
		{http.MethodPost, "/v1/admin/install/reset-bootstrap"},
		// evidence push
		{http.MethodPost, "/v1/evidence:push"},
	}
	for _, rt := range routes {
		if got := do(rt.method, rt.path, syncKey, ""); got != http.StatusForbidden {
			t.Errorf("policy-sync key %s %s = %d, want 403", rt.method, rt.path, got)
		}
		if got := do(rt.method, rt.path, syncKey, session); got != http.StatusForbidden {
			t.Errorf("policy-sync key with a session cookie %s %s = %d, want 403", rt.method, rt.path, got)
		}
		if got := do(rt.method, rt.path, ordinaryKey, ""); got != http.StatusUnauthorized {
			t.Errorf("ordinary API key %s %s = %d, want the 401 /v1 gives a non-JWT bearer", rt.method, rt.path, got)
		}
	}

	// gRPC: evidence push and the admin credentials service.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(srv.GRPC.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	push := evidencev1.NewEvidenceIngestServiceClient(conn)
	creds := adminv1.NewAdminCredentialsServiceClient(conn)

	if _, err := push.Push(adminCall(syncKey), &evidencev1.PushRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("gRPC Push with a policy-sync key: err = %v, want PermissionDenied", err)
	}
	if _, err := creds.List(adminCall(syncKey), &adminv1.ListRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("gRPC AdminCredentials.List with a policy-sync key: err = %v, want PermissionDenied", err)
	}
	// The ordinary admin key gets past authentication on the same RPC.
	if _, err := creds.List(adminCall(ordinaryKey), &adminv1.ListRequest{}); status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
		t.Errorf("gRPC AdminCredentials.List with an ordinary admin key: err = %v, want it past authentication", err)
	}
}
