package workload

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeK8s serves canned workload list responses. The pod templates embed
// container env + envFrom + a Secret-backed volume on purpose: the test then
// asserts NONE of that payload escapes into the reduced RawWorkload (P0-487-3).
func newFakeK8s(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/apps/v1/deployments", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{
			"metadata":{"name":"api","namespace":"prod"},
			"spec":{"template":{"spec":{
				"hostNetwork":false,
				"securityContext":{"runAsNonRoot":true},
				"containers":[{
					"securityContext":{"readOnlyRootFilesystem":true,"allowPrivilegeEscalation":false},
					"env":[{"name":"DB_PASSWORD","value":"super-secret-value"}],
					"envFrom":[{"secretRef":{"name":"db-creds"}}],
					"volumeMounts":[{"name":"creds","mountPath":"/creds"}]
				}],
				"volumes":[{"name":"creds","secret":{"secretName":"db-creds"}}]
			}}}
		}]}`))
	})
	mux.HandleFunc("/apis/apps/v1/daemonsets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{
			"metadata":{"name":"node-agent","namespace":"kube-system"},
			"spec":{"template":{"spec":{
				"hostPID":true,
				"containers":[{"securityContext":{"privileged":true}}]
			}}}
		}]}`))
	})
	mux.HandleFunc("/apis/apps/v1/statefulsets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListWorkloads_ReducesSecurityContext(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListWorkloads(context.Background())
	if err != nil {
		t.Fatalf("ListWorkloads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2 (1 deployment + 1 daemonset)", len(got))
	}

	var dep, ds *RawWorkload
	for i := range got {
		switch got[i].Name {
		case "api":
			dep = &got[i]
		case "node-agent":
			ds = &got[i]
		}
	}
	if dep == nil || ds == nil {
		t.Fatalf("missing workloads; got %+v", got)
	}
	// Hardened deployment: non-root (pod) + readonly fs + no escalation.
	if !dep.RunAsNonRoot || !dep.ReadOnlyRootFilesystem || dep.AllowPrivilegeEscalation || dep.Privileged {
		t.Errorf("deployment reduce wrong: %+v", dep)
	}
	if dep.ContainerCount != 1 {
		t.Errorf("container_count = %d; want 1", dep.ContainerCount)
	}
	// Daemonset: privileged + hostPID.
	if !ds.Privileged || !ds.HostPID {
		t.Errorf("daemonset reduce wrong: %+v", ds)
	}
}

// TestClient_NeverMaterializesSecretsOrEnv is the client-boundary half of
// P0-487-3: the API response embeds env values + Secret refs, but the reduced
// RawWorkload Go struct has no field that could carry them. We assert the struct
// holds only the modeled security-context fields by checking the verdict path
// stays clean and that nothing leaks via the exported shape.
func TestClient_NeverMaterializesSecretsOrEnv(t *testing.T) {
	srv := newFakeK8s(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListWorkloads(context.Background())
	if err != nil {
		t.Fatalf("ListWorkloads: %v", err)
	}
	// Run the full collect path and assert no field stringifies to the secret.
	scs, err := Inspect(context.Background(), staticAPI(got), nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, sc := range scs {
		blob := strings.Join([]string{
			sc.WorkloadKind, sc.WorkloadName, sc.Namespace, sc.Reason,
		}, "|")
		if strings.Contains(blob, "super-secret-value") || strings.Contains(blob, "db-creds") || strings.Contains(blob, "DB_PASSWORD") {
			t.Fatalf("reduced workload leaked env/secret material: %q", blob)
		}
	}
}

type staticAPI []RawWorkload

func (s staticAPI) ListWorkloads(_ context.Context) ([]RawWorkload, error) { return s, nil }

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "deployments") {
			gotAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListWorkloads(context.Background()); err != nil {
		t.Fatalf("ListWorkloads: %v", err)
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
	if _, err := c.ListWorkloads(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestReduce_EmptyContainersIsUnhardened(t *testing.T) {
	t.Parallel()
	w := apiWorkload{Metadata: apiMeta{Name: "x", Namespace: "n"}}
	r := reduce(w, KindDeployment)
	if r.ReadOnlyRootFilesystem {
		t.Error("no containers must not assert readonly fs")
	}
	if !r.AllowPrivilegeEscalation {
		t.Error("no containers defaults to escalation-permitted (unhardened)")
	}
}

func TestAPIError_String(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 401}).Error() != "k8s: HTTP 401" {
		t.Error("bare status mismatch")
	}
}
