// Package netpol assesses Kubernetes NetworkPolicy coverage — the load-bearing
// signal for the connector's network-segmentation evidence kind.
//
// Source: read-only Kubernetes API (get/list on networking.k8s.io/v1
// networkpolicies + core namespaces). The connector reads NetworkPolicy SPEC
// metadata only — NEVER pod contents, container env / Secret / ConfigMap values,
// nor actual traffic. The per-policy struct has no field that could carry
// workload payload.
package netpol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// CoverageResult enumerates the per-namespace segmentation verdict. Maps 1:1
// onto the gRPC Result enum at the cmd layer.
type CoverageResult string

const (
	// ResultPass — the namespace has a default-deny posture for at least one
	// direction (an empty-podSelector policy that selects every pod with no
	// allow rule in that direction).
	ResultPass CoverageResult = "pass"
	// ResultFail — the namespace has no default-deny in either direction
	// (unprotected or partially-protected by per-pod allow policies only).
	ResultFail CoverageResult = "fail"
	// ResultInconclusive — the per-namespace read errored.
	ResultInconclusive CoverageResult = "inconclusive"
)

// PolicyType is a direction a NetworkPolicy governs.
const (
	PolicyTypeIngress = "Ingress"
	PolicyTypeEgress  = "Egress"
)

// Policy SOURCE identifiers (slice 622, AC-2). The coverage record tags every
// policy summary with the API group it was read from so the platform evaluator
// can reason about which enforcement plane established a namespace's
// segmentation. SourceUpstream is the absent/default value: a record without a
// per-policy source is implicitly upstream networking.k8s.io (back-compat with
// slice-523 records that predate this field).
const (
	// SourceUpstream is the in-tree networking.k8s.io/v1 NetworkPolicy plane.
	SourceUpstream = "networking.k8s.io"
	// SourceCilium is the Cilium CNI plane (cilium.io CiliumNetworkPolicy /
	// CiliumClusterwideNetworkPolicy).
	SourceCilium = "cilium.io"
	// SourceCalico is the Calico CNI plane (crd.projectcalico.org NetworkPolicy /
	// GlobalNetworkPolicy).
	SourceCalico = "crd.projectcalico.org"
)

// RawNamespace is the narrow view the API surface returns for one namespace's
// NetworkPolicy posture. The concrete client maps the Kubernetes API responses
// into this shape; tests construct it directly. Policy SPEC metadata only — no
// pod contents, no env, no Secret refs, no traffic.
type RawNamespace struct {
	Name string
	// Policies are the NetworkPolicies that live in this namespace. Empty means
	// the namespace has zero policies (fully unprotected — allow-all default).
	Policies []RawPolicy
	// ReadError, when non-empty, marks the namespace INCONCLUSIVE (its
	// per-namespace policy read errored) rather than dropping it.
	ReadError string
}

// RawPolicy is the narrow view of a single NetworkPolicy object. SPEC metadata
// only: its name, the directions it governs, whether its podSelector is empty
// (selects every pod in the namespace), and a bounded count of ingress/egress
// rules. NO pod contents, NO from/to peer payloads beyond their bounded count.
type RawPolicy struct {
	Name string
	// Source is the API group the policy was read from (SourceUpstream /
	// SourceCilium / SourceCalico). Empty is treated as SourceUpstream. SPEC
	// metadata only — the source name is an API-group string, never workload data.
	Source string
	// PolicyTypes is the spec.policyTypes list ("Ingress" / "Egress"). When the
	// API omits it, the client derives it per Kubernetes semantics (Ingress is
	// always implied; Egress only when an egress block is present).
	PolicyTypes []string
	// SelectsAllPods is true when spec.podSelector is empty ({}), i.e. the policy
	// applies to every pod in the namespace — the marker of a namespace-wide
	// default rule.
	SelectsAllPods bool
	// IngressRuleCount / EgressRuleCount are the number of ingress / egress rule
	// blocks. Zero ingress rules on an Ingress-typed all-pods policy is the
	// canonical "default-deny ingress" shape.
	IngressRuleCount int
	EgressRuleCount  int
}

// Coverage is the per-namespace assessment the connector emits. Field names map
// 1:1 to k8s.networkpolicy_coverage.v1 schema.
type Coverage struct {
	Namespace          string
	PolicyCount        int
	Policies           []PolicySummary
	DefaultDenyIngress bool
	DefaultDenyEgress  bool
	// Sources is the deterministic, deduplicated set of API groups that
	// contributed policies to this namespace (e.g. ["cilium.io",
	// "networking.k8s.io"]). Empty when the namespace has zero policies. Lets the
	// evaluator see at a glance which enforcement planes cover the namespace
	// (slice 622, AC-2).
	Sources    []string
	Result     CoverageResult
	Reason     string
	ObservedAt time.Time
}

// PolicySummary is the per-policy summary carried in a Coverage record. SPEC
// metadata only.
type PolicySummary struct {
	Name string
	// Source is the API group the policy was read from. Defaults to
	// SourceUpstream when unset. Lets the evaluator distinguish upstream
	// NetworkPolicy from CNI-native CRD enforcement (slice 622, AC-2).
	Source           string
	PolicyTypes      []string
	SelectsAllPods   bool
	IngressRuleCount int
	EgressRuleCount  int
}

// API is the narrow surface Assess depends on. The concrete implementation
// issues read-only Kubernetes API calls; tests pass a fake. v0 lists the first
// bounded page per namespace; cursor pagination is a documented follow-on
// (threat-model D).
type API interface {
	// ListNamespaceCoverage returns one RawNamespace per visible namespace, each
	// carrying that namespace's NetworkPolicy objects (SPEC metadata only).
	ListNamespaceCoverage(ctx context.Context) ([]RawNamespace, error)
}

// Assess returns the NetworkPolicy coverage assessment for every visible
// namespace. now is injectable for deterministic tests (nil → time.Now UTC).
func Assess(ctx context.Context, api API, now func() time.Time) ([]Coverage, error) {
	if api == nil {
		return nil, errors.New("netpol: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListNamespaceCoverage(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespace coverage: %w", err)
	}
	observedAt := now()
	out := make([]Coverage, 0, len(raw))
	for _, ns := range raw {
		if ns.Name == "" {
			continue
		}
		out = append(out, assessNamespace(ns, observedAt))
	}
	return out, nil
}

// assessNamespace derives one namespace's coverage verdict from its policies.
//
// Coverage call (JUDGMENT, decisions-log D3): a namespace is "default-deny" in a
// direction when it has at least one policy that (a) selects every pod
// (empty podSelector) and (b) governs that direction with zero allow rules for
// it. That is the Kubernetes canonical default-deny shape. PASS when default-deny
// holds for at least one direction; FAIL when neither direction is default-deny;
// INCONCLUSIVE when the read errored.
func assessNamespace(ns RawNamespace, observedAt time.Time) Coverage {
	c := Coverage{
		Namespace:   ns.Name,
		PolicyCount: len(ns.Policies),
		ObservedAt:  observedAt,
		Policies:    make([]PolicySummary, 0, len(ns.Policies)),
	}
	if ns.ReadError != "" {
		c.Result = ResultInconclusive
		c.Reason = "read namespace network policies: " + ns.ReadError
		return c
	}
	sources := map[string]bool{}
	for _, p := range ns.Policies {
		types := normalizeTypes(p.PolicyTypes)
		src := normalizeSource(p.Source)
		sources[src] = true
		c.Policies = append(c.Policies, PolicySummary{
			Name:             p.Name,
			Source:           src,
			PolicyTypes:      types,
			SelectsAllPods:   p.SelectsAllPods,
			IngressRuleCount: p.IngressRuleCount,
			EgressRuleCount:  p.EgressRuleCount,
		})
		if !p.SelectsAllPods {
			continue
		}
		if governs(types, PolicyTypeIngress) && p.IngressRuleCount == 0 {
			c.DefaultDenyIngress = true
		}
		if governs(types, PolicyTypeEgress) && p.EgressRuleCount == 0 {
			c.DefaultDenyEgress = true
		}
	}
	c.Sources = sortedKeys(sources)
	c.Result, c.Reason = verdict(c)
	return c
}

// normalizeSource maps a raw policy source to one of the three known API-group
// identifiers; an empty/unknown source defaults to SourceUpstream so slice-523
// records (which carry no source) read as upstream networking.k8s.io.
func normalizeSource(s string) string {
	switch s {
	case SourceCilium, SourceCalico:
		return s
	default:
		return SourceUpstream
	}
}

// sortedKeys returns the map's keys sorted, for deterministic record output.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func verdict(c Coverage) (CoverageResult, string) {
	switch {
	case c.DefaultDenyIngress && c.DefaultDenyEgress:
		return ResultPass, ""
	case c.DefaultDenyIngress:
		return ResultPass, "namespace enforces default-deny ingress (egress is open)"
	case c.DefaultDenyEgress:
		return ResultPass, "namespace enforces default-deny egress (ingress is open)"
	case c.PolicyCount == 0:
		return ResultFail, "namespace has no NetworkPolicy (allow-all default)"
	default:
		return ResultFail, "namespace has NetworkPolicies but no default-deny (only per-pod allow rules)"
	}
}

// governs reports whether the direction is in the policy's normalized type list.
func governs(types []string, direction string) bool {
	for _, t := range types {
		if t == direction {
			return true
		}
	}
	return false
}

// normalizeTypes returns a deterministic, deduplicated set of the two valid
// policy directions present in the input. Unknown values are dropped. The result
// is sorted (Egress, Ingress) for stable record output.
func normalizeTypes(in []string) []string {
	seen := map[string]bool{}
	for _, t := range in {
		switch t {
		case PolicyTypeIngress, PolicyTypeEgress:
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
