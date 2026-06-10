package admission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReduceConfig_EmptyConfigNameYieldsNothing covers the reduceConfig early
// return: a configuration with no metadata.name contributes no webhook records.
func TestReduceConfig_EmptyConfigNameYieldsNothing(t *testing.T) {
	t.Parallel()
	got := reduceConfig(KindValidating, apiWebhookConfiguration{})
	if len(got) != 0 {
		t.Fatalf("empty-named config must yield nothing; got %d", len(got))
	}
}

// TestReduceWebhook_ServiceNameOnlyTarget covers the target-service branch where
// the service ref carries a name but no namespace.
func TestReduceWebhook_ServiceNameOnlyTarget(t *testing.T) {
	t.Parallel()
	wh := apiWebhookEntry{
		Name:         "w",
		ClientConfig: apiClientConfig{Service: &apiServiceRef{Name: "svc-only"}},
	}
	raw := reduceWebhook(KindValidating, "cfg", wh)
	if raw.TargetService != "svc-only" {
		t.Errorf("name-only target = %q; want svc-only", raw.TargetService)
	}
	// A service ref with neither namespace nor name yields an empty target.
	wh2 := apiWebhookEntry{Name: "w", ClientConfig: apiClientConfig{Service: &apiServiceRef{}}}
	if got := reduceWebhook(KindValidating, "cfg", wh2).TargetService; got != "" {
		t.Errorf("empty service ref target = %q; want empty", got)
	}
}

// TestDedupeSorted_AllEmptyYieldsNil covers the dedupeSorted branch where every
// input entry is empty (so the deduped result is empty -> nil).
func TestDedupeSorted_AllEmptyYieldsNil(t *testing.T) {
	t.Parallel()
	if got := dedupeSorted([]string{"", "", ""}); got != nil {
		t.Errorf("all-empty dedupe = %v; want nil", got)
	}
	if got := dedupeSorted(nil); got != nil {
		t.Errorf("nil dedupe = %v; want nil", got)
	}
}

// TestPolicyClient_KyvernoPresentButKindsAbsent covers the listOrAbsent 404 path:
// the kyverno.io group probe 200s but both list paths 404 (a version that does
// not serve those kinds) — yields zero policies, no error.
func TestPolicyClient_KyvernoPresentButKindsAbsent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis/"+kyvernoGV {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"APIResourceList"}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("present-but-no-kinds must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want zero policies; got %d", len(got))
	}
}

// TestPolicyClient_GatekeeperListError covers the collectGatekeeper list-error
// path: the templates group is present (probe 200) but the list returns a 500.
func TestPolicyClient_GatekeeperListError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/" + gatekeeperTemplatesGV:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"APIResourceList"}`))
		case gatekeeperTemplates:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListPolicies(context.Background()); err == nil {
		t.Fatal("want error on gatekeeper template list 500")
	}
}

// TestPolicyClient_KyvernoNamespacedListError covers the namespaced-list error
// path after the clusterpolicy list succeeds.
func TestPolicyClient_KyvernoNamespacedListError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/" + kyvernoGV:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"APIResourceList"}`))
		case kyvernoClusterPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[],"metadata":{"continue":""}}`))
		case kyvernoNamespacedPath:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewPolicyClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListPolicies(context.Background()); err == nil {
		t.Fatal("want error on kyverno namespaced-policy list 500")
	}
}

// TestWebhookClient_MutatingListErrorPropagates covers the mutating-list error
// branch after the validating list succeeds.
func TestWebhookClient_MutatingListErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[],"metadata":{"continue":""}}`))
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := NewWebhookClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListWebhooks(context.Background()); err == nil {
		t.Fatal("want error on mutating-list 403")
	}
}

// TestWebhookClient_SkipsEmptyConfigNameItem covers the ListWebhooks path where
// an item has no config name (filtered out by reduceConfig).
func TestWebhookClient_SkipsEmptyConfigNameItem(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":""},"webhooks":[{"name":"w"}]}],"metadata":{"continue":""}}`))
		case "/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[],"metadata":{"continue":""}}`))
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
	if len(got) != 0 {
		t.Fatalf("empty-named config must be skipped; got %d", len(got))
	}
}
