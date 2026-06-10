package netpol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// CNI-native NetworkPolicy support (slice 622).
//
// Many production clusters enforce segmentation entirely through their CNI's own
// policy CRDs rather than (or in addition to) upstream networking.k8s.io
// NetworkPolicy. Under slice 523 such a cluster reads as fully UNPROTECTED — a
// false-FAIL. This file folds the installed CNI's policy CRDs into the
// per-namespace default-deny assessment.
//
// Two CNIs are supported by CRD presence (detected via API discovery — NEVER
// hard-fail when absent):
//
//	Cilium  (cilium.io/v2):           CiliumNetworkPolicy (namespaced) +
//	                                   CiliumClusterwideNetworkPolicy (cluster-wide)
//	Calico  (crd.projectcalico.org/v1): NetworkPolicy (namespaced) +
//	                                   GlobalNetworkPolicy (cluster-wide)
//
// CRITICAL (P0-523 over-collection guard, verbatim for slice 622): the JSON
// decode targets below model ONLY CRD SPEC metadata — the policy name, whether
// its endpoint/pod selector is empty (selects every endpoint in scope), which
// directions it governs, and a COUNT of its ingress/egress rule blocks. The
// peers inside each rule (toEndpoints / fromEndpoints / toCIDR / source /
// destination selectors / ports), pod contents, and traffic are NOT decoded —
// the decoder discards unmodeled keys, so they never materialize into Go memory
// and can never reach an evidence record.
//
// Read-only: every call is a GET (`get,list`); no CRD is mutated, no new verb
// beyond get,list is used, and no `secrets`/wildcard is touched (AC-3 / AC-4).

// cniResource enumerates one CNI policy CRD: its discovery API path (to probe
// for presence) and its cluster-wide list path. cniReader reads each present
// CRD via the shared paginating k8slist.Reader.
type cniResource struct {
	source       string // SourceCilium / SourceCalico
	groupVersion string // e.g. "cilium.io/v2" — the discovery probe path suffix
	// namespaced is the cluster-wide list path for the namespaced CRD kind
	// (Kubernetes lists a namespaced resource across all namespaces at the
	// non-namespaced collection path; each item carries metadata.namespace).
	namespaced string
	// clusterwide is the list path for the cluster-scoped CRD kind. A
	// cluster-wide policy with an empty selector folds default-deny into EVERY
	// namespace. Empty when the CNI has no cluster-wide kind.
	clusterwide string
}

// supportedCNIs is the fixed set of CNI policy CRDs the collector folds in. The
// list is closed (no wildcard discovery) so the ClusterRole grant is exactly
// these resources and nothing more (AC-3).
func supportedCNIs() []cniResource {
	return []cniResource{
		{
			source:       SourceCilium,
			groupVersion: "cilium.io/v2",
			namespaced:   "/apis/cilium.io/v2/ciliumnetworkpolicies",
			clusterwide:  "/apis/cilium.io/v2/ciliumclusterwidenetworkpolicies",
		},
		{
			source:       SourceCalico,
			groupVersion: "crd.projectcalico.org/v1",
			namespaced:   "/apis/crd.projectcalico.org/v1/networkpolicies",
			clusterwide:  "/apis/crd.projectcalico.org/v1/globalnetworkpolicies",
		},
	}
}

// --- minimal CNI CRD JSON shapes (SPEC metadata ONLY) ---
//
// We intentionally do NOT model the peer/selector contents of a rule. ingress /
// egress are decoded as opaque RawMessage arrays so we can take their length
// (the rule-block COUNT) without materializing any peer/CIDR/port payload.
//
// The selector shapes differ per CNI; both are modeled only enough to decide
// emptiness:
//   - Cilium endpointSelector: a Kubernetes-style {matchLabels, matchExpressions}.
//   - Calico spec.selector: a string DSL ("" / "all()" => all endpoints).

type apiCiliumPolicy struct {
	Metadata apiMeta `json:"metadata"`
	Spec     struct {
		EndpointSelector *apiLabelSelector `json:"endpointSelector"`
		// NodeSelector-based or empty endpointSelector variants are not credited;
		// we only credit an explicit empty endpointSelector below.
		Ingress []json.RawMessage `json:"ingress"`
		Egress  []json.RawMessage `json:"egress"`
	} `json:"spec"`
	// Cilium also supports a `specs` array (list of rule specs). We do not decode
	// its contents; its presence with a selector is not credited as default-deny
	// because the per-spec selector is ambiguous — conservative under-credit.
}

type apiCalicoPolicy struct {
	Metadata apiMeta `json:"metadata"`
	Spec     struct {
		Selector string            `json:"selector"`
		Types    []string          `json:"types"`
		Ingress  []json.RawMessage `json:"ingress"`
		Egress   []json.RawMessage `json:"egress"`
	} `json:"spec"`
}

// cniReader reads the installed CNI policy CRDs and reduces them to source-
// tagged RawPolicy values folded by namespace. It probes each CRD's presence via
// API discovery and contributes nothing for an absent CRD (no hard-fail).
type cniReader struct {
	r *k8slist.Reader
}

func newCNIReader(r *k8slist.Reader) *cniReader { return &cniReader{r: r} }

// collect returns the CNI-native policies grouped by namespace plus the set of
// cluster-wide policies (which the caller folds into every namespace — it owns
// the canonical namespace set). An absent CRD contributes nothing; a present-
// but-erroring CRD list propagates the error (a partial read is never silently
// treated as "no CNI policies").
func (c *cniReader) collect(ctx context.Context) (byNamespace map[string][]RawPolicy, clusterwide []RawPolicy, err error) {
	byNamespace = make(map[string][]RawPolicy)
	for _, res := range supportedCNIs() {
		present, perr := c.present(ctx, res.groupVersion)
		if perr != nil {
			return nil, nil, fmt.Errorf("probe %s: %w", res.groupVersion, perr)
		}
		if !present {
			continue
		}
		nsPolicies, cw, lerr := c.list(ctx, res)
		if lerr != nil {
			return nil, nil, lerr
		}
		for ns, ps := range nsPolicies {
			byNamespace[ns] = append(byNamespace[ns], ps...)
		}
		clusterwide = append(clusterwide, cw...)
	}
	return byNamespace, clusterwide, nil
}

// present probes API discovery for the CRD's group/version. A 200 means the
// group/version is served (CRD installed); a 404 means it is absent. Any other
// status is a real error. Discovery is a read-only GET against /apis/<gv> and
// requires no extra ClusterRole grant beyond the resource get,list.
func (c *cniReader) present(ctx context.Context, groupVersion string) (bool, error) {
	status, err := c.r.Probe(ctx, "/apis/"+groupVersion)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, &k8slist.APIError{Status: status}
	}
}

// list reads one CNI's namespaced + cluster-wide policy collections and reduces
// each object to a source-tagged RawPolicy. A 404 on the list path (CRD removed
// between probe and list, or a kind the CNI version does not serve) is treated
// as "no policies" rather than an error.
func (c *cniReader) list(ctx context.Context, res cniResource) (map[string][]RawPolicy, []RawPolicy, error) {
	byNamespace := make(map[string][]RawPolicy)
	if err := c.listInto(ctx, res, res.namespaced, false, byNamespace, nil); err != nil {
		return nil, nil, err
	}
	var clusterwide []RawPolicy
	cw := &clusterwide
	if res.clusterwide != "" {
		if err := c.listInto(ctx, res, res.clusterwide, true, nil, cw); err != nil {
			return nil, nil, err
		}
	}
	return byNamespace, clusterwide, nil
}

// listInto lists one collection path and folds the reduced policies into either
// byNamespace (namespaced kind) or *clusterwide (cluster-wide kind).
func (c *cniReader) listInto(ctx context.Context, res cniResource, path string, clusterScoped bool, byNamespace map[string][]RawPolicy, clusterwide *[]RawPolicy) error {
	switch res.source {
	case SourceCilium:
		items, err := listOrAbsent[apiCiliumPolicy](ctx, c.r, path)
		if err != nil {
			return err
		}
		for _, it := range items {
			rp := reduceCilium(it)
			foldRaw(rp, it.Metadata.Namespace, clusterScoped, byNamespace, clusterwide)
		}
	case SourceCalico:
		items, err := listOrAbsent[apiCalicoPolicy](ctx, c.r, path)
		if err != nil {
			return err
		}
		for _, it := range items {
			rp := reduceCalico(it)
			foldRaw(rp, it.Metadata.Namespace, clusterScoped, byNamespace, clusterwide)
		}
	}
	return nil
}

// foldRaw routes a reduced policy to the right bucket.
func foldRaw(rp RawPolicy, namespace string, clusterScoped bool, byNamespace map[string][]RawPolicy, clusterwide *[]RawPolicy) {
	if clusterScoped {
		*clusterwide = append(*clusterwide, rp)
		return
	}
	byNamespace[namespace] = append(byNamespace[namespace], rp)
}

// listOrAbsent lists a CRD collection but treats a 404 (kind not served) as an
// empty list rather than an error — a Cilium-only cluster has no Calico kind and
// vice versa, and a CNI version may not serve the cluster-wide kind.
func listOrAbsent[T any](ctx context.Context, r *k8slist.Reader, path string) ([]T, error) {
	items, err := k8slist.ListAll[T](ctx, r, path)
	if err != nil {
		var apiErr *k8slist.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return items, nil
}

// reduceCilium collapses one CiliumNetworkPolicy / CiliumClusterwideNetworkPolicy
// into the SPEC-metadata-only RawPolicy. A Cilium policy is default-deny in a
// direction when (a) its endpointSelector is empty (selects every endpoint in
// scope) and (b) it governs that direction with zero rule entries. We derive the
// governed directions from the presence of the ingress/egress keys (Cilium has
// no policyTypes field): an empty `ingress: []` present means "deny all ingress".
//
// Conservative under-credit: we only credit a direction as governed when its key
// is present in the spec; a Cilium policy that omits both ingress and egress is
// not crediting any default-deny.
func reduceCilium(p apiCiliumPolicy) RawPolicy {
	selectsAll := p.Spec.EndpointSelector.isEmpty()
	var types []string
	if p.Spec.Ingress != nil {
		types = append(types, PolicyTypeIngress)
	}
	if p.Spec.Egress != nil {
		types = append(types, PolicyTypeEgress)
	}
	return RawPolicy{
		Name:             p.Metadata.Name,
		Source:           SourceCilium,
		PolicyTypes:      types,
		SelectsAllPods:   selectsAll,
		IngressRuleCount: len(p.Spec.Ingress),
		EgressRuleCount:  len(p.Spec.Egress),
	}
}

// reduceCalico collapses one Calico NetworkPolicy / GlobalNetworkPolicy into the
// SPEC-metadata-only RawPolicy. Calico is default-deny in a direction when (a)
// its selector is all-endpoints ("" or "all()") and (b) spec.types includes the
// direction and (c) there are zero rule entries for it. Calico's spec.types is
// the authoritative governed-direction list (it mirrors upstream policyTypes).
func reduceCalico(p apiCalicoPolicy) RawPolicy {
	return RawPolicy{
		Name:             p.Metadata.Name,
		Source:           SourceCalico,
		PolicyTypes:      p.Spec.Types,
		SelectsAllPods:   calicoSelectsAll(p.Spec.Selector),
		IngressRuleCount: len(p.Spec.Ingress),
		EgressRuleCount:  len(p.Spec.Egress),
	}
}

// calicoSelectsAll reports whether a Calico selector DSL string selects every
// endpoint. Calico's empty selector and the explicit `all()` both mean
// "all endpoints in scope". Any other selector expression is a narrower match —
// conservatively NOT all-pods, so it cannot establish a namespace-wide
// default-deny.
func calicoSelectsAll(sel string) bool {
	switch strings.TrimSpace(sel) {
	case "", "all()":
		return true
	default:
		return false
	}
}
