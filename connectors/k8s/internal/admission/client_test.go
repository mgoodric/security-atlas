package admission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReduceWebhook_DropsCABundleAndURL is THE load-bearing config-metadata-only
// proof for webhooks: feed a webhook configuration JSON with a REAL caBundle + a
// dispatch URL + an intercepted-object-shaped field and assert that ONLY
// configuration metadata survives — no caBundle, no URL, no TLS key, ever enters
// a RawWebhook. The fixture values are obviously-fake neutral markers (no
// vendor-shaped cert / PEM), so the branch-scoped secret scanner does not flag
// them.
func TestReduceWebhook_DropsCABundleAndURL(t *testing.T) {
	t.Parallel()
	const caBundleMarker = "test-cabundle-should-be-dropped"
	const urlMarker = "https://test-webhook-url-should-be-dropped.example/validate"
	const tlsKeyMarker = "test-tls-private-key-should-be-dropped"
	cfgJSON := []byte(`{
	  "metadata": {"name": "gatekeeper-validating-webhook-configuration"},
	  "webhooks": [{
	    "name": "validation.gatekeeper.sh",
	    "failurePolicy": "Fail",
	    "sideEffects": "None",
	    "namespaceSelector": {"matchLabels": {"team": "should-not-be-read"}},
	    "clientConfig": {
	      "caBundle": "` + caBundleMarker + `",
	      "url": "` + urlMarker + `",
	      "tlsKey": "` + tlsKeyMarker + `",
	      "service": {"namespace": "gatekeeper-system", "name": "gatekeeper-webhook-service", "path": "/v1/admit"}
	    },
	    "rules": [{"operations": ["CREATE", "UPDATE"], "resources": ["pods", "deployments"]}]
	  }]
	}`)

	var cfg apiWebhookConfiguration
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	raws := reduceConfig(KindValidating, cfg)
	if len(raws) != 1 {
		t.Fatalf("reduceConfig returned %d; want 1", len(raws))
	}
	raw := raws[0]

	// Metadata survives.
	if raw.ConfigName != "gatekeeper-validating-webhook-configuration" || raw.WebhookName != "validation.gatekeeper.sh" {
		t.Errorf("metadata not preserved: %+v", raw)
	}
	if raw.FailurePolicy != FailurePolicyFail {
		t.Errorf("failurePolicy = %q", raw.FailurePolicy)
	}
	if !raw.HasNamespaceSelector {
		t.Errorf("namespace selector presence not detected")
	}
	if raw.TargetService != "gatekeeper-system/gatekeeper-webhook-service" {
		t.Errorf("target service = %q", raw.TargetService)
	}
	if strings.Join(raw.InterceptedResources, ",") != "pods,deployments" {
		t.Errorf("resources = %v", raw.InterceptedResources)
	}

	// No caBundle / URL / TLS key — anywhere in the reduced struct.
	assertNoLeak(t, raw, caBundleMarker, urlMarker, tlsKeyMarker, "should-not-be-read")

	// And through the full Collect -> Webhook transform too.
	got, err := CollectWebhooks(context.Background(), &fakeWebhookAPI{webhooks: []RawWebhook{raw}}, fixedNow())
	if err != nil {
		t.Fatalf("CollectWebhooks: %v", err)
	}
	assertNoLeak(t, got[0], caBundleMarker, urlMarker, tlsKeyMarker, "should-not-be-read")
}

// TestPolicyDecode_DropsRegoAndCELBody is THE load-bearing config-metadata-only
// proof for policy engines: feed a Gatekeeper ConstraintTemplate WITH a Rego
// target body and a Kyverno policy WITH a CEL/validate rule body, and assert that
// ONLY configuration metadata (name / kind / scope / enforcement-action) survives
// — no Rego, no CEL, no rule body, ever enters a RawPolicy.
func TestPolicyDecode_DropsRegoAndCELBody(t *testing.T) {
	t.Parallel()
	const regoMarker = "test-rego-body-should-be-dropped"
	const celMarker = "test-cel-validate-body-should-be-dropped"

	tmplJSON := []byte(`{
	  "metadata": {"name": "k8srequiredlabels"},
	  "spec": {
	    "crd": {"spec": {"names": {"kind": "K8sRequiredLabels"}}},
	    "targets": [{"target": "admission.k8s.gatekeeper.sh", "rego": "` + regoMarker + `"}]
	  }
	}`)
	var tmpl apiGatekeeperTemplate
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatalf("unmarshal template: %v", err)
	}
	if tmpl.Metadata.Name != "k8srequiredlabels" || tmpl.Spec.CRD.Spec.Names.Kind != "K8sRequiredLabels" {
		t.Errorf("template metadata not preserved: %+v", tmpl)
	}
	// The decode target has no field for the Rego body, so it cannot have been read.
	blob, _ := json.Marshal(tmpl)
	if strings.Contains(string(blob), regoMarker) {
		t.Fatalf("REGO BODY LEAKED into the template decode target: %s", blob)
	}

	polJSON := []byte(`{
	  "metadata": {"name": "require-team-label", "namespace": "team-a"},
	  "spec": {
	    "validationFailureAction": "Enforce",
	    "rules": [{"name": "check", "validate": {"cel": "` + celMarker + `", "message": "should-not-be-read"}}]
	  }
	}`)
	var pol apiKyvernoPolicy
	if err := json.Unmarshal(polJSON, &pol); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	if pol.Metadata.Name != "require-team-label" || pol.Spec.ValidationFailureAction != "Enforce" {
		t.Errorf("policy metadata not preserved: %+v", pol)
	}
	pblob, _ := json.Marshal(pol)
	if strings.Contains(string(pblob), celMarker) || strings.Contains(string(pblob), "should-not-be-read") {
		t.Fatalf("CEL/VALIDATE BODY LEAKED into the policy decode target: %s", pblob)
	}
}

// TestPolicyClient_AbsentEngineNotAnError proves the slice-622 probe pattern: a
// cluster with NEITHER Gatekeeper NOR Kyverno installed (both discovery probes
// 404) yields zero policies and no error.
func TestPolicyClient_AbsentEngineNotAnError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Every discovery / list path 404s — no engine installed.
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("absent engine must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("absent engine must yield no policies; got %d", len(got))
	}
}

// TestPolicyClient_GatekeeperPresentListsTemplates drives the present-engine path:
// the templates.gatekeeper.sh discovery probe returns 200 and the template list
// returns one ConstraintTemplate. We assert the template surfaces as a
// cluster-scoped Gatekeeper policy with its rendered kind, and that the Rego
// target body in the list response never reaches a RawPolicy.
func TestPolicyClient_GatekeeperPresentListsTemplates(t *testing.T) {
	t.Parallel()
	const regoMarker = "test-rego-should-not-surface"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/" + gatekeeperTemplatesGV:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"APIResourceList"}`))
		case gatekeeperTemplates:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[
			  {"metadata":{"name":"k8srequiredlabels"},"spec":{"crd":{"spec":{"names":{"kind":"K8sRequiredLabels"}}},"targets":[{"rego":"` + regoMarker + `"}]}}
			],"metadata":{"continue":""}}`))
		default:
			// kyverno probe + anything else: absent.
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 gatekeeper template policy; got %d (%+v)", len(got), got)
	}
	p := got[0]
	if p.Engine != EngineGatekeeper || p.Name != "k8srequiredlabels" || p.PolicyKind != "K8sRequiredLabels" {
		t.Errorf("gatekeeper policy not mapped: %+v", p)
	}
	if p.Scope != ScopeCluster {
		t.Errorf("gatekeeper template scope = %q; want cluster", p.Scope)
	}
	assertNoLeak(t, p, regoMarker)
}

// TestPolicyClient_KyvernoPresentListsPolicies drives the Kyverno present path:
// the kyverno.io probe 200s, clusterpolicies + policies both return one item. We
// assert both surface with the right scope + enforcement action.
func TestPolicyClient_KyvernoPresentListsPolicies(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/" + kyvernoGV:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"APIResourceList"}`))
		case kyvernoClusterPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[
			  {"metadata":{"name":"require-labels"},"spec":{"validationFailureAction":"Enforce","rules":[{"validate":{"cel":"test-cel-drop"}}]}}
			],"metadata":{"continue":""}}`))
		case kyvernoNamespacedPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[
			  {"metadata":{"name":"audit-pol","namespace":"team-a"},"spec":{"validationFailureAction":"Audit"}}
			],"metadata":{"continue":""}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 kyverno policies; got %d (%+v)", len(got), got)
	}
	byName := map[string]RawPolicy{}
	for _, p := range got {
		byName[p.Name] = p
	}
	cp := byName["require-labels"]
	if cp.Scope != ScopeCluster || cp.PolicyKind != "ClusterPolicy" || cp.EnforcementAction != "Enforce" {
		t.Errorf("clusterpolicy not mapped: %+v", cp)
	}
	np := byName["audit-pol"]
	if np.Scope != ScopeNamespaced || np.Namespace != "team-a" || np.EnforcementAction != "Audit" {
		t.Errorf("namespaced policy not mapped: %+v", np)
	}
	assertNoLeak(t, cp, "test-cel-drop")
}

// TestPolicyClient_ProbeServerErrorPropagates proves a non-404 probe error (e.g.
// a 500 from the API server) is a real error, not silently treated as absent.
func TestPolicyClient_ProbeServerErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListPolicies(context.Background()); err == nil {
		t.Fatal("want error on probe 500")
	}
}

// TestWebhookClient_ListsBothKinds drives the webhook list path end-to-end: a
// validating + a mutating configuration each return one webhook entry. We assert
// both surface with the right kind and the caBundle in the response never
// reaches a RawWebhook.
func TestWebhookClient_ListsBothKinds(t *testing.T) {
	t.Parallel()
	const caMarker = "test-cabundle-drop-via-client"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[
			  {"metadata":{"name":"vcfg"},"webhooks":[{"name":"v.example.com","failurePolicy":"Fail","clientConfig":{"caBundle":"` + caMarker + `","service":{"namespace":"ns","name":"svc"}},"rules":[{"operations":["CREATE"],"resources":["pods"]}]}]}
			],"metadata":{"continue":""}}`))
		case "/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[
			  {"metadata":{"name":"mcfg"},"webhooks":[{"name":"m.example.com","failurePolicy":"Ignore","clientConfig":{"service":{"namespace":"ns","name":"msvc"}}}]}
			],"metadata":{"continue":""}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewWebhookClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListWebhooks(context.Background())
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 webhooks; got %d (%+v)", len(got), got)
	}
	var sawValidating, sawMutating bool
	for _, wh := range got {
		switch wh.Kind {
		case KindValidating:
			sawValidating = true
		case KindMutating:
			sawMutating = true
		}
		assertNoLeak(t, wh, caMarker)
	}
	if !sawValidating || !sawMutating {
		t.Errorf("want both validating + mutating; got %+v", got)
	}
}

// TestWebhookClient_ValidatingListErrorPropagates proves a non-200 on the
// validating-webhook list is a hard error (a partial read is never reported as
// complete).
func TestWebhookClient_ValidatingListErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewWebhookClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListWebhooks(context.Background()); err == nil {
		t.Fatal("want error on validating-list 403")
	}
}

// assertNoLeak marshals the whole record and asserts none of the forbidden
// markers appears anywhere — the value-never-materializes proof.
func assertNoLeak(t *testing.T, record any, markers ...string) {
	t.Helper()
	blob, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	hay := string(blob)
	for _, needle := range markers {
		if needle != "" && strings.Contains(hay, needle) {
			t.Fatalf("FORBIDDEN MATERIAL LEAKED into a record (found %q): %s", needle, hay)
		}
	}
}
