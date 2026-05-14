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
)

// ===== ISC-19: a Rego evidence query evaluates with input = records only =====

func TestEvalRegoQuery_PassWhenAllRecordsPass(t *testing.T) {
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
	if _, err := evalRegoQuery(context.Background(), "", nil); err == nil {
		t.Fatalf("evalRegoQuery(empty policy) should error")
	}
}

// ===== ISC-20 + ISC-21: the sandbox strips network/runtime builtins =====

func TestEvalRegoQuery_HTTPSendRejectedAtCompileTime(t *testing.T) {
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
	caps := evalSandboxCapabilities()
	for _, b := range caps.Builtins {
		switch b.Name {
		case "http.send", "net.lookup_ip_addr", "opa.runtime", "rego.parse_module":
			t.Fatalf("sandbox capability set still contains denied builtin %q", b.Name)
		}
	}
}
