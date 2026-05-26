// Unit tests for the atlas-jira cmd glue, lifting merged coverage
// from 30.4% to 70%+ per slice 300.
//
// Load-bearing functions and the branches each test exercises:
//
//   - classifyResult: ticket-status -> evidencev1.Result enum mapping.
//     Three branches: empty status (UNSPECIFIED), terminal-list match
//     (PASS for each of "Done", "Resolved", "Closed", "Cancelled",
//     "Canceled", "Completed"), default (INCONCLUSIVE).
//   - resolveCommon: env-var + global flag resolution. Six paths:
//     endpoint via flag, endpoint via env, endpoint missing (error);
//     token via flag, token via env, token missing (error).
//   - newRootCmd: smoke that the root cobra command instantiates,
//     wires all three subcommands (register + run + scopes), and
//     exposes the persistent flags (--endpoint, --token, --insecure).
//   - newRegisterCmd: PreRunE error path when env vars are missing;
//     RunE error path against an unreachable endpoint (covers the
//     dial-success-RPC-error branch end-to-end).
//   - newRunCmd PreRunE: five error branches —
//     (a) invalid --platform,
//     (b) missing --environment,
//     (c) --platform=jira missing --jql,
//     (d) --platform=linear missing --team-key,
//     (e) all flags valid + resolveCommon fails (no endpoint).
//   - newScopesCmd: Run renders a PLATFORM/NAME/ACCESS/GATES table.
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
//   - actorID: pins the connector:<platform>:<service>@<version> shape
//     for both jira and linear platforms.
//   - doRun: covers the unsupported-platform branch (defense-in-depth
//     against a future PreRunE relaxation) plus the jira/linear path
//     auth-resolve-error branches via runJira / runLinear when the
//     credential env vars are unset and no flag value is supplied.
//   - runJira: covers the missing --jira-base-url error branch and
//     the jiraauth.ResolveJira error wrap.
//   - runLinear: covers the lineartickets.ResolveLinear error wrap
//     when LINEAR_API_KEY is unset and no --linear-key supplied.
//
// The global `common` struct is saved + restored per-test via a
// helper to prevent cross-test state pollution (cobra binds the
// flags into package-level globals).
//
// No vendor-prefixed tokens (Atlassian API tokens, Linear API keys,
// etc.) appear in fixtures — neutral "test-*" strings only, per
// CLAUDE.md's hard rule.
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

// TestClassifyResult_AllBranches covers every branch of classifyResult:
// empty -> UNSPECIFIED, terminal-list -> PASS (six values), default ->
// INCONCLUSIVE.
func TestClassifyResult_AllBranches(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want evidencev1.Result
	}{
		{"empty", "", evidencev1.Result_RESULT_UNSPECIFIED},
		{"done", "Done", evidencev1.Result_RESULT_PASS},
		{"resolved", "Resolved", evidencev1.Result_RESULT_PASS},
		{"closed", "Closed", evidencev1.Result_RESULT_PASS},
		{"cancelled-uk", "Cancelled", evidencev1.Result_RESULT_PASS},
		{"canceled-us", "Canceled", evidencev1.Result_RESULT_PASS},
		{"completed", "Completed", evidencev1.Result_RESULT_PASS},
		{"open-default", "Open", evidencev1.Result_RESULT_INCONCLUSIVE},
		{"in-progress-default", "In Progress", evidencev1.Result_RESULT_INCONCLUSIVE},
		{"unknown-default", "Triage", evidencev1.Result_RESULT_INCONCLUSIVE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyResult(tc.in); got != tc.want {
				t.Errorf("classifyResult(%q) = %v; want %v", tc.in, got, tc.want)
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

// TestNewRunCmd_PreRunRejectsInvalidPlatform covers the first PreRunE
// guard: --platform must be "jira" or "linear".
func TestNewRunCmd_PreRunRejectsInvalidPlatform(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--platform", "bogus",
		"--environment", "prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid --platform")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Errorf("err = %v; want platform mention", err)
	}
}

// TestNewRunCmd_PreRunRejectsMissingEnvironment covers the second
// PreRunE guard: --environment must be set.
func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--platform", "jira",
		"--jql", "project = CR",
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

// TestNewRunCmd_PreRunJiraMissingJQL covers the third PreRunE guard:
// --platform=jira requires --jql.
func TestNewRunCmd_PreRunJiraMissingJQL(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--platform", "jira",
		"--environment", "prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --jql in jira mode")
	}
	if !strings.Contains(err.Error(), "jql") {
		t.Errorf("err = %v; want jql mention", err)
	}
}

// TestNewRunCmd_PreRunLinearMissingTeamKey covers the fourth PreRunE
// guard: --platform=linear requires --team-key.
func TestNewRunCmd_PreRunLinearMissingTeamKey(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--platform", "linear",
		"--environment", "prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --team-key in linear mode")
	}
	if !strings.Contains(err.Error(), "team-key") {
		t.Errorf("err = %v; want team-key mention", err)
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
		"--platform", "jira",
		"--environment", "prod",
		"--jql", "project = CR",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewRunCmd_PreRunHappyJira asserts the happy path: --platform=jira
// + --environment + --jql + endpoint/token all set -> PreRunE returns
// nil. This pins the success branch through every guard.
func TestNewRunCmd_PreRunHappyJira(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "127.0.0.1:9999")
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-bearer")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{
		"--platform", "jira",
		"--environment", "prod",
		"--jql", "project = CR",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE returned error on happy path: %v", err)
	}
}

// TestNewScopesCmd_RendersTable drives the scopes subcommand's Run
// path and asserts the output emits the PLATFORM/NAME/ACCESS/GATES
// header plus at least one data row from jiraauth.DocumentedScopes().
func TestNewScopesCmd_RendersTable(t *testing.T) {
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
	for _, hdr := range []string{"PLATFORM", "NAME", "ACCESS", "GATES"} {
		if !strings.Contains(out, hdr) {
			t.Errorf("missing %q header; got %q", hdr, out)
		}
	}
	// At least one data row beyond the header.
	if strings.Count(out, "\n") < 2 {
		t.Errorf("expected header + at least one data row; got %q", out)
	}
	// Both platforms documented.
	for _, p := range []string{"jira", "linear"} {
		if !strings.Contains(out, p) {
			t.Errorf("scopes output missing platform %q; got %q", p, out)
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

// TestActorID_Shape pins the connector:<platform>:<service>@<version>
// format. The platform field encodes "jira" or "linear" so the audit
// log distinguishes records.
func TestActorID_Shape(t *testing.T) {
	for _, platform := range []string{"jira", "linear"} {
		id := actorID(platform, "tickets")
		wantPrefix := "connector:" + platform + ":tickets@"
		if !strings.HasPrefix(id, wantPrefix) {
			t.Errorf("actorID(%q,tickets) = %q; want prefix %q", platform, id, wantPrefix)
		}
	}
}

// TestDoRun_UnsupportedPlatform covers doRun's defense-in-depth
// unreachable branch — the PreRunE guard would normally reject any
// platform that isn't "jira" or "linear", but doRun's switch has an
// explicit fallthrough that returns an error if it's ever called
// directly with a bad platform.
//
// We have to spin sdkClient construction successfully first; that's
// non-blocking because sdk.NewClient is lazy (grpc.NewClient under
// the hood doesn't dial). The switch then evaluates and returns the
// documented "unsupported platform" error.
func TestDoRun_UnsupportedPlatform(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	err := doRun(context.Background(), runFlags{
		platform:    "neither",
		environment: "prod",
		controlID:   "scf:CHG-02",
	})
	if err == nil {
		t.Fatal("expected unsupported-platform error from doRun")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("err = %v; want unsupported platform mention", err)
	}
}

// TestDoRun_JiraMissingBaseURL drives the runJira branch with
// --jira-base-url unset (and the PreRunE guard bypassed by calling
// doRun directly). Exercises the first error wrap inside runJira.
func TestDoRun_JiraMissingBaseURL(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	err := doRun(context.Background(), runFlags{
		platform:    "jira",
		environment: "prod",
		controlID:   "scf:CHG-02",
		jql:         "project = CR",
		// jiraBaseURL intentionally empty
	})
	if err == nil {
		t.Fatal("expected error for missing --jira-base-url")
	}
	if !strings.Contains(err.Error(), "jira-base-url") {
		t.Errorf("err = %v; want jira-base-url mention", err)
	}
}

// TestDoRun_JiraAuthResolveFails drives runJira past the base-url guard
// to the jiraauth.ResolveJira error wrap when JIRA_EMAIL and
// JIRA_API_TOKEN are both unset.
func TestDoRun_JiraAuthResolveFails(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")
	err := doRun(context.Background(), runFlags{
		platform:    "jira",
		environment: "prod",
		controlID:   "scf:CHG-02",
		jiraBaseURL: "https://example.atlassian.net",
		jql:         "project = CR",
		// jiraEmail / jiraToken intentionally empty
	})
	if err == nil {
		t.Fatal("expected jira auth resolve error")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("err = %v; want auth-wrap mention", err)
	}
}

// TestDoRun_LinearAuthResolveFails drives runLinear to the
// jiraauth.ResolveLinear error wrap when LINEAR_API_KEY is unset and
// no --linear-key flag value is supplied.
func TestDoRun_LinearAuthResolveFails(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("LINEAR_API_KEY", "")
	err := doRun(context.Background(), runFlags{
		platform:      "linear",
		environment:   "prod",
		controlID:     "scf:CHG-02",
		linearBaseURL: "https://api.linear.app",
		teamKey:       "ENG",
		// linearKey intentionally empty
	})
	if err == nil {
		t.Fatal("expected linear auth resolve error")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("err = %v; want auth-wrap mention", err)
	}
}
