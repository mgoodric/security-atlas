// Unit tests for the atlas-azure cmd glue. Mirrors the slice-302 okta-connector
// coverage suite: resolveCommon paths, root/sub-command wiring, the result-enum
// mapper, the record builders' optional-field branches, dial transport
// branches, authedContext, sdkOpts, connectorVersion, actorID, and the
// permissions subcommand render.
//
// No vendor-prefixed tokens or real Azure secrets appear in fixtures — neutral
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

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/firewall"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/nsg"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
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

func TestMapStorageResult(t *testing.T) {
	cases := []struct {
		name string
		in   storage.ConfigResult
		want evidencev1.Result
	}{
		{"pass", storage.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", storage.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", storage.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"default", storage.ConfigResult("unknown"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapStorageResult(tc.in); got != tc.want {
				t.Errorf("mapStorageResult(%q) = %v; want %v", tc.in, got, tc.want)
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

func TestNewRunCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

func TestNewRunCmd_PreRunRejectsBadAuthMode(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod", "--auth-mode", "bogus", "--skip-storage"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "auth-mode") {
		t.Fatalf("want auth-mode error; got %v", err)
	}
}

func TestNewRunCmd_PreRunRequiresSubscriptionForStorage(t *testing.T) {
	resetCommon(t)
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "subscription-id") {
		t.Fatalf("want subscription-id error; got %v", err)
	}
}

func TestNewRunCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod", "--skip-storage"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

func TestNewPermissionsCmd_RendersTable(t *testing.T) {
	cmd := newPermissionsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Run(cmd, nil)
	out := buf.String()
	if !strings.Contains(out, "SURFACE") {
		t.Errorf("permissions output missing header; got %q", out)
	}
	for _, name := range []string{"Directory.Read.All", "Application.Read.All", "Reader"} {
		if !strings.Contains(out, name) {
			t.Errorf("permissions output missing %q; got %q", name, out)
		}
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

func TestMapAKSResult(t *testing.T) {
	cases := []struct {
		name string
		in   aks.ConfigResult
		want evidencev1.Result
	}{
		{"pass", aks.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", aks.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", aks.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"default", aks.ConfigResult("unknown"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapAKSResult(tc.in); got != tc.want {
				t.Errorf("mapAKSResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMapNSGResult(t *testing.T) {
	cases := []struct {
		name string
		in   nsg.ConfigResult
		want evidencev1.Result
	}{
		{"pass", nsg.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", nsg.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", nsg.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"default", nsg.ConfigResult("unknown"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapNSGResult(tc.in); got != tc.want {
				t.Errorf("mapNSGResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMapFirewallResult(t *testing.T) {
	cases := []struct {
		name string
		in   firewall.ConfigResult
		want evidencev1.Result
	}{
		{"pass", firewall.ResultPass, evidencev1.Result_RESULT_PASS},
		{"fail", firewall.ResultFail, evidencev1.Result_RESULT_FAIL},
		{"inconclusive", firewall.ResultInconclusive, evidencev1.Result_RESULT_INCONCLUSIVE},
		{"default", firewall.ConfigResult("unknown"), evidencev1.Result_RESULT_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapFirewallResult(tc.in); got != tc.want {
				t.Errorf("mapFirewallResult(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestActorID_Shape(t *testing.T) {
	for _, svc := range []string{"entra", "storage", "aks", "nsg", "keyvault", "firewall"} {
		id := actorID(svc)
		if !strings.HasPrefix(id, "connector:azure:"+svc+"@") {
			t.Errorf("actorID(%q) = %q", svc, id)
		}
	}
}

func TestBuildEntraRecord_Shape(t *testing.T) {
	a := entra.Assignment{
		AssignmentID: "ra-1", PrincipalID: "p-1", PrincipalType: "user",
		PrincipalDisplayName: "Alice", RoleDefinitionID: "role-1",
		RoleDisplayName: "Reader", DirectoryScopeID: "/", IsPrivileged: false,
		TenantID: "tenant-1", ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildEntraRecord(a, "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildEntraRecord: %v", err)
	}
	if rec.EvidenceKind != "azure.entra_role_assignment.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.Result != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE (descriptive)", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "azure:tenant-1" {
		t.Errorf("cloud_account = %q; want azure:tenant-1", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("environment = %q; want prod", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"assignment_id", "principal_id", "principal_type", "role_definition_id", "is_privileged", "principal_display_name", "role_display_name", "directory_scope_id", "tenant_id"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
}

func TestBuildEntraRecord_OmitsEmptyOptionals(t *testing.T) {
	a := entra.Assignment{
		AssignmentID: "ra", PrincipalID: "p", PrincipalType: "group",
		RoleDefinitionID: "role", TenantID: "t",
		ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildEntraRecord(a, "prod", "scf:IAC-21")
	pl := rec.GetPayload().AsMap()
	if _, ok := pl["principal_display_name"]; ok {
		t.Error("empty display name should be omitted")
	}
	if _, ok := pl["role_display_name"]; ok {
		t.Error("empty role display name should be omitted")
	}
}

func TestBuildStorageRecord_Shape(t *testing.T) {
	c := storage.AccountConfig{
		AccountID: "/sub/acct", AccountName: "acct", SubscriptionID: "sub-1",
		ResourceGroup: "rg", Location: "eastus", EncryptionEnabled: true,
		EncryptionKeySource: "Microsoft.Storage", HTTPSTrafficOnly: true,
		MinimumTLSVersion: "TLS1_2", AllowBlobPublicAccess: false,
		Result: storage.ResultPass, ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildStorageRecord(c, "prod", "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildStorageRecord: %v", err)
	}
	if rec.EvidenceKind != "azure.storage_account_config.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("result = %v; want PASS", rec.Result)
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "azure:sub-1" {
		t.Errorf("cloud_account = %q; want azure:sub-1", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"account_id", "account_name", "subscription_id", "encryption_enabled", "https_traffic_only", "allow_blob_public_access", "resource_group", "location", "encryption_key_source", "minimum_tls_version"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
}

func TestBuildStorageRecord_OmitsEmptyOptionals(t *testing.T) {
	c := storage.AccountConfig{
		AccountID: "/sub/a", AccountName: "a", SubscriptionID: "s",
		EncryptionEnabled: true, HTTPSTrafficOnly: true,
		Result: storage.ResultPass, ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildStorageRecord(c, "prod", "scf:CRY-04")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"resource_group", "location", "encryption_key_source", "minimum_tls_version"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
}

func TestBuildAKSRecord_Shape(t *testing.T) {
	c := aks.ClusterConfig{
		ClusterID: "/sub/clu", ClusterName: "clu", SubscriptionID: "sub-1",
		ResourceGroup: "rg", Location: "eastus", KubernetesVersion: "1.29.2",
		RBACEnabled: true, NetworkPolicy: "calico", PrivateCluster: true,
		AuthorizedIPRanges: true, ManagedIdentity: true, LocalAccountsDisabled: true,
		OIDCIssuerEnabled: true, NodePoolCount: 2,
		Result: aks.ResultPass, ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildAKSRecord(c, "prod", "scf:CFG-02")
	if err != nil {
		t.Fatalf("buildAKSRecord: %v", err)
	}
	if rec.EvidenceKind != "azure.aks_cluster_config.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.SchemaVersion != "1.0.0" {
		t.Errorf("schema version = %q; want 1.0.0", rec.SchemaVersion)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("result = %v; want PASS", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:azure:aks@") {
		t.Errorf("aks actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "azure:sub-1" {
		t.Errorf("cloud_account = %q; want azure:sub-1", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("environment = %q; want prod", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{
		"cluster_id", "cluster_name", "subscription_id", "rbac_enabled",
		"private_cluster", "authorized_ip_ranges", "resource_group", "location",
		"kubernetes_version", "network_policy", "managed_identity",
		"local_accounts_disabled", "oidc_issuer_enabled", "node_pool_count",
	} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	// Structural over-collection guard at the record layer (P0-519-1/3): no
	// payload key may name a secret / credential / kubeconfig / workload surface.
	for k := range pl {
		low := strings.ToLower(k)
		for _, banned := range []string{"secret", "credential", "kubeconfig", "password", "token", "manifest", "container", "image"} {
			if strings.Contains(low, banned) {
				t.Errorf("aks payload key %q contains banned over-collection token %q", k, banned)
			}
		}
	}
}

func TestBuildAKSRecord_OmitsEmptyOptionals(t *testing.T) {
	c := aks.ClusterConfig{
		ClusterID: "/sub/c", ClusterName: "c", SubscriptionID: "s",
		RBACEnabled: true, PrivateCluster: true, AuthorizedIPRanges: true,
		Result: aks.ResultPass, ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildAKSRecord(c, "prod", "scf:CFG-02")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"resource_group", "location", "kubernetes_version", "network_policy"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
	// Booleans + count always present (false / 0 is signal, not absence).
	for _, k := range []string{"managed_identity", "local_accounts_disabled", "oidc_issuer_enabled", "node_pool_count"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("boolean/count field %q must always be present", k)
		}
	}
}

func TestBuildNSGRecord_Shape(t *testing.T) {
	g := nsg.GroupConfig{
		NSGID: "/sub/nsg", NSGName: "nsg", SubscriptionID: "sub-1",
		ResourceGroup: "rg", Location: "eastus",
		AssociatedSubnets: 2, AssociatedNICs: 1,
		Rules: []nsg.SecurityRule{
			{Name: "allow-ssh-corp", Direction: "inbound", Access: "allow", Protocol: "tcp",
				Priority: 100, SourceAddressPrefix: "203.0.113.0/24",
				DestinationAddressPrefix: "*", SourcePortRange: "*", DestinationPortRange: "22"},
		},
		Result: nsg.ResultPass, ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildNSGRecord(g, "prod", "scf:NET-04")
	if err != nil {
		t.Fatalf("buildNSGRecord: %v", err)
	}
	if rec.EvidenceKind != "azure.nsg_rules.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.SchemaVersion != "1.0.0" {
		t.Errorf("schema version = %q; want 1.0.0", rec.SchemaVersion)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("result = %v; want PASS", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:azure:nsg@") {
		t.Errorf("nsg actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "azure:sub-1" {
		t.Errorf("cloud_account = %q; want azure:sub-1", got)
	}
	if got := scopeValue(rec.GetScope(), "environment"); got != "prod" {
		t.Errorf("environment = %q; want prod", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"nsg_id", "nsg_name", "subscription_id", "resource_group",
		"location", "associated_subnets", "associated_nics", "rules"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	rules, ok := pl["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("rules payload not a 1-item list: %v", pl["rules"])
	}
	rule, _ := rules[0].(map[string]any)
	for _, k := range []string{"name", "direction", "access", "protocol", "priority",
		"source_address_prefix", "destination_address_prefix", "source_port_range",
		"destination_port_range"} {
		if _, ok := rule[k]; !ok {
			t.Errorf("rule payload missing %q; got %v", k, rule)
		}
	}
	// Structural over-collection guard at the record layer (P0-520-2): no
	// payload key may name a flow-log / packet / traffic-content / secret surface.
	for k := range pl {
		low := strings.ToLower(k)
		for _, banned := range []string{"flowlog", "flow_log", "packet", "capture", "payload", "traffic", "secret", "credential", "password", "token"} {
			if strings.Contains(low, banned) {
				t.Errorf("nsg payload key %q contains banned over-collection token %q", k, banned)
			}
		}
	}
}

func TestBuildNSGRecord_OmitsEmptyOptionals(t *testing.T) {
	g := nsg.GroupConfig{
		NSGID: "/sub/nsg", NSGName: "nsg", SubscriptionID: "s",
		Rules:  []nsg.SecurityRule{{Name: "deny", Direction: "inbound", Access: "deny"}},
		Result: nsg.ResultPass, ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildNSGRecord(g, "prod", "scf:NET-04")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"resource_group", "location"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
	// Association counts always present (0 is signal, not absence).
	for _, k := range []string{"associated_subnets", "associated_nics"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("count field %q must always be present", k)
		}
	}
	// Rule with empty optionals omits them.
	rules := pl["rules"].([]any)
	rule := rules[0].(map[string]any)
	for _, k := range []string{"protocol", "source_address_prefix", "destination_port_range"} {
		if _, ok := rule[k]; ok {
			t.Errorf("empty rule optional %q should be omitted", k)
		}
	}
}

func TestBuildFirewallRecord_Shape(t *testing.T) {
	p := firewall.PolicyConfig{
		PolicyID: "/sub/fw", PolicyName: "fw", SubscriptionID: "sub-1",
		ResourceGroup: "rg", Location: "eastus",
		RuleCollectionGroups: []firewall.RuleCollectionGroup{
			{Name: "DefaultNetworkRuleCollectionGroup", Priority: 200, RuleCollections: []firewall.RuleCollection{
				{Name: "allow-corp", Type: "network", Action: "allow", Priority: 100, Rules: []firewall.Rule{
					{Name: "ssh", Protocols: []string{"tcp"}, SourceAddresses: []string{"203.0.113.0/24"},
						DestinationAddresses: []string{"10.0.0.0/8"}, DestinationPorts: []string{"22"}}}},
			}},
			{Name: "DefaultApplicationRuleCollectionGroup", Priority: 300, RuleCollections: []firewall.RuleCollection{
				{Name: "allow-fqdn", Type: "application", Action: "allow", Priority: 100, Rules: []firewall.Rule{
					{Name: "to-updates", Protocols: []string{"https"}, SourceAddresses: []string{"10.0.0.0/8"},
						DestinationFQDNs: []string{"updates.example.com"}}}},
			}},
		},
		Result: firewall.ResultPass, ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
	rec, err := buildFirewallRecord(p, "prod", "scf:NET-04")
	if err != nil {
		t.Fatalf("buildFirewallRecord: %v", err)
	}
	if rec.EvidenceKind != "azure.firewall_rules.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.SchemaVersion != "1.0.0" {
		t.Errorf("schema version = %q; want 1.0.0", rec.SchemaVersion)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("result = %v; want PASS", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:azure:firewall@") {
		t.Errorf("firewall actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(rec.GetScope(), "cloud_account"); got != "azure:sub-1" {
		t.Errorf("cloud_account = %q; want azure:sub-1", got)
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"policy_id", "policy_name", "subscription_id", "resource_group", "location", "rule_collection_groups"} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	groups, ok := pl["rule_collection_groups"].([]any)
	if !ok || len(groups) != 2 {
		t.Fatalf("rule_collection_groups payload not a 2-item list: %v", pl["rule_collection_groups"])
	}
	g0 := groups[0].(map[string]any)
	if g0["priority"].(float64) != 200 {
		t.Errorf("group priority not preserved: %v", g0["priority"])
	}
	cols := g0["rule_collections"].([]any)
	col := cols[0].(map[string]any)
	for _, k := range []string{"name", "type", "action", "priority", "rules"} {
		if _, ok := col[k]; !ok {
			t.Errorf("collection payload missing %q; got %v", k, col)
		}
	}
	rule := col["rules"].([]any)[0].(map[string]any)
	for _, k := range []string{"name", "protocols", "source_addresses", "destination_addresses", "destination_ports"} {
		if _, ok := rule[k]; !ok {
			t.Errorf("rule payload missing %q; got %v", k, rule)
		}
	}
	// Structural over-collection guard at the record layer (P0-614-2): no payload
	// key may name a flow-log / packet / traffic / NAT-secret / threat-intel /
	// route-table surface.
	assertNoFirewallOverCollection(t, pl)
	for _, raw := range groups {
		for _, rawCol := range raw.(map[string]any)["rule_collections"].([]any) {
			assertNoFirewallOverCollection(t, rawCol.(map[string]any))
			for _, rawRule := range rawCol.(map[string]any)["rules"].([]any) {
				assertNoFirewallOverCollection(t, rawRule.(map[string]any))
			}
		}
	}
}

func assertNoFirewallOverCollection(t *testing.T, m map[string]any) {
	t.Helper()
	banned := []string{"flowlog", "flow_log", "packet", "capture", "traffic", "secret",
		"credential", "password", "token", "threatintel", "threat_intel", "routetable", "route_table"}
	for k := range m {
		low := strings.ToLower(k)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("firewall payload key %q contains banned over-collection token %q", k, b)
			}
		}
	}
}

func TestBuildFirewallRecord_OmitsEmptyOptionals(t *testing.T) {
	p := firewall.PolicyConfig{
		PolicyID: "/sub/fw", PolicyName: "fw", SubscriptionID: "s",
		RuleCollectionGroups: []firewall.RuleCollectionGroup{
			{Name: "g", Priority: 100, RuleCollections: []firewall.RuleCollection{
				{Name: "deny", Type: "network", Action: "deny", Priority: 4096, Rules: []firewall.Rule{
					{Name: "r"}}}}},
		},
		Result: firewall.ResultPass, ObservedAt: time.Now().UTC(),
	}
	rec, _ := buildFirewallRecord(p, "prod", "scf:NET-04")
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{"resource_group", "location"} {
		if _, ok := pl[k]; ok {
			t.Errorf("empty optional %q should be omitted", k)
		}
	}
	// A rule with no list fields omits all of them but keeps its name.
	rule := pl["rule_collection_groups"].([]any)[0].(map[string]any)["rule_collections"].([]any)[0].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if _, ok := rule["name"]; !ok {
		t.Error("rule name must always be present")
	}
	for _, k := range []string{"protocols", "source_addresses", "destination_ports", "destination_fqdns"} {
		if _, ok := rule[k]; ok {
			t.Errorf("empty rule list field %q should be omitted", k)
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

// TestDoRun_FailsOnMissingCredential drives doRun's first error branch:
// azureauth.Resolve fails when no tenant id is set.
func TestDoRun_FailsOnMissingCredential(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")

	err := doRun(context.Background(), runFlags{environment: "prod", authMode: "client-credentials", skipStorage: true})
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}
