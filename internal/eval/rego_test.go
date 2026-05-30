// Unit tests for the Rego evidence-query sandbox. A control bundle's
// `evidence_queries[]` (slice 009) may carry a Rego expression; the
// evaluation engine runs it in a capabilities-restricted OPA sandbox whose
// input is ONLY the in-window evidence records for that (control, cell).
// Network / runtime / introspection builtins are stripped from the
// capability set so a hostile or buggy query fails at COMPILE time, not at
// eval time. This is the same hardening slice 054 applied to custom-rego
// severity policies (internal/risk/aggrule/severity.go).
package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/rego"
)

// ===== ISC-19: a Rego evidence query evaluates with input = records only =====

func TestEvalRegoQuery_PassWhenAllRecordsPass(t *testing.T) {
	t.Parallel()
	// The query inspects input.records and asserts every record passed.
	policy := `
package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}
`
	records := []inWindowRecord{{result: "pass"}, {result: "pass"}}
	got, err := evalRegoQuery(context.Background(), policy, records)
	if err != nil {
		t.Fatalf("evalRegoQuery: %v", err)
	}
	if got != "pass" {
		t.Fatalf("evalRegoQuery all-pass = %q, want pass", got)
	}
}

func TestEvalRegoQuery_FailWhenARecordFails(t *testing.T) {
	t.Parallel()
	policy := `
package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}
`
	records := []inWindowRecord{{result: "pass"}, {result: "fail"}}
	got, err := evalRegoQuery(context.Background(), policy, records)
	if err != nil {
		t.Fatalf("evalRegoQuery: %v", err)
	}
	if got != "fail" {
		t.Fatalf("evalRegoQuery with-fail = %q, want fail", got)
	}
}

func TestEvalRegoQuery_EmptyRecordsHandled(t *testing.T) {
	t.Parallel()
	// With zero records the policy's default branch fires. The engine maps a
	// non-pass Rego result over an empty record set to `inconclusive` at the
	// caller; evalRegoQuery itself just returns whatever the policy said.
	policy := `
package evidence.query
import rego.v1
default result := "inconclusive"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}
`
	got, err := evalRegoQuery(context.Background(), policy, nil)
	if err != nil {
		t.Fatalf("evalRegoQuery(empty records): %v", err)
	}
	if got != "inconclusive" {
		t.Fatalf("evalRegoQuery(empty records) = %q, want inconclusive (default branch)", got)
	}
}

func TestEvalRegoQuery_EmptyPolicyErrors(t *testing.T) {
	t.Parallel()
	if _, err := evalRegoQuery(context.Background(), "", nil); err == nil {
		t.Fatalf("evalRegoQuery(empty policy) should error")
	}
}

// ===== ISC-20 + ISC-21: the sandbox strips network/runtime builtins =====

func TestEvalRegoQuery_HTTPSendRejectedAtCompileTime(t *testing.T) {
	t.Parallel()
	// A query that tries to reach the network must fail to COMPILE — the
	// builtin is not in the restricted capability set. This is the
	// structural half of the AI-assist-boundary guarantee: a
	// tenant-authored-adjacent query cannot exfiltrate because http.send is
	// not even a known function.
	hostile := `
package evidence.query
import rego.v1
result := "pass" if {
	http.send({"method": "GET", "url": "http://169.254.169.254/"})
}
`
	_, err := evalRegoQuery(context.Background(), hostile, []inWindowRecord{{result: "pass"}})
	if err == nil {
		t.Fatalf("expected http.send query to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "compile") && !strings.Contains(err.Error(), "rego_type_error") && !strings.Contains(err.Error(), "undefined") {
		t.Fatalf("expected a compile-time rejection, got: %v", err)
	}
}

func TestEvalRegoQuery_OPARuntimeRejectedAtCompileTime(t *testing.T) {
	t.Parallel()
	hostile := `
package evidence.query
import rego.v1
result := "pass" if {
	rt := opa.runtime()
	rt.env.PATH != ""
}
`
	_, err := evalRegoQuery(context.Background(), hostile, []inWindowRecord{{result: "pass"}})
	if err == nil {
		t.Fatalf("expected opa.runtime query to be rejected, got nil error")
	}
}

func TestSandboxCapabilities_StripsDeniedBuiltins(t *testing.T) {
	t.Parallel()
	caps := evalSandboxCapabilities()
	for _, b := range caps.Builtins {
		switch b.Name {
		case "http.send", "net.lookup_ip_addr", "opa.runtime", "rego.parse_module":
			t.Fatalf("sandbox capability set still contains denied builtin %q", b.Name)
		}
	}
}

// ===== Benchmark: slice 332 F-OPA-1 closure verification =====
//
// BenchmarkEvalRegoQueryRepeatedCompile exercises the hot-path
// (cached-by-default) evalRegoQuery against a representative
// evidence-query policy. The benchmark measures the per-call cost of
// the steady-state cached path; the cache miss happens once at first
// call and is amortised across b.N iterations.
//
// BenchmarkEvalRegoQueryRepeatedCompile_Uncached measures the same
// shape WITHOUT the cache — it re-prepares the policy via the
// pre-#377 code path on every iteration. Comparing the two gives the
// audit F-OPA-1 remediation delta.
//
// Reproducibility: the policy text is a fixed const so the cache key
// is stable across runs; b.ResetTimer is called after the first
// (miss) call so the steady-state cost dominates.

const benchPolicy = `
package evidence.query
import rego.v1
default result := "fail"
result := "pass" if {
	count(input.records) > 0
	every r in input.records { r.result == "pass" }
}
`

func BenchmarkEvalRegoQueryRepeatedCompile(b *testing.B) {
	ctx := context.Background()
	records := []inWindowRecord{{result: "pass"}, {result: "pass"}, {result: "pass"}}

	// Warm the cache so the steady-state hit cost dominates.
	if _, err := evalRegoQuery(ctx, benchPolicy, records); err != nil {
		b.Fatalf("warm: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := evalRegoQuery(ctx, benchPolicy, records); err != nil {
			b.Fatalf("eval: %v", err)
		}
	}
}

func BenchmarkEvalRegoQueryRepeatedCompile_Uncached(b *testing.B) {
	ctx := context.Background()
	records := []inWindowRecord{{result: "pass"}, {result: "pass"}, {result: "pass"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reproduces the pre-#377 hot path: re-prepare on every call.
		inRecs := make([]regoInputRecord, len(records))
		for j, r := range records {
			inRecs[j] = regoInputRecord{Result: r.result}
		}
		input := map[string]interface{}{"records": inRecs}
		q, err := rego.New(
			rego.Query(regoQuery),
			rego.Module(evidenceQueryModuleName, benchPolicy),
			rego.Input(input),
			rego.Capabilities(evalSandboxCapabilities()),
		).PrepareForEval(ctx)
		if err != nil {
			b.Fatalf("prepare: %v", err)
		}
		if _, err := q.Eval(ctx); err != nil {
			b.Fatalf("eval: %v", err)
		}
	}
}
