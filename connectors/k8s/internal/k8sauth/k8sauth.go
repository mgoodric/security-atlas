// Package k8sauth resolves the Kubernetes cluster credential for the connector
// and documents the least-privilege read-only ClusterRole the connector
// requires.
//
// The connector authenticates to the cluster's API server with a read-only
// ServiceAccount token (a kubeconfig / in-cluster token). Two sources are
// supported:
//
//	kubeconfig-token — KUBECONFIG_TOKEN + the API server URL (out-of-cluster)
//	in-cluster       — the projected ServiceAccount token mounted in the pod
//
// The resolved Credential never reveals its token: fmt.Sprintf("%v", cred)
// returns a redacted summary so accidental log / print paths cannot leak it.
// The unit test pins this behaviour (AC-11 / P0-487-4).
//
// Anti-criterion: no log line in this package — or anywhere downstream of
// Resolve — may emit the token. DocumentedClusterRole returns the canonical
// least-privilege ClusterRole rules; the companion test rejects any future
// widening into write verbs, Secret reads, or wildcard grants
// (P0-487-2 / P0-487-3).
package k8sauth

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Env var names carrying the cluster credential. Preferred over flags so the
// token never appears in shell history.
const (
	// EnvAPIServer is the Kubernetes API server URL (out-of-cluster mode).
	EnvAPIServer = "KUBERNETES_API_SERVER"
	// EnvToken carries the read-only ServiceAccount bearer token.
	EnvToken = "KUBECONFIG_TOKEN"
	// EnvCACert is an optional path to the cluster CA bundle (out-of-cluster).
	EnvCACert = "KUBERNETES_CA_CERT"

	// inClusterTokenPath is the projected ServiceAccount token mount.
	inClusterTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// AuthMode selects how the connector obtains its cluster credential.
type AuthMode string

const (
	// ModeKubeconfigToken uses an explicit bearer token + API server URL.
	ModeKubeconfigToken AuthMode = "kubeconfig-token"
	// ModeInCluster reads the projected ServiceAccount token from the pod.
	ModeInCluster AuthMode = "in-cluster"
)

// Credential is the resolved cluster auth material. The token is kept off
// String() so accidental %v / %+v formatting paths cannot leak it.
type Credential struct {
	mode      AuthMode
	apiServer string
	token     string
	caCert    string
}

// Mode returns the resolved auth mode.
func (c Credential) Mode() AuthMode { return c.mode }

// APIServer returns the API server URL. Non-secret.
func (c Credential) APIServer() string { return c.apiServer }

// Token returns the bearer token. Callers pass it straight to the HTTP client's
// Authorization header; it must never be logged.
func (c Credential) Token() string { return c.token }

// CACert returns the optional CA bundle path. Non-secret.
func (c Credential) CACert() string { return c.caCert }

// String never reveals the token. Tests rely on this.
func (c Credential) String() string {
	return fmt.Sprintf("k8sauth.Credential{mode: %s, api_server: %q, token: <redacted %d bytes>}",
		c.mode, c.apiServer, len(c.token))
}

// GoString mirrors String so %#v cannot leak the token either.
func (c Credential) GoString() string { return c.String() }

// ResolveOpts is the input to Resolve. The cmd layer threads its parsed flags
// through this so the package never imports cobra.
type ResolveOpts struct {
	Mode AuthMode
	// APIServer overrides the API server URL. Empty falls back to the
	// KUBERNETES_API_SERVER env var (kubeconfig-token mode).
	APIServer string
	// Token overrides the bearer token. Empty falls back to KUBECONFIG_TOKEN
	// (kubeconfig-token mode) or the projected mount (in-cluster mode).
	Token string
	// CACert overrides the CA bundle path. Empty falls back to KUBERNETES_CA_CERT.
	CACert string

	// readFile is injected by tests to fake the in-cluster token mount. nil
	// uses os.ReadFile.
	readFile func(string) ([]byte, error)
}

// Resolve returns a live credential. In kubeconfig-token mode an API server URL
// and a token are both required (after env fallback). In in-cluster mode the
// API server URL is required and the token is read from the projected mount.
func Resolve(opts ResolveOpts) (Credential, error) {
	mode := opts.Mode
	if mode == "" {
		mode = ModeKubeconfigToken
	}
	apiServer := strings.TrimSpace(firstNonEmpty(opts.APIServer, os.Getenv(EnvAPIServer)))
	if apiServer == "" {
		return Credential{}, fmt.Errorf("k8sauth: API server URL required (set %s or pass --api-server)", EnvAPIServer)
	}
	caCert := strings.TrimSpace(firstNonEmpty(opts.CACert, os.Getenv(EnvCACert)))

	switch mode {
	case ModeKubeconfigToken:
		token := strings.TrimSpace(firstNonEmpty(opts.Token, os.Getenv(EnvToken)))
		if token == "" {
			return Credential{}, fmt.Errorf("k8sauth: bearer token required (set %s)", EnvToken)
		}
		return Credential{mode: ModeKubeconfigToken, apiServer: apiServer, token: token, caCert: caCert}, nil
	case ModeInCluster:
		read := opts.readFile
		if read == nil {
			read = os.ReadFile
		}
		raw, err := read(inClusterTokenPath)
		if err != nil {
			return Credential{}, fmt.Errorf("k8sauth: read in-cluster token %s: %w", inClusterTokenPath, err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return Credential{}, fmt.Errorf("k8sauth: in-cluster token at %s is empty", inClusterTokenPath)
		}
		return Credential{mode: ModeInCluster, apiServer: apiServer, token: token, caCert: caCert}, nil
	default:
		return Credential{}, fmt.Errorf("k8sauth: unknown auth mode %q (want %s or %s)",
			mode, ModeKubeconfigToken, ModeInCluster)
	}
}

// PolicyRule is one ClusterRole rule the connector requires. Mirrors the
// rbac.authorization.k8s.io/v1 PolicyRule shape (the fields the README renders).
type PolicyRule struct {
	APIGroups []string
	Resources []string
	Verbs     []string
	Gates     string // which evidence kind this rule gates
}

// readOnlyVerbs is the only verb set any rule may grant. The connector reads;
// it never mutates the cluster.
var readOnlyVerbs = []string{"get", "list"}

// DocumentedClusterRole returns the canonical least-privilege read-only
// ClusterRole the connector requires. The cmd help text and README both render
// this; keeping it programmatic lets the test pin the doc + the README in sync.
//
// Anti-criteria enforced by the companion test:
//   - P0-487-2: verbs are EXACTLY {get,list} — never create/update/patch/
//     delete/deletecollection/* (no write verbs).
//   - P0-487-3: resources NEVER include "secrets" (no Secret read) and never a
//     wildcard "*".
func DocumentedClusterRole() []PolicyRule {
	return []PolicyRule{
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"roles", "clusterroles", "rolebindings", "clusterrolebindings"},
			Verbs:     readOnlyVerbs,
			Gates:     "k8s.rbac_binding.v1 (RBAC roles + bindings)",
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments", "daemonsets", "statefulsets"},
			Verbs:     readOnlyVerbs,
			Gates:     "k8s.workload_security_context.v1 (workload security contexts)",
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"networkpolicies"},
			Verbs:     readOnlyVerbs,
			Gates:     "k8s.networkpolicy_coverage.v1 (NetworkPolicy segmentation posture)",
		},
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     readOnlyVerbs,
			Gates:     "namespace enumeration (scope context for all kinds)",
		},
	}
}

// ReadOnlyVerbs returns a copy of the only verbs the connector grants.
func ReadOnlyVerbs() []string {
	out := make([]string, len(readOnlyVerbs))
	copy(out, readOnlyVerbs)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ParseMode validates a mode string from the CLI.
func ParseMode(s string) (AuthMode, error) {
	switch AuthMode(strings.TrimSpace(s)) {
	case ModeKubeconfigToken:
		return ModeKubeconfigToken, nil
	case ModeInCluster:
		return ModeInCluster, nil
	case "":
		return ModeKubeconfigToken, nil
	default:
		return "", errors.New("k8sauth: --auth-mode must be kubeconfig-token or in-cluster")
	}
}

// SortedVerbs returns the rule's verbs sorted, for deterministic rendering.
func (r PolicyRule) SortedVerbs() []string {
	out := make([]string, len(r.Verbs))
	copy(out, r.Verbs)
	sort.Strings(out)
	return out
}
