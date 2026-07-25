package netpol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// Client is a thin read-only HTTP client for the Kubernetes endpoints the
// NetworkPolicy collector reads: core namespaces + networking.k8s.io/v1
// networkpolicies. It delegates HTTP + pagination to the shared k8slist.Reader:
// every list call follows the Kubernetes `metadata.continue` cursor to
// completion (slice 621), so a cluster with more than one page of namespaces or
// networkpolicies is no longer silently truncated. It holds a short-lived bearer
// token (never logged) and issues only GET requests.
//
// CRITICAL (P0-523 over-collection guard): the JSON decode targets below model
// ONLY NetworkPolicy SPEC metadata — the policy name, policyTypes, whether the
// podSelector is empty, and a COUNT of ingress/egress rule blocks. The peers
// inside each ingress/egress block (from/to/ports), pod contents, container env,
// Secret / ConfigMap refs, and traffic data are NOT decoded — Go's json decoder
// discards unmodeled keys, so they never materialize into Go memory here and can
// never reach an evidence record.
type Client struct {
	r   *k8slist.Reader
	cni *cniReader
}

// NewClient builds a NetworkPolicy client. token is a read-only ServiceAccount
// bearer token (from k8sauth.Credential.Token). baseURL is the API server URL.
// The client also folds in CNI-native policy CRDs (Cilium / Calico) when those
// CRDs are present in the cluster (slice 622) — detected by API discovery, never
// hard-failing when absent.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	r := k8slist.NewReader(httpClient, baseURL, token)
	return &Client{r: r, cni: newCNIReader(r)}
}

// APIError is re-exported from the shared reader so existing callers and tests
// keep referring to netpol.APIError.
type APIError = k8slist.APIError

// --- minimal Kubernetes API JSON shapes (NetworkPolicy SPEC metadata ONLY) ---
//
// We intentionally do NOT model spec.ingress[].from / spec.egress[].to / ports.
// We count the rule blocks (len of the arrays) but never decode their contents,
// so no peer CIDR, namespaceSelector label, or port payload is materialized.

type apiMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// apiLabelSelector models only enough of spec.podSelector to decide whether it
// is empty. An empty podSelector ({}) selects every pod in the namespace.
type apiLabelSelector struct {
	MatchLabels      map[string]string `json:"matchLabels"`
	MatchExpressions []json.RawMessage `json:"matchExpressions"`
}

func (s *apiLabelSelector) isEmpty() bool {
	return s == nil || (len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0)
}

// apiNetpolSpec models the rule COUNTS and direction metadata only. ingress /
// egress are decoded as RawMessage arrays so we can take their length without
// materializing the peer/port contents inside each block.
type apiNetpolSpec struct {
	PodSelector *apiLabelSelector `json:"podSelector"`
	PolicyTypes []string          `json:"policyTypes"`
	Ingress     []json.RawMessage `json:"ingress"`
	Egress      []json.RawMessage `json:"egress"`
}

type apiNetpol struct {
	Metadata apiMeta       `json:"metadata"`
	Spec     apiNetpolSpec `json:"spec"`
}

type apiNamespace struct {
	Metadata apiMeta `json:"metadata"`
}

// ListNamespaceCoverage reads every namespace + every NetworkPolicy cluster-wide
// (each list call follows the continue cursor to completion — read-only) and
// groups the policies by namespace. A namespace with zero policies appears with
// an empty Policies slice (fully unprotected). Read-only: only GET requests
// against core + networking.k8s.io.
func (c *Client) ListNamespaceCoverage(ctx context.Context) ([]RawNamespace, error) {
	namespaces, err := k8slist.ListAll[apiNamespace](ctx, c.r, "/api/v1/namespaces")
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	policies, err := k8slist.ListAll[apiNetpol](ctx, c.r, "/apis/networking.k8s.io/v1/networkpolicies")
	if err != nil {
		return nil, fmt.Errorf("list networkpolicies: %w", err)
	}

	byNamespace := make(map[string][]RawPolicy)
	for _, np := range policies {
		ns := np.Metadata.Namespace
		byNamespace[ns] = append(byNamespace[ns], reduce(np))
	}

	// The ordered, deduplicated set of real namespaces — the only namespaces a
	// cluster-wide CNI policy may fold default-deny into.
	nsNames := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if ns.Metadata.Name != "" {
			nsNames = append(nsNames, ns.Metadata.Name)
		}
	}

	// Fold in CNI-native policy CRDs (Cilium / Calico) when present. Absent CRDs
	// contribute nothing (no hard-fail). cni may be nil in a directly-constructed
	// Client used by a narrow test; guard it.
	if c.cni != nil {
		cniByNS, clusterwide, cerr := c.cni.collect(ctx)
		if cerr != nil {
			return nil, fmt.Errorf("list cni networkpolicies: %w", cerr)
		}
		for ns, ps := range cniByNS {
			byNamespace[ns] = append(byNamespace[ns], ps...)
		}
		// Cluster-wide CNI policies (CiliumClusterwide / Calico GlobalNetworkPolicy)
		// apply to every namespace — fold a copy into each real namespace so the
		// per-namespace default-deny assessment credits them.
		for _, cw := range clusterwide {
			for _, name := range nsNames {
				byNamespace[name] = append(byNamespace[name], cw)
			}
		}
	}

	out := make([]RawNamespace, 0, len(namespaces))
	for _, ns := range namespaces {
		name := ns.Metadata.Name
		if name == "" {
			continue
		}
		policies := byNamespace[name]
		sort.Slice(policies, func(i, j int) bool {
			if policies[i].Name != policies[j].Name {
				return policies[i].Name < policies[j].Name
			}
			return policies[i].Source < policies[j].Source
		})
		out = append(out, RawNamespace{Name: name, Policies: policies})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// reduce collapses one NetworkPolicy object into the SPEC-metadata-only RawPolicy
// the evidence kind carries. Rule blocks are counted, never decoded.
func reduce(np apiNetpol) RawPolicy {
	spec := np.Spec
	return RawPolicy{
		Name:             np.Metadata.Name,
		Source:           SourceUpstream,
		PolicyTypes:      derivePolicyTypes(spec),
		SelectsAllPods:   spec.PodSelector.isEmpty(),
		IngressRuleCount: len(spec.Ingress),
		EgressRuleCount:  len(spec.Egress),
	}
}

// derivePolicyTypes returns the directions the policy governs. When the API
// supplies spec.policyTypes, use it. When it is omitted (older manifests),
// derive per Kubernetes semantics: Ingress is always implied; Egress applies
// only when an egress block is present.
func derivePolicyTypes(spec apiNetpolSpec) []string {
	if len(spec.PolicyTypes) > 0 {
		return spec.PolicyTypes
	}
	types := []string{PolicyTypeIngress}
	if len(spec.Egress) > 0 {
		types = append(types, PolicyTypeEgress)
	}
	return types
}
