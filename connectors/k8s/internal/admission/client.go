package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// WebhookClient is a thin read-only HTTP client for the two admission-webhook
// endpoints the collector reads: admissionregistration.k8s.io/v1
// validatingwebhookconfigurations + mutatingwebhookconfigurations (get/list —
// the NEW base rule this slice adds, k8sauth.AdmissionWebhookRule). It delegates
// HTTP + pagination to the shared k8slist.Reader: each list call follows the
// Kubernetes metadata.continue cursor to completion (slice 621). It holds a
// short-lived bearer token (never logged) and issues only GET requests.
//
// CRITICAL (the load-bearing config-metadata-only guard): a webhook
// configuration's most sensitive fields are .clientConfig.caBundle (the TLS
// trust bundle) and any TLS key material. This client models .clientConfig as
// ONLY its service ref (namespace/name) and does NOT model caBundle / url / any
// TLS field at all, so Go's json decoder discards them before they reach Go
// memory. reduceWebhook() is the single chokepoint; a test
// (TestReduceWebhook_DropsCABundle) feeds a configuration WITH a caBundle and a
// URL and proves only configuration metadata survives.
type WebhookClient struct {
	r *k8slist.Reader
}

// NewWebhookClient builds a webhook-configuration client. token is a read-only
// ServiceAccount bearer token (from k8sauth.Credential.Token). baseURL is the
// API server URL.
func NewWebhookClient(httpClient *http.Client, baseURL, token string) *WebhookClient {
	return &WebhookClient{r: k8slist.NewReader(httpClient, baseURL, token)}
}

// APIError is re-exported from the shared reader so callers and tests keep
// referring to admission.APIError.
type APIError = k8slist.APIError

// --- minimal Kubernetes API JSON shapes (webhook CONFIGURATION metadata ONLY) ---
//
// We model ONLY the configuration metadata. We deliberately do NOT model
// .webhooks[].clientConfig.caBundle, .webhooks[].clientConfig.url, or any TLS
// field — the decoder discards unmodeled keys, so no caBundle / TLS material
// ever materializes into Go memory. .clientConfig.service is modeled only as the
// namespace/name dispatch ref (non-secret routing metadata).

type apiObjectMeta struct {
	Name string `json:"name"`
}

type apiServiceRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Port / Path are intentionally NOT modeled — the dispatch target's
	// namespace/name is enough config metadata; we never record the URL.
}

type apiClientConfig struct {
	// Service is the dispatch target. caBundle / url are NOT modeled (discarded).
	Service *apiServiceRef `json:"service"`
}

type apiRule struct {
	Operations []string `json:"operations"`
	Resources  []string `json:"resources"`
}

type apiWebhookEntry struct {
	Name              string           `json:"name"`
	FailurePolicy     string           `json:"failurePolicy"`
	SideEffects       string           `json:"sideEffects"`
	NamespaceSelector *json.RawMessage `json:"namespaceSelector"`
	ObjectSelector    *json.RawMessage `json:"objectSelector"`
	ClientConfig      apiClientConfig  `json:"clientConfig"`
	Rules             []apiRule        `json:"rules"`
}

type apiWebhookConfiguration struct {
	Metadata apiObjectMeta     `json:"metadata"`
	Webhooks []apiWebhookEntry `json:"webhooks"`
}

// ListWebhooks reads every validating + mutating webhook configuration (each
// list call follows the continue cursor to completion — read-only) and reduces
// each webhook ENTRY to its CONFIGURATION metadata.
func (c *WebhookClient) ListWebhooks(ctx context.Context) ([]RawWebhook, error) {
	out := make([]RawWebhook, 0)

	validating, err := k8slist.ListAll[apiWebhookConfiguration](ctx, c.r,
		"/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations")
	if err != nil {
		return nil, fmt.Errorf("list validatingwebhookconfigurations: %w", err)
	}
	for _, cfg := range validating {
		out = append(out, reduceConfig(KindValidating, cfg)...)
	}

	mutating, err := k8slist.ListAll[apiWebhookConfiguration](ctx, c.r,
		"/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations")
	if err != nil {
		return nil, fmt.Errorf("list mutatingwebhookconfigurations: %w", err)
	}
	for _, cfg := range mutating {
		out = append(out, reduceConfig(KindMutating, cfg)...)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ConfigName != out[j].ConfigName {
			return out[i].ConfigName < out[j].ConfigName
		}
		return out[i].WebhookName < out[j].WebhookName
	})
	return out, nil
}

// reduceConfig collapses one *WebhookConfiguration into one RawWebhook per
// webhook entry. The configuration name gates the rule; each .webhooks[] entry
// is its own record.
func reduceConfig(kind WebhookKind, cfg apiWebhookConfiguration) []RawWebhook {
	if cfg.Metadata.Name == "" {
		return nil
	}
	out := make([]RawWebhook, 0, len(cfg.Webhooks))
	for _, wh := range cfg.Webhooks {
		out = append(out, reduceWebhook(kind, cfg.Metadata.Name, wh))
	}
	return out
}

// reduceWebhook is THE over-collection chokepoint for webhook configs: it reads
// ONLY configuration metadata (failurePolicy, sideEffects, the PRESENCE of a
// namespace/object selector, the service dispatch ref, and the intercepted
// resource/operation sets). It NEVER reads the caBundle / url / any TLS material
// (those are not even modeled) and NEVER an intercepted object.
func reduceWebhook(kind WebhookKind, configName string, wh apiWebhookEntry) RawWebhook {
	var target string
	if wh.ClientConfig.Service != nil {
		svc := wh.ClientConfig.Service
		if svc.Namespace != "" && svc.Name != "" {
			target = svc.Namespace + "/" + svc.Name
		} else if svc.Name != "" {
			target = svc.Name
		}
	}
	var resources, operations []string
	for _, r := range wh.Rules {
		resources = append(resources, r.Resources...)
		operations = append(operations, r.Operations...)
	}
	return RawWebhook{
		Kind:                  kind,
		ConfigName:            configName,
		WebhookName:           wh.Name,
		FailurePolicy:         FailurePolicy(wh.FailurePolicy),
		SideEffects:           wh.SideEffects,
		HasNamespaceSelector:  wh.NamespaceSelector != nil,
		HasObjectSelector:     wh.ObjectSelector != nil,
		TargetService:         target,
		InterceptedResources:  resources,
		InterceptedOperations: operations,
	}
}

// PolicyClient is a thin read-only HTTP client for the third-party policy-engine
// CRDs. It detects each engine by API-discovery probe (slice 622) and reads
// nothing for an absent engine (never a hard-fail). It reads CONFIGURATION
// metadata ONLY — the policy name, kind, scope, and enforcement action — never
// the Rego/CEL decision-logic body (which is not modeled, so the decoder
// discards it).
type PolicyClient struct {
	r *k8slist.Reader
}

// NewPolicyClient builds a policy-engine client.
func NewPolicyClient(httpClient *http.Client, baseURL, token string) *PolicyClient {
	return &PolicyClient{r: k8slist.NewReader(httpClient, baseURL, token)}
}

// Group/version discovery + list paths for the supported engines.
const (
	gatekeeperTemplatesGV = "templates.gatekeeper.sh/v1"
	gatekeeperTemplates   = "/apis/templates.gatekeeper.sh/v1/constrainttemplates"
	kyvernoGV             = "kyverno.io/v1"
	kyvernoClusterPath    = "/apis/kyverno.io/v1/clusterpolicies"
	kyvernoNamespacedPath = "/apis/kyverno.io/v1/policies"
)

// --- minimal policy-engine CRD JSON shapes (CONFIGURATION metadata ONLY) ---
//
// We model ONLY metadata.name/namespace + the enforcement-action spec field +
// the Gatekeeper template's rendered kind. We deliberately do NOT model the rule
// body: a Gatekeeper ConstraintTemplate carries spec.targets[].rego (the Rego
// decision logic) and a Kyverno policy carries spec.rules[] (the match/validate/
// mutate CEL/JMESPath logic). Neither is modeled, so the decoder discards them
// before they reach Go memory — they can never reach an evidence record.

// apiGatekeeperTemplate models a ConstraintTemplate. We read its name and the
// rendered constraint KIND (spec.crd.spec.names.kind) — NOT the Rego targets.
type apiGatekeeperTemplate struct {
	Metadata apiObjectMeta `json:"metadata"`
	Spec     struct {
		CRD struct {
			Spec struct {
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
			} `json:"spec"`
		} `json:"crd"`
		// targets[] (the Rego decision logic) is intentionally NOT modeled.
	} `json:"spec"`
}

// apiKyvernoPolicy models a Kyverno (Cluster)Policy. We read its name/namespace
// and the top-level validationFailureAction (enforce/audit). spec.rules[] (the
// decision logic) is intentionally NOT modeled.
type apiKyvernoPolicy struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		// ValidationFailureAction is Kyverno's enforce/audit knob. Newer Kyverno
		// nests it per-rule, but the top-level field remains the cluster-wide
		// default the auditor cares about.
		ValidationFailureAction string `json:"validationFailureAction"`
	} `json:"spec"`
}

// ListPolicies probes each engine's group/version and lists its policies only
// when present. An absent engine contributes nothing (no hard-fail).
func (c *PolicyClient) ListPolicies(ctx context.Context) ([]RawPolicy, error) {
	out := make([]RawPolicy, 0)

	gkPolicies, err := c.collectGatekeeper(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, gkPolicies...)

	kyPolicies, err := c.collectKyverno(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, kyPolicies...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].Engine != out[j].Engine {
			return out[i].Engine < out[j].Engine
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// collectGatekeeper reads the Gatekeeper ConstraintTemplate catalog — the
// static, NAMED resource that proves WHICH OPA/Gatekeeper policies are defined
// cluster-wide (each template renders one constraint kind). v0 records the
// policy SET (template name + rendered kind); the per-constraint enforcement
// action is OUT of v0 because reading it would require a wildcard grant over the
// dynamic per-template constraint CRDs (decisions-log D5). enforcementAction is
// left unset for Gatekeeper templates (the platform evaluator owns the call). An
// absent group contributes nothing.
func (c *PolicyClient) collectGatekeeper(ctx context.Context) ([]RawPolicy, error) {
	present, err := c.present(ctx, "/apis/"+gatekeeperTemplatesGV)
	if err != nil {
		return nil, fmt.Errorf("probe gatekeeper templates: %w", err)
	}
	if !present {
		return nil, nil
	}
	templates, err := listOrAbsent[apiGatekeeperTemplate](ctx, c.r, gatekeeperTemplates)
	if err != nil {
		return nil, err
	}
	out := make([]RawPolicy, 0, len(templates))
	for _, t := range templates {
		if t.Metadata.Name == "" {
			continue
		}
		out = append(out, RawPolicy{
			Engine:     EngineGatekeeper,
			Name:       t.Metadata.Name,
			Scope:      ScopeCluster,
			PolicyKind: t.Spec.CRD.Spec.Names.Kind,
			// enforcementAction unset — per-constraint, out of v0 (D5).
		})
	}
	return out, nil
}

// collectKyverno reads Kyverno ClusterPolicy + Policy when the engine is present.
func (c *PolicyClient) collectKyverno(ctx context.Context) ([]RawPolicy, error) {
	present, err := c.present(ctx, "/apis/"+kyvernoGV)
	if err != nil {
		return nil, fmt.Errorf("probe kyverno: %w", err)
	}
	if !present {
		return nil, nil
	}
	out := make([]RawPolicy, 0)

	clusterPolicies, err := listOrAbsent[apiKyvernoPolicy](ctx, c.r, kyvernoClusterPath)
	if err != nil {
		return nil, err
	}
	for _, p := range clusterPolicies {
		if p.Metadata.Name == "" {
			continue
		}
		out = append(out, RawPolicy{
			Engine:            EngineKyverno,
			Name:              p.Metadata.Name,
			Scope:             ScopeCluster,
			PolicyKind:        "ClusterPolicy",
			EnforcementAction: p.Spec.ValidationFailureAction,
		})
	}

	nsPolicies, err := listOrAbsent[apiKyvernoPolicy](ctx, c.r, kyvernoNamespacedPath)
	if err != nil {
		return nil, err
	}
	for _, p := range nsPolicies {
		if p.Metadata.Name == "" {
			continue
		}
		out = append(out, RawPolicy{
			Engine:            EngineKyverno,
			Name:              p.Metadata.Name,
			Namespace:         p.Metadata.Namespace,
			Scope:             ScopeNamespaced,
			PolicyKind:        "Policy",
			EnforcementAction: p.Spec.ValidationFailureAction,
		})
	}
	return out, nil
}

// present probes API discovery for a group/version. A 200 means served (engine
// installed); a 404 means absent. Any other status is a real error. Discovery is
// a read-only GET requiring no extra grant beyond the resource get,list.
func (c *PolicyClient) present(ctx context.Context, discoveryPath string) (bool, error) {
	status, err := c.r.Probe(ctx, discoveryPath)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, &k8slist.APIError{Status: status}
	}
}

// listOrAbsent lists a CRD collection but treats a 404 (kind not served) as an
// empty list rather than an error — an engine version may not serve a given
// kind, and a CRD can be removed between probe and list.
func listOrAbsent[T any](ctx context.Context, r *k8slist.Reader, path string) ([]T, error) {
	items, err := k8slist.ListAll[T](ctx, r, path)
	if err != nil {
		var apiErr *k8slist.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return items, nil
}
