// Package admission inventories Kubernetes admission-time policy enforcement as
// CONFIGURATION metadata only — the "is hardening enforced beyond namespace PSS
// labels?" surfaces an auditor asks about once PSS labels are covered (slice
// 652, the #524 follow-on). It reads two distinct surfaces:
//
//   - Admission webhooks: ValidatingWebhookConfiguration +
//     MutatingWebhookConfiguration (admissionregistration.k8s.io/v1). Proves a
//     policy webhook is wired in — which resources/operations it intercepts, its
//     failurePolicy (fail-open vs fail-closed), its namespace/object selector
//     SCOPE, its sideEffects, and the target service it dispatches to — WITHOUT
//     reading the webhook's TLS client key / caBundle or its decision logic.
//   - Third-party policy engines: OPA/Gatekeeper (constrainttemplates +
//     constraints CRDs) and Kyverno (policies + clusterpolicies CRDs), detected
//     by API-discovery probe so an absent engine is NOT an error (slice 622
//     pattern). Proves which admission policies are enforced cluster-wide + their
//     enforcement action (enforce / audit / dryrun) WITHOUT reading the policy's
//     Rego/CEL decision-logic body.
//
// Scope discipline (decisions-log): CONFIGURATION metadata only — never the
// webhook caBundle / TLS client key, the policy's Rego/CEL decision-logic body,
// or any intercepted-object payload. The guard is STRUCTURAL, not procedural:
// the Webhook and Policy record structs have NO field that can hold a caBundle,
// a decision-logic body, or an intercepted payload; a reflection guard
// (admission_test.go) fails the build if such a field is ever added.
//
// Source: read-only Kubernetes API (get/list). Two NEW ClusterRole rules over
// the slice-487 base (the deliberate, flagged expansion this slice owns —
// k8sauth.AdmissionWebhookRule); the policy-engine CRD groups are detected by
// presence and need their own get,list rules only when present
// (k8sauth.PolicyEngineRules). Every rule stays get,list-only — never secrets,
// never a write verb, never a wildcard.
package admission

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WebhookKind names which admission-webhook configuration a record came from.
type WebhookKind string

const (
	// KindValidating is a ValidatingWebhookConfiguration (rejects/admits).
	KindValidating WebhookKind = "validating"
	// KindMutating is a MutatingWebhookConfiguration (patches the object).
	KindMutating WebhookKind = "mutating"
)

// FailurePolicy is the webhook's fail-open vs fail-closed posture when the
// webhook endpoint is unreachable. Fail is the hardened (fail-closed) value.
type FailurePolicy string

const (
	// FailurePolicyFail rejects the request when the webhook is unreachable
	// (fail-closed — the hardened posture).
	FailurePolicyFail FailurePolicy = "Fail"
	// FailurePolicyIgnore admits the request when the webhook is unreachable
	// (fail-open — the request slips past the policy).
	FailurePolicyIgnore FailurePolicy = "Ignore"
	// FailurePolicyUnset means the configuration omitted failurePolicy; the
	// Kubernetes default is Fail, but we record the absence honestly rather than
	// silently asserting the default.
	FailurePolicyUnset FailurePolicy = ""
)

// RawWebhook is the narrow view the API surface returns for one admission
// webhook entry — CONFIGURATION metadata ONLY. The concrete client maps the
// Kubernetes API response into this shape; tests construct it directly. There is
// deliberately NO field that can carry the caBundle / TLS key or an intercepted
// payload. A structural reflection guard (admission_test.go) fails the build if
// such a field is added.
type RawWebhook struct {
	// Kind is validating or mutating.
	Kind WebhookKind
	// ConfigName is the *WebhookConfiguration object name (the resource the rule
	// gates), e.g. "gatekeeper-validating-webhook-configuration".
	ConfigName string
	// WebhookName is the individual webhook entry name within the configuration
	// (the .webhooks[].name field, an FQDN-style id).
	WebhookName string
	// FailurePolicy is Fail / Ignore / unset.
	FailurePolicy FailurePolicy
	// SideEffects is the declared side-effect class (None / NoneOnDryRun / Some /
	// Unknown) — config metadata, never the effect itself.
	SideEffects string
	// HasNamespaceSelector / HasObjectSelector report whether the webhook scopes
	// its interception by a namespace / object label selector. We record the
	// PRESENCE of a selector (the scope-narrowing signal) — never the selector's
	// match expressions (those can encode tenant-identifying labels).
	HasNamespaceSelector bool
	HasObjectSelector    bool
	// TargetService is the dispatch target "namespace/name" the webhook calls
	// (the .clientConfig.service ref). Empty when the webhook dispatches to a raw
	// URL (no service ref) — we never record the URL itself.
	TargetService string
	// InterceptedResources is the set of resource types the webhook intercepts
	// (the .rules[].resources union), e.g. ["pods","deployments"]. Config
	// metadata — which resource TYPES, never an instance's contents.
	InterceptedResources []string
	// InterceptedOperations is the set of operations the webhook intercepts (the
	// .rules[].operations union), e.g. ["CREATE","UPDATE"].
	InterceptedOperations []string
}

// Webhook is the per-webhook configuration record the connector emits. Field
// names map 1:1 to the k8s.admission_webhook.v1 schema. CONFIGURATION metadata
// ONLY — there is deliberately NO field for the webhook caBundle / TLS key or an
// intercepted payload (structural over-collection guard).
type Webhook struct {
	Kind                  WebhookKind
	ConfigName            string
	WebhookName           string
	FailurePolicy         FailurePolicy
	SideEffects           string
	HasNamespaceSelector  bool
	HasObjectSelector     bool
	TargetService         string
	InterceptedResources  []string
	InterceptedOperations []string
	// FailClosed is true when the webhook rejects on an unreachable endpoint
	// (failurePolicy=Fail). The derived hardening signal.
	FailClosed bool
	ObservedAt time.Time
}

// PolicyEngine names the third-party admission policy engine a policy record
// came from.
type PolicyEngine string

const (
	// EngineGatekeeper is OPA/Gatekeeper (constrainttemplates + constraints).
	EngineGatekeeper PolicyEngine = "gatekeeper"
	// EngineKyverno is Kyverno (policies + clusterpolicies).
	EngineKyverno PolicyEngine = "kyverno"
)

// PolicyScope reports whether a policy is namespaced or cluster-wide.
type PolicyScope string

const (
	// ScopeNamespaced is a namespaced policy (Kyverno Policy).
	ScopeNamespaced PolicyScope = "namespaced"
	// ScopeCluster is a cluster-wide policy (Gatekeeper Constraint / Kyverno
	// ClusterPolicy).
	ScopeCluster PolicyScope = "cluster"
)

// RawPolicy is the narrow view the API surface returns for one policy-engine
// object — CONFIGURATION metadata ONLY. There is deliberately NO field for the
// policy's Rego/CEL decision-logic body. A structural reflection guard fails the
// build if such a field is added.
type RawPolicy struct {
	// Engine is gatekeeper or kyverno.
	Engine PolicyEngine
	// Name is the policy object name (metadata.name).
	Name string
	// Namespace is set only for namespaced policies (Kyverno Policy); empty for
	// cluster-wide policies.
	Namespace string
	// Scope is namespaced or cluster.
	Scope PolicyScope
	// PolicyKind is the CRD kind, e.g. "K8sRequiredLabels" (a Gatekeeper
	// Constraint's kind, which is the ConstraintTemplate name) or "ClusterPolicy"
	// (Kyverno). The KIND, never the rule body.
	PolicyKind string
	// EnforcementAction is the policy's action — enforce / deny / audit / warn /
	// dryrun — as the engine reports it. The verb that decides whether a
	// violation blocks admission. Config metadata, never the rule logic.
	EnforcementAction string
}

// Policy is the per-policy configuration record the connector emits. Field names
// map 1:1 to the k8s.admission_policy.v1 schema. CONFIGURATION metadata ONLY —
// there is deliberately NO field for the policy's Rego/CEL decision-logic body
// (structural over-collection guard).
type Policy struct {
	Engine            PolicyEngine
	Name              string
	Namespace         string
	Scope             PolicyScope
	PolicyKind        string
	EnforcementAction string
	// Enforcing is true when the action blocks admission on a violation
	// (enforce / deny) rather than only observing (audit / warn / dryrun). The
	// derived hardening signal.
	Enforcing  bool
	ObservedAt time.Time
}

// WebhookAPI is the narrow surface CollectWebhooks depends on. The concrete
// implementation issues read-only Kubernetes API calls; tests pass a fake. The
// list calls follow the metadata.continue cursor to completion via the shared
// k8slist reader (slice 621).
type WebhookAPI interface {
	// ListWebhooks returns one RawWebhook per webhook entry across every
	// validating + mutating configuration, carrying ONLY configuration metadata —
	// never the caBundle / TLS key or an intercepted payload.
	ListWebhooks(ctx context.Context) ([]RawWebhook, error)
}

// PolicyAPI is the narrow surface CollectPolicies depends on. It detects the
// installed policy engines by API-discovery probe (slice 622) and returns
// nothing for an absent engine (never a hard-fail).
type PolicyAPI interface {
	// ListPolicies returns one RawPolicy per Gatekeeper Constraint / Kyverno
	// Policy/ClusterPolicy on the cluster, carrying ONLY configuration metadata —
	// never the Rego/CEL decision-logic body. An absent engine contributes
	// nothing.
	ListPolicies(ctx context.Context) ([]RawPolicy, error)
}

// maxWebhooks / maxPolicies bound the per-run record counts so a pathological
// cluster (or a hostile API response) cannot blow up memory. The client already
// bounds the page read; these are the collector-side caps (mirrors
// pss.maxNamespaces / secretmeta.maxSecrets).
const (
	maxWebhooks = 5000
	maxPolicies = 20000
)

// CollectWebhooks returns the configuration record for every admission webhook
// entry. now is injectable for deterministic tests (nil -> time.Now UTC). The
// list is bounded by maxWebhooks.
func CollectWebhooks(ctx context.Context, api WebhookAPI, now func() time.Time) ([]Webhook, error) {
	if api == nil {
		return nil, errors.New("admission: WebhookAPI is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListWebhooks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list admission webhooks: %w", err)
	}
	observedAt := now().UTC()
	out := make([]Webhook, 0, len(raw))
	for _, w := range raw {
		if w.ConfigName == "" || w.WebhookName == "" {
			continue
		}
		if len(out) >= maxWebhooks {
			break
		}
		out = append(out, normalizeWebhook(w, observedAt))
	}
	return out, nil
}

// CollectPolicies returns the configuration record for every policy-engine
// policy. now is injectable for deterministic tests (nil -> time.Now UTC). The
// list is bounded by maxPolicies.
func CollectPolicies(ctx context.Context, api PolicyAPI, now func() time.Time) ([]Policy, error) {
	if api == nil {
		return nil, errors.New("admission: PolicyAPI is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list admission policies: %w", err)
	}
	observedAt := now().UTC()
	out := make([]Policy, 0, len(raw))
	for _, p := range raw {
		if p.Name == "" || p.Engine == "" {
			continue
		}
		if len(out) >= maxPolicies {
			break
		}
		out = append(out, normalizePolicy(p, observedAt))
	}
	return out, nil
}

// normalizeWebhook derives one webhook's configuration record. It copies ONLY
// configuration metadata; there is no code path that could copy a caBundle / TLS
// key / intercepted payload because RawWebhook carries none.
func normalizeWebhook(w RawWebhook, observedAt time.Time) Webhook {
	fp := normalizeFailurePolicy(w.FailurePolicy)
	res := dedupeSorted(w.InterceptedResources)
	ops := dedupeSorted(w.InterceptedOperations)
	return Webhook{
		Kind:                  w.Kind,
		ConfigName:            w.ConfigName,
		WebhookName:           w.WebhookName,
		FailurePolicy:         fp,
		SideEffects:           w.SideEffects,
		HasNamespaceSelector:  w.HasNamespaceSelector,
		HasObjectSelector:     w.HasObjectSelector,
		TargetService:         w.TargetService,
		InterceptedResources:  res,
		InterceptedOperations: ops,
		FailClosed:            fp == FailurePolicyFail,
		ObservedAt:            observedAt,
	}
}

// normalizePolicy derives one policy's configuration record. It copies ONLY
// configuration metadata; there is no code path that could copy a Rego/CEL body
// because RawPolicy carries none.
func normalizePolicy(p RawPolicy, observedAt time.Time) Policy {
	scope := p.Scope
	if scope == "" {
		if p.Namespace != "" {
			scope = ScopeNamespaced
		} else {
			scope = ScopeCluster
		}
	}
	action := normalizeAction(p.EnforcementAction)
	return Policy{
		Engine:            p.Engine,
		Name:              p.Name,
		Namespace:         p.Namespace,
		Scope:             scope,
		PolicyKind:        p.PolicyKind,
		EnforcementAction: action,
		Enforcing:         isEnforcing(action),
		ObservedAt:        observedAt,
	}
}

// normalizeFailurePolicy keeps only the two valid failurePolicy values; any
// other value (including a malformed one) collapses to unset rather than being
// recorded verbatim.
func normalizeFailurePolicy(fp FailurePolicy) FailurePolicy {
	switch fp {
	case FailurePolicyFail, FailurePolicyIgnore:
		return fp
	default:
		return FailurePolicyUnset
	}
}

// normalizeAction lower-cases the enforcement action for stable comparison. The
// engines spell the action differently (Gatekeeper: deny/dryrun/warn; Kyverno:
// Enforce/Audit) — we keep the engine's spelling, lower-cased, and never invent
// a value.
func normalizeAction(a string) string {
	return strings.ToLower(strings.TrimSpace(a))
}

// isEnforcing reports whether an enforcement action BLOCKS admission on a
// violation. enforce (Kyverno) and deny (Gatekeeper) block; audit / warn /
// dryrun observe only. An unknown/empty action conservatively reads as
// NOT-enforcing (the platform evaluator owns the final call).
func isEnforcing(action string) bool {
	switch action {
	case "enforce", "deny":
		return true
	default:
		return false
	}
}

// dedupeSorted returns the unique, sorted, non-empty entries of in. Used for the
// intercepted resource / operation sets so the record is deterministic.
func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
