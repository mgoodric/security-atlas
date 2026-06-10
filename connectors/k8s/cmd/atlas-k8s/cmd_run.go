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

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/admission"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8sauth"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/netpol"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/pss"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/secretmeta"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the two Kubernetes reads + the sdk client constructor
// without hitting a live cluster or a real platform endpoint. Production code
// paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	rbacPull             = rbac.Pull
	workloadScan         = workload.Inspect
	netpolScan           = netpol.Assess
	pssScan              = pss.Assess
	secretMetaScan       = secretmeta.Collect
	admissionWebhookScan = admission.CollectWebhooks
	admissionPolicyScan  = admission.CollectPolicies
	newSDKClient         = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newRBACAPI / newWorkloadAPI / newNetpolAPI / newPSSAPI build the live
	// read-only HTTP clients; seamed so tests inject fakes.
	newRBACAPI = func(hc *http.Client, baseURL, token string) rbac.API {
		return rbac.NewClient(hc, baseURL, token)
	}
	newWorkloadAPI = func(hc *http.Client, baseURL, token string) workload.API {
		return workload.NewClient(hc, baseURL, token)
	}
	newNetpolAPI = func(hc *http.Client, baseURL, token string) netpol.API {
		return netpol.NewClient(hc, baseURL, token)
	}
	newPSSAPI = func(hc *http.Client, baseURL, token string) pss.API {
		return pss.NewClient(hc, baseURL, token)
	}
	newSecretMetaAPI = func(hc *http.Client, baseURL, token string) secretmeta.API {
		return secretmeta.NewClient(hc, baseURL, token)
	}
	newAdmissionWebhookAPI = func(hc *http.Client, baseURL, token string) admission.WebhookAPI {
		return admission.NewWebhookClient(hc, baseURL, token)
	}
	newAdmissionPolicyAPI = func(hc *http.Client, baseURL, token string) admission.PolicyAPI {
		return admission.NewPolicyClient(hc, baseURL, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	cluster          string
	environment      string
	authMode         string
	apiServer        string
	rbacControl      string
	workloadControl  string
	netpolControl    string
	pssControl       string
	secretControl    string
	webhookControl   string
	policyControl    string
	skipRBAC         bool
	skipWorkload     bool
	skipNetpol       bool
	skipPSS          bool
	skipAdmissionWH  bool
	skipAdmissionPol bool
	// collectSecretInventory is OPT-IN (default false): the k8s.secret_inventory.v1
	// kind requires the one `secrets` get/list ClusterRole grant the base
	// connector intentionally withholds (slice 525). It is never collected
	// unless the operator explicitly enables it AND has granted the rule.
	collectSecretInventory bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Kubernetes RBAC + workload security contexts + NetworkPolicy coverage + Pod-Security-Standards admission config and push evidence records",
		Long: `Read Kubernetes RBAC roles + bindings, workload security contexts,
NetworkPolicy coverage, and Pod-Security-Standards admission configuration via
the read-only Kubernetes API, transform to evidence records, and push to the
platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Least-privilege Kubernetes access (read-only ClusterRole — verbs get,list only):
  - rbac.authorization.k8s.io: roles/clusterroles/rolebindings/clusterrolebindings
  - apps: deployments/daemonsets/statefulsets
  - networking.k8s.io: networkpolicies
  - cilium.io: ciliumnetworkpolicies/ciliumclusterwidenetworkpolicies
    (optional — only read when the Cilium CRD is present in the cluster)
  - crd.projectcalico.org: networkpolicies/globalnetworkpolicies
    (optional — only read when the Calico CRD is present in the cluster)
  - admissionregistration.k8s.io: validatingwebhookconfigurations/
    mutatingwebhookconfigurations  (NEW in slice 652 — gates
    k8s.admission_webhook.v1; CONFIG metadata only, NEVER the caBundle/TLS key)
  - templates.gatekeeper.sh: constrainttemplates  (optional — OPA/Gatekeeper
    policy catalog; only read when the CRD is present)
  - kyverno.io: clusterpolicies/policies  (optional — Kyverno policy CONFIG
    metadata; only read when the CRD is present)
  - core: namespaces  (also gates the Pod-Security-Standards admission kind —
    PSS config lives in pod-security.kubernetes.io/* labels on the namespace;
    NO new ClusterRole rule is required)
NEVER write verbs, NEVER cluster-admin / wildcards. The base ClusterRole
deliberately EXCLUDES 'secrets'.

OPT-IN (--collect-secret-inventory, slice 525): adds the k8s.secret_inventory.v1
kind, which needs EXACTLY ONE extra rule — core 'secrets' verbs get,list. Even
then the connector reads Secret METADATA ONLY (type/namespace/name/age/
key-NAMES); it NEVER reads, decodes, or records a Secret VALUE.

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
	cmd.Flags().StringVar(&f.pssControl, "pss-control", "scf:CFG-02", "control_id to attach to k8s.pod_security_admission.v1 records")
	cmd.Flags().StringVar(&f.secretControl, "secret-control", "scf:CRY-01", "control_id to attach to k8s.secret_inventory.v1 records")
	cmd.Flags().StringVar(&f.webhookControl, "webhook-control", "scf:CFG-02", "control_id to attach to k8s.admission_webhook.v1 records")
	cmd.Flags().StringVar(&f.policyControl, "policy-control", "scf:CFG-02", "control_id to attach to k8s.admission_policy.v1 records")
	cmd.Flags().BoolVar(&f.skipRBAC, "skip-rbac", false, "skip k8s.rbac_binding.v1 pull")
	cmd.Flags().BoolVar(&f.skipWorkload, "skip-workload", false, "skip k8s.workload_security_context.v1 pull")
	cmd.Flags().BoolVar(&f.skipNetpol, "skip-netpol", false, "skip k8s.networkpolicy_coverage.v1 pull")
	cmd.Flags().BoolVar(&f.skipPSS, "skip-pss", false, "skip k8s.pod_security_admission.v1 pull")
	cmd.Flags().BoolVar(&f.skipAdmissionWH, "skip-admission-webhooks", false, "skip k8s.admission_webhook.v1 pull")
	cmd.Flags().BoolVar(&f.skipAdmissionPol, "skip-admission-policies", false, "skip k8s.admission_policy.v1 pull (OPA/Gatekeeper + Kyverno; only emits when an engine is installed)")
	cmd.Flags().BoolVar(&f.collectSecretInventory, "collect-secret-inventory", false,
		"OPT-IN: collect k8s.secret_inventory.v1 (Secret METADATA only — type/namespace/name/age/key-NAMES, NEVER a value). Requires the one extra 'secrets' get/list ClusterRole grant the base connector withholds (see 'permissions --secret-inventory').")
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

	if !f.skipPSS {
		admissions, err := pssScan(ctx, newPSSAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("pss assess: %w", err)
		}
		for _, a := range admissions {
			rec, err := buildPSSRecord(a, f.cluster, f.environment, f.pssControl)
			if err != nil {
				return fmt.Errorf("build pss record %s: %w", a.Namespace, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push pss %s: %w", a.Namespace, err)
			}
			pushed++
		}
	}

	if !f.skipAdmissionWH {
		webhooks, err := admissionWebhookScan(ctx, newAdmissionWebhookAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("admission-webhook collect: %w", err)
		}
		for _, w := range webhooks {
			rec, err := buildAdmissionWebhookRecord(w, f.cluster, f.environment, f.webhookControl)
			if err != nil {
				return fmt.Errorf("build admission-webhook record %s/%s: %w", w.ConfigName, w.WebhookName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push admission-webhook %s/%s: %w", w.ConfigName, w.WebhookName, err)
			}
			pushed++
		}
	}

	// Admission policy-engine collection: OPA/Gatekeeper + Kyverno, detected by
	// API-discovery probe — an absent engine contributes nothing (no hard-fail).
	// CONFIG metadata only — never the policy's Rego/CEL decision-logic body.
	if !f.skipAdmissionPol {
		policies, err := admissionPolicyScan(ctx, newAdmissionPolicyAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("admission-policy collect: %w", err)
		}
		for _, p := range policies {
			rec, err := buildAdmissionPolicyRecord(p, f.cluster, f.environment, f.policyControl)
			if err != nil {
				return fmt.Errorf("build admission-policy record %s/%s: %w", p.Engine, p.Name, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push admission-policy %s/%s: %w", p.Engine, p.Name, err)
			}
			pushed++
		}
	}

	// Secret-inventory is OPT-IN: it requires the one extra `secrets` get/list
	// grant the base ClusterRole withholds (slice 525). Skipped entirely unless
	// the operator explicitly enables it. METADATA ONLY — the collector never
	// reads, decodes, or records a Secret value.
	if f.collectSecretInventory {
		inventory, err := secretMetaScan(ctx, newSecretMetaAPI(httpClient, cred.APIServer(), cred.Token()), nil)
		if err != nil {
			return fmt.Errorf("secret-inventory collect: %w", err)
		}
		for _, s := range inventory {
			rec, err := buildSecretMetaRecord(s, f.cluster, f.environment, f.secretControl)
			if err != nil {
				return fmt.Errorf("build secret-inventory record %s/%s: %w", s.Namespace, s.Name, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push secret-inventory %s/%s: %w", s.Namespace, s.Name, err)
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
	// sources is the set of policy SOURCES (API groups) that contributed coverage
	// for this namespace (slice 622, AC-2). Omitted when the namespace has no
	// policies. Lets the evaluator distinguish upstream NetworkPolicy from
	// CNI-native (Cilium / Calico) enforcement.
	if len(c.Sources) > 0 {
		pm["sources"] = toAnySlice(c.Sources)
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
			if p.Source != "" {
				m["source"] = p.Source
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

// buildPSSRecord maps one namespace's Pod-Security-Standards admission
// assessment into an evidence record. PSS LABEL configuration only — namespace
// name + the three modes' levels + optional pinned versions + the configured
// flag. NO pod specs, secrets, or arbitrary namespace labels/annotations.
func buildPSSRecord(a pss.Admission, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := a.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"namespace":  a.Namespace,
		"configured": a.Configured,
	}
	if a.EnforceLevel != pss.LevelUnset {
		pm["enforce_level"] = string(a.EnforceLevel)
	}
	if a.EnforceVersion != "" {
		pm["enforce_version"] = a.EnforceVersion
	}
	if a.AuditLevel != pss.LevelUnset {
		pm["audit_level"] = string(a.AuditLevel)
	}
	if a.AuditVersion != "" {
		pm["audit_version"] = a.AuditVersion
	}
	if a.WarnLevel != pss.LevelUnset {
		pm["warn_level"] = string(a.WarnLevel)
	}
	if a.WarnVersion != "" {
		pm["warn_version"] = a.WarnVersion
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.PSSAdmissionKey(a.Namespace, now),
		EvidenceKind:   "k8s.pod_security_admission.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cluster", Values: []string{cluster}},
			{Key: "environment", Values: []string{env}},
			{Key: "namespace", Values: []string{a.Namespace}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapPSSResult(a.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("pss"),
		},
	}, nil
}

// buildSecretMetaRecord maps one Secret's metadata inventory into an evidence
// record (slice 525). Secret METADATA ONLY — namespace, name, type, age, and
// the KEY NAMES of .data. There is deliberately NO value field: the secretmeta
// collector physically cannot carry a Secret value, so no value can reach the
// payload here. The record is descriptive (INCONCLUSIVE) — it is an inventory
// signal (rotation / sprawl), not a pass/fail control verdict; the platform
// evaluator owns the policy call.
func buildSecretMetaRecord(s secretmeta.Inventory, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := s.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"namespace":   s.Namespace,
		"secret_name": s.Name,
		"secret_type": s.Type,
		"age_days":    float64(s.AgeDays),
		"key_count":   float64(len(s.KeyNames)),
	}
	if !s.CreatedAt.IsZero() {
		pm["created_at"] = s.CreatedAt.UTC().Format(time.RFC3339)
	}
	if len(s.KeyNames) > 0 {
		pm["key_names"] = toAnySlice(s.KeyNames)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.SecretInventoryKey(s.Namespace, s.Name, now),
		EvidenceKind:   "k8s.secret_inventory.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cluster", Values: []string{cluster}},
			{Key: "environment", Values: []string{env}},
			{Key: "namespace", Values: []string{s.Namespace}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive inventory — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("secretmeta"),
		},
	}, nil
}

// buildAdmissionWebhookRecord maps one admission-webhook configuration entry
// into an evidence record (slice 652). CONFIGURATION metadata ONLY — the webhook
// kind, configuration + entry names, failurePolicy + derived fail_closed,
// sideEffects, whether it scopes by namespace/object selector, the dispatch
// service ref, and the intercepted resource/operation sets. There is
// deliberately NO field for the caBundle / TLS key or an intercepted payload:
// the admission collector physically cannot carry them. The record is
// descriptive (INCONCLUSIVE) — it reports the webhook's wiring; the platform
// evaluator owns any fail-open / scope policy call.
func buildAdmissionWebhookRecord(w admission.Webhook, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := w.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"webhook_kind":           string(w.Kind),
		"config_name":            w.ConfigName,
		"webhook_name":           w.WebhookName,
		"fail_closed":            w.FailClosed,
		"has_namespace_selector": w.HasNamespaceSelector,
		"has_object_selector":    w.HasObjectSelector,
	}
	if w.FailurePolicy != admission.FailurePolicyUnset {
		pm["failure_policy"] = string(w.FailurePolicy)
	}
	if w.SideEffects != "" {
		pm["side_effects"] = w.SideEffects
	}
	if w.TargetService != "" {
		pm["target_service"] = w.TargetService
	}
	if len(w.InterceptedResources) > 0 {
		pm["intercepted_resources"] = toAnySlice(w.InterceptedResources)
	}
	if len(w.InterceptedOperations) > 0 {
		pm["intercepted_operations"] = toAnySlice(w.InterceptedOperations)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.AdmissionWebhookKey(string(w.Kind), w.ConfigName, w.WebhookName, now),
		EvidenceKind:   "k8s.admission_webhook.v1",
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
			ActorId:   actorID("admission-webhook"),
		},
	}, nil
}

// buildAdmissionPolicyRecord maps one policy-engine policy into an evidence
// record (slice 652). CONFIGURATION metadata ONLY — the engine, policy name,
// scope, kind, and enforcement action + derived enforcing flag. There is
// deliberately NO field for the policy's Rego/CEL decision-logic body: the
// admission collector physically cannot carry it. The record is descriptive
// (INCONCLUSIVE).
func buildAdmissionPolicyRecord(p admission.Policy, cluster, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := p.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"engine":      string(p.Engine),
		"policy_name": p.Name,
		"scope":       string(p.Scope),
		"enforcing":   p.Enforcing,
	}
	if p.PolicyKind != "" {
		pm["policy_kind"] = p.PolicyKind
	}
	if p.EnforcementAction != "" {
		pm["enforcement_action"] = p.EnforcementAction
	}
	scope := []*evidencev1.ScopeDimension{
		{Key: "cluster", Values: []string{cluster}},
		{Key: "environment", Values: []string{env}},
	}
	if p.Namespace != "" {
		pm["namespace"] = p.Namespace
		scope = append(scope, &evidencev1.ScopeDimension{Key: "namespace", Values: []string{p.Namespace}})
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.AdmissionPolicyKey(string(p.Engine), p.Namespace, p.Name, now),
		EvidenceKind:   "k8s.admission_policy.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope:          scope,
		ObservedAt:     timestamppb.New(now),
		Result:         evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:        payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("admission-policy"),
		},
	}, nil
}

func mapPSSResult(r pss.AssessResult) evidencev1.Result {
	switch r {
	case pss.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case pss.ResultFail:
		return evidencev1.Result_RESULT_FAIL
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
