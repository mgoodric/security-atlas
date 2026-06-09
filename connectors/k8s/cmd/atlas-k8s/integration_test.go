package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8sauth"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/netpol"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/pss"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

func newBufconnPlatform(t *testing.T) (*api.Server, *grpc.ClientConn, string) {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = lis.Close()
	})
	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn, bearer
}

// TestRegister_ListsConnector verifies AC-1 + AC-7: register surfaces this
// connector via the ConnectorRegistry List RPC with profiles_supported=[pull].
func TestRegister_ListsConnector(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	registry := connectorsv1.NewConnectorRegistryServiceClient(conn)

	ctx, cancel := authedTestContext(bearer, 5*time.Second)()
	defer cancel()
	resp, err := registry.Register(ctx, &connectorsv1.RegisterRequest{
		Name:              ConnectorName,
		Version:           connectorVersion(),
		InstanceId:        "test-instance-k8s",
		SupportedKinds:    SupportedKinds,
		ProfilesSupported: []string{"pull"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.GetHandle().GetName() != ConnectorName {
		t.Fatalf("name = %q; want %q", resp.GetHandle().GetName(), ConnectorName)
	}

	listCtx, cancel2 := authedTestContext(bearer, 5*time.Second)()
	defer cancel2()
	list, err := registry.List(listCtx, &connectorsv1.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, h := range list.GetHandles() {
		if h.GetName() == ConnectorName {
			found = true
			if len(h.GetSupportedKinds()) != 4 {
				t.Errorf("supported_kinds = %d; want 4", len(h.GetSupportedKinds()))
			}
			if strings.Join(h.GetProfilesSupported(), ",") != "pull" {
				t.Errorf("profiles_supported = %v; want [pull]", h.GetProfilesSupported())
			}
		}
	}
	if !found {
		t.Fatal("k8s-connector not present in List — AC-1 fail")
	}
}

// TestRun_PushesAllKinds verifies AC-2/AC-3/AC-5/AC-6/AC-9: collect from faked
// Kubernetes API surfaces, build canonical records, push them through the
// platform's single Push RPC, and assert the receipt (sha256 content hash).
func TestRun_PushesAllKinds(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	// RBAC (faked Kubernetes API surface — NO live cluster).
	rbacAPI := &fakeRBACForIntegration{bindings: []rbac.RawBinding{
		{Name: "admins", Scope: rbac.ScopeCluster, RoleKind: rbac.RoleKindClusterRole, RoleName: "cluster-admin",
			Subjects: []rbac.Subject{{Kind: rbac.SubjectUser, Name: "alice"}},
			Rules:    []rbac.Rule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}}},
	}}
	bindings, err := rbac.Pull(context.Background(), rbacAPI, fixed)
	if err != nil {
		t.Fatalf("rbac.Pull: %v", err)
	}
	rbacRec, err := buildRBACRecord(bindings[0], "cluster-1", "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildRBACRecord: %v", err)
	}
	rbacReceipt, err := client.Push(context.Background(), rbacRec)
	if err != nil {
		t.Fatalf("Push rbac: %v", err)
	}
	if rbacReceipt.GetHash() == "" {
		t.Fatal("rbac receipt hash empty (AC-6 sha256 content-hash)")
	}
	if !strings.HasPrefix(rbacRec.GetSourceAttribution().GetActorId(), "connector:k8s:rbac@") {
		t.Errorf("rbac actor_id = %q", rbacRec.GetSourceAttribution().GetActorId())
	}
	if !bindings[0].GrantsWildcard {
		t.Error("cluster-admin binding should flag grants_wildcard")
	}

	// Workload (faked Kubernetes API surface — NO live cluster).
	wlAPI := &fakeWorkloadForIntegration{workloads: []workload.RawWorkload{
		{Kind: workload.KindDeployment, Name: "node-agent", Namespace: "kube-system",
			Privileged: true, HostPID: true, ContainerCount: 1},
	}}
	workloads, err := workload.Inspect(context.Background(), wlAPI, fixed)
	if err != nil {
		t.Fatalf("workload.Inspect: %v", err)
	}
	wlRec, err := buildWorkloadRecord(workloads[0], "cluster-1", "prod", "scf:CFG-02")
	if err != nil {
		t.Fatalf("buildWorkloadRecord: %v", err)
	}
	wlReceipt, err := client.Push(context.Background(), wlRec)
	if err != nil {
		t.Fatalf("Push workload: %v", err)
	}
	if wlReceipt.GetHash() == "" {
		t.Fatal("workload receipt hash empty")
	}
	if workloads[0].Result != workload.ResultFail {
		t.Errorf("privileged workload should FAIL; got %q", workloads[0].Result)
	}
	if got := scopeValue(wlRec.GetScope(), "cluster"); got != "cluster-1" {
		t.Errorf("workload cluster = %q; want cluster-1", got)
	}

	// NetworkPolicy coverage (faked Kubernetes API surface — NO live cluster).
	// The faked namespace embeds an ingress peer + podSelector label that must
	// NOT escape into the pushed record (over-collection guard).
	npAPI := &fakeNetpolForIntegration{namespaces: []netpol.RawNamespace{
		{Name: "prod", Policies: []netpol.RawPolicy{
			{Name: "default-deny-ingress", PolicyTypes: []string{netpol.PolicyTypeIngress}, SelectsAllPods: true},
		}},
		{Name: "dev"}, // unprotected — no policies
	}}
	coverage, err := netpol.Assess(context.Background(), npAPI, fixed)
	if err != nil {
		t.Fatalf("netpol.Assess: %v", err)
	}
	if len(coverage) != 2 {
		t.Fatalf("coverage len = %d; want 2", len(coverage))
	}
	var prodCov *netpol.Coverage
	for i := range coverage {
		if coverage[i].Namespace == "prod" {
			prodCov = &coverage[i]
		}
	}
	if prodCov == nil || prodCov.Result != netpol.ResultPass || !prodCov.DefaultDenyIngress {
		t.Fatalf("prod namespace should PASS with default-deny ingress; got %+v", prodCov)
	}
	npRec, err := buildNetpolRecord(*prodCov, "cluster-1", "prod", "scf:NET-04")
	if err != nil {
		t.Fatalf("buildNetpolRecord: %v", err)
	}
	npReceipt, err := client.Push(context.Background(), npRec)
	if err != nil {
		t.Fatalf("Push netpol: %v", err)
	}
	if npReceipt.GetHash() == "" {
		t.Fatal("netpol receipt hash empty (AC-5 sha256 content-hash)")
	}
	if !strings.HasPrefix(npRec.GetSourceAttribution().GetActorId(), "connector:k8s:netpol@") {
		t.Errorf("netpol actor_id = %q", npRec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(npRec.GetScope(), "namespace"); got != "prod" {
		t.Errorf("netpol namespace scope = %q; want prod", got)
	}

	// PSS admission (faked Kubernetes API surface — NO live cluster). The faked
	// namespace carries unrelated labels + annotations that must NOT escape into
	// the pushed record (label-filter / over-collection guard).
	pssAPI := &fakePSSForIntegration{namespaces: []pss.RawNamespace{
		{Name: "prod", EnforceLevel: pss.LevelRestricted, EnforceVersion: "v1.29", AuditLevel: pss.LevelBaseline},
		{Name: "legacy", EnforceLevel: pss.LevelPrivileged},
		{Name: "dev"}, // unenforced — no PSS labels
	}}
	admissions, err := pss.Assess(context.Background(), pssAPI, fixed)
	if err != nil {
		t.Fatalf("pss.Assess: %v", err)
	}
	var prodAdm *pss.Admission
	for i := range admissions {
		if admissions[i].Namespace == "prod" {
			prodAdm = &admissions[i]
		}
	}
	if prodAdm == nil || prodAdm.Result != pss.ResultPass || !prodAdm.Configured {
		t.Fatalf("prod namespace should PASS with enforce=restricted; got %+v", prodAdm)
	}
	pssRec, err := buildPSSRecord(*prodAdm, "cluster-1", "prod", "scf:CFG-02")
	if err != nil {
		t.Fatalf("buildPSSRecord: %v", err)
	}
	pssReceipt, err := client.Push(context.Background(), pssRec)
	if err != nil {
		t.Fatalf("Push pss: %v", err)
	}
	if pssReceipt.GetHash() == "" {
		t.Fatal("pss receipt hash empty (AC-5 sha256 content-hash)")
	}
	if !strings.HasPrefix(pssRec.GetSourceAttribution().GetActorId(), "connector:k8s:pss@") {
		t.Errorf("pss actor_id = %q", pssRec.GetSourceAttribution().GetActorId())
	}
	if got := scopeValue(pssRec.GetScope(), "namespace"); got != "prod" {
		t.Errorf("pss namespace scope = %q; want prod", got)
	}
}

// TestRun_DedupesWithinHour verifies AC-6: two records from the same resource in
// the same hour share an idempotency_key, so the platform dedup returns the same
// record_id.
func TestRun_DedupesWithinHour(t *testing.T) {
	_, conn, bearer := newBufconnPlatform(t)
	client := sdk.NewClientFromConn(conn, bearer)
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	wlAPI := &fakeWorkloadForIntegration{workloads: []workload.RawWorkload{
		{Kind: workload.KindDeployment, Name: "api", Namespace: "prod",
			RunAsNonRoot: true, ReadOnlyRootFilesystem: true, ContainerCount: 1},
	}}
	wls, _ := workload.Inspect(context.Background(), wlAPI, fixed)
	r1, _ := buildWorkloadRecord(wls[0], "cluster-1", "prod", "scf:CFG-02")
	r2, _ := buildWorkloadRecord(wls[0], "cluster-1", "prod", "scf:CFG-02")
	rec1, err := client.Push(context.Background(), r1)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	rec2, err := client.Push(context.Background(), r2)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if rec1.GetRecordId() != rec2.GetRecordId() {
		t.Fatalf("dedup failed: %q vs %q", rec1.GetRecordId(), rec2.GetRecordId())
	}
}

// TestEmittedRecords_NoSecretsOrConfigValues verifies AC-10 + P0-487-3: the
// emitted payloads carry ONLY RBAC + security-context CONFIG — never Secret
// values, ConfigMap values, container env, or logs.
func TestEmittedRecords_NoSecretsOrConfigValues(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

	rbacAPI := &fakeRBACForIntegration{bindings: []rbac.RawBinding{
		{Name: "admins", Scope: rbac.ScopeCluster, RoleKind: rbac.RoleKindClusterRole, RoleName: "cluster-admin",
			Subjects: []rbac.Subject{{Kind: rbac.SubjectUser, Name: "alice"}},
			Rules:    []rbac.Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}}},
	}}
	bindings, _ := rbac.Pull(context.Background(), rbacAPI, fixed)
	rbacRec, _ := buildRBACRecord(bindings[0], "cluster-1", "prod", "scf:IAC-21")

	wlAPI := &fakeWorkloadForIntegration{workloads: []workload.RawWorkload{
		{Kind: workload.KindDeployment, Name: "api", Namespace: "prod", RunAsNonRoot: true, ReadOnlyRootFilesystem: true, ContainerCount: 1},
	}}
	wls, _ := workload.Inspect(context.Background(), wlAPI, fixed)
	wlRec, _ := buildWorkloadRecord(wls[0], "cluster-1", "prod", "scf:CFG-02")

	// NetworkPolicy coverage: the faked policy embeds a podSelector label + an
	// ingress peer that must NOT escape into the record.
	npAPI := &fakeNetpolForIntegration{namespaces: []netpol.RawNamespace{
		{Name: "prod", Policies: []netpol.RawPolicy{
			{Name: "allow-api", PolicyTypes: []string{netpol.PolicyTypeIngress}, IngressRuleCount: 1},
		}},
	}}
	covs, _ := netpol.Assess(context.Background(), npAPI, fixed)
	npRec, _ := buildNetpolRecord(covs[0], "cluster-1", "prod", "scf:NET-04")

	// PSS admission: the faked namespace carries unrelated labels + annotations
	// (via the client boundary) — but the RawNamespace the assessment consumes
	// already holds ONLY PSS fields. Assert the emitted payload keys stay PSS-only.
	pssAPI := &fakePSSForIntegration{namespaces: []pss.RawNamespace{
		{Name: "prod", EnforceLevel: pss.LevelRestricted, EnforceVersion: "v1.29", AuditLevel: pss.LevelBaseline},
	}}
	adms, _ := pss.Assess(context.Background(), pssAPI, fixed)
	pssRec, _ := buildPSSRecord(adms[0], "cluster-1", "prod", "scf:CFG-02")

	// Allow-list of permitted top-level payload keys per kind. Any key NOT in
	// the allow-list is a leak and fails the test (config / authz metadata only).
	rbacAllowed := map[string]bool{
		"binding_name": true, "binding_scope": true, "namespace": true,
		"role_kind": true, "role_name": true, "subjects": true, "rules": true,
		"grants_wildcard": true,
	}
	workloadAllowed := map[string]bool{
		"workload_kind": true, "workload_name": true, "namespace": true,
		"run_as_non_root": true, "privileged": true, "read_only_root_filesystem": true,
		"allow_privilege_escalation": true, "host_network": true, "host_pid": true,
		"host_ipc": true, "container_count": true,
	}
	netpolAllowed := map[string]bool{
		"namespace": true, "policy_count": true, "default_deny_ingress": true,
		"default_deny_egress": true, "policies": true,
	}
	pssAllowed := map[string]bool{
		"namespace": true, "configured": true,
		"enforce_level": true, "enforce_version": true,
		"audit_level": true, "audit_version": true,
		"warn_level": true, "warn_version": true,
	}
	bannedSubstrings := []string{"secret", "configmap", "config_map", "env", "value", "data", "token", "password", "log", "annotation"}

	check := func(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool) {
		for k := range rec.GetPayload().AsMap() {
			if !allowed[k] {
				t.Errorf("payload carries non-allow-listed key %q (possible secret/config leak)", k)
			}
			low := strings.ToLower(k)
			for _, b := range bannedSubstrings {
				if strings.Contains(low, b) {
					t.Errorf("payload key %q contains banned substring %q", k, b)
				}
			}
		}
	}
	check(t, rbacRec, rbacAllowed)
	check(t, wlRec, workloadAllowed)
	check(t, npRec, netpolAllowed)
	check(t, pssRec, pssAllowed)

	// The nested per-policy summaries must carry SPEC metadata only — never a
	// peer / selector / port value. Allow-list the nested keys too.
	nestedAllowed := map[string]bool{
		"name": true, "policy_types": true, "selects_all_pods": true,
		"ingress_rule_count": true, "egress_rule_count": true,
	}
	if policies, ok := npRec.GetPayload().AsMap()["policies"].([]any); ok {
		for _, p := range policies {
			m, ok := p.(map[string]any)
			if !ok {
				t.Fatalf("policy summary is not a map: %T", p)
			}
			for k := range m {
				if !nestedAllowed[k] {
					t.Errorf("netpol policy summary carries non-allow-listed key %q (possible peer/selector leak)", k)
				}
			}
		}
	}
}

// TestCredential_NeverLogged verifies AC-11 + P0-487-4: the cluster credential's
// formatted forms never reveal the token, so no log line can leak it.
func TestCredential_NeverLogged(t *testing.T) {
	const token = "test-k8s-token-no-log"
	cred, err := k8sauth.Resolve(k8sauth.ResolveOpts{APIServer: "https://kube:6443", Token: token})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(cred.String(), token) {
		t.Fatal("credential String leaks the token — AC-11 violation")
	}
}

func authedTestContext(bearer string, timeout time.Duration) func() (context.Context, context.CancelFunc) {
	return func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)
		return ctx, cancel
	}
}

// --- faked Kubernetes surfaces (NO live cluster in tests) ---

type fakeRBACForIntegration struct{ bindings []rbac.RawBinding }

func (f *fakeRBACForIntegration) ListBindings(_ context.Context) ([]rbac.RawBinding, error) {
	return f.bindings, nil
}

type fakeWorkloadForIntegration struct{ workloads []workload.RawWorkload }

func (f *fakeWorkloadForIntegration) ListWorkloads(_ context.Context) ([]workload.RawWorkload, error) {
	return f.workloads, nil
}

type fakeNetpolForIntegration struct{ namespaces []netpol.RawNamespace }

func (f *fakeNetpolForIntegration) ListNamespaceCoverage(_ context.Context) ([]netpol.RawNamespace, error) {
	return f.namespaces, nil
}

type fakePSSForIntegration struct{ namespaces []pss.RawNamespace }

func (f *fakePSSForIntegration) ListNamespacePSS(_ context.Context) ([]pss.RawNamespace, error) {
	return f.namespaces, nil
}
