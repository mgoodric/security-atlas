package netpol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// newReaderForTest builds a k8slist.Reader pointed at a test server, for the
// CNI-reader unit tests that exercise list/probe branches directly.
func newReaderForTest(srv *httptest.Server) *k8slist.Reader {
	return k8slist.NewReader(srv.Client(), srv.URL, "test-k8s-token")
}

// cniHandlers builds an httptest mux that serves discovery probes + list
// responses. present controls which CRD group/versions answer 200 (installed)
// vs 404 (absent). The CNI policy specs deliberately embed selector labels,
// peer endpoints, a CIDR, and a port so the no-leak test can assert none of it
// escapes.
func cniHandlers(t *testing.T, present map[string]bool) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"prod"}},{"metadata":{"name":"dev"}}]}`))
	})
	mux.HandleFunc("/apis/networking.k8s.io/v1/networkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})

	// Discovery probes.
	probe := func(gv string) {
		mux.HandleFunc("/apis/"+gv, func(w http.ResponseWriter, _ *http.Request) {
			if present[gv] {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"resources":[]}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		})
	}
	probe("cilium.io/v2")
	probe("crd.projectcalico.org/v1")

	// Cilium namespaced policy in prod: all-endpoints (empty endpointSelector),
	// Ingress key present + EMPTY => default-deny ingress.
	mux.HandleFunc("/apis/cilium.io/v2/ciliumnetworkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		if !present["cilium.io/v2"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"cnp-deny","namespace":"prod"},
			 "spec":{"endpointSelector":{},"ingress":[]}},
			{"metadata":{"name":"cnp-allow","namespace":"prod"},
			 "spec":{"endpointSelector":{"matchLabels":{"app":"cilium-secret-label"}},
			   "ingress":[{"fromEndpoints":[{"matchLabels":{"team":"cilium-peer-label"}}],
			               "toPorts":[{"ports":[{"port":"9443"}]}]}]}}
		]}`))
	})
	// Cilium cluster-wide policy: all-endpoints, EMPTY egress => default-deny egress
	// for every namespace.
	mux.HandleFunc("/apis/cilium.io/v2/ciliumclusterwidenetworkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		if !present["cilium.io/v2"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"ccnp-egress-deny"},
			 "spec":{"endpointSelector":{},"egress":[]}}
		]}`))
	})

	// Calico namespaced policy in prod: selector all(), types [Ingress], zero rules.
	mux.HandleFunc("/apis/crd.projectcalico.org/v1/networkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		if !present["crd.projectcalico.org/v1"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"calico-deny","namespace":"prod"},
			 "spec":{"selector":"all()","types":["Ingress"]}},
			{"metadata":{"name":"calico-narrow","namespace":"prod"},
			 "spec":{"selector":"app == 'calico-secret-selector'","types":["Ingress"],
			   "ingress":[{"source":{"nets":["10.4.4.4/32"]},"destination":{"ports":[7443]}}]}}
		]}`))
	})
	mux.HandleFunc("/apis/crd.projectcalico.org/v1/globalnetworkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		if !present["crd.projectcalico.org/v1"] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	return mux
}

func newCNIServer(t *testing.T, present map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(cniHandlers(t, present))
	t.Cleanup(srv.Close)
	return srv
}

// TestClient_CNIAbsent_NoCNIPolicies pins the CRD-absence fallback: when neither
// CNI CRD is installed, the assessment is exactly the upstream-only result (no
// hard-fail, no CNI policies folded in).
func TestClient_CNIAbsent_NoCNIPolicies(t *testing.T) {
	srv := newCNIServer(t, map[string]bool{})
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	for _, ns := range got {
		if len(ns.Policies) != 0 {
			t.Errorf("ns %q should have 0 policies when no CNI present; got %d", ns.Name, len(ns.Policies))
		}
	}
}

// TestClient_CiliumPresent_FoldsNamespacedAndClusterwide pins AC-1: a present
// Cilium CRD contributes its namespaced default-deny to prod AND its cluster-wide
// default-deny to every namespace.
func TestClient_CiliumPresent_FoldsNamespacedAndClusterwide(t *testing.T) {
	srv := newCNIServer(t, map[string]bool{"cilium.io/v2": true})
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	covs, err := Assess(context.Background(), staticAPI(raw), fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	byNS := map[string]Coverage{}
	for _, cv := range covs {
		byNS[cv.Namespace] = cv
	}
	// prod gets namespaced ingress-deny + cluster-wide egress-deny => PASS both dirs.
	prod := byNS["prod"]
	if !prod.DefaultDenyIngress || !prod.DefaultDenyEgress {
		t.Errorf("prod default-deny ingress=%v egress=%v; want both", prod.DefaultDenyIngress, prod.DefaultDenyEgress)
	}
	// dev has no namespaced policy but inherits the cluster-wide egress-deny => PASS.
	dev := byNS["dev"]
	if !dev.DefaultDenyEgress {
		t.Errorf("dev should inherit cluster-wide egress default-deny; got %+v", dev)
	}
	if dev.Result != ResultPass {
		t.Errorf("dev should PASS via cluster-wide policy; got %q", dev.Result)
	}
	// The allow policy must NOT credit default-deny ingress on its own, but the
	// deny policy does — assert the source is tagged.
	foundCilium := false
	for _, p := range prod.Policies {
		if p.Source == SourceCilium {
			foundCilium = true
		}
	}
	if !foundCilium {
		t.Error("prod should carry a cilium.io-sourced policy summary")
	}
}

// TestClient_CalicoPresent_AllSelectorDefaultDeny pins the Calico mapping: an
// all()-selector zero-rule Ingress policy is default-deny; a narrow-selector
// policy is not.
func TestClient_CalicoPresent_AllSelectorDefaultDeny(t *testing.T) {
	srv := newCNIServer(t, map[string]bool{"crd.projectcalico.org/v1": true})
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	covs, err := Assess(context.Background(), staticAPI(raw), fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	var prod Coverage
	for _, cv := range covs {
		if cv.Namespace == "prod" {
			prod = cv
		}
	}
	if !prod.DefaultDenyIngress {
		t.Errorf("Calico all() zero-rule Ingress should be default-deny; got %+v", prod)
	}
	if prod.DefaultDenyEgress {
		t.Error("no Calico egress default-deny expected")
	}
}

// TestClient_CNINeverMaterializesPeerOrSelectorPayload is the slice-622 no-leak
// guard (AC-4, verbatim with slice 523): the CNI specs embed selector labels,
// peer endpoints, a CIDR, and ports; none may reach a record-bound field.
func TestClient_CNINeverMaterializesPeerOrSelectorPayload(t *testing.T) {
	srv := newCNIServer(t, map[string]bool{"cilium.io/v2": true, "crd.projectcalico.org/v1": true})
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListNamespaceCoverage(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaceCoverage: %v", err)
	}
	covs, err := Assess(context.Background(), staticAPI(raw), fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	banned := []string{
		"cilium-secret-label", "cilium-peer-label", "9443",
		"calico-secret-selector", "10.4.4.4", "7443",
	}
	for _, cov := range covs {
		blob := cov.Namespace + "|" + cov.Reason + "|" + strings.Join(cov.Sources, ",")
		for _, p := range cov.Policies {
			blob += "|" + p.Name + "|" + p.Source + "|" + strings.Join(p.PolicyTypes, ",") +
				"|" + fmt.Sprintf("%d/%d", p.IngressRuleCount, p.EgressRuleCount)
		}
		for _, b := range banned {
			if strings.Contains(blob, b) {
				t.Fatalf("CNI coverage record leaked payload %q in %q", b, blob)
			}
		}
	}
}

// TestClient_CNIProbeError_Propagates ensures a non-200/404 discovery status is a
// real error (not silently treated as absent).
func TestClient_CNIProbeError_Propagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"prod"}}]}`))
	})
	mux.HandleFunc("/apis/networking.k8s.io/v1/networkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	mux.HandleFunc("/apis/cilium.io/v2", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespaceCoverage(context.Background()); err == nil || !strings.Contains(err.Error(), "cni") {
		t.Fatalf("want cni probe error; got %v", err)
	}
}

func TestReduceCilium_Mapping(t *testing.T) {
	t.Parallel()
	// All-endpoints, empty ingress present => default-deny ingress shape.
	p := apiCiliumPolicy{}
	p.Metadata.Name = "x"
	p.Spec.Ingress = []json.RawMessage{} // present but empty
	rp := reduceCilium(p)
	if rp.Source != SourceCilium || !rp.SelectsAllPods || rp.IngressRuleCount != 0 {
		t.Errorf("cilium reduce = %+v", rp)
	}
	if len(rp.PolicyTypes) != 1 || rp.PolicyTypes[0] != PolicyTypeIngress {
		t.Errorf("cilium ingress-present should govern Ingress; got %v", rp.PolicyTypes)
	}
}

func TestCalicoSelectsAll(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", " ", "all()", " all() "} {
		if !calicoSelectsAll(s) {
			t.Errorf("calicoSelectsAll(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"app == 'x'", "has(role)"} {
		if calicoSelectsAll(s) {
			t.Errorf("calicoSelectsAll(%q) = true; want false", s)
		}
	}
}

// TestListOrAbsent_404IsEmpty pins that a present-CRD probe followed by a 404 on
// the list path (kind not served / removed between probe and list) yields an
// empty list, not an error.
func TestListOrAbsent_404IsEmpty(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	r := newReaderForTest(srv)
	got, err := listOrAbsent[apiCalicoPolicy](context.Background(), r, "/apis/crd.projectcalico.org/v1/globalnetworkpolicies")
	if err != nil {
		t.Fatalf("404 should be empty, not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty; got %d", len(got))
	}
}

// TestListOrAbsent_RealErrorPropagates pins that a 403 (forbidden) on a present
// CRD list is a real error, not silently dropped.
func TestListOrAbsent_RealErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	r := newReaderForTest(srv)
	if _, err := listOrAbsent[apiCiliumPolicy](context.Background(), r, "/apis/cilium.io/v2/ciliumnetworkpolicies"); err == nil {
		t.Fatal("403 should propagate as an error")
	}
}

// TestCNICollect_ListErrorPropagates drives the list-error branch through the
// public collect path: the CRD probes present but the namespaced list 403s.
func TestCNICollect_ListErrorPropagates(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/cilium.io/v2", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/apis/crd.projectcalico.org/v1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/apis/cilium.io/v2/ciliumnetworkpolicies", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	rd := newCNIReader(newReaderForTest(srv))
	if _, _, err := rd.collect(context.Background()); err == nil {
		t.Fatal("want list error to propagate from collect")
	}
}

func TestReduceCalico_Mapping(t *testing.T) {
	t.Parallel()
	p := apiCalicoPolicy{}
	p.Metadata.Name = "gnp"
	p.Spec.Selector = "all()"
	p.Spec.Types = []string{PolicyTypeIngress, PolicyTypeEgress}
	rp := reduceCalico(p)
	if rp.Source != SourceCalico || !rp.SelectsAllPods {
		t.Errorf("calico reduce = %+v", rp)
	}
	if len(rp.PolicyTypes) != 2 {
		t.Errorf("calico types = %v", rp.PolicyTypes)
	}
}
