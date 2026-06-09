package netpol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeK8s serves canned namespace + networkpolicy list responses. The
// NetworkPolicy spec blocks deliberately embed ingress `from` peers carrying a
// namespaceSelector label, an explicit CIDR, and a port — plus a podSelector
// matchLabels value. The test then asserts NONE of that peer/selector payload
// escapes into the reduced RawPolicy (the P0-523 over-collection guard); only
// SPEC metadata (name, types, all-pods flag, rule COUNTS) survives.
func newFakeK8s(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"prod"}},
			{"metadata":{"name":"dev"}}
		]}`))
	})
	mux.HandleFunc("/apis/networking.k8s.io/v1/networkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{
				"metadata":{"name":"default-deny-ingress","namespace":"prod"},
				"spec":{"podSelector":{},"policyTypes":["Ingress"]}
			},
			{
				"metadata":{"name":"allow-api","namespace":"prod"},
				"spec":{
					"podSelector":{"matchLabels":{"app":"top-secret-app-label"}},
					"policyTypes":["Ingress"],
					"ingress":[{
						"from":[
							{"namespaceSelector":{"matchLabels":{"team":"confidential-team-label"}}},
							{"ipBlock":{"cidr":"10.9.8.7/32"}}
						],
						"ports":[{"protocol":"TCP","port":8443}]
					}]
				}
			}
		]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListNamespaceCoverage_GroupsAndReduces(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("namespaces = %d; want 2", len(got))
	}

	var prod, dev *RawNamespace
	for i := range got {
		switch got[i].Name {
		case "prod":
			prod = &got[i]
		case "dev":
			dev = &got[i]
		}
	}
	if prod == nil || dev == nil {
		t.Fatalf("missing namespaces; got %+v", got)
	}
	if len(dev.Policies) != 0 {
		t.Errorf("dev should have 0 policies; got %d", len(dev.Policies))
	}
	if len(prod.Policies) != 2 {
		t.Fatalf("prod should have 2 policies; got %d", len(prod.Policies))
	}
	// Sorted by name: allow-api, default-deny-ingress.
	deny := prod.Policies[1]
	if deny.Name != "default-deny-ingress" || !deny.SelectsAllPods || deny.IngressRuleCount != 0 {
		t.Errorf("default-deny reduce wrong: %+v", deny)
	}
	allow := prod.Policies[0]
	if allow.Name != "allow-api" || allow.SelectsAllPods || allow.IngressRuleCount != 1 {
		t.Errorf("allow reduce wrong: %+v", allow)
	}
}

// TestClient_NeverMaterializesPeerOrSelectorPayload is the client-boundary half
// of the P0-523 over-collection guard: the API response embeds podSelector
// labels, ingress peer namespaceSelector labels, a CIDR, and a port, but the
// reduced RawPolicy + the full Coverage assessment carry only SPEC metadata. We
// run the whole collect → assess path and assert none of the banned payload
// stringifies into any record-bound field.
func TestClient_NeverMaterializesPeerOrSelectorPayload(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	covs, err := Assess(context.Background(), staticAPI(raw), nil)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	banned := []string{"top-secret-app-label", "confidential-team-label", "10.9.8.7", "8443"}
	for _, cov := range covs {
		blob := cov.Namespace + "|" + cov.Reason
		for _, p := range cov.Policies {
			blob += "|" + p.Name + "|" + strings.Join(p.PolicyTypes, ",") +
				"|" + fmt.Sprintf("%d/%d", p.IngressRuleCount, p.EgressRuleCount)
		}
		for _, b := range banned {
			if strings.Contains(blob, b) {
				t.Fatalf("coverage record leaked peer/selector payload %q in %q", b, blob)
			}
		}
	}
}

type staticAPI []RawNamespace

func (s staticAPI) ListNamespaceCoverage(_ context.Context) ([]RawNamespace, error) { return s, nil }

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "networkpolicies") {
			gotAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespaceCoverage(context.Background()); err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	if gotAuth != "Bearer test-k8s-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestClient_NamespaceListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespaceCoverage(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestClient_NetpolListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "networkpolicies") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespaceCoverage(context.Background()); err == nil || !strings.Contains(err.Error(), "networkpolicies") {
		t.Fatalf("want networkpolicies error; got %v", err)
	}
}

func TestDerivePolicyTypes_OmittedDerivesFromBlocks(t *testing.T) {
	t.Parallel()
	// No policyTypes, no egress -> Ingress only.
	got := derivePolicyTypes(apiNetpolSpec{})
	if len(got) != 1 || got[0] != PolicyTypeIngress {
		t.Errorf("ingress-only derive = %v", got)
	}
	// No policyTypes, with an egress block -> Ingress + Egress.
	got = derivePolicyTypes(apiNetpolSpec{Egress: []json.RawMessage{[]byte("{}")}})
	if len(got) != 2 {
		t.Errorf("ingress+egress derive = %v", got)
	}
}

func TestLabelSelector_IsEmpty(t *testing.T) {
	t.Parallel()
	if !(&apiLabelSelector{}).isEmpty() {
		t.Error("zero selector should be empty")
	}
	if (&apiLabelSelector{MatchLabels: map[string]string{"a": "b"}}).isEmpty() {
		t.Error("selector with matchLabels is not empty")
	}
	var nilSel *apiLabelSelector
	if !nilSel.isEmpty() {
		t.Error("nil selector should be empty")
	}
}

func TestAPIError_String(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 401}).Error() != "k8s: HTTP 401" {
		t.Error("bare status mismatch")
	}
	if !strings.Contains((&APIError{Status: 500, Body: "boom"}).Error(), "boom") {
		t.Error("body should be included")
	}
}

// TestClient_ListNamespaceCoverage_FollowsContinueAcrossPages is the slice-621
// AC-2 proof for the netpol client: the networkpolicies endpoint returns two
// pages and the client accumulates policies across both, grouping them under
// their namespaces. A cluster with more than one page of networkpolicies is no
// longer silently truncated.
func TestClient_ListNamespaceCoverage_FollowsContinueAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[{"metadata":{"name":"prod"}}]}`))
	})
	mux.HandleFunc("/apis/networking.k8s.io/v1/networkpolicies", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("continue") {
		case "":
			_, _ = w.Write([]byte(`{"metadata":{"continue":"PAGE-2"},"items":[
				{"metadata":{"name":"np-one","namespace":"prod"},"spec":{"podSelector":{},"policyTypes":["Ingress"]}}
			]}`))
		case "PAGE-2":
			_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[
				{"metadata":{"name":"np-two","namespace":"prod"},"spec":{"podSelector":{},"policyTypes":["Ingress"]}}
			]}`))
		default:
			t.Errorf("unexpected continue token %q", r.URL.Query().Get("continue"))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	if len(got) != 1 || got[0].Name != "prod" {
		t.Fatalf("namespaces = %+v; want one ('prod')", got)
	}
	if len(got[0].Policies) != 2 {
		t.Fatalf("prod policies = %d; want 2 accumulated across two pages", len(got[0].Policies))
	}
	names := map[string]bool{got[0].Policies[0].Name: true, got[0].Policies[1].Name: true}
	if !names["np-one"] || !names["np-two"] {
		t.Errorf("expected np-one + np-two across pages; got %+v", got[0].Policies)
	}
}
