package netpol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the Kubernetes endpoints the
// NetworkPolicy collector reads: core namespaces + networking.k8s.io/v1
// networkpolicies. It holds a short-lived bearer token (never logged) and issues
// only GET requests.
//
// CRITICAL (P0-523 over-collection guard): the JSON decode targets below model
// ONLY NetworkPolicy SPEC metadata — the policy name, policyTypes, whether the
// podSelector is empty, and a COUNT of ingress/egress rule blocks. The peers
// inside each ingress/egress block (from/to/ports), pod contents, container env,
// Secret / ConfigMap refs, and traffic data are NOT decoded — Go's json decoder
// discards unmodeled keys, so they never materialize into Go memory here and can
// never reach an evidence record.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a NetworkPolicy client. token is a read-only ServiceAccount
// bearer token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

const pageLimit = 500

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

type netpolList struct {
	Items []apiNetpol `json:"items"`
}

type apiNamespace struct {
	Metadata apiMeta `json:"metadata"`
}

type namespaceList struct {
	Items []apiNamespace `json:"items"`
}

// ListNamespaceCoverage reads every namespace + every NetworkPolicy cluster-wide
// (one bounded list call each — read-only) and groups the policies by namespace.
// A namespace with zero policies appears with an empty Policies slice (fully
// unprotected). Read-only: only GET requests against core + networking.k8s.io.
func (c *Client) ListNamespaceCoverage(ctx context.Context) ([]RawNamespace, error) {
	var nsList namespaceList
	if err := c.getJSON(ctx, "/api/v1/namespaces", &nsList); err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	var npList netpolList
	if err := c.getJSON(ctx, "/apis/networking.k8s.io/v1/networkpolicies", &npList); err != nil {
		return nil, fmt.Errorf("list networkpolicies: %w", err)
	}

	byNamespace := make(map[string][]RawPolicy)
	for _, np := range npList.Items {
		ns := np.Metadata.Namespace
		byNamespace[ns] = append(byNamespace[ns], reduce(np))
	}

	out := make([]RawNamespace, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		name := ns.Metadata.Name
		if name == "" {
			continue
		}
		policies := byNamespace[name]
		sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
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

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	u := fmt.Sprintf("%s%s?limit=%d", c.BaseURL, path, pageLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// APIError carries Kubernetes REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("k8s: HTTP %d", e.Status)
	}
	return fmt.Sprintf("k8s: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
