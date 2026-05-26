// Unit tests for the atlas-github cmd glue, lifting merged coverage
// from 15.1% to 70%+ per slice 301.
//
// Load-bearing functions and the branches each test exercises:
//
//   - mapResult: enum mapping (PASS/FAIL/INCONCLUSIVE/UNSPECIFIED
//     default) — table-driven; covers all four branches of the switch.
//   - resolveCommon: env-var + global flag resolution. Six paths:
//     endpoint via flag, endpoint via env, endpoint missing (error);
//     token via flag, token via env, token missing (error).
//   - newRootCmd: smoke that the root cobra command instantiates,
//     wires all four subcommands (register + run + webhook + scopes),
//     and exposes the persistent flags (--endpoint, --token, --insecure).
//   - newRegisterCmd: PreRunE error path when env vars are missing.
//     RunE error path against an unreachable endpoint (covers the
//     dial-success-RPC-error branch end-to-end).
//   - newRunCmd PreRunE: three error branches —
//     (a) missing --org,
//     (b) missing --environment,
//     (c) both supplied + resolveCommon fails (no endpoint).
//   - newWebhookCmd PreRunE: three error branches —
//     (a) missing --environment,
//     (b) environment set but GITHUB_WEBHOOK_SECRET empty,
//     (c) environment + secret set, but resolveCommon fails.
//   - newScopesCmd: Run path renders the documented scope table from
//     githubauth.DocumentedScopes() to the command's writer.
//   - dialConnectorRegistry: both transport branches —
//     (a) insecure=true returns a non-nil client/conn,
//     (b) insecure=false (TLS) returns a non-nil client/conn.
//     grpc.NewClient is lazy (no dial happens), so this exercises
//     the transport-credential selection without network IO.
//   - authedContext: returns a context bearing the
//     authorization metadata header plus a cancel func that
//     actually cancels.
//   - sdkOpts: returns nil when insecure=false; returns a slice
//     of length 1 (WithInsecure) when insecure=true.
//   - connectorVersion: returns a non-empty string (falls back to
//     "dev" outside a tagged release).
//   - actorID: pins the connector:github:<service>@<version> shape.
//   - buildRepoProtectionRecord: covers the inconclusive + unspecified
//     result branches (the integration test only exercises pass/fail).
//   - buildSCIMRecord: covers (a) deprovisioned → FAIL branch and
//     (b) both optional-field branches (external_id, primary_email).
//   - buildAuditEventRecord: covers the optional `repo` field branch.
//   - doRun: drives the auth-resolve-error path via --use-app PreferAppMode,
//     which returns githubauth.ErrAppNotWired regardless of env state.
//     This is the only doRun branch unit-coverable without a seam
//     refactor that the slice-301 hard rule (mirroring slice 299 + 302)
//     explicitly forbids. The push loop + GitHub HTTP pulls are
//     exercised by the existing integration_test.go suite + the
//     self-host bundle e2e job. The seam refactor is filed as
//     slice 305's exclusive scope.
//
// The global `common` struct is saved + restored per-test via a
// helper to prevent cross-test state pollution (cobra binds the
// flags into package-level globals).
//
// No vendor-prefixed tokens (`ghp_*`, `github_pat_*`) appear in
// fixtures — neutral "test-*" strings only, per CLAUDE.md's hard rule
// and slice 069's GitGuardian invariant.
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubscim"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubwebhook"
)

// resetCommon snapshots the package-global `common` struct and
// restores it on test cleanup. Cobra's flag binding mutates this
// global, so without restoration a single test poisons every test
// that follows.
func resetCommon(t *testing.T) {
	t.Helper()
	saved := common
	t.Cleanup(func() { common = saved })
	common.endpoint = ""
	common.token = ""
	common.insecure = false
}

// TestMapResult covers all four branches of the githubrepo.Result →
// evidencev1.Result enum mapping.
func TestMapResult(t *testing.T) {
	cases := []struct {
		name string
		in   githubrepo.Result
		want evidencev1.Result
	}{
		{"pass", githubrepo.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", githubrepo.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", githubrepo.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unspecified-default", githubrepo.Result("unknown-sentinel"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapResult(tc.in); got != tc.want {
				t.Errorf("mapResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveCommon_EndpointFromFlag exercises the happy path when
// both endpoint and token are set via flags (globals).
func TestResolveCommon_EndpointFromFlag(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:9999"
	common.token = "test-bearer"
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
	if common.endpoint != "127.0.0.1:9999" {
		t.Errorf("endpoint mutated: %q", common.endpoint)
	}
	if common.token != "test-bearer" {
		t.Errorf("token mutated: %q", common.token)
	}
}

// TestResolveCommon_EndpointFromEnv exercises the env-var fallback
// for endpoint.
func TestResolveCommon_EndpointFromEnv(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "env-endpoint:9999")
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-env-token")
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
	if common.endpoint != "env-endpoint:9999" {
		t.Errorf("endpoint: got %q want env-endpoint:9999", common.endpoint)
	}
}

// TestResolveCommon_MissingEndpoint asserts the error path when
// neither flag nor env supply the endpoint.
func TestResolveCommon_MissingEndpoint(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-token")
	err := resolveCommon()
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error = %v; want endpoint mention", err)
	}
}

// TestResolveCommon_TokenFromFlag covers token-via-flag with
// endpoint via env.
func TestResolveCommon_TokenFromFlag(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "127.0.0.1:9999")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	common.token = "test-flag-token"
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
	if common.token != "test-flag-token" {
		t.Errorf("token: got %q", common.token)
	}
}

// TestResolveCommon_TokenFromEnv covers token-via-env with
// endpoint via flag.
func TestResolveCommon_TokenFromEnv(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:9999"
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-env-token-value")
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
	if common.token != "test-env-token-value" {
		t.Errorf("token: got %q want test-env-token-value", common.token)
	}
}

// TestResolveCommon_MissingToken asserts the error path when neither
// flag nor env supply the token.
func TestResolveCommon_MissingToken(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:9999"
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	err := resolveCommon()
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error = %v; want token mention", err)
	}
}

// TestNewRootCmd_HasSubcommands asserts the root command wires all
// four subcommands and the persistent flags.
func TestNewRootCmd_HasSubcommands(t *testing.T) {
	resetCommon(t)
	root := newRootCmd()
	if root == nil {
		t.Fatal("newRootCmd returned nil")
	}
	if root.Use != ConnectorName {
		t.Errorf("Use = %q; want %q", root.Use, ConnectorName)
	}
	if !root.SilenceUsage || !root.SilenceErrors {
		t.Errorf("expected SilenceUsage + SilenceErrors true; got %v / %v",
			root.SilenceUsage, root.SilenceErrors)
	}

	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"register", "run", "webhook", "scopes"} {
		if !names[want] {
			t.Errorf("subcommand %q missing; got %v", want, names)
		}
	}
	for _, want := range []string{"endpoint", "token", "insecure"} {
		if root.PersistentFlags().Lookup(want) == nil {
			t.Errorf("persistent flag %q missing", want)
		}
	}
}

// TestNewRegisterCmd_PreRunErrorOnMissingEnv drives the register
// subcommand's PreRunE error path: with no endpoint set, resolveCommon
// must fail before any dial is attempted.
func TestNewRegisterCmd_PreRunErrorOnMissingEnv(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	reg := newRegisterCmd()
	if reg.Use != "register" {
		t.Errorf("Use = %q; want register", reg.Use)
	}
	err := reg.PreRunE(reg, nil)
	if err == nil {
		t.Fatal("expected PreRunE error when endpoint/token unset")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("err = %v; want endpoint mention", err)
	}
}

// TestNewRegisterCmd_RunEFailsOnUnreachableEndpoint drives the
// register subcommand's full RunE path against an unreachable
// endpoint. dialConnectorRegistry succeeds (grpc.NewClient is lazy)
// but the actual Register RPC fails fast against 127.0.0.1:1. This
// exercises the dial-success-RPC-error branch end-to-end.
func TestNewRegisterCmd_RunEFailsOnUnreachableEndpoint(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	reg := newRegisterCmd()
	err := reg.RunE(reg, nil)
	if err == nil {
		t.Fatal("expected RPC error against unreachable endpoint")
	}
	if !strings.Contains(err.Error(), "register") {
		t.Errorf("err = %v; want register error wrap", err)
	}
}

// TestNewRunCmd_PreRunRejectsMissingOrg covers PreRunE branch (a):
// missing --org.
func TestNewRunCmd_PreRunRejectsMissingOrg(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --org")
	}
	if !strings.Contains(err.Error(), "org") {
		t.Errorf("err = %v; want org mention", err)
	}
}

// TestNewRunCmd_PreRunRejectsMissingEnvironment covers PreRunE branch
// (b): --org set, --environment missing.
func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--org", "example"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --environment")
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Errorf("err = %v; want environment mention", err)
	}
}

// TestNewRunCmd_PreRunResolveCommonFails: all run flags valid but
// neither --endpoint nor SECURITY_ATLAS_ENDPOINT is set, so the
// PreRunE falls through to resolveCommon and errors.
func TestNewRunCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--org", "example", "--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewWebhookCmd_PreRunRejectsMissingEnvironment covers PreRunE
// branch (a): --environment is required.
func TestNewWebhookCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	t.Setenv(EnvWebhookSecret, "test-secret-value")
	cmd := newWebhookCmd()
	if err := cmd.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --environment")
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Errorf("err = %v; want environment mention", err)
	}
}

// TestNewWebhookCmd_PreRunRejectsMissingSecret covers PreRunE branch
// (b): with --environment set but GITHUB_WEBHOOK_SECRET empty, the
// receiver refuses to start (anti-criterion P0).
func TestNewWebhookCmd_PreRunRejectsMissingSecret(t *testing.T) {
	resetCommon(t)
	t.Setenv(EnvWebhookSecret, "")
	cmd := newWebhookCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing webhook secret")
	}
	if !strings.Contains(err.Error(), EnvWebhookSecret) {
		t.Errorf("err = %v; want %s mention", err, EnvWebhookSecret)
	}
}

// TestNewWebhookCmd_PreRunResolveCommonFails covers PreRunE branch
// (c): environment + secret set, but neither --endpoint nor
// SECURITY_ATLAS_ENDPOINT is configured, so the trailing resolveCommon
// errors.
func TestNewWebhookCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv(EnvWebhookSecret, "test-secret-value")
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newWebhookCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewScopesCmd_RendersTable drives the scopes subcommand's Run
// path and asserts the output emits the documented header + scope
// names from githubauth.DocumentedScopes.
func TestNewScopesCmd_RendersTable(t *testing.T) {
	cmd := newScopesCmd()
	if cmd.Use != "scopes" {
		t.Errorf("Use = %q; want scopes", cmd.Use)
	}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Run(cmd, nil)
	out := buf.String()
	if out == "" {
		t.Fatal("scopes produced no output")
	}
	if !strings.Contains(out, "TOKEN_KIND") {
		t.Errorf("missing TOKEN_KIND header: %q", out)
	}
	for _, header := range []string{"NAME", "ACCESS", "GATES"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing %q header: %q", header, out)
		}
	}
	// At least one documented scope name (from githubauth.DocumentedScopes)
	// must render. "Repository: Administration" gates the
	// github.repo_protection.v1 evidence kind.
	if !strings.Contains(out, "Repository: Administration") {
		t.Errorf("scopes output missing Repository: Administration entry: %q", out)
	}
	// Anti-criterion: the documented list must NOT include write/admin
	// scopes. The internal package's TestDocumentedScopes_NoWriteOrDeleteAccess
	// is the source of truth; this is a smoke that the cmd renders the
	// same list (no banned tokens leak into the help text).
	for _, banned := range []string{"admin:org", "delete_repo", "write:"} {
		if strings.Contains(out, banned) {
			t.Errorf("scopes output must not contain banned scope %q: %q", banned, out)
		}
	}
}

// TestDialConnectorRegistry_InsecurePath covers the insecure
// transport branch. grpc.NewClient is lazy — no real dial happens.
func TestDialConnectorRegistry_InsecurePath(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.insecure = true
	client, conn, err := dialConnectorRegistry()
	if err != nil {
		t.Fatalf("dialConnectorRegistry: %v", err)
	}
	if client == nil {
		t.Error("client nil")
	}
	if conn == nil {
		t.Error("conn nil")
	} else {
		_ = conn.Close()
	}
}

// TestDialConnectorRegistry_TLSPath covers the TLS transport branch.
// grpc.NewClient is lazy; the handshake would only happen on the
// first RPC. We close immediately.
func TestDialConnectorRegistry_TLSPath(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.insecure = false
	client, conn, err := dialConnectorRegistry()
	if err != nil {
		t.Fatalf("dialConnectorRegistry: %v", err)
	}
	if client == nil {
		t.Error("client nil")
	}
	if conn == nil {
		t.Error("conn nil")
	} else {
		_ = conn.Close()
	}
}

// TestAuthedContext_HasAuthMetadata asserts the returned context
// carries the Authorization header (Bearer <token>) and the cancel
// func actually cancels.
func TestAuthedContext_HasAuthMetadata(t *testing.T) {
	resetCommon(t)
	common.token = "test-bearer-token"
	ctx, cancel := authedContext(5 * time.Second)
	defer cancel()

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("no outgoing metadata on ctx")
	}
	vals := md.Get(sdk.MetadataAuthorization)
	if len(vals) == 0 {
		t.Fatal("no authorization header on ctx")
	}
	want := sdk.BearerPrefix + "test-bearer-token"
	if vals[0] != want {
		t.Errorf("auth = %q; want %q", vals[0], want)
	}

	// Verify cancel works.
	cancel()
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("ctx not cancelled after cancel()")
	}
}

// TestSDKOpts_SecureReturnsNil: when insecure=false, sdkOpts is nil
// (default TLS path).
func TestSDKOpts_SecureReturnsNil(t *testing.T) {
	resetCommon(t)
	common.insecure = false
	opts := sdkOpts()
	if opts != nil {
		t.Errorf("sdkOpts() = %v; want nil", opts)
	}
}

// TestSDKOpts_InsecureReturnsOpt: when insecure=true, sdkOpts
// returns a non-empty slice carrying the WithInsecure option.
func TestSDKOpts_InsecureReturnsOpt(t *testing.T) {
	resetCommon(t)
	common.insecure = true
	opts := sdkOpts()
	if len(opts) != 1 {
		t.Errorf("sdkOpts() len = %d; want 1", len(opts))
	}
}

// TestConnectorVersion_NonEmpty asserts connectorVersion returns
// a non-empty string. Falls back to "dev" when the binary isn't
// from a tagged release (the usual `go test` case).
func TestConnectorVersion_NonEmpty(t *testing.T) {
	v := connectorVersion()
	if v == "" {
		t.Error("connectorVersion empty")
	}
}

// TestActorID_Shape pins the connector:github:<service>@<version>
// format across all three services this connector exposes.
func TestActorID_Shape(t *testing.T) {
	for _, svc := range []string{"repo", "scim", "webhook"} {
		id := actorID(svc)
		wantPrefix := "connector:github:" + svc + "@"
		if !strings.HasPrefix(id, wantPrefix) {
			t.Errorf("actorID(%q) = %q; want prefix %q", svc, id, wantPrefix)
		}
	}
}

// TestBuildRepoProtectionRecord_AllResults exercises the
// inconclusive + unspecified branches of buildRepoProtectionRecord's
// mapResult call. The integration_test.go suite only covers
// pass/fail; this fills the matrix.
func TestBuildRepoProtectionRecord_AllResults(t *testing.T) {
	cases := []struct {
		name string
		in   githubrepo.Result
		want evidencev1.Result
	}{
		{"inconclusive", githubrepo.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unspecified-default", githubrepo.Result("unknown-sentinel"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := githubrepo.ProtectionState{
				RepoFullName:  "example/repo",
				DefaultBranch: "main",
				Result:        tc.in,
				ObservedAt:    time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
			}
			rec, err := buildRepoProtectionRecord(state, "example", "prod", "scf:TDA-06")
			if err != nil {
				t.Fatalf("buildRepoProtectionRecord: %v", err)
			}
			if rec.GetResult() != tc.want {
				t.Errorf("result = %v; want %v", rec.GetResult(), tc.want)
			}
			if rec.GetEvidenceKind() != "github.repo_protection.v1" {
				t.Errorf("kind = %q; want github.repo_protection.v1", rec.GetEvidenceKind())
			}
		})
	}
}

// TestBuildSCIMRecord_DeprovisionedFail asserts the inactive-user
// branch flips the record result to FAIL so the evaluator can pick
// up stale entitlements.
func TestBuildSCIMRecord_DeprovisionedFail(t *testing.T) {
	u := githubscim.User{
		SCIMUserID: "scim-deactivated",
		UserName:   "alice@example.com",
		Active:     false, // deprovisioned
		Org:        "example",
		ObservedAt: time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
	}
	rec, err := buildSCIMRecord(u, "prod", "scf:IAC-22")
	if err != nil {
		t.Fatalf("buildSCIMRecord: %v", err)
	}
	if rec.GetResult() != evidencev1.Result_RESULT_FAIL {
		t.Errorf("result = %v; want FAIL", rec.GetResult())
	}
}

// TestBuildSCIMRecord_OptionalFields covers the optional-field
// branches: external_id + primary_email render when set, and are
// omitted when empty.
func TestBuildSCIMRecord_OptionalFields(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	t.Run("present", func(t *testing.T) {
		u := githubscim.User{
			SCIMUserID:   "scim-1",
			UserName:     "alice@example.com",
			Active:       true,
			ExternalID:   "ext-alice-123",
			PrimaryEmail: "alice@example.com",
			Org:          "example",
			ObservedAt:   now,
		}
		rec, err := buildSCIMRecord(u, "prod", "scf:IAC-22")
		if err != nil {
			t.Fatalf("buildSCIMRecord: %v", err)
		}
		pl := rec.GetPayload().AsMap()
		if v, ok := pl["external_id"].(string); !ok || v != "ext-alice-123" {
			t.Errorf("external_id = %v; want ext-alice-123", pl["external_id"])
		}
		if v, ok := pl["primary_email"].(string); !ok || v != "alice@example.com" {
			t.Errorf("primary_email = %v; want alice@example.com", pl["primary_email"])
		}
		if rec.GetResult() != evidencev1.Result_RESULT_PASS {
			t.Errorf("result = %v; want PASS", rec.GetResult())
		}
	})
	t.Run("absent", func(t *testing.T) {
		u := githubscim.User{
			SCIMUserID: "scim-2",
			UserName:   "bob@example.com",
			Active:     true,
			Org:        "example",
			ObservedAt: now,
			// ExternalID + PrimaryEmail intentionally empty
		}
		rec, err := buildSCIMRecord(u, "prod", "scf:IAC-22")
		if err != nil {
			t.Fatalf("buildSCIMRecord: %v", err)
		}
		pl := rec.GetPayload().AsMap()
		if _, ok := pl["external_id"]; ok {
			t.Errorf("external_id should be omitted when empty; got: %v", pl)
		}
		if _, ok := pl["primary_email"]; ok {
			t.Errorf("primary_email should be omitted when empty; got: %v", pl)
		}
	})
}

// TestBuildAuditEventRecord_OptionalRepo covers the optional `repo`
// field branch: organization-level webhooks (e.g. member events) have
// no repo, in which case the payload omits the key.
func TestBuildAuditEventRecord_OptionalRepo(t *testing.T) {
	r := &githubwebhook.AuditEventRecord{
		IdempotencyKey: "test-delivery-1",
		EventType:      "member",
		Action:         "added",
		Actor:          "ghost",
		Org:            "example",
		DeliveryID:     "test-delivery-1",
		CreatedAt:      time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
		// Repo intentionally empty (org-level event)
	}
	rec, err := buildAuditEventRecord(r, "prod", "scf:MON-01")
	if err != nil {
		t.Fatalf("buildAuditEventRecord: %v", err)
	}
	pl := rec.GetPayload().AsMap()
	if _, ok := pl["repo"]; ok {
		t.Errorf("repo should be omitted for org-level events; got: %v", pl)
	}
	if rec.GetIdempotencyKey() != "test-delivery-1" {
		t.Errorf("idempotency_key = %q; want test-delivery-1 verbatim (anti-criterion P0)",
			rec.GetIdempotencyKey())
	}
	if rec.GetResult() != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE (event-level)", rec.GetResult())
	}
}

// TestBuildAuditEventRecord_WithRepo asserts the repo-present branch
// renders the field on the payload.
func TestBuildAuditEventRecord_WithRepo(t *testing.T) {
	r := &githubwebhook.AuditEventRecord{
		IdempotencyKey: "test-delivery-2",
		EventType:      "repository",
		Action:         "edited",
		Actor:          "alice",
		Org:            "example",
		Repo:           "example/web",
		DeliveryID:     "test-delivery-2",
		CreatedAt:      time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
	}
	rec, err := buildAuditEventRecord(r, "prod", "scf:MON-01")
	if err != nil {
		t.Fatalf("buildAuditEventRecord: %v", err)
	}
	pl := rec.GetPayload().AsMap()
	if v, ok := pl["repo"].(string); !ok || v != "example/web" {
		t.Errorf("repo = %v; want example/web", pl["repo"])
	}
}

// TestDoRun_AppModeReturnsErrAppNotWired drives doRun's first error
// branch — when --use-app (PreferAppMode) is set, githubauth.Resolve
// returns ErrAppNotWired (slice 044 ships only the contract surface;
// slice 045 wires the JWT signer). This exercises the auth-resolve
// error wrap in doRun without requiring environment-variable scrubbing
// (PreferAppMode short-circuits before any PAT env-var lookup).
//
// This is the only doRun branch unit-coverable without a seam refactor
// that the slice-301 hard rule (mirroring slice 299 + 302) explicitly
// forbids. The post-Resolve push loop is exercised by integration_test.go
// + the self-host bundle e2e job.
func TestDoRun_AppModeReturnsErrAppNotWired(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	// PreferAppMode short-circuits the PAT lookup. With app id +
	// private key supplied, Resolve specifically returns ErrAppNotWired
	// (not "missing env"), exercising the contract-only branch.
	err := doRun(context.Background(), runFlags{
		org:             "example",
		environment:     "prod",
		githubBaseURL:   "https://api.github.invalid",
		useApp:          true,
		appID:           "12345",
		appPrivateKey:   "test-placeholder-key-material", // non-PEM placeholder; PreferAppMode short-circuits before parsing
		repoProtControl: "scf:TDA-06",
		scimUserControl: "scf:IAC-22",
	})
	if err == nil {
		t.Fatal("expected doRun to fail with ErrAppNotWired")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("err = %v; want auth-wrap mention", err)
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Errorf("err = %v; want 'not wired' mention from ErrAppNotWired", err)
	}
}
