// severity.go — the slice 054 rule-severity computation.
//
// Three of the four canvas §6.6 severity functions (max, weighted_max, sum)
// are already implemented and unit-tested in slice 053's
// risk.ComputeSeverity. This file does NOT reimplement them — it delegates.
// The fourth, custom_rego, evaluates a tenant-authored OPA Rego policy in a
// sandbox whose input is ONLY {child_severities, child_count}: no database
// handle, no other-tenant data, nothing else reaches the policy. That is the
// AI-assist-boundary guarantee — a custom severity function cannot exfiltrate
// or cross tenants because there is structurally nothing for it to reach.
package aggrule

import (
	"context"
	"errors"
	"fmt"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"

	"github.com/mgoodric/security-atlas/internal/eval/regocache"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ErrCustomRego wraps any failure evaluating a custom_rego severity policy:
// a compile error, an evaluation error, or a result that is not a usable
// integer severity. Callers map it to a 4xx/5xx as appropriate.
var ErrCustomRego = errors.New("aggrule: custom_rego severity policy failed")

// ChildSeverity is one child risk's severity scalar (likelihood × impact on
// the 5×5 grid). The engine extracts these from candidate risks before
// calling ComputeRuleSeverity.
type ChildSeverity int

// ComputeRuleSeverity returns the aggregated severity for a fired rule.
//
//   - "max" | "weighted_max" | "sum" — delegated verbatim to slice 053's
//     risk.ComputeSeverity (DO NOT reimplement). Each ChildSeverity is fed
//     in as a 1×severity ChildScore so the existing capped arithmetic
//     applies unchanged.
//   - "custom_rego" — the regoPolicy bytes are compiled and evaluated with
//     input {child_severities: []int, child_count: int} ONLY. The query is
//     `data.aggrule.severity.severity`; the policy must assign an integer.
//
// The result is clamped to [0, risk.SeverityScaleMax] regardless of function
// so a meta-risk can never be more severe than the worst single grid cell.
func ComputeRuleSeverity(ctx context.Context, fn string, scores []ChildSeverity, regoPolicy string) (int, error) {
	if len(scores) == 0 {
		return 0, risk.ErrEmptyChildren
	}

	switch fn {
	case "max", "weighted_max", "sum":
		// Delegate to slice 053. Each ChildSeverity is already a severity
		// scalar, so it maps to a ChildScore of (1 × severity): Severity()
		// returns Likelihood*Impact = 1*severity = severity.
		childScores := make([]risk.ChildScore, len(scores))
		for i, s := range scores {
			childScores[i] = risk.ChildScore{Likelihood: 1, Impact: int(s)}
		}
		return risk.ComputeSeverity(risk.SeverityFunction(fn), childScores)

	case "custom_rego":
		return evalCustomRego(ctx, regoPolicy, scores)

	default:
		return 0, fmt.Errorf("%w: %q", risk.ErrUnknownSeverityFunction, fn)
	}
}

// customRegoQuery is the fixed query the sandbox evaluates. A custom_rego
// policy MUST live in package `aggrule.severity` and assign `severity`.
const customRegoQuery = "data.aggrule.severity.severity"

// deniedBuiltins are the OPA builtins a tenant-authored severity policy must
// never be able to reach. OPA's DEFAULT capability set includes http.send,
// net.lookup_ip_addr, opa.runtime, etc. — leaving them in place would let a
// custom_rego policy hit the cloud metadata endpoint, do DNS exfiltration, or
// read the runtime environment. We strip them from the capability set so a
// policy that references any of them fails at COMPILE time (PrepareForEval
// errors), not merely at eval time.
var deniedBuiltins = map[string]bool{
	"http.send":          true,
	"net.lookup_ip_addr": true,
	"opa.runtime":        true,
	"rego.parse_module":  true,
	"trace":              true,
	"print":              true,
}

// sandboxCapabilities returns an OPA capability set with every network /
// runtime / introspection builtin removed. This is the structural half of
// the AI-assist-boundary guarantee — the input shape is the other half.
func sandboxCapabilities() *ast.Capabilities {
	caps := ast.CapabilitiesForThisVersion()
	filtered := make([]*ast.Builtin, 0, len(caps.Builtins))
	for _, b := range caps.Builtins {
		if deniedBuiltins[b.Name] {
			continue
		}
		filtered = append(filtered, b)
	}
	caps.Builtins = filtered
	return caps
}

// customSeverityModuleName is the fixed module name passed to OPA. Kept
// as a const so it participates in the cache key deterministically.
const customSeverityModuleName = "custom_severity.rego"

// defaultRegoCache caches compiled custom_rego severity policies across
// evalCustomRego calls. Package-level (distinct from internal/eval's
// cache — different policy population, separate cache surface; see D4 of
// docs/audit-log/377). Slice 332 audit F-OPA-1 closure for this site.
var defaultRegoCache = regocache.New()

// evalCustomRego compiles and evaluates a tenant-authored Rego policy.
//
// SANDBOX CONTRACT (the AI-assist-boundary guarantee):
//   - The ONLY input is {child_severities: []int, child_count: int}.
//   - No rego.Store, no rego.Function — and the capability set is restricted
//     via sandboxCapabilities() so http.send / net.* / opa.runtime are not
//     even compilable. A policy referencing them fails at PrepareForEval.
//   - The policy bytes travel WITH the rule (aggregation_rules.rule_body) so
//     nothing is fetched at evaluation time.
//
// PERFORMANCE: the compiled query is cached in defaultRegoCache keyed by
// (policy text || capability fingerprint). The first call for a given
// (policy, capabilities) pair compiles via PrepareForEval; subsequent
// calls re-use the cached *rego.PreparedEvalQuery. Closes slice 332
// audit F-OPA-1 at this call site.
func evalCustomRego(ctx context.Context, policy string, scores []ChildSeverity) (int, error) {
	if policy == "" {
		return 0, fmt.Errorf("%w: policy is empty", ErrCustomRego)
	}

	q, err := defaultRegoCache.GetOrPrepare(ctx, policy, sandboxCapabilities(), customRegoQuery, customSeverityModuleName)
	if err != nil {
		return 0, fmt.Errorf("%w: compile: %v", ErrCustomRego, err)
	}

	sevInts := make([]interface{}, len(scores))
	for i, s := range scores {
		sevInts[i] = int(s)
	}
	input := map[string]interface{}{
		"child_severities": sevInts,
		"child_count":      len(scores),
	}

	rs, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return 0, fmt.Errorf("%w: eval: %v", ErrCustomRego, err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return 0, fmt.Errorf("%w: policy did not assign `severity`", ErrCustomRego)
	}

	// OPA decodes JSON numbers as json.Number; accept that plus the
	// occasional plain float/int.
	sev, err := coerceSeverity(rs[0].Expressions[0].Value)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrCustomRego, err)
	}
	return clampSeverity(sev), nil
}

// coerceSeverity turns OPA's result value into an int. OPA yields
// json.Number for numeric results.
func coerceSeverity(v interface{}) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case interface{ Int64() (int64, error) }: // json.Number
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("severity is not an integer: %v", v)
		}
		return int(i), nil
	default:
		return 0, fmt.Errorf("severity result is %T, want a number", v)
	}
}

// clampSeverity bounds a severity to [0, risk.SeverityScaleMax]. A meta-risk
// can never exceed the worst single 5×5 cell.
func clampSeverity(v int) int {
	if v < 0 {
		return 0
	}
	if v > risk.SeverityScaleMax {
		return risk.SeverityScaleMax
	}
	return v
}
