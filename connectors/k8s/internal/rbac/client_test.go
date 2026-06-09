package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeK8s serves canned RBAC list responses so the client is exercised without a
// live cluster. No real tokens — a neutral "test-k8s-token" only.
func newFakeK8s(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/clusterroles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"cluster-admin"},"rules":[{"apiGroups":["*"],"resources":["*"],"verbs":["*"]}]},
			{"metadata":{"name":"view"},"rules":[{"apiGroups":[""],"resources":["pods"],"verbs":["get","list"]}]}
		]}`))
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"reader","namespace":"default"},"rules":[{"apiGroups":[""],"resources":["configmaps"],"verbs":["get"]}]}
		]}`))
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"admins"},"roleRef":{"kind":"ClusterRole","name":"cluster-admin"},"subjects":[{"kind":"User","name":"alice"}]}
		]}`))
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/rolebindings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"readers","namespace":"default"},"roleRef":{"kind":"Role","name":"reader"},"subjects":[{"kind":"ServiceAccount","name":"sa","namespace":"default"}]}
		]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListBindings_ResolvesRulesAndScopes(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListBindings(context.Background())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2 (1 CRB + 1 RB)", len(got))
	}

	var crb, rb *RawBinding
	for i := range got {
		switch got[i].Name {
		case "admins":
			crb = &got[i]
		case "readers":
			rb = &got[i]
		}
	}
	if crb == nil || rb == nil {
		t.Fatalf("missing expected bindings; got %+v", got)
	}
	if crb.Scope != ScopeCluster {
		t.Errorf("admins scope = %q; want cluster", crb.Scope)
	}
	if len(crb.Rules) != 1 || crb.Rules[0].Verbs[0] != "*" {
		t.Errorf("cluster-admin rules not resolved: %+v", crb.Rules)
	}
	if rb.Scope != ScopeNamespace || rb.Namespace != "default" {
		t.Errorf("readers scope/ns = %q/%q", rb.Scope, rb.Namespace)
	}
	if len(rb.Rules) != 1 || rb.Rules[0].Resources[0] != "configmaps" {
		t.Errorf("namespaced role rules not resolved: %+v", rb.Rules)
	}
}

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "clusterroles") {
			gotAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListBindings(context.Background()); err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if gotAuth != "Bearer test-k8s-token" {
		t.Errorf("Authorization = %q; want Bearer test-k8s-token", gotAuth)
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	_, err := c.ListBindings(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestAPIError_String(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 500}).Error() != "k8s: HTTP 500" {
		t.Error("bare status error mismatch")
	}
	if !strings.Contains((&APIError{Status: 500, Body: "boom"}).Error(), "boom") {
		t.Error("body should appear in error")
	}
}

// TestClient_ListBindings_FollowsContinueAcrossPages is the slice-621 AC-2 proof
// for the rbac client: the clusterrolebindings endpoint returns two pages (page
// 1 carries a non-empty metadata.continue, page 2 an empty one) and the client
// accumulates bindings across both. A cluster with more than one page of
// clusterrolebindings is no longer silently truncated.
func TestClient_ListBindings_FollowsContinueAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/clusterroles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[
			{"metadata":{"name":"view"},"rules":[{"apiGroups":[""],"resources":["pods"],"verbs":["get","list"]}]}
		]}`))
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[]}`))
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("continue") {
		case "":
			_, _ = w.Write([]byte(`{"metadata":{"continue":"PAGE-2"},"items":[
				{"metadata":{"name":"crb-one"},"roleRef":{"kind":"ClusterRole","name":"view"},"subjects":[{"kind":"User","name":"alice"}]}
			]}`))
		case "PAGE-2":
			_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[
				{"metadata":{"name":"crb-two"},"roleRef":{"kind":"ClusterRole","name":"view"},"subjects":[{"kind":"User","name":"bob"}]}
			]}`))
		default:
			t.Errorf("unexpected continue token %q", r.URL.Query().Get("continue"))
		}
	})
	mux.HandleFunc("/apis/rbac.authorization.k8s.io/v1/rolebindings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListBindings(context.Background())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2 clusterrolebindings accumulated across two pages", len(got))
	}
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["crb-one"] || !names["crb-two"] {
		t.Errorf("expected crb-one + crb-two across pages; got %+v", got)
	}
}
