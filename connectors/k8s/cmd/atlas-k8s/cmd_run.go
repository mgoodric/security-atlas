package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8sauth"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/netpol"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the two Kubernetes reads + the sdk client constructor
// without hitting a live cluster or a real platform endpoint. Production code
// paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	rbacPull     = rbac.Pull
	workloadScan = workload.Inspect
	netpolScan   = netpol.Assess
	newSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newRBACAPI / newWorkloadAPI / newNetpolAPI build the live read-only HTTP
	// clients; seamed so tests inject fakes.
	newRBACAPI = func(hc *http.Client, baseURL, token string) rbac.API {
		return rbac.NewClient(hc, baseURL, token)
	}
	newWorkloadAPI = func(hc *http.Client, baseURL, token string) workload.API {
		return workload.NewClient(hc, baseURL, token)
	}
	newNetpolAPI = func(hc *http.Client, baseURL, token string) netpol.API {
		return netpol.NewClient(hc, baseURL, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	cluster         string
	environment     string
	authMode        string
	apiServer       string
	rbacControl     string
	workloadControl string
	netpolControl   string
	skipRBAC        bool
	skipWorkload    bool
	skipNetpol      bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Kubernetes RBAC + workload security contexts + NetworkPolicy coverage and push evidence records",
		Long: `Read Kubernetes RBAC roles + bindings, workload security contexts, and
NetworkPolicy coverage via the read-only Kubernetes API, transform to evidence
records, and push to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Least-privilege Kubernetes access (read-only ClusterRole — verbs get,list only):
  - rbac.authorization.k8s.io: roles/clusterroles/rolebindings/clusterrolebindings
  - apps: deployments/daemonsets/statefulsets
  - networking.k8s.io: networkpolicies
  - core: namespaces
NEVER 'secrets', NEVER write verbs, NEVER cluster-admin / wildcards.

Auth: set KUBERNETES_API_SERVER + KUBECONFIG_TOKEN (out-of-cluster), or pass
--auth-mode in-cluster. The token never appears in a log line or an evidence
record.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.cluster == "" {
				return errors.New("--cluster is required (records must be scoped to a cluster)")
			}
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			if _, err := k8sauth.ParseMode(f.authMode); err != nil {
				return err
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "cluster identifier [required] (scopes every record)")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.authMode, "auth-mode", "kubeconfig-token", "auth mode: kubeconfig-token | in-cluster")
	cmd.Flags().StringVar(&f.apiServer, "api-server", "", "Kubernetes API server URL (env: KUBERNETES_API_SERVER)")
	cmd.Flags().StringVar(&f.rbacControl, "rbac-control", "scf:IAC-21", "control_id to attach to k8s.rbac_binding.v1 records")
	cmd.Flags().StringVar(&f.workloadControl, "workload-control", "scf:CFG-02", "control_id to attach to k8s.workload_security_context.v1 records")
	cmd.Flags().StringVar(&f.netpolControl, "netpol-control", "scf:NET-04", "control_id to attach to k8s.networkpolicy_coverage.v1 records")
	cmd.Flags().BoolVar(&f.skipRBAC, "skip-rbac", false, "skip k8s.rbac_binding.v1 pull")
	cmd.Flags().BoolVar(&f.skipWorkload, "skip-workload", false, "skip k8s.workload_security_context.v1 pull")
	cmd.Flags().BoolVar(&f.skipNetpol, "skip-netpol", false, "skip k8s.networkpolicy_coverage.v1 pull")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	mode, err := k8sauth.ParseMode(f.authMode)
	if err != nil {
		return err
	}
	cred, err := k8sauth.Resolve(k8sauth.ResolveOpts{
		Mode:      mode,
		APIServer: f.apiServer,
		// Token is read from env / projected mount only — never a CLI flag (it
		// would land in shell history).
	})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	pushed := 0

	if !f.skipRBAC {
		bindings, err := rbacPull(ctx, newRBACAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("rbac pull: %w", err)
		}
		for _, b := range bindings {
			rec, err := buildRBACRecord(b, f.cluster, f.environment, f.rbacControl)
			if err != nil {
				return fmt.Errorf("build rbac record %s: %w", b.BindingName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push rbac %s: %w", b.BindingName, err)
			}
			pushed++
		}
	}

	if !f.skipWorkload {
		workloads, err := workloadScan(ctx, newWorkloadAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("workload inspect: %w", err)
		}
		for _, w := range workloads {
			rec, err := buildWorkloadRecord(w, f.cluster, f.environment, f.workloadControl)
			if err != nil {
				return fmt.Errorf("build workload record %s: %w", w.WorkloadName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push workload %s: %w", w.WorkloadName, err)
			}
			pushed++
		}
	}

	if !f.skipNetpol {
		coverage, err := netpolScan(ctx, newNetpolAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("netpol assess: %w", err)
		}
		for _, c := range coverage {
			rec, err := buildNetpolRecord(c, f.cluster, f.environment, f.netpolControl)
			if err != nil {
				return fmt.Errorf("build netpol record %s: %w", c.Namespace, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push netpol %s: %w", c.Namespace, err)
			}
			pushed++
		}
	}

	fmt.Printf("pushed %d records (cluster=%s environment=%s)\n", pushed, f.cluster, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}

func buildRBACRecord(b rbac.Binding, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := b.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"binding_name":    b.BindingName,
		"binding_scope":   b.BindingScope,
		"role_kind":       b.RoleKind,
		"role_name":       b.RoleName,
		"grants_wildcard": b.GrantsWildcard,
	}
	if b.Namespace != "" {
		pm["namespace"] = b.Namespace
	}
	if len(b.Subjects) > 0 {
		subs := make([]any, 0, len(b.Subjects))
		for _, s := range b.Subjects {
			m := map[string]any{"kind": s.Kind, "name": s.Name}
			if s.Namespace != "" {
				m["namespace"] = s.Namespace
			}
			subs = append(subs, m)
		}
		pm["subjects"] = subs
	}
	if len(b.Rules) > 0 {
		rules := make([]any, 0, len(b.Rules))
		for _, r := range b.Rules {
			rules = append(rules, map[string]any{
				"api_groups": toAnySlice(r.APIGroups),
				"resources":  toAnySlice(r.Resources),
				"verbs":      toAnySlice(r.Verbs),
			})
		}
		pm["rules"] = rules
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.RBACBindingKey(b.BindingScope, b.Namespace, b.BindingName, now),
		EvidenceKind:   "k8s.rbac_binding.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cluster", Values: []string{cluster}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("rbac"),
		},
	}, nil
}

func buildWorkloadRecord(w workload.SecurityContext, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := w.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"workload_kind":              w.WorkloadKind,
		"workload_name":              w.WorkloadName,
		"namespace":                  w.Namespace,
		"run_as_non_root":            w.RunAsNonRoot,
		"privileged":                 w.Privileged,
		"read_only_root_filesystem":  w.ReadOnlyRootFilesystem,
		"allow_privilege_escalation": w.AllowPrivilegeEscalation,
		"host_network":               w.HostNetwork,
		"host_pid":                   w.HostPID,
		"host_ipc":                   w.HostIPC,
		"container_count":            float64(w.ContainerCount),
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.WorkloadKey(w.WorkloadKind, w.Namespace, w.WorkloadName, now),
		EvidenceKind:   "k8s.workload_security_context.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cluster", Values: []string{cluster}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapWorkloadResult(w.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("workload"),
		},
	}, nil
}

func buildNetpolRecord(c netpol.Coverage, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := c.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"namespace":            c.Namespace,
		"policy_count":         float64(c.PolicyCount),
		"default_deny_ingress": c.DefaultDenyIngress,
		"default_deny_egress":  c.DefaultDenyEgress,
	}
	if len(c.Policies) > 0 {
		policies := make([]any, 0, len(c.Policies))
		for _, p := range c.Policies {
			m := map[string]any{
				"name":               p.Name,
				"selects_all_pods":   p.SelectsAllPods,
				"ingress_rule_count": float64(p.IngressRuleCount),
				"egress_rule_count":  float64(p.EgressRuleCount),
			}
			if len(p.PolicyTypes) > 0 {
				m["policy_types"] = toAnySlice(p.PolicyTypes)
			}
			policies = append(policies, m)
		}
		pm["policies"] = policies
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.NetpolCoverageKey(c.Namespace, now),
		EvidenceKind:   "k8s.networkpolicy_coverage.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cluster", Values: []string{cluster}},
			{Key: "environment", Values: []string{env}},
			{Key: "namespace", Values: []string{c.Namespace}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapNetpolResult(c.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("netpol"),
		},
	}, nil
}

func mapNetpolResult(r netpol.CoverageResult) evidencev1.Result {
	switch r {
	case netpol.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case netpol.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case netpol.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

func mapWorkloadResult(r workload.ConfigResult) evidencev1.Result {
	switch r {
	case workload.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case workload.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case workload.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

func toAnySlice(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}
