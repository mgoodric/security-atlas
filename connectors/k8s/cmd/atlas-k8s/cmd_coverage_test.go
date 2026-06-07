// Unit tests for the atlas-k8s cmd glue. Mirrors the slice-486 azure-connector
// coverage suite: resolveCommon paths, root/sub-command wiring, the result-enum
// mapper, the record builders' optional-field branches, dial transport
// branches, authedContext, sdkOpts, connectorVersion, actorID, and the
// permissions subcommand render.
//
// No real cluster tokens or vendor-prefixed JWTs appear in fixtures — neutral
// "test-*" strings only, per CLAUDE.md's hard rule.
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

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

// resetCommon snapshots the package-global `common` struct and restores it on
// test cleanup. Cobra's flag binding mutates this global.
func resetCommon(t *testing.T) {
	t.Helper()
	saved := common
	t.Cleanup(func() { common = saved })
	common.endpoint = ""
	common.token = ""
	common.insecure = false
}

func TestMapWorkloadResult(t *testing.T) {
	cases := []struct {
		name string
		in   workload.ConfigResult
		want evidencev1.Result
	}{
		{"pass", workload.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", workload.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", workload.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"default", workload.ConfigResult("unknown"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapWorkloadResult(tc.in); got != tc.want {
				t.Errorf("mapWorkloadResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
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

func TestNewRunCmd_PreRunRejectsMissingCluster(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "cluster") {
		t.Fatalf("want cluster error; got %v", err)
	}
}

func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--cluster", "c1"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

func TestNewRunCmd_PreRunRejectsBadAuthMode(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--cluster", "c1", "--environment", "prod", "--auth-mode", "bogus"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "auth-mode") {
		t.Fatalf("want auth-mode error; got %v", err)
	}
}

func TestNewRunCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--cluster", "c1", "--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

func TestNewPermissionsCmd_RendersClusterRole(t *testing.T) {
	cmd := newPermissionsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Run(cmd, nil)
	out := buf.String()
	if !strings.Contains(out, "API GROUP") {
		t.Errorf("permissions output missing header; got %q", out)
	}
	for _, want := range []string{"rbac.authorization.k8s.io", "clusterrolebindings", "deployments", "get,list"} {
		if !strings.Contains(out, want) {
			t.Errorf("permissions output missing %q; got %q", want, out)
		}
	}
	// P0-487-3: the rendered ClusterRole must never mention 'secrets'.
	if strings.Contains(out, "secrets") {
		t.Errorf("permissions output must NOT grant 'secrets'; got %q", out)
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
	for _, svc := range []string{"rbac", "workload"} {
		id := actorID(svc)
		if !strings.HasPrefix(id, "connector:k8s:"+svc+"@") {
			t.Errorf("actorID(%q) = %q", svc, id)
		}
	}
}

func TestBuildRBACRecord_Shape(t *testing.T) {
	b := rbac.Binding{
		BindingName: "admins", BindingScope: rbac.ScopeCluster, RoleKind: rbac.RoleKindClusterRole,
		RoleName: "cluster-admin", GrantsWildcard: true,
		Subjects:   []rbac.Subject{{Kind: rbac.SubjectUser, Name: "alice"}},
		Rules:      []rbac.Rule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}},
		ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildRBACRecord(b, "cluster-1", "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildRBACRecord: %v", err)
	}
	if rec.EvidenceKind != "k8s.rbac_binding.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.Result != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if got := scopeValue(rec.GetScope(), "cluster"); got != "cluster-1" {
		t.Errorf("cluster = %q; want cluster-1", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("environment = %q; want prod", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"binding_name", "binding_scope", "role_kind", "role_name", "grants_wildcard", "subjects", "rules"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
}

func TestBuildRBACRecord_OmitsEmptyOptionals(t *testing.T) {
	b := rbac.Binding{
		BindingName: "b", BindingScope: rbac.ScopeCluster, RoleKind: rbac.RoleKindClusterRole,
		RoleName: "r", ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildRBACRecord(b, "c", "prod", "scf:IAC-21")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"namespace", "subjects", "rules"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
}

func TestBuildWorkloadRecord_Shape(t *testing.T) {
	w := workload.SecurityContext{
		WorkloadKind: workload.KindDeployment, WorkloadName: "api", Namespace: "prod",
		RunAsNonRoot: true, Privileged: false, ReadOnlyRootFilesystem: true,
		AllowPrivilegeEscalation: false, ContainerCount: 2,
		Result: workload.ResultPass, ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildWorkloadRecord(w, "cluster-1", "prod", "scf:CFG-02")
	if err != nil {
		t.Fatalf("buildWorkloadRecord: %v", err)
	}
	if rec.EvidenceKind != "k8s.workload_security_context.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("result = %v; want PASS", rec.Result)
	}
	if got := scopeValue(rec.GetScope(), "cluster"); got != "cluster-1" {
		t.Errorf("cluster = %q", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{
		"workload_kind", "workload_name", "namespace", "run_as_non_root", "privileged",
		"read_only_root_filesystem", "allow_privilege_escalation", "host_network",
		"host_pid", "host_ipc", "container_count",
	} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
}

// scopeValue returns the first scope value for key. Empty when key absent.
func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}

func TestToAnySlice(t *testing.T) {
	got := toAnySlice([]string{"a", "b"})
	if len(got) != 2 || got[0] != "a" {
		t.Errorf("toAnySlice = %v", got)
	}
}

// TestDoRun_FailsOnMissingCredential drives doRun's first error branch:
// k8sauth.Resolve fails when no API server is set.
func TestDoRun_FailsOnMissingCredential(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("KUBERNETES_API_SERVER", "")
	t.Setenv("KUBECONFIG_TOKEN", "")

	err := doRun(context.Background(), runFlags{cluster: "c", environment: "prod", authMode: "kubeconfig-token", skipWorkload: true})
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}
