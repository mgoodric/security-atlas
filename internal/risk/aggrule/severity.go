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

	"github.com/open-policy-agent/opa/v1/rego"

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

// evalCustomRego compiles and evaluates a tenant-authored Rego policy.
//
// SANDBOX CONTRACT (the AI-assist-boundary guarantee):
//   - The ONLY input is {child_severities: []int, child_count: int}.
//   - No rego.Store, no rego.Function, no http.send capability is wired —
//     the default OPA runtime has no network or storage access, and we pass
//     no custom builtins, so the policy cannot reach a database, the
//     filesystem, the network, or any other tenant's data.
//   - The policy bytes travel WITH the rule (aggregation_rules.rule_body) so
//     nothing is fetched at evaluation time.
func evalCustomRego(ctx context.Context, policy string, scores []ChildSeverity) (int, error) {
	if policy == "" {
		return 0, fmt.Errorf("%w: policy is empty", ErrCustomRego)
	}

	sevInts := make([]interface{}, len(scores))
	for i, s := range scores {
		sevInts[i] = int(s)
	}
	input := map[string]interface{}{
		"child_severities": sevInts,
		"child_count":      len(scores),
	}

	q, err := rego.New(
		rego.Query(customRegoQuery),
		rego.Module("custom_severity.rego", policy),
		rego.Input(input),
	).PrepareForEval(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: compile: %v", ErrCustomRego, err)
	}

	rs, err := q.Eval(ctx)
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
