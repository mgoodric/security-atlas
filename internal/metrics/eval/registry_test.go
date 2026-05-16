package eval_test

import (
	"sort"
	"testing"

	"github.com/mgoodric/security-atlas/internal/metrics/eval"
)

func TestRegistry_RegistersAllEightStarters(t *testing.T) {
	r := eval.NewRegistry(nil)
	got := r.Names()
	want := []string{
		"audit_readiness_score",
		"critical_findings_sla",
		"evidence_freshness_pct",
		"exception_expiration_runway",
		"open_risk_financial_exposure",
		"policy_attestation_rate",
		"program_effectiveness",
		"vendor_risk_concentration",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("registry has %d evaluators (%v); want %d (%v)", len(got), got, len(want), want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("evaluator[%d] = %q; want %q", i, got[i], name)
		}
	}
}

func TestRegistry_HasReportsRegistered(t *testing.T) {
	r := eval.NewRegistry(nil)
	if !r.Has("program_effectiveness") {
		t.Error("Has(program_effectiveness) = false; want true")
	}
	if r.Has("nonexistent") {
		t.Error("Has(nonexistent) = true; want false")
	}
}

func TestRegistry_GetReturnsEvaluator(t *testing.T) {
	r := eval.NewRegistry(nil)
	e, ok := r.Get("program_effectiveness")
	if !ok {
		t.Fatal("Get returned ok=false for program_effectiveness")
	}
	if e.Name() != "program_effectiveness" {
		t.Errorf("evaluator.Name() = %q; want program_effectiveness", e.Name())
	}
}
