// Unit tests for the atlas-osquery cmd glue, lifting merged coverage
// from 28.2% to 70%+ per slice 303.
//
// Load-bearing functions and the branches each test exercises:
//
//   - mapResult: enum mapping (PASS/FAIL/INCONCLUSIVE/UNSPECIFIED
//     default) — table-driven; covers all four branches of the
//     switch.
//   - inferDataClassification: MDM-enrolled → "restricted";
//     un-enrolled → "unknown". Both branches.
//   - resolveCommon: env-var + global flag resolution. Six paths:
//     endpoint via flag, endpoint via env, endpoint missing (error);
//     token via flag, token via env, token missing (error).
//   - newRootCmd: smoke that the root cobra command instantiates,
//     wires all three subcommands (register + run + scopes), and
//     exposes the persistent flags (--endpoint, --token, --insecure).
//   - newRegisterCmd: PreRunE error path when env vars are missing;
//     RunE failure against an unreachable endpoint — drives
//     resolveCommon + dialConnectorRegistry + the Register RPC.
//   - newRunCmd PreRunE: five error branches —
//     (a) missing --org,
//     (b) missing --environment,
//     (c) --mode=fleet missing --fleet-base-url,
//     (d) --mode=local missing --osqueryd-socket,
//     (e) --mode=<invalid>.
//   - newScopesCmd: Run writes a tab-separated header + at least one
//     row to the command's OutOrStdout.
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
//   - connectorVersion: non-empty fallback to "dev".
//   - actorID: connector:osquery:<service>@<version> shape.
//   - doRun: --mode=local short-circuits to ErrLocalSocketNotWired
//     (already covered by integration_test.go); additional doRun
//     entry-branch coverage via an already-cancelled context driving
//     the fleet pull to fail-fast.
//
// The global `common` struct is saved + restored per-test via a
// helper to prevent cross-test state pollution (cobra binds the
// flags into package-level globals).
//
// No vendor-prefixed tokens appear in fixtures — neutral "test-*"
// strings only, per CLAUDE.md's hard rule.
package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryposture"
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

// TestMapResult covers all four branches of the Result → evidencev1.Result
// enum mapping.
func TestMapResult(t *testing.T) {
	cases := []struct {
		name string
		in   osqueryposture.Result
		want evidencev1.Result
	}{
		{"pass", osqueryposture.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", osqueryposture.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", osqueryposture.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unspecified-default", osqueryposture.Result("unknown-sentinel"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapResult(tc.in); got != tc.want {
				t.Errorf("mapResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestInferDataClassification covers both branches of the per-host
// classification ladder.
func TestInferDataClassification(t *testing.T) {
	managed := osqueryposture.HostPosture{MDMEnrolled: true}
	if got := inferDataClassification(managed); got != "restricted" {
		t.Errorf("MDM-enrolled = %q; want restricted", got)
	}
	unmanaged := osqueryposture.HostPosture{MDMEnrolled: false}
	if got := inferDataClassification(unmanaged); got != "unknown" {
		t.Errorf("un-enrolled = %q; want unknown", got)
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

// TestNewRunCmd_PreRunRejectsMissingOrg covers PreRunE branch (a):
// missing --org.
func TestNewRunCmd_PreRunRejectsMissingOrg(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod", "--fleet-base-url", "https://fleet.example.com"}); err != nil {
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
	if err := cmd.ParseFlags([]string{"--org", "example", "--fleet-base-url", "https://fleet.example.com"}); err != nil {
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

// TestNewRunCmd_PreRunFleetModeMissingBaseURL covers PreRunE branch
// (c): --mode=fleet (default) requires --fleet-base-url.
func TestNewRunCmd_PreRunFleetModeMissingBaseURL(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--org", "example", "--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --fleet-base-url")
	}
	if !strings.Contains(err.Error(), "fleet-base-url") {
		t.Errorf("err = %v; want fleet-base-url mention", err)
	}
}

// TestNewRunCmd_PreRunLocalModeMissingSocket covers PreRunE branch
// (d): --mode=local with --osqueryd-socket cleared.
func TestNewRunCmd_PreRunLocalModeMissingSocket(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--environment", "prod",
		"--mode", "local",
		"--osqueryd-socket", "",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --osqueryd-socket in local mode")
	}
	if !strings.Contains(err.Error(), "osqueryd-socket") {
		t.Errorf("err = %v; want osqueryd-socket mention", err)
	}
}

// TestNewRunCmd_PreRunInvalidMode covers PreRunE branch (e): unknown
// --mode value.
func TestNewRunCmd_PreRunInvalidMode(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--environment", "prod",
		"--mode", "bogus",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid --mode")
	}
	if !strings.Contains(err.Error(), "mode") {
		t.Errorf("err = %v; want mode mention", err)
	}
}

// TestNewRunCmd_PreRunResolveCommonFails: all run flags valid but
// neither --endpoint nor SECURITY_ATLAS_ENDPOINT is set, so the
// PreRunE falls through to resolveCommon and errors. This exercises
// the happy-flag-validation path through to the common-resolve step.
func TestNewRunCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--org", "example",
		"--environment", "prod",
		"--fleet-base-url", "https://fleet.example.com",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewScopesCmd_PrintsTabularRoster runs the scopes subcommand and
// confirms it emits the expected tabwriter header plus at least one
// row from osqueryauth.DocumentedScopes().
func TestNewScopesCmd_PrintsTabularRoster(t *testing.T) {
	resetCommon(t)
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
	if !strings.Contains(out, "NAME") {
		t.Errorf("missing NAME header: %q", out)
	}
	if !strings.Contains(out, "ACCESS") {
		t.Errorf("missing ACCESS header: %q", out)
	}
	if !strings.Contains(out, "GATES") {
		t.Errorf("missing GATES header: %q", out)
	}
	// At least one data row beyond the header.
	if strings.Count(out, "\n") < 2 {
		t.Errorf("expected header + at least one data row; got %q", out)
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

// TestActorID_Shape pins the connector:osquery:<service>@<version>
// format from the cross-connector convention.
func TestActorID_Shape(t *testing.T) {
	id := actorID("posture")
	if !strings.HasPrefix(id, "connector:osquery:posture@") {
		t.Errorf("actorID = %q; want prefix connector:osquery:posture@", id)
	}
}

// TestDoRun_FleetMode_FailsOnUnreachableFleet drives doRun's fleet
// branch through to the fleet pull, which fails against an immediately
// closed httptest server. Exercises the auth-resolve + sdk-client
// construction + fleet-client construction + first pull error wrap.
func TestDoRun_FleetMode_FailsOnUnreachableFleet(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	// Spin up a fleet server then close it immediately to make pulls
	// fail fast without a real network reach.
	srv := httptest.NewServer(nil)
	srv.Close()

	err := doRun(context.Background(), runFlags{
		mode:           "fleet",
		org:            "example",
		environment:    "prod",
		fleetBaseURL:   srv.URL,
		token:          "fleet-test",
		hostPostureCtl: "scf:END-04",
	})
	if err == nil {
		t.Fatal("expected doRun fleet-pull error against closed server")
	}
	if !strings.Contains(err.Error(), "fleet pull") {
		t.Errorf("err = %v; want fleet pull wrap", err)
	}
}

// TestDoRun_InvalidMode covers doRun's unreachable-default branch.
// The PreRunE guard would normally reject an invalid mode, but
// doRun is exported via doRun(ctx, runFlags{...}) and the test
// confirms the in-function default branch returns the documented
// "unreachable: invalid mode" error if it's ever called directly
// with a bad mode (defense in depth).
//
// The fleet token is supplied via runFlags.token so auth.Resolve
// succeeds and the switch statement actually evaluates.
func TestDoRun_InvalidMode(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	err := doRun(context.Background(), runFlags{
		mode:           "bogus",
		org:            "example",
		environment:    "prod",
		token:          "fleet-test",
		hostPostureCtl: "scf:END-04",
	})
	if err == nil {
		t.Fatal("expected unreachable-mode error from doRun")
	}
	if !strings.Contains(err.Error(), "unreachable") && !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("err = %v; want unreachable/invalid-mode mention", err)
	}
}

// TestDoRun_AuthResolveFails covers doRun's first error wrap when
// fleet mode is selected but no token is supplied via flag or env.
// This exercises the osqueryauth.Resolve error wrap branch.
func TestDoRun_AuthResolveFails(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	// Ensure no fleet token leaks in from the host env.
	t.Setenv("FLEET_API_TOKEN", "")
	err := doRun(context.Background(), runFlags{
		mode:           "fleet",
		org:            "example",
		environment:    "prod",
		fleetBaseURL:   "https://fleet.example.invalid",
		hostPostureCtl: "scf:END-04",
	})
	if err == nil {
		t.Fatal("expected auth.Resolve error from doRun")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("err = %v; want auth: prefix from doRun's wrap", err)
	}
}

// TestBuildHostPostureRecord_HostnameFallback covers the
// hostname-empty branch: when Fleet reports a host with no hostname,
// the build function falls back to host_uuid so the record stays
// schema-valid (host_uuid + hostname both required). This pins the
// AC-2 schema-conformance behavior.
func TestBuildHostPostureRecord_HostnameFallback(t *testing.T) {
	state := osqueryposture.HostPosture{
		HostUUID:   "uuid-no-hostname",
		Hostname:   "", // intentionally blank
		Platform:   "darwin",
		ObservedAt: time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
	}
	rec, err := buildHostPostureRecord(state, "example", "prod", "scf:END-04")
	if err != nil {
		t.Fatalf("buildHostPostureRecord: %v", err)
	}
	payload := rec.GetPayload().AsMap()
	gotHostname, _ := payload["hostname"].(string)
	if gotHostname != "uuid-no-hostname" {
		t.Errorf("hostname fallback = %q; want uuid-no-hostname", gotHostname)
	}
	// host_uuid still present and intact
	gotUUID, _ := payload["host_uuid"].(string)
	if gotUUID != "uuid-no-hostname" {
		t.Errorf("host_uuid = %q; want uuid-no-hostname", gotUUID)
	}
}
