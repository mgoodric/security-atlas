package pss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeK8s serves a canned namespace list. The namespaces deliberately carry
// UNRELATED labels (a secret-looking value, a team label) AND annotations (a
// kubectl last-applied blob with embedded secret material) alongside the
// pod-security.kubernetes.io/* labels. The label-filter test then asserts NONE
// of that non-PSS payload escapes into a RawNamespace / Admission — only the PSS
// labels survive (the structural over-collection guard's client-boundary half).
func newFakeK8s(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{
				"metadata":{
					"name":"prod",
					"labels":{
						"pod-security.kubernetes.io/enforce":"restricted",
						"pod-security.kubernetes.io/enforce-version":"v1.29",
						"pod-security.kubernetes.io/audit":"baseline",
						"pod-security.kubernetes.io/warn":"baseline",
						"team":"confidential-team-label",
						"kubernetes.io/metadata.name":"prod",
						"some-other-label":"super-secret-label-value"
					},
					"annotations":{
						"kubectl.kubernetes.io/last-applied-configuration":"{\"secret\":\"annotation-secret-blob\"}",
						"owner-email":"pii-leak@example.test"
					}
				},
				"spec":{"finalizers":["kubernetes"]},
				"status":{"phase":"Active"}
			},
			{
				"metadata":{
					"name":"dev",
					"labels":{"kubernetes.io/metadata.name":"dev"}
				}
			}
		]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListNamespacePSS_ReducesLabels(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListNamespacePSS(context.Background())
	if err != nil {
		t.Fatalf("ListNamespacePSS: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("namespaces = %d; want 2", len(got))
	}
	// Sorted by name: dev, prod.
	dev, prod := got[0], got[1]
	if dev.Name != "dev" || prod.Name != "prod" {
		t.Fatalf("sort wrong: %+v", got)
	}
	if prod.EnforceLevel != LevelRestricted || prod.EnforceVersion != "v1.29" {
		t.Errorf("prod enforce reduce wrong: %+v", prod)
	}
	if prod.AuditLevel != LevelBaseline || prod.WarnLevel != LevelBaseline {
		t.Errorf("prod audit/warn reduce wrong: %+v", prod)
	}
	// dev has no PSS labels -> all unset.
	if dev.EnforceLevel != LevelUnset || dev.AuditLevel != LevelUnset || dev.WarnLevel != LevelUnset {
		t.Errorf("dev should have no PSS levels; got %+v", dev)
	}
}

// TestClient_OnlyPSSLabelsReachRecord is the load-bearing label-filter test. The
// namespace object carries unrelated labels (a team label, a secret-looking
// label value) AND annotations (a kubectl last-applied blob with embedded
// secret material, an owner-email PII annotation). We run the full collect →
// assess path and assert NONE of that non-PSS payload stringifies into any
// record-bound field of RawNamespace or Admission. Only the
// pod-security.kubernetes.io/* labels can reach a record.
func TestClient_OnlyPSSLabelsReachRecord(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListNamespacePSS(context.Background())
	if err != nil {
		t.Fatalf("ListNamespacePSS: %v", err)
	}

	banned := []string{
		"confidential-team-label",     // unrelated label value
		"super-secret-label-value",    // unrelated label value
		"annotation-secret-blob",      // annotation content
		"pii-leak@example.test",       // annotation PII
		"last-applied-configuration",  // annotation key
		"kubernetes.io/metadata.name", // unrelated system label key
		"finalizers",                  // spec field
		"Active",                      // status field
	}

	// Assert at the RawNamespace boundary (the client reduce()).
	for _, ns := range raw {
		assertNoBanned(t, "raw:"+ns.Name, rawBlob(ns), banned)
	}

	// Assert again after the full assessment transform.
	admissions, err := Assess(context.Background(), staticAPI(raw), nil)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	for _, a := range admissions {
		assertNoBanned(t, "admission:"+a.Namespace, admissionBlob(a), banned)
	}
}

func rawBlob(ns RawNamespace) string {
	return strings.Join([]string{
		ns.Name,
		string(ns.EnforceLevel), ns.EnforceVersion,
		string(ns.AuditLevel), ns.AuditVersion,
		string(ns.WarnLevel), ns.WarnVersion,
	}, "|")
}

func admissionBlob(a Admission) string {
	return strings.Join([]string{
		a.Namespace, a.Reason,
		string(a.EnforceLevel), a.EnforceVersion,
		string(a.AuditLevel), a.AuditVersion,
		string(a.WarnLevel), a.WarnVersion,
	}, "|")
}

func assertNoBanned(t *testing.T, where, blob string, banned []string) {
	t.Helper()
	for _, b := range banned {
		if strings.Contains(blob, b) {
			t.Fatalf("%s leaked non-PSS payload %q in %q", where, b, blob)
		}
	}
}

type staticAPI []RawNamespace

func (s staticAPI) ListNamespacePSS(_ context.Context) ([]RawNamespace, error) { return s, nil }

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "namespaces") {
			gotAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespacePSS(context.Background()); err != nil {
		t.Fatalf("ListNamespacePSS: %v", err)
	}
	if gotAuth != "Bearer test-k8s-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListNamespacePSS(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestClient_SkipsUnnamedNamespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":""}},
			{"metadata":{"name":"prod","labels":{"pod-security.kubernetes.io/enforce":"restricted"}}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListNamespacePSS(context.Background())
	if err != nil {
		t.Fatalf("ListNamespacePSS: %v", err)
	}
	if len(got) != 1 || got[0].Name != "prod" {
		t.Fatalf("want only named namespace; got %+v", got)
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
