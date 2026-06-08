// Unit tests for the atlas-bamboohr cmd glue. Mirrors the slice-490 jamf-connector
// coverage suite: resolveCommon paths, root/sub-command wiring, dial transport
// branches, authedContext, sdkOpts, connectorVersion, actorID, and the
// permissions subcommand render.
//
// No real BambooHR credentials or vendor-prefixed JWTs appear in fixtures —
// neutral "test-*" / "fake-*" strings only, per CLAUDE.md's hard rule.
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

func resetCommon(t *testing.T) {
	t.Helper()
	saved := common
	t.Cleanup(func() { common = saved })
	common.endpoint = ""
	common.token = ""
	common.insecure = false
}

func TestResolveCommon_FromFlags(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:9999"
	common.token = "test-bearer"
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
}

func TestResolveCommon_FromEnv(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "env:9999")
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-env-token")
	if err := resolveCommon(); err != nil {
		t.Fatalf("resolveCommon: %v", err)
	}
	if common.endpoint != "env:9999" {
		t.Errorf("endpoint = %q", common.endpoint)
	}
}

func TestResolveCommon_MissingEndpoint(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "test-token")
	if err := resolveCommon(); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("want endpoint error; got %v", err)
	}
}

func TestResolveCommon_MissingToken(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:9999"
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	if err := resolveCommon(); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error; got %v", err)
	}
}

func TestNewRootCmd_HasSubcommands(t *testing.T) {
	resetCommon(t)
	root := newRootCmd()
	if root.Use != ConnectorName {
		t.Errorf("Use = %q; want %q", root.Use, ConnectorName)
	}
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"register", "run", "permissions"} {
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

func TestNewRegisterCmd_PreRunErrorOnMissingEnv(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	reg := newRegisterCmd()
	if err := reg.PreRunE(reg, nil); err == nil {
		t.Fatal("expected PreRunE error when endpoint/token unset")
	}
}

func TestNewRegisterCmd_RunEFailsOnUnreachableEndpoint(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	reg := newRegisterCmd()
	err := reg.RunE(reg, nil)
	if err == nil || !strings.Contains(err.Error(), "register") {
		t.Fatalf("want register error; got %v", err)
	}
}

func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

func TestNewRunCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

func TestNewPermissionsCmd_RendersScope(t *testing.T) {
	cmd := newPermissionsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Run(cmd, nil)
	out := buf.String()
	for _, want := range []string{"CREDENTIAL", "API key"} {
		if !strings.Contains(out, want) {
			t.Errorf("permissions output missing %q; got %q", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "read-only") {
		t.Errorf("permissions output missing read-only; got %q", out)
	}
	// P0-491-2 / P0-491-3: the rendered output must carry the
	// no-full-PII / no-write warning.
	if !strings.Contains(out, "NEVER use") || !strings.Contains(strings.ToLower(out), "write scope") {
		t.Errorf("permissions output missing the read-only / no-full-PII warning; got %q", out)
	}
}

func TestDialConnectorRegistry_BothTransports(t *testing.T) {
	for _, insecure := range []bool{true, false} {
		resetCommon(t)
		common.endpoint = "127.0.0.1:1"
		common.insecure = insecure
		client, conn, err := dialConnectorRegistry()
		if err != nil {
			t.Fatalf("dialConnectorRegistry(insecure=%v): %v", insecure, err)
		}
		if client == nil || conn == nil {
			t.Errorf("nil client/conn (insecure=%v)", insecure)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func TestAuthedContext_HasAuthMetadata(t *testing.T) {
	resetCommon(t)
	common.token = "test-bearer-token"
	ctx, cancel := authedContext(5 * time.Second)
	defer cancel()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("no outgoing metadata")
	}
	vals := md.Get(sdk.MetadataAuthorization)
	if len(vals) == 0 || vals[0] != sdk.BearerPrefix+"test-bearer-token" {
		t.Errorf("auth header = %v", vals)
	}
}

func TestSDKOpts(t *testing.T) {
	resetCommon(t)
	common.insecure = false
	if sdkOpts() != nil {
		t.Error("sdkOpts() should be nil when secure")
	}
	common.insecure = true
	if len(sdkOpts()) != 1 {
		t.Error("sdkOpts() should carry WithInsecure when insecure")
	}
}

func TestConnectorVersion_NonEmpty(t *testing.T) {
	if connectorVersion() == "" {
		t.Error("connectorVersion empty")
	}
}

func TestActorID_Shape(t *testing.T) {
	id := actorID("workers")
	if !strings.HasPrefix(id, "connector:bamboohr:workers@") {
		t.Errorf("actorID = %q", id)
	}
}

func TestSupportedKinds_WorkerLifecycle(t *testing.T) {
	if len(SupportedKinds) != 1 || SupportedKinds[0] != "hris.worker_lifecycle.v1" {
		t.Errorf("SupportedKinds = %v", SupportedKinds)
	}
}

func TestPullInterval_NotContinuous(t *testing.T) {
	if strings.Contains(strings.ToLower(PullInterval), "continuous monitoring") && !strings.Contains(PullInterval, "NOT continuous") {
		t.Errorf("PullInterval must not claim continuous monitoring (P0-491-6): %q", PullInterval)
	}
}
