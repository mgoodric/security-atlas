package k8sauth

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestResolve_KubeconfigToken(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{
		Mode: ModeKubeconfigToken, APIServer: "https://kube:6443", Token: "test-k8s-token",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Mode() != ModeKubeconfigToken {
		t.Errorf("mode = %q", cred.Mode())
	}
	if cred.Token() != "test-k8s-token" {
		t.Errorf("token not preserved")
	}
	if cred.APIServer() != "https://kube:6443" {
		t.Errorf("api server = %q", cred.APIServer())
	}
}

func TestResolve_DefaultsToKubeconfigToken(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{APIServer: "https://k:6443", Token: "test-k8s-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Mode() != ModeKubeconfigToken {
		t.Errorf("default mode = %q; want kubeconfig-token", cred.Mode())
	}
}

func TestResolve_MissingAPIServer(t *testing.T) {
	t.Setenv(EnvAPIServer, "")
	if _, err := Resolve(ResolveOpts{Token: "t"}); err == nil || !strings.Contains(err.Error(), "API server") {
		t.Fatalf("want API server error; got %v", err)
	}
}

func TestResolve_MissingToken(t *testing.T) {
	t.Setenv(EnvToken, "")
	if _, err := Resolve(ResolveOpts{APIServer: "https://k:6443"}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error; got %v", err)
	}
}

func TestResolve_InCluster(t *testing.T) {
	t.Parallel()
	cred, err := Resolve(ResolveOpts{
		Mode: ModeInCluster, APIServer: "https://kubernetes.default.svc",
		readFile: func(string) ([]byte, error) { return []byte("test-projected-token\n"), nil },
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token() != "test-projected-token" {
		t.Errorf("token = %q; want trimmed projected token", cred.Token())
	}
}

func TestResolve_InClusterReadError(t *testing.T) {
	t.Parallel()
	_, err := Resolve(ResolveOpts{
		Mode: ModeInCluster, APIServer: "https://k", readFile: func(string) ([]byte, error) { return nil, errors.New("no mount") },
	})
	if err == nil || !strings.Contains(err.Error(), "in-cluster token") {
		t.Fatalf("want in-cluster read error; got %v", err)
	}
}

func TestResolve_InClusterEmptyToken(t *testing.T) {
	t.Parallel()
	_, err := Resolve(ResolveOpts{
		Mode: ModeInCluster, APIServer: "https://k", readFile: func(string) ([]byte, error) { return []byte("  \n"), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-token error; got %v", err)
	}
}

func TestResolve_UnknownMode(t *testing.T) {
	t.Parallel()
	if _, err := Resolve(ResolveOpts{Mode: "bogus", APIServer: "https://k", Token: "t"}); err == nil {
		t.Fatal("want unknown-mode error")
	}
}

// TestCredential_NeverLeaksToken pins P0-487-4 / AC-11: no formatting path may
// reveal the token.
func TestCredential_NeverLeaksToken(t *testing.T) {
	t.Parallel()
	const token = "test-k8s-secret-token-never-log"
	cred, err := Resolve(ResolveOpts{APIServer: "https://k:6443", Token: token})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, s := range []string{cred.String(), cred.GoString()} {
		if strings.Contains(s, token) {
			t.Fatalf("formatted credential leaked the token: %q", s)
		}
		if !strings.Contains(s, "redacted") {
			t.Errorf("formatted credential should mark the token redacted: %q", s)
		}
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		want AuthMode
		ok   bool
	}{
		"kubeconfig-token": {ModeKubeconfigToken, true},
		"in-cluster":       {ModeInCluster, true},
		"":                 {ModeKubeconfigToken, true},
		"bogus":            {"", false},
	}
	for in, exp := range cases {
		got, err := ParseMode(in)
		if exp.ok && (err != nil || got != exp.want) {
			t.Errorf("ParseMode(%q) = %q,%v; want %q,nil", in, got, err, exp.want)
		}
		if !exp.ok && err == nil {
			t.Errorf("ParseMode(%q) should error", in)
		}
	}
}

// TestDocumentedClusterRole_IsLeastPrivilege pins P0-487-2 + P0-487-3: every
// rule grants only get/list, never a write verb or wildcard, and NO rule grants
// any access to 'secrets'.
func TestDocumentedClusterRole_IsLeastPrivilege(t *testing.T) {
	t.Parallel()
	rules := DocumentedClusterRole()
	if len(rules) == 0 {
		t.Fatal("ClusterRole has no rules")
	}
	allowedVerbs := map[string]bool{"get": true, "list": true}
	bannedResources := map[string]bool{"secrets": true, "*": true}
	for _, r := range rules {
		for _, v := range r.Verbs {
			if !allowedVerbs[v] {
				t.Errorf("rule on %v grants non-read verb %q (P0-487-2)", r.Resources, v)
			}
		}
		for _, res := range r.Resources {
			if bannedResources[res] {
				t.Errorf("rule grants access to banned resource %q (P0-487-3 / P0-487-2)", res)
			}
		}
		for _, g := range r.APIGroups {
			if g == "*" {
				t.Errorf("rule grants wildcard apiGroup (P0-487-2)")
			}
		}
	}
}

func TestReadOnlyVerbs_AreGetList(t *testing.T) {
	t.Parallel()
	v := ReadOnlyVerbs()
	sort.Strings(v)
	if strings.Join(v, ",") != "get,list" {
		t.Errorf("read-only verbs = %v; want [get list]", v)
	}
	// Mutating the returned slice must not affect the package's copy.
	v[0] = "delete"
	if strings.Join(ReadOnlyVerbs(), ",") != "get,list" {
		t.Error("ReadOnlyVerbs returned a mutable reference")
	}
}

func TestSortedVerbs(t *testing.T) {
	t.Parallel()
	r := PolicyRule{Verbs: []string{"list", "get"}}
	if strings.Join(r.SortedVerbs(), ",") != "get,list" {
		t.Errorf("SortedVerbs = %v", r.SortedVerbs())
	}
}
