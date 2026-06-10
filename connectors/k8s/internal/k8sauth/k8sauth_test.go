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

// TestSecretsRule_IsGetListOnSecretsOnly pins slice-525 AC-4 / P0-525: the one
// rule the secret-inventory mode adds grants EXACTLY core `secrets` with verbs
// get,list — no write verb, no wildcard verb, no wildcard resource, no wildcard
// apiGroup.
func TestSecretsRule_IsGetListOnSecretsOnly(t *testing.T) {
	t.Parallel()
	r := SecretsRule()
	if len(r.APIGroups) != 1 || r.APIGroups[0] != "" {
		t.Errorf("secrets rule apiGroups = %v; want [\"\"] (core)", r.APIGroups)
	}
	if len(r.Resources) != 1 || r.Resources[0] != "secrets" {
		t.Errorf("secrets rule resources = %v; want [secrets]", r.Resources)
	}
	verbs := r.SortedVerbs()
	if strings.Join(verbs, ",") != "get,list" {
		t.Errorf("secrets rule verbs = %v; want [get list]", verbs)
	}
}

// TestSecretInventoryClusterRole_AddsExactlyTheSecretsRule pins slice-525 AC-4:
// the secret-inventory ClusterRole is the base least-privilege set PLUS EXACTLY
// one rule — core secrets get/list. It (a) still grants only get/list across
// every rule, (b) introduces no wildcard, and (c) adds `secrets` to exactly one
// rule and no other.
func TestSecretInventoryClusterRole_AddsExactlyTheSecretsRule(t *testing.T) {
	t.Parallel()
	base := DocumentedClusterRole()
	full := SecretInventoryClusterRole()

	if len(full) != len(base)+1 {
		t.Fatalf("secret-inventory ClusterRole has %d rules; want base(%d)+1", len(full), len(base))
	}

	allowedVerbs := map[string]bool{"get": true, "list": true}
	secretsRuleCount := 0
	for _, r := range full {
		for _, v := range r.Verbs {
			if !allowedVerbs[v] {
				t.Errorf("rule on %v grants non-read verb %q (P0-525 — secret-inventory mode stays read-only)", r.Resources, v)
			}
		}
		for _, res := range r.Resources {
			if res == "*" {
				t.Errorf("rule grants wildcard resource (P0-525)")
			}
			if res == "secrets" {
				secretsRuleCount++
			}
		}
		for _, g := range r.APIGroups {
			if g == "*" {
				t.Errorf("rule grants wildcard apiGroup (P0-525)")
			}
		}
	}
	// secrets must appear in exactly ONE rule (the one we added), in the core
	// apiGroup only.
	if secretsRuleCount != 1 {
		t.Errorf("secrets appears in %d rules; want exactly 1 (the slice-525 grant)", secretsRuleCount)
	}

	// And the BASE role must still EXCLUDE secrets entirely — operators not
	// running the secret-inventory mode keep the narrower grant.
	for _, r := range base {
		for _, res := range r.Resources {
			if res == "secrets" {
				t.Errorf("base ClusterRole leaked a secrets grant (must stay slice-487 narrow): %v", r.Resources)
			}
		}
	}
}

// TestAdmissionWebhookRule_IsGetListOnly pins slice-652: the admission-webhook
// rule grants EXACTLY admissionregistration.k8s.io
// validatingwebhookconfigurations + mutatingwebhookconfigurations with verbs
// get,list — no write verb, no wildcard, no `secrets`.
func TestAdmissionWebhookRule_IsGetListOnly(t *testing.T) {
	t.Parallel()
	r := AdmissionWebhookRule()
	if len(r.APIGroups) != 1 || r.APIGroups[0] != "admissionregistration.k8s.io" {
		t.Errorf("apiGroups = %v; want [admissionregistration.k8s.io]", r.APIGroups)
	}
	wantRes := map[string]bool{"validatingwebhookconfigurations": true, "mutatingwebhookconfigurations": true}
	if len(r.Resources) != 2 {
		t.Fatalf("resources = %v; want exactly the two webhook-config kinds", r.Resources)
	}
	for _, res := range r.Resources {
		if !wantRes[res] {
			t.Errorf("unexpected resource %q", res)
		}
		if res == "secrets" || res == "*" {
			t.Errorf("admission-webhook rule must never grant %q (P0)", res)
		}
	}
	if strings.Join(r.SortedVerbs(), ",") != "get,list" {
		t.Errorf("verbs = %v; want [get list]", r.SortedVerbs())
	}
}

// TestPolicyEngineRules_AreGetListNoWildcardNoSecrets pins slice-652: every
// optional policy-engine rule grants only get,list on a NAMED resource — never a
// wildcard resource/apiGroup, never `secrets`, never a write verb.
func TestPolicyEngineRules_AreGetListNoWildcardNoSecrets(t *testing.T) {
	t.Parallel()
	rules := PolicyEngineRules()
	if len(rules) == 0 {
		t.Fatal("PolicyEngineRules is empty")
	}
	allowedVerbs := map[string]bool{"get": true, "list": true}
	for _, r := range rules {
		for _, v := range r.Verbs {
			if !allowedVerbs[v] {
				t.Errorf("policy-engine rule on %v grants non-read verb %q (P0)", r.Resources, v)
			}
		}
		for _, res := range r.Resources {
			if res == "secrets" {
				t.Errorf("policy-engine rule grants `secrets` (P0): %v", r.Resources)
			}
			if res == "*" {
				t.Errorf("policy-engine rule grants wildcard resource (P0): %v", r.Resources)
			}
		}
		for _, g := range r.APIGroups {
			if g == "*" {
				t.Errorf("policy-engine rule grants wildcard apiGroup (P0): %v", r.APIGroups)
			}
		}
	}
}

// TestAdmissionEvidenceClusterRole_AddsExactlyTheAdmissionRules pins slice-652:
// the admission-evidence ClusterRole is the base least-privilege set PLUS the
// admission-webhook rule PLUS the policy-engine rules. It (a) still grants only
// get,list across every rule, (b) introduces no wildcard resource and no
// `secrets`, and (c) leaves the BASE role unchanged (still excluding both the
// webhook and policy grants AND `secrets`).
func TestAdmissionEvidenceClusterRole_AddsExactlyTheAdmissionRules(t *testing.T) {
	t.Parallel()
	base := DocumentedClusterRole()
	full := AdmissionEvidenceClusterRole()

	wantAdded := 1 + len(PolicyEngineRules())
	if len(full) != len(base)+wantAdded {
		t.Fatalf("admission ClusterRole has %d rules; want base(%d)+%d", len(full), len(base), wantAdded)
	}

	allowedVerbs := map[string]bool{"get": true, "list": true}
	for _, r := range full {
		for _, v := range r.Verbs {
			if !allowedVerbs[v] {
				t.Errorf("rule on %v grants non-read verb %q (P0 — admission mode stays read-only)", r.Resources, v)
			}
		}
		for _, res := range r.Resources {
			if res == "*" {
				t.Errorf("rule grants wildcard resource (P0): %v", r.Resources)
			}
			if res == "secrets" {
				t.Errorf("admission mode must never grant `secrets` (P0): %v", r.Resources)
			}
		}
		for _, g := range r.APIGroups {
			if g == "*" {
				t.Errorf("rule grants wildcard apiGroup (P0): %v", r.APIGroups)
			}
		}
	}

	// The base role must still EXCLUDE the admission grants entirely.
	for _, r := range base {
		for _, g := range r.APIGroups {
			if g == "admissionregistration.k8s.io" || g == "templates.gatekeeper.sh" || g == "kyverno.io" {
				t.Errorf("base ClusterRole leaked an admission-evidence grant (must stay slice-487 narrow): %v", r.APIGroups)
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
