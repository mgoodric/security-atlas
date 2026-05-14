// rego.go — Rego evidence-query evaluation in a capabilities-restricted OPA
// sandbox.
//
// A control bundle's `evidence_queries[]` (slice 009) may declare a Rego
// expression over the evidence ledger. When the engine evaluates such a
// control it runs the query here. The sandbox contract:
//
//   - The ONLY input is {records: [{result, observed_at, hash}, ...]} — the
//     in-window evidence records for that (control, scope_cell). No DB
//     handle, no other-tenant data, nothing else reaches the policy.
//   - No rego.Store, no rego.Function.
//   - The capability set is restricted via evalSandboxCapabilities() so the
//     network / runtime / introspection builtins (http.send, net.*,
//     opa.runtime, ...) are not even compilable. A query referencing one
//     fails at PrepareForEval — COMPILE time — not merely at eval time.
//
// This is the same hardening slice 054 applied to custom-rego severity
// policies (internal/risk/aggrule/severity.go). A control bundle's evidence
// query is tenant-authored-adjacent content and is sandboxed identically.
package eval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
)

// ErrRegoQuery wraps any failure evaluating a Rego evidence query: a compile
// error (including a reference to a stripped builtin), an evaluation error,
// or a result that is not a usable evidence_result string.
var ErrRegoQuery = errors.New("eval: rego evidence query failed")

// regoQuery is the fixed query the sandbox evaluates. A Rego evidence query
// MUST live in package `evidence.query` and assign `result` to one of
// pass | fail | na | inconclusive.
const regoQuery = "data.evidence.query.result"

// deniedRegoBuiltins are the OPA builtins a Rego evidence query must never be
// able to reach. OPA's DEFAULT capability set includes http.send,
// net.lookup_ip_addr, opa.runtime, etc. Leaving them in place would let a
// query hit the cloud metadata endpoint, do DNS exfiltration, or read the
// runtime environment. Stripping them from the capability set means a query
// referencing any of them fails at COMPILE time.
var deniedRegoBuiltins = map[string]bool{
	"http.send":          true,
	"net.lookup_ip_addr": true,
	"opa.runtime":        true,
	"rego.parse_module":  true,
	"trace":              true,
	"print":              true,
}

// evalSandboxCapabilities returns an OPA capability set with every network /
// runtime / introspection builtin removed. The structural half of the
// sandbox guarantee — the input shape is the other half.
func evalSandboxCapabilities() *ast.Capabilities {
	caps := ast.CapabilitiesForThisVersion()
	filtered := make([]*ast.Builtin, 0, len(caps.Builtins))
	for _, b := range caps.Builtins {
		if deniedRegoBuiltins[b.Name] {
			continue
		}
		filtered = append(filtered, b)
	}
	caps.Builtins = filtered
	return caps
}

// regoInputRecord is one in-window evidence record as the Rego policy sees
// it. Deliberately minimal — the policy gets the result enum and nothing
// that could identify another tenant's data.
type regoInputRecord struct {
	Result     string    `json:"result"`
	ObservedAt time.Time `json:"observed_at"`
}

// evalRegoQuery compiles and evaluates a Rego evidence query against the
// in-window records. Returns the policy's `result` assignment as a string.
//
// SANDBOX CONTRACT (see package doc): input is ONLY {records: [...]}, no
// store, no custom functions, capability set restricted so network/runtime
// builtins fail at compile time.
func evalRegoQuery(ctx context.Context, policy string, records []inWindowRecord) (string, error) {
	if policy == "" {
		return "", fmt.Errorf("%w: policy is empty", ErrRegoQuery)
	}

	inRecs := make([]regoInputRecord, len(records))
	for i, r := range records {
		inRecs[i] = regoInputRecord{Result: r.result}
	}
	input := map[string]interface{}{"records": inRecs}

	q, err := rego.New(
		rego.Query(regoQuery),
		rego.Module("evidence_query.rego", policy),
		rego.Input(input),
		rego.Capabilities(evalSandboxCapabilities()),
	).PrepareForEval(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: compile: %v", ErrRegoQuery, err)
	}

	rs, err := q.Eval(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: eval: %v", ErrRegoQuery, err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return "", fmt.Errorf("%w: policy did not assign `result`", ErrRegoQuery)
	}

	s, ok := rs[0].Expressions[0].Value.(string)
	if !ok {
		return "", fmt.Errorf("%w: result is %T, want a string", ErrRegoQuery, rs[0].Expressions[0].Value)
	}
	switch s {
	case ResultPass, ResultFail, ResultNA, ResultInconclusive:
		return s, nil
	default:
		return "", fmt.Errorf("%w: result %q is not one of pass|fail|na|inconclusive", ErrRegoQuery, s)
	}
}
