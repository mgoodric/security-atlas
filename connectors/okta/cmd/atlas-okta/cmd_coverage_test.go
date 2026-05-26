// Unit tests for the atlas-okta cmd glue, lifting merged coverage
// from 20.7% to 70%+ per slice 302.
//
// Load-bearing functions and the branches each test exercises:
//
//   - mapPolicyResult: enum mapping (PASS/FAIL/INCONCLUSIVE/UNSPECIFIED
//     default) — table-driven; covers all four branches of the switch.
//   - mapUsersResult: enum mapping (PASS/FAIL/INCONCLUSIVE/UNSPECIFIED
//     default) — table-driven; covers all four branches.
//   - resolveCommon: env-var + global flag resolution. Six paths:
//     endpoint via flag, endpoint via env, endpoint missing (error);
//     token via flag, token via env, token missing (error).
//   - newRootCmd: smoke that the root cobra command instantiates,
//     wires all three subcommands (register + run + scopes), and
//     exposes the persistent flags (--endpoint, --token, --insecure).
//   - newRegisterCmd: PreRunE error path when env vars are missing.
//     RunE error path against an unreachable endpoint (covers the
//     dial-success-RPC-error branch end-to-end).
//   - newRunCmd PreRunE: four error branches —
//     (a) missing --org,
//     (b) missing --environment,
//     (c) missing --okta-base-url,
//     (d) all three valid + resolveCommon fails (no endpoint).
//   - newScopesCmd: Run path renders the documented scope table to
//     the command's writer.
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
//   - actorID: pins the connector:okta:<service>@<version> shape.
//   - buildUserLifecycleRecord: covers the optional-field branches
//     (primary_email, created_at, activated_at, last_login_at,
//     deactivated_at gated on status="DEPROVISIONED").
//   - doRun: drives the auth-resolve-error path (no OKTA_API_TOKEN
//     set) — the only doRun branch unit-coverable without a seam
//     refactor. The push loop + Okta HTTP pulls are exercised by
//     the existing integration_test.go suite + the self-host
//     bundle e2e job; covering them here would require refactoring
//     doRun to inject oktaauth/oktapolicy/oktaapps/oktausers seams,
//     which the slice-302 hard rule (mirroring slice 299) forbids.
//     The seam refactor is filed as the follow-on spillover slice.
//
// The global `common` struct is saved + restored per-test via a
// helper to prevent cross-test state pollution (cobra binds the
// flags into package-level globals).
//
// No vendor-prefixed tokens (Okta `00...` 42-char API tokens, etc.)
// appear in fixtures — neutral "test-*" strings only, per CLAUDE.md's
// hard rule.
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

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktausers"
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

// TestMapPolicyResult covers all four branches of the
// oktapolicy.Result -> evidencev1.Result enum mapping.
func TestMapPolicyResult(t *testing.T) {
	cases := []struct {
		name string
		in   oktapolicy.Result
		want evidencev1.Result
	}{
		{"pass", oktapolicy.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", oktapolicy.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", oktapolicy.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unspecified-default", oktapolicy.Result("unknown-sentinel"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapPolicyResult(tc.in); got != tc.want {
				t.Errorf("mapPolicyResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMapUsersResult covers all four branches of the
// oktausers.Result -> evidencev1.Result enum mapping.
func TestMapUsersResult(t *testing.T) {
	cases := []struct {
		name string
		in   oktausers.Result
		want evidencev1.Result
	}{
		{"pass", oktausers.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", oktausers.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", oktausers.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unspecified-default", oktausers.Result("unknown-sentinel"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapUsersResult(tc.in); got != tc.want {
				t.Errorf("mapUsersResult(%q) = %v; want %v", tc.in, got, tc.want)
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
// three subcommands and the persistent flags.
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
	for _, want := range []string{"register", "run", "scopes"} {
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

// TestNewRunCmd_PreRunRejectsMissingOrg covers the first PreRunE
// guard: --org must be set.
func TestNewRunCmd_PreRunRejectsMissingOrg(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--environment", "prod",
		"--okta-base-url", "https://example.okta.com",
	}); err != nil {
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

// TestNewRunCmd_PreRunRejectsMissingEnvironment covers the second
// PreRunE guard: --environment must be set.
func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--okta-base-url", "https://example.okta.com",
	}); err != nil {
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

// TestNewRunCmd_PreRunRejectsMissingOktaBaseURL covers the third
// PreRunE guard: --okta-base-url must be set.
func TestNewRunCmd_PreRunRejectsMissingOktaBaseURL(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--environment", "prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --okta-base-url")
	}
	if !strings.Contains(err.Error(), "okta-base-url") {
		t.Errorf("err = %v; want okta-base-url mention", err)
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
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--environment", "prod",
		"--okta-base-url", "https://example.okta.com",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewScopesCmd_RendersTable drives the scopes subcommand's Run
// path and asserts the output names every documented scope.
func TestNewScopesCmd_RendersTable(t *testing.T) {
	cmd := newScopesCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Run(cmd, nil)
	out := buf.String()
	if !strings.Contains(out, "TOKEN_KIND") {
		t.Errorf("scopes output missing header; got: %q", out)
	}
	// All four documented scope names must render.
	for _, name := range []string{
		"okta.users.read",
		"okta.groups.read",
		"okta.apps.read",
		"okta.policies.read",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("scopes output missing %q; got: %q", name, out)
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

// TestActorID_Shape pins the connector:okta:<service>@<version>
// format (mirrors the manual + aws connectors' TestActorID_Shape).
func TestActorID_Shape(t *testing.T) {
	for _, svc := range []string{"policy", "apps", "users"} {
		id := actorID(svc)
		wantPrefix := "connector:okta:" + svc + "@"
		if !strings.HasPrefix(id, wantPrefix) {
			t.Errorf("actorID(%q) = %q; want prefix %q", svc, id, wantPrefix)
		}
	}
}

// TestBuildUserLifecycleRecord_AllOptionalFields exercises every
// optional-field branch of buildUserLifecycleRecord (primary_email,
// created_at, activated_at, last_login_at, deactivated_at gated on
// status="DEPROVISIONED"). The DEPROVISIONED status is the only one
// that lets deactivated_at render.
func TestBuildUserLifecycleRecord_AllOptionalFields(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	u := oktausers.Lifecycle{
		UserID:        "u1",
		Login:         "alice@example.com",
		Status:        "DEPROVISIONED",
		MFAEnrolled:   true,
		PrimaryEmail:  "alice@example.com",
		CreatedAt:     now.Add(-72 * time.Hour),
		ActivatedAt:   now.Add(-48 * time.Hour),
		LastLoginAt:   now.Add(-24 * time.Hour),
		DeactivatedAt: now.Add(-1 * time.Hour),
		Result:        oktausers.ResultFail,
		ObservedAt:    now,
	}
	rec, err := buildUserLifecycleRecord(u, "example", "prod", "scf:IAC-22")
	if err != nil {
		t.Fatalf("buildUserLifecycleRecord: %v", err)
	}
	pl := rec.GetPayload().AsMap()
	for _, key := range []string{
		"user_id", "login", "status", "mfa_enrolled",
		"primary_email", "created_at", "activated_at", "last_login_at",
		"deactivated_at",
	} {
		if _, ok := pl[key]; !ok {
			t.Errorf("payload missing key %q; got %v", key, pl)
		}
	}
	if rec.GetResult() != evidencev1.Result_RESULT_FAIL {
		t.Errorf("result = %v; want FAIL", rec.GetResult())
	}
}

// TestBuildUserLifecycleRecord_DeactivatedAtRequiresDeprovisioned
// asserts that deactivated_at is omitted unless status=DEPROVISIONED,
// even when the timestamp is present. This pins the second clause of
// the gated branch.
func TestBuildUserLifecycleRecord_DeactivatedAtRequiresDeprovisioned(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	u := oktausers.Lifecycle{
		UserID:        "u2",
		Login:         "bob@example.com",
		Status:        "ACTIVE", // not DEPROVISIONED
		DeactivatedAt: now.Add(-1 * time.Hour),
		Result:        oktausers.ResultPass,
		ObservedAt:    now,
	}
	rec, err := buildUserLifecycleRecord(u, "example", "prod", "scf:IAC-22")
	if err != nil {
		t.Fatalf("buildUserLifecycleRecord: %v", err)
	}
	pl := rec.GetPayload().AsMap()
	if _, ok := pl["deactivated_at"]; ok {
		t.Errorf("deactivated_at should be omitted for status=ACTIVE; got: %v", pl)
	}
}

// TestAsAny_EmptyReturnsNil asserts the early-return guard.
func TestAsAny_EmptyReturnsNil(t *testing.T) {
	if asAny(nil) != nil {
		t.Error("asAny(nil) should be nil")
	}
	if asAny([]string{}) != nil {
		t.Error("asAny([]) should be nil")
	}
}

// TestAsAny_PopulatedRoundTrips asserts the populated path preserves
// values + order.
func TestAsAny_PopulatedRoundTrips(t *testing.T) {
	got := asAny([]string{"a", "b", "c"})
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i] != want {
			t.Errorf("[%d] = %v; want %q", i, got[i], want)
		}
	}
}

// TestDoRun_FailsOnMissingOktaToken drives doRun's first error
// branch — oktaauth.Resolve fails when OKTA_API_TOKEN is unset and
// the runFlags.token field is empty. This is the only doRun branch
// unit-coverable without a seam refactor that the slice-302 hard
// rule (mirroring slice 299) explicitly forbids. The rest of doRun
// (oktapolicy/oktaapps/oktausers Pull + push loop) is exercised by
// integration_test.go and the self-host bundle e2e job.
func TestDoRun_FailsOnMissingOktaToken(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("OKTA_API_TOKEN", "")

	err := doRun(context.Background(), runFlags{
		org:              "example",
		environment:      "prod",
		oktaBaseURL:      "https://example.okta.com",
		mfaPolicyControl: "scf:IAC-06",
		appAssignControl: "scf:IAC-21",
		userLifeControl:  "scf:IAC-22",
	})
	if err == nil {
		t.Fatal("expected doRun to fail when OKTA_API_TOKEN is unset")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("err = %v; want auth-wrap mention", err)
	}
}
