package admission

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeWebhookAPI struct {
	webhooks []RawWebhook
	err      error
}

func (f *fakeWebhookAPI) ListWebhooks(_ context.Context) ([]RawWebhook, error) {
	return f.webhooks, f.err
}

type fakePolicyAPI struct {
	policies []RawPolicy
	err      error
}

func (f *fakePolicyAPI) ListPolicies(_ context.Context) ([]RawPolicy, error) {
	return f.policies, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }
}

func TestCollectWebhooks_MetadataPreservedAndFailClosedDerived(t *testing.T) {
	t.Parallel()
	api := &fakeWebhookAPI{webhooks: []RawWebhook{{
		Kind:                  KindValidating,
		ConfigName:            "gatekeeper-validating-webhook-configuration",
		WebhookName:           "validation.gatekeeper.sh",
		FailurePolicy:         FailurePolicyFail,
		SideEffects:           "None",
		HasNamespaceSelector:  true,
		HasObjectSelector:     false,
		TargetService:         "gatekeeper-system/gatekeeper-webhook-service",
		InterceptedResources:  []string{"pods", "deployments", "pods"},
		InterceptedOperations: []string{"UPDATE", "CREATE"},
	}}}
	got, err := CollectWebhooks(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("CollectWebhooks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	w := got[0]
	if w.Kind != KindValidating || w.ConfigName != "gatekeeper-validating-webhook-configuration" {
		t.Errorf("metadata not preserved: %+v", w)
	}
	if !w.FailClosed {
		t.Errorf("failurePolicy=Fail must derive fail_closed=true")
	}
	if !w.HasNamespaceSelector || w.HasObjectSelector {
		t.Errorf("selector flags not preserved: %+v", w)
	}
	if w.TargetService != "gatekeeper-system/gatekeeper-webhook-service" {
		t.Errorf("target service = %q", w.TargetService)
	}
	// Resources deduped + sorted.
	if strings.Join(w.InterceptedResources, ",") != "deployments,pods" {
		t.Errorf("resources = %v; want sorted+deduped [deployments pods]", w.InterceptedResources)
	}
	if strings.Join(w.InterceptedOperations, ",") != "CREATE,UPDATE" {
		t.Errorf("operations = %v; want sorted [CREATE UPDATE]", w.InterceptedOperations)
	}
}

func TestCollectWebhooks_IgnoreIsFailOpen(t *testing.T) {
	t.Parallel()
	got, _ := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: []RawWebhook{
		{Kind: KindMutating, ConfigName: "c", WebhookName: "w", FailurePolicy: FailurePolicyIgnore},
	}}, fixedNow())
	if got[0].FailClosed {
		t.Errorf("failurePolicy=Ignore must derive fail_closed=false")
	}
	if got[0].FailurePolicy != FailurePolicyIgnore {
		t.Errorf("failurePolicy = %q; want Ignore", got[0].FailurePolicy)
	}
}

func TestCollectWebhooks_UnknownFailurePolicyNormalizesToUnset(t *testing.T) {
	t.Parallel()
	got, _ := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: []RawWebhook{
		{Kind: KindValidating, ConfigName: "c", WebhookName: "w", FailurePolicy: "Bogus"},
	}}, fixedNow())
	if got[0].FailurePolicy != FailurePolicyUnset {
		t.Errorf("unknown failurePolicy = %q; want unset", got[0].FailurePolicy)
	}
	if got[0].FailClosed {
		t.Errorf("unset failurePolicy must not be fail_closed")
	}
}

func TestCollectWebhooks_SkipsUnidentified(t *testing.T) {
	t.Parallel()
	got, err := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: []RawWebhook{
		{ConfigName: "", WebhookName: "w"},
		{ConfigName: "c", WebhookName: ""},
		{ConfigName: "c", WebhookName: "ok"},
	}}, fixedNow())
	if err != nil {
		t.Fatalf("CollectWebhooks: %v", err)
	}
	if len(got) != 1 || got[0].WebhookName != "ok" {
		t.Fatalf("want only the fully-identified webhook; got %+v", got)
	}
}

func TestCollectWebhooks_BoundedByCap(t *testing.T) {
	t.Parallel()
	over := maxWebhooks + 10
	raw := make([]RawWebhook, 0, over)
	for i := 0; i < over; i++ {
		raw = append(raw, RawWebhook{Kind: KindValidating, ConfigName: "c", WebhookName: "w" + strconv.Itoa(i)})
	}
	got, _ := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: raw}, fixedNow())
	if len(got) != maxWebhooks {
		t.Errorf("collected = %d; want capped at %d", len(got), maxWebhooks)
	}
}

func TestCollectWebhooks_NilAPIAndError(t *testing.T) {
	t.Parallel()
	if _, err := CollectWebhooks(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
	sentinel := errors.New("403")
	if _, err := CollectWebhooks(context.Background(), &fakeWebhookAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollectWebhooks_DefaultNow(t *testing.T) {
	t.Parallel()
	got, _ := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: []RawWebhook{
		{Kind: KindValidating, ConfigName: "c", WebhookName: "w"},
	}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

func TestCollectPolicies_EnforcementDerivesEnforcing(t *testing.T) {
	t.Parallel()
	api := &fakePolicyAPI{policies: []RawPolicy{
		{Engine: EngineKyverno, Name: "require-labels", Scope: ScopeCluster, PolicyKind: "ClusterPolicy", EnforcementAction: "Enforce"},
		{Engine: EngineKyverno, Name: "audit-only", Scope: ScopeNamespaced, Namespace: "team-a", PolicyKind: "Policy", EnforcementAction: "Audit"},
		{Engine: EngineGatekeeper, Name: "k8srequiredlabels", Scope: ScopeCluster, PolicyKind: "K8sRequiredLabels", EnforcementAction: "deny"},
	}}
	got, err := CollectPolicies(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("CollectPolicies: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	byName := map[string]Policy{}
	for _, p := range got {
		byName[p.Name] = p
	}
	if !byName["require-labels"].Enforcing {
		t.Errorf("Enforce must derive enforcing=true")
	}
	if byName["audit-only"].Enforcing {
		t.Errorf("Audit must derive enforcing=false")
	}
	if !byName["k8srequiredlabels"].Enforcing {
		t.Errorf("Gatekeeper deny must derive enforcing=true")
	}
	// action lower-cased.
	if byName["require-labels"].EnforcementAction != "enforce" {
		t.Errorf("action not lower-cased: %q", byName["require-labels"].EnforcementAction)
	}
}

func TestCollectPolicies_ScopeInferredFromNamespace(t *testing.T) {
	t.Parallel()
	got, _ := CollectPolicies(context.Background(), &fakePolicyAPI{policies: []RawPolicy{
		{Engine: EngineKyverno, Name: "ns-pol", Namespace: "team-a"}, // scope empty -> inferred namespaced
		{Engine: EngineGatekeeper, Name: "cw-pol"},                   // scope empty, no ns -> cluster
	}}, fixedNow())
	byName := map[string]Policy{}
	for _, p := range got {
		byName[p.Name] = p
	}
	if byName["ns-pol"].Scope != ScopeNamespaced {
		t.Errorf("namespaced policy scope = %q; want namespaced", byName["ns-pol"].Scope)
	}
	if byName["cw-pol"].Scope != ScopeCluster {
		t.Errorf("cluster policy scope = %q; want cluster", byName["cw-pol"].Scope)
	}
}

func TestCollectPolicies_GatekeeperTemplateNoActionNotEnforcing(t *testing.T) {
	t.Parallel()
	// Gatekeeper templates carry no enforcement action in v0; enforcing must be
	// false (the platform evaluator owns the call), not a crash.
	got, _ := CollectPolicies(context.Background(), &fakePolicyAPI{policies: []RawPolicy{
		{Engine: EngineGatekeeper, Name: "tmpl", Scope: ScopeCluster, PolicyKind: "K8sFoo"},
	}}, fixedNow())
	if got[0].Enforcing {
		t.Errorf("template with no action must not be enforcing")
	}
	if got[0].EnforcementAction != "" {
		t.Errorf("template action = %q; want empty", got[0].EnforcementAction)
	}
}

func TestCollectPolicies_SkipsUnidentifiedAndBounds(t *testing.T) {
	t.Parallel()
	got, err := CollectPolicies(context.Background(), &fakePolicyAPI{policies: []RawPolicy{
		{Engine: "", Name: "no-engine"},
		{Engine: EngineKyverno, Name: ""},
		{Engine: EngineKyverno, Name: "ok"},
	}}, fixedNow())
	if err != nil {
		t.Fatalf("CollectPolicies: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("want only the fully-identified policy; got %+v", got)
	}
}

func TestCollectPolicies_BoundedByCap(t *testing.T) {
	t.Parallel()
	over := maxPolicies + 5
	raw := make([]RawPolicy, 0, over)
	for i := 0; i < over; i++ {
		raw = append(raw, RawPolicy{Engine: EngineKyverno, Name: "p" + strconv.Itoa(i)})
	}
	got, _ := CollectPolicies(context.Background(), &fakePolicyAPI{policies: raw}, fixedNow())
	if len(got) != maxPolicies {
		t.Errorf("collected = %d; want capped at %d", len(got), maxPolicies)
	}
}

func TestCollectPolicies_NilAPIAndErrorAndDefaultNow(t *testing.T) {
	t.Parallel()
	if _, err := CollectPolicies(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
	sentinel := errors.New("403")
	if _, err := CollectPolicies(context.Background(), &fakePolicyAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
	got, _ := CollectPolicies(context.Background(), &fakePolicyAPI{policies: []RawPolicy{
		{Engine: EngineKyverno, Name: "p"},
	}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

// TestStruct_ConfigMetadataOnly_NoBodyOrTLSFields is the LOAD-BEARING structural
// over-collection guard (the P0 config-metadata-only criterion). It reflects over
// every field name of the four collector structs and FAILS if any field name
// hints at a webhook caBundle / TLS key, a policy decision-logic body, or an
// intercepted-object payload, so a future field that opens that door trips the
// build. The structs may carry ONLY: webhook/policy CONFIGURATION metadata.
func TestStruct_ConfigMetadataOnly_NoBodyOrTLSFields(t *testing.T) {
	t.Parallel()
	banned := []string{
		"cabundle", "tls", "cert", "certificate", "privatekey", "key",
		"rego", "cel", "body", "rule", "rules", "logic", "expression", "expr",
		"parameters", "params", "match", "validate", "mutate", "decision",
		"payload", "object", "content", "raw", "secret", "value", "data",
		"caburl", "url", "endpoint", "token", "credential",
	}
	// allow lists field names that legitimately contain a banned substring but are
	// NOT a forbidden surface.
	allow := map[string]bool{
		"policykind":            true, // the CRD kind NAME, never the rule body
		"webhookkind":           true, // validating/mutating, not a TLS key
		"kind":                  true, // validating/mutating | engine kind label
		"enforcementaction":     true, // the action verb, not the rule logic
		"interceptedresources":  true, // resource TYPE names, never an object
		"interceptedoperations": true, // operation verb names (CREATE/UPDATE)
		"hasobjectselector":     true, // a bool PRESENCE flag, never the object
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			if allow[name] {
				continue
			}
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned token %q — admission structs must carry only CONFIG metadata, NEVER a caBundle/TLS key, a policy Rego/CEL body, or an intercepted payload",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(RawWebhook{}))
	check(reflect.TypeOf(Webhook{}))
	check(reflect.TypeOf(RawPolicy{}))
	check(reflect.TypeOf(Policy{}))
}
