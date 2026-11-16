//go:build integration

// #2121: first-login recovery end to end on a single-tenant install. The
// operator holds only ATLAS_BOOTSTRAP_TOKEN (an admin API key, not a JWT)
// and a user has already signed in. /v1 accepts only JWTs, so the HTTP
// reset-bootstrap endpoint refuses that token with 401; the gRPC
// AdminCredentialsService.ResetBootstrap, where API keys authenticate,
// performs the reset against the real platform_status row.
//
// The single-tenant gate counts every row in `tenants`, so the test
// truncates tenants (as internal/platform's TenantCount test does) and
// seeds exactly one. Requires DATABASE_URL_APP (atlas_app, RLS) and
// DATABASE_URL (BYPASSRLS).
package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/platform"
)

const recoveryEnvToken = "FAKE_ATLAS_BOOTSTRAP_TOKEN_for_2121_not_a_secret"

func resetInstallState(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `TRUNCATE TABLE tenants, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tenants: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE platform_status SET first_signin_at = NULL, bootstrap_token_consumed_at = NULL`); err != nil {
		t.Fatalf("reset platform_status: %v", err)
	}
}

func adminCall(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func TestFirstLoginRecovery_SingleTenantEndToEnd(t *testing.T) {
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

	resetInstallState(t, admin)
	t.Cleanup(func() { resetInstallState(t, admin) })
	tenant := uuid.New()
	if _, err := admin.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, 'Default Tenant', TRUE)`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	status_ := platform.NewStatus(app, admin)
	// A user has signed in, so the bootstrap token file is gone and the
	// reset needs --force.
	if _, err := status_.MarkFirstSignin(ctx, time.Now()); err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}

	// The server as cmd/atlas wires it for this path.
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	srv := New(Config{})
	srv.AttachDB(app)
	srv.AttachJWTValidator(tokensign.New(ks), nil, "https://atlas.test", "https://atlas.test")
	srv.AttachPlatformResetter(status_)
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token")
	srv.AttachBootstrapTokenPath(tokenPath)
	if _, err := srv.IssueBootstrapFixedAdminCredential(tenant.String(), recoveryEnvToken); err != nil {
		t.Fatalf("IssueBootstrapFixedAdminCredential: %v", err)
	}

	// The reported failure: over HTTP the bootstrap token is a 401.
	httpSrv := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(httpSrv.Close)
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/admin/install/reset-bootstrap", strings.NewReader(`{"force":true}`))
	req.Header.Set("Authorization", "Bearer "+recoveryEnvToken)
	req.Header.Set("Content-Type", "application/json")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST reset-bootstrap: %v", err)
	}
	_ = httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("HTTP reset with the bootstrap API key = %d; /v1 is JWT-only, want 401", httpResp.StatusCode)
	}

	// The recovery path: gRPC, with the same token.
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
	client := adminv1.NewAdminCredentialsServiceClient(conn)

	// Without --force the foot-gun gate refuses, and the marker stays.
	if _, err := client.ResetBootstrap(adminCall(recoveryEnvToken), &adminv1.ResetBootstrapRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("reset without force: err = %v; want FailedPrecondition", err)
	}
	if first, _ := status_.IsFirstInstall(ctx); first {
		t.Fatalf("a refused reset cleared first_signin_at")
	}

	resp, err := client.ResetBootstrap(adminCall(recoveryEnvToken), &adminv1.ResetBootstrapRequest{Force: true})
	if err != nil {
		t.Fatalf("ResetBootstrap: %v", err)
	}
	if resp.GetTenantId() != tenant.String() || !resp.GetFileWritten() {
		t.Fatalf("response = %v; want tenant %s and the file written", resp, tenant)
	}
	if first, err := status_.IsFirstInstall(ctx); err != nil || !first {
		t.Fatalf("IsFirstInstall after reset = %v, %v; want true", first, err)
	}
	file, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read bootstrap-token file: %v", err)
	}
	if strings.TrimSpace(string(file)) != resp.GetBearerToken() {
		t.Fatalf("bootstrap-token file does not hold the returned bearer")
	}

	// The recovered key is a live admin key for the tenant: it lists the
	// tenant's keys, and its own handle is among them.
	list, err := client.List(adminCall(resp.GetBearerToken()), &adminv1.ListRequest{})
	if err != nil {
		t.Fatalf("List with the recovered key: %v", err)
	}
	found := false
	for _, h := range list.GetHandles() {
		found = found || h.GetId() == resp.GetCredentialId()
	}
	if !found {
		t.Fatalf("recovered key %s not listed for its tenant", resp.GetCredentialId())
	}
	cred, err := srv.credStore.Authenticate(resp.GetBearerToken())
	if err != nil || !cred.IsAdmin {
		t.Fatalf("recovered key = %+v, %v; want an admin key", cred, err)
	}

	// A second tenant makes the install multi-tenant: an admin key may no
	// longer reset over either transport.
	if _, err := admin.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'second')`, uuid.New()); err != nil {
		t.Fatalf("seed second tenant: %v", err)
	}
	if _, err := client.ResetBootstrap(adminCall(recoveryEnvToken), &adminv1.ResetBootstrapRequest{Force: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("reset on a multi-tenant install: err = %v; want PermissionDenied", err)
	}
}
