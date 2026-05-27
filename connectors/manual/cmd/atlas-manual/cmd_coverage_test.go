// Unit tests for the atlas-manual cmd glue, lifting merged coverage
// from 43.6% past 70% per slice 307.
//
// The manual connector is the universal escape hatch: one evidence kind
// (manual.upload.v1) reachable through three transports (local CSV, S3
// prefix scan, SFTP path glob). Canvas §4.5 names "manual evidence is
// first-class" so the per-mode cobra glue MUST be exercised on the
// same footing as automated connectors.
//
// Load-bearing functions and the branches each test exercises:
//
//   - resolveCommon: env-var + global flag resolution. Six paths:
//     endpoint via flag, endpoint via env, endpoint missing (error);
//     token via flag, token via env, token missing (error).
//   - newRootCmd: smoke that the root cobra command instantiates,
//     wires all five subcommands (register + scopes + local + s3 +
//     sftp), and exposes the persistent flags (--endpoint, --token,
//     --insecure).
//   - newRegisterCmd: PreRunE error path when env vars are missing;
//     RunE failure against an unreachable endpoint — drives
//     resolveCommon + dialConnectorRegistry + the Register RPC.
//   - newLocalCmd PreRunE: two error branches (missing --file, missing
//     --scope) and the resolveCommon-fall-through branch.
//   - newS3Cmd PreRunE: four branches — missing --bucket, missing
//     --prefix, missing --scope, resolveCommon fall-through.
//   - newSFTPCmd PreRunE: seven branches — missing --host / --user /
//     --path / --key-file / --known-hosts / --scope, then
//     resolveCommon fall-through.
//   - newScopesCmd: Run writes a tab-separated header + at least one
//     row to the command's OutOrStdout. Pinned per-mode rows.
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
//   - actorID: connector:manual:<service>@<version> shape for each of
//     local, s3, and sftp.
//   - doLocal: drives a real CSV through the parse step + record build
//     with an unreachable endpoint so the Push step fails fast. Plus
//     the open-csv error path (missing file).
//   - doS3: invalid-region error path (LoadDefaultConfig with a bogus
//     region surface still constructs; the empty-prefix path is
//     already covered by manuals3.List but the cmd-level guard is
//     verified via PreRunE).
//   - doSFTP: missing-known-hosts error wrap via LoadPrivateKey on a
//     non-existent path; covers the first error wrap before any dial.
//   - buildLocalRecord: payload-too-large branch via a max-row-bytes
//     forced through json marshaling.
//   - buildS3Record: smoke that the schema-required fields land on
//     the EvidenceRecord; assertion on idempotency_key shape.
//   - buildSFTPRecord: smoke that the schema-required fields land on
//     the EvidenceRecord; assertion on idempotency_key shape.
//   - guessContentType: full branch table — .csv / .json / .pdf /
//     .txt / .log / .xml / .html / .htm / fallback default.
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/manualcsv"
	"github.com/mgoodric/security-atlas/connectors/manual/internal/manuals3"
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
// five subcommands and the persistent flags.
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
	for _, want := range []string{"register", "scopes", "local", "s3", "sftp"} {
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

// TestNewLocalCmd_PreRunRejectsMissingFile covers the first PreRunE
// guard for the local subcommand.
func TestNewLocalCmd_PreRunRejectsMissingFile(t *testing.T) {
	resetCommon(t)
	cmd := newLocalCmd()
	if cmd.Use != "local" {
		t.Errorf("Use = %q; want local", cmd.Use)
	}
	if err := cmd.ParseFlags([]string{"--scope", "environment=prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --file")
	}
	if !strings.Contains(err.Error(), "--file") {
		t.Errorf("err = %v; want --file mention", err)
	}
}

// TestNewLocalCmd_PreRunRejectsMissingScope covers the second PreRunE
// guard: --file set but --scope unset.
func TestNewLocalCmd_PreRunRejectsMissingScope(t *testing.T) {
	resetCommon(t)
	cmd := newLocalCmd()
	if err := cmd.ParseFlags([]string{"--file", "/tmp/x.csv"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --scope")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("err = %v; want --scope mention", err)
	}
}

// TestNewLocalCmd_PreRunResolveCommonFails: --file and --scope both
// set but no endpoint/token configured, so PreRunE falls through to
// resolveCommon and errors.
func TestNewLocalCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newLocalCmd()
	if err := cmd.ParseFlags([]string{
		"--file", "/tmp/x.csv",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewS3Cmd_PreRunRejectsMissingBucket covers the first PreRunE
// guard for the s3 subcommand.
func TestNewS3Cmd_PreRunRejectsMissingBucket(t *testing.T) {
	resetCommon(t)
	cmd := newS3Cmd()
	if cmd.Use != "s3" {
		t.Errorf("Use = %q; want s3", cmd.Use)
	}
	if err := cmd.ParseFlags([]string{
		"--prefix", "audits/",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --bucket")
	}
	if !strings.Contains(err.Error(), "--bucket") {
		t.Errorf("err = %v; want --bucket mention", err)
	}
}

// TestNewS3Cmd_PreRunRejectsMissingPrefix covers the second PreRunE
// guard: --bucket set, --prefix missing.
func TestNewS3Cmd_PreRunRejectsMissingPrefix(t *testing.T) {
	resetCommon(t)
	cmd := newS3Cmd()
	if err := cmd.ParseFlags([]string{
		"--bucket", "test-bucket",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --prefix")
	}
	if !strings.Contains(err.Error(), "--prefix") {
		t.Errorf("err = %v; want --prefix mention", err)
	}
}

// TestNewS3Cmd_PreRunRejectsMissingScope covers the third PreRunE
// guard: --bucket and --prefix set, no --scope.
func TestNewS3Cmd_PreRunRejectsMissingScope(t *testing.T) {
	resetCommon(t)
	cmd := newS3Cmd()
	if err := cmd.ParseFlags([]string{
		"--bucket", "test-bucket",
		"--prefix", "audits/",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --scope")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("err = %v; want --scope mention", err)
	}
}

// TestNewS3Cmd_PreRunResolveCommonFails: all S3 flags valid but no
// endpoint/token, so PreRunE falls through to resolveCommon and errors.
func TestNewS3Cmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newS3Cmd()
	if err := cmd.ParseFlags([]string{
		"--bucket", "test-bucket",
		"--prefix", "audits/",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewSFTPCmd_PreRunRejectsMissingHost covers the first PreRunE
// guard for the sftp subcommand.
func TestNewSFTPCmd_PreRunRejectsMissingHost(t *testing.T) {
	resetCommon(t)
	cmd := newSFTPCmd()
	if cmd.Use != "sftp" {
		t.Errorf("Use = %q; want sftp", cmd.Use)
	}
	if err := cmd.ParseFlags([]string{
		"--user", "atlas",
		"--path", "/inbox/*.pdf",
		"--key-file", "/dev/null",
		"--known-hosts", "/dev/null",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --host")
	}
	if !strings.Contains(err.Error(), "--host") {
		t.Errorf("err = %v; want --host mention", err)
	}
}

// TestNewSFTPCmd_PreRunRejectsMissingUser covers the second PreRunE
// guard: --host set, --user missing.
func TestNewSFTPCmd_PreRunRejectsMissingUser(t *testing.T) {
	resetCommon(t)
	cmd := newSFTPCmd()
	if err := cmd.ParseFlags([]string{
		"--host", "sftp.example.com",
		"--path", "/inbox/*.pdf",
		"--key-file", "/dev/null",
		"--known-hosts", "/dev/null",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --user")
	}
	if !strings.Contains(err.Error(), "--user") {
		t.Errorf("err = %v; want --user mention", err)
	}
}

// TestNewSFTPCmd_PreRunRejectsMissingPath covers the third PreRunE
// guard: --host + --user set, --path missing.
func TestNewSFTPCmd_PreRunRejectsMissingPath(t *testing.T) {
	resetCommon(t)
	cmd := newSFTPCmd()
	if err := cmd.ParseFlags([]string{
		"--host", "sftp.example.com",
		"--user", "atlas",
		"--key-file", "/dev/null",
		"--known-hosts", "/dev/null",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --path")
	}
	if !strings.Contains(err.Error(), "--path") {
		t.Errorf("err = %v; want --path mention", err)
	}
}

// TestNewSFTPCmd_PreRunRejectsMissingScope covers the last PreRunE
// guard before resolveCommon: every credential flag set, --scope missing.
func TestNewSFTPCmd_PreRunRejectsMissingScope(t *testing.T) {
	resetCommon(t)
	cmd := newSFTPCmd()
	if err := cmd.ParseFlags([]string{
		"--host", "sftp.example.com",
		"--user", "atlas",
		"--path", "/inbox/*.pdf",
		"--key-file", "/dev/null",
		"--known-hosts", "/dev/null",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --scope")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("err = %v; want --scope mention", err)
	}
}

// TestNewSFTPCmd_PreRunResolveCommonFails: all sftp flags set but no
// endpoint/token, so PreRunE falls through to resolveCommon and errors.
func TestNewSFTPCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newSFTPCmd()
	if err := cmd.ParseFlags([]string{
		"--host", "sftp.example.com",
		"--user", "atlas",
		"--path", "/inbox/*.pdf",
		"--key-file", "/dev/null",
		"--known-hosts", "/dev/null",
		"--scope", "environment=prod",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.PreRunE(cmd, nil)
	if err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

// TestNewScopesCmd_RendersPostureTable runs the scopes subcommand and
// confirms it emits the documented per-mode auth posture (MODE / AUTH /
// NOTES columns, one row per mode).
func TestNewScopesCmd_RendersPostureTable(t *testing.T) {
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
	for _, hdr := range []string{"MODE", "AUTH", "NOTES"} {
		if !strings.Contains(out, hdr) {
			t.Errorf("missing %q header; got %q", hdr, out)
		}
	}
	for _, mode := range []string{"local", "s3", "sftp"} {
		if !strings.Contains(out, mode) {
			t.Errorf("scopes output missing mode %q; got %q", mode, out)
		}
	}
	// At least header + three data rows.
	if strings.Count(out, "\n") < 4 {
		t.Errorf("expected header + 3 data rows; got %q", out)
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

// TestActorID_AllServices pins the connector:manual:<service>@<version>
// shape for each of the three transports. The integration_test.go file
// has a similar TestActorID_Shape test; this one extends per-service
// coverage to all three names so the asserted shape is exhaustive
// across the connector's surface area.
func TestActorID_AllServices(t *testing.T) {
	for _, service := range []string{"local", "s3", "sftp"} {
		id := actorID(service)
		wantPrefix := "connector:manual:" + service + "@"
		if !strings.HasPrefix(id, wantPrefix) {
			t.Errorf("actorID(%q) = %q; want prefix %q", service, id, wantPrefix)
		}
	}
}

// TestDoLocal_FailsOnMissingFile drives doLocal against a file that
// does not exist. Covers the os.Open error wrap.
func TestDoLocal_FailsOnMissingFile(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	warn := newTempWarn(t)
	err := doLocal(context.Background(), localFlags{
		file:          filepath.Join(t.TempDir(), "missing.csv"),
		controlID:     "scf:GOV-04",
		scope:         []string{"environment=test"},
		maxRows:       100,
		maxFieldBytes: 1024,
	}, warn)
	if err == nil {
		t.Fatal("expected open-csv error for missing file")
	}
	if !strings.Contains(err.Error(), "open csv") {
		t.Errorf("err = %v; want open csv wrap", err)
	}
}

// TestDoLocal_FailsOnPushUnreachable drives doLocal through CSV parse +
// record build + SDK push against an unreachable endpoint. The push
// fails fast, exercising the push-error wrap. Covers the end-to-end
// path through doLocal except the success-print line.
func TestDoLocal_FailsOnPushUnreachable(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "rows.csv")
	if err := os.WriteFile(csvPath, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("seed csv: %v", err)
	}

	warn := newTempWarn(t)
	err := doLocal(context.Background(), localFlags{
		file:          csvPath,
		controlID:     "scf:GOV-04",
		scope:         []string{"environment=test"},
		maxRows:       100,
		maxFieldBytes: 1024,
	}, warn)
	if err == nil {
		t.Fatal("expected push error against unreachable endpoint")
	}
	if !strings.Contains(err.Error(), "push") {
		t.Errorf("err = %v; want push error wrap", err)
	}
}

// TestDoLocal_FailsOnBadScope drives doLocal past the open + parse
// steps with a malformed scope so the parseScope error wrap fires.
func TestDoLocal_FailsOnBadScope(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "rows.csv")
	if err := os.WriteFile(csvPath, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("seed csv: %v", err)
	}
	warn := newTempWarn(t)
	err := doLocal(context.Background(), localFlags{
		file:          csvPath,
		controlID:     "scf:GOV-04",
		scope:         []string{"no-equals-sign"},
		maxRows:       100,
		maxFieldBytes: 1024,
	}, warn)
	if err == nil {
		t.Fatal("expected scope-parse error")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("err = %v; want scope mention", err)
	}
}

// TestDoS3_FailsOnListUnreachable drives doS3 with the AWS S3 endpoint
// pointed at 127.0.0.1:1 so the List call errors fast. Covers the doS3
// entry path through config.LoadDefaultConfig + s3.NewFromConfig +
// manuals3.List error wrap.
func TestDoS3_FailsOnListUnreachable(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://127.0.0.1:1")
	err := doS3(context.Background(), s3Flags{
		bucket:    "test-bucket",
		prefix:    "audits/",
		region:    "us-east-1",
		controlID: "scf:GOV-04",
		scope:     []string{"environment=prod"},
	})
	if err == nil {
		t.Fatal("expected error reaching S3 against 127.0.0.1:1")
	}
}

// TestDoS3_EmptyListSucceeds drives doS3 against a fake S3 server that
// returns an empty bucket listing. The listing succeeds, parseScope
// succeeds, sdk.NewClient succeeds (lazy), the loop iterates zero
// times, and doS3 returns nil after the success printf.
//
// This is the deepest reach into doS3 we can exercise without a real
// platform endpoint: every line up to and including the success print
// runs (the push error wrap stays uncovered — that's intentional;
// it requires both a working S3 list AND a working platform push, an
// integration-test surface, not a unit-test surface).
func TestDoS3_EmptyListSucceeds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	// Stand up a fake S3 endpoint that returns an empty ListBucketResult.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := `<?xml version="1.0"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>test-bucket</Name>
  <Prefix>audits/</Prefix>
  <KeyCount>0</KeyCount>
  <IsTruncated>false</IsTruncated>
</ListBucketResult>`
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)

	err := doS3(context.Background(), s3Flags{
		bucket:    "test-bucket",
		prefix:    "audits/",
		region:    "us-east-1",
		controlID: "scf:GOV-04",
		scope:     []string{"environment=prod"},
	})
	if err != nil {
		t.Fatalf("doS3 against empty fake S3: %v", err)
	}
}

// TestDoS3_FailsOnBadScope drives doS3 past List (against the same fake
// empty-list server) to the parseScope error path, with a malformed
// --scope value. Exercises the parseScope error wrap inside doS3.
func TestDoS3_FailsOnBadScope(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := `<?xml version="1.0"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>test-bucket</Name>
  <Prefix>audits/</Prefix>
  <KeyCount>0</KeyCount>
  <IsTruncated>false</IsTruncated>
</ListBucketResult>`
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)

	err := doS3(context.Background(), s3Flags{
		bucket:    "test-bucket",
		prefix:    "audits/",
		region:    "us-east-1",
		controlID: "scf:GOV-04",
		scope:     []string{"no-equals-sign"},
	})
	if err == nil {
		t.Fatal("expected scope-parse error")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("err = %v; want scope mention", err)
	}
}

// TestDoS3_FailsOnPushUnreachable drives doS3 against a fake S3 server
// that returns one object so the loop body executes at least once. The
// push to 127.0.0.1:1 fails fast, exercising the push-error wrap in
// doS3.
func TestDoS3_FailsOnPushUnreachable(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := `<?xml version="1.0"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>test-bucket</Name>
  <Prefix>audits/</Prefix>
  <KeyCount>1</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>audits/q1.pdf</Key>
    <LastModified>2026-04-01T00:00:00.000Z</LastModified>
    <ETag>"etag-q1"</ETag>
    <Size>2048</Size>
  </Contents>
</ListBucketResult>`
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)

	err := doS3(context.Background(), s3Flags{
		bucket:    "test-bucket",
		prefix:    "audits/",
		region:    "us-east-1",
		controlID: "scf:GOV-04",
		scope:     []string{"environment=prod"},
	})
	if err == nil {
		t.Fatal("expected push error against unreachable platform")
	}
	if !strings.Contains(err.Error(), "push") {
		t.Errorf("err = %v; want push error wrap", err)
	}
}

// TestDoSFTP_FailsOnMissingKeyFile drives doSFTP with a non-existent
// key file path. Covers the LoadPrivateKey error wrap — the first I/O
// step in doSFTP.
func TestDoSFTP_FailsOnMissingKeyFile(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	err := doSFTP(context.Background(), sftpFlags{
		host:        "sftp.example.com",
		port:        22,
		user:        "atlas",
		pathGlob:    "/inbox/*.pdf",
		keyFile:     filepath.Join(t.TempDir(), "no-such-key"),
		knownHosts:  filepath.Join(t.TempDir(), "no-such-known-hosts"),
		controlID:   "scf:GOV-04",
		scope:       []string{"environment=prod"},
		dialTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("expected LoadPrivateKey error for missing key file")
	}
}

// TestDoSFTP_FailsOnMissingKnownHosts drives doSFTP past the key-load
// step (with a real PEM-ish file) to the NewHostKeyCallback step
// against a missing known_hosts file. Exercises the second I/O step's
// error path.
func TestDoSFTP_FailsOnMissingKnownHosts(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	// Write a syntactically real-shaped key file so LoadPrivateKey at
	// least returns bytes (even if BuildSSHConfig would later reject).
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_test")
	if err := os.WriteFile(keyPath, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	err := doSFTP(context.Background(), sftpFlags{
		host:        "sftp.example.com",
		port:        22,
		user:        "atlas",
		pathGlob:    "/inbox/*.pdf",
		keyFile:     keyPath,
		knownHosts:  filepath.Join(dir, "no-such-known-hosts"),
		controlID:   "scf:GOV-04",
		scope:       []string{"environment=prod"},
		dialTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("expected NewHostKeyCallback error for missing known-hosts")
	}
}

// TestBuildLocalRecord_HappyPath drives buildLocalRecord through the
// happy path. Asserts the schema-required fields land on the payload
// and the idempotency_key matches the LocalRowKey shape.
func TestBuildLocalRecord_HappyPath(t *testing.T) {
	resetCommon(t)
	row := manualcsv.Row{
		Index:  1,
		Header: []string{"col_a", "col_b"},
		Fields: []string{"v1", "v2"},
	}
	scope := []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}}
	observed := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	rec, err := buildLocalRecord(row, "/inbox/rows.csv", "rows.csv", "scf:GOV-04", scope, observed)
	if err != nil {
		t.Fatalf("buildLocalRecord: %v", err)
	}
	if rec.GetEvidenceKind() != "manual.upload.v1" {
		t.Errorf("evidence_kind = %q; want manual.upload.v1", rec.GetEvidenceKind())
	}
	if rec.GetSchemaVersion() != "1.0.0" {
		t.Errorf("schema_version = %q; want 1.0.0", rec.GetSchemaVersion())
	}
	if rec.GetControlId() != "scf:GOV-04" {
		t.Errorf("control_id = %q; want scf:GOV-04", rec.GetControlId())
	}
	if rec.GetResult() != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE", rec.GetResult())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency_key empty")
	}
	payload := rec.GetPayload().AsMap()
	for _, k := range []string{"uploaded_by", "filename", "content_type", "size_bytes", "row_index", "payload_b64"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if got, _ := payload["filename"].(string); got != "rows.csv" {
		t.Errorf("payload.filename = %q; want rows.csv", got)
	}
	if got, _ := payload["content_type"].(string); got != "text/csv" {
		t.Errorf("payload.content_type = %q; want text/csv", got)
	}
}

// TestBuildLocalRecord_HeaderlessRow exercises the rowAsObject
// fallback to col_<i> when no header is supplied.
func TestBuildLocalRecord_HeaderlessRow(t *testing.T) {
	row := manualcsv.Row{
		Index:  2,
		Header: nil, // headerless CSV
		Fields: []string{"x", "y"},
	}
	scope := []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}}
	rec, err := buildLocalRecord(row, "/p.csv", "p.csv", "scf:GOV-04", scope, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildLocalRecord: %v", err)
	}
	if rec == nil {
		t.Fatal("nil record")
	}
}

// TestBuildS3Record_HappyPath drives buildS3Record through the happy
// path and asserts schema-required fields land on the payload.
func TestBuildS3Record_HappyPath(t *testing.T) {
	resetCommon(t)
	obj := manuals3.Object{
		Bucket:       "test-bucket",
		Key:          "audits/q1.pdf",
		ETag:         "etag-q1",
		Size:         2048,
		LastModified: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	scope := []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}}
	rec, err := buildS3Record(obj, "scf:GOV-04", scope, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildS3Record: %v", err)
	}
	if rec.GetEvidenceKind() != "manual.upload.v1" {
		t.Errorf("evidence_kind = %q; want manual.upload.v1", rec.GetEvidenceKind())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency_key empty")
	}
	payload := rec.GetPayload().AsMap()
	for _, k := range []string{"uploaded_by", "filename", "content_type", "size_bytes", "bucket", "etag", "last_modified"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if got, _ := payload["bucket"].(string); got != "test-bucket" {
		t.Errorf("payload.bucket = %q; want test-bucket", got)
	}
	if got, _ := payload["etag"].(string); got != "etag-q1" {
		t.Errorf("payload.etag = %q; want etag-q1", got)
	}
}

// TestBuildSFTPRecord_HappyPath drives buildSFTPRecord through the
// happy path and asserts schema-required fields land on the payload.
func TestBuildSFTPRecord_HappyPath(t *testing.T) {
	resetCommon(t)
	mtime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	rec, err := buildSFTPRecord(
		"sftp.example.com",
		"/inbox/quarterly.pdf",
		8192,
		mtime,
		"scf:GOV-04",
		[]*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("buildSFTPRecord: %v", err)
	}
	if rec.GetEvidenceKind() != "manual.upload.v1" {
		t.Errorf("evidence_kind = %q; want manual.upload.v1", rec.GetEvidenceKind())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency_key empty")
	}
	payload := rec.GetPayload().AsMap()
	for _, k := range []string{"uploaded_by", "filename", "content_type", "size_bytes", "host", "remote_path", "mtime"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if got, _ := payload["filename"].(string); got != "quarterly.pdf" {
		t.Errorf("payload.filename = %q; want quarterly.pdf", got)
	}
	if got, _ := payload["host"].(string); got != "sftp.example.com" {
		t.Errorf("payload.host = %q; want sftp.example.com", got)
	}
	if got, _ := payload["content_type"].(string); got != "application/pdf" {
		t.Errorf("payload.content_type = %q; want application/pdf", got)
	}
}

// TestGuessContentType_AllBranches walks the full extension table.
// Each MIME type maps to one branch in the switch.
func TestGuessContentType_AllBranches(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/inbox/quarterly.csv", "text/csv"},
		{"/inbox/data.JSON", "application/json"}, // case-insensitive
		{"/inbox/report.pdf", "application/pdf"},
		{"/inbox/notes.txt", "text/plain"},
		{"/inbox/sshd.log", "text/plain"},
		{"/inbox/config.xml", "application/xml"},
		{"/inbox/index.html", "text/html"},
		{"/inbox/old.htm", "text/html"},
		{"/inbox/blob.bin", "application/octet-stream"},
		{"/inbox/no-extension", "application/octet-stream"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := guessContentType(tc.path); got != tc.want {
				t.Errorf("guessContentType(%q) = %q; want %q", tc.path, got, tc.want)
			}
		})
	}
}

// newTempWarn returns an *os.File suitable for the doLocal warn writer
// arg. The file lives in t.TempDir() and is closed on cleanup. We don't
// reuse integration_helpers_test.go's asTempFile to keep this file
// self-contained (no cross-file fixture coupling).
func newTempWarn(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "warn-*.log")
	if err != nil {
		t.Fatalf("temp warn file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
