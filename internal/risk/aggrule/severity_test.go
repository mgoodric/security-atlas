package aggrule_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

// ISC-12: ComputeRuleSeverity max delegates to risk.ComputeSeverity.
func TestComputeRuleSeverity_Max(t *testing.T) {
	t.Parallel()
	scores := []aggrule.ChildSeverity{15, 12, 9}
	got, err := aggrule.ComputeRuleSeverity(context.Background(), "max", scores, "")
	if err != nil {
		t.Fatalf("ComputeRuleSeverity max: %v", err)
	}
	// Cross-check: feeding the same severities through slice 053 directly
	// must produce the identical answer — proves delegation, not reimpl.
	want, err := risk.ComputeSeverity(risk.SeverityFunctionMax, []risk.ChildScore{
		{Likelihood: 1, Impact: 15}, {Likelihood: 1, Impact: 12}, {Likelihood: 1, Impact: 9},
	})
	if err != nil {
		t.Fatalf("risk.ComputeSeverity: %v", err)
	}
	if got != want {
		t.Fatalf("max: got %d, want %d (slice 053 delegation)", got, want)
	}
	if got != 15 {
		t.Fatalf("max: got %d, want 15", got)
	}
}

// ISC-13: ComputeRuleSeverity weighted_max delegates to risk.ComputeSeverity.
func TestComputeRuleSeverity_WeightedMax(t *testing.T) {
	t.Parallel()
	scores := []aggrule.ChildSeverity{15, 12, 9}
	got, err := aggrule.ComputeRuleSeverity(context.Background(), "weighted_max", scores, "")
	if err != nil {
		t.Fatalf("ComputeRuleSeverity weighted_max: %v", err)
	}
	want, err := risk.ComputeSeverity(risk.SeverityFunctionWeightedMax, []risk.ChildScore{
		{Likelihood: 1, Impact: 15}, {Likelihood: 1, Impact: 12}, {Likelihood: 1, Impact: 9},
	})
	if err != nil {
		t.Fatalf("risk.ComputeSeverity: %v", err)
	}
	if got != want {
		t.Fatalf("weighted_max: got %d, want %d (slice 053 delegation)", got, want)
	}
	// max(15,12,9)=15; 15*(1+log10(3))≈22.16; ceil=23.
	if got != 23 {
		t.Fatalf("weighted_max: got %d, want 23", got)
	}
}

// ISC-14: ComputeRuleSeverity sum delegates to risk.ComputeSeverity.
func TestComputeRuleSeverity_Sum(t *testing.T) {
	t.Parallel()
	scores := []aggrule.ChildSeverity{15, 12, 9}
	got, err := aggrule.ComputeRuleSeverity(context.Background(), "sum", scores, "")
	if err != nil {
		t.Fatalf("ComputeRuleSeverity sum: %v", err)
	}
	want, err := risk.ComputeSeverity(risk.SeverityFunctionSum, []risk.ChildScore{
		{Likelihood: 1, Impact: 15}, {Likelihood: 1, Impact: 12}, {Likelihood: 1, Impact: 9},
	})
	if err != nil {
		t.Fatalf("risk.ComputeSeverity: %v", err)
	}
	if got != want {
		t.Fatalf("sum: got %d, want %d (slice 053 delegation)", got, want)
	}
	// 15+12+9=36, capped at 25.
	if got != 25 {
		t.Fatalf("sum: got %d, want 25 (capped)", got)
	}
}

func TestComputeRuleSeverity_EmptyAndUnknown(t *testing.T) {
	t.Parallel()
	if _, err := aggrule.ComputeRuleSeverity(context.Background(), "max", nil, ""); !errors.Is(err, risk.ErrEmptyChildren) {
		t.Errorf("empty scores: got %v, want ErrEmptyChildren", err)
	}
	if _, err := aggrule.ComputeRuleSeverity(context.Background(), "median", []aggrule.ChildSeverity{1}, ""); !errors.Is(err, risk.ErrUnknownSeverityFunction) {
		t.Errorf("unknown fn: got %v, want ErrUnknownSeverityFunction", err)
	}
}

// ISC-15: ComputeRuleSeverity custom_rego evaluates Rego; the policy sees
// child_severities + child_count and nothing else.
func TestComputeRuleSeverity_CustomRego(t *testing.T) {
	t.Parallel()

	// A policy that returns child_count * 2 — proves child_count is wired.
	policyCount := `package aggrule.severity
severity := count * 2 if {
	count := input.child_count
}`
	got, err := aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{5, 5, 5}, policyCount)
	if err != nil {
		t.Fatalf("custom_rego count policy: %v", err)
	}
	if got != 6 {
		t.Fatalf("custom_rego count: got %d, want 6 (child_count 3 * 2)", got)
	}

	// A policy that sums child_severities — proves child_severities is wired.
	policySum := `package aggrule.severity
severity := s if {
	s := sum(input.child_severities)
}`
	got, err = aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{4, 3, 2}, policySum)
	if err != nil {
		t.Fatalf("custom_rego sum policy: %v", err)
	}
	if got != 9 {
		t.Fatalf("custom_rego sum: got %d, want 9", got)
	}

	// A policy that returns a value above the scale max is clamped to 25.
	policyHuge := `package aggrule.severity
severity := 999`
	got, err = aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{1}, policyHuge)
	if err != nil {
		t.Fatalf("custom_rego huge policy: %v", err)
	}
	if got != risk.SeverityScaleMax {
		t.Fatalf("custom_rego clamp: got %d, want %d", got, risk.SeverityScaleMax)
	}
}

// ISC-16: a custom_rego policy cannot reach the DB or other-tenant data —
// the sandbox passes no store, no custom builtins, and the only input keys
// are child_severities + child_count. A policy that tries to read anything
// else gets `undefined`, never another tenant's rows.
func TestComputeRuleSeverity_CustomRego_Sandboxed(t *testing.T) {
	t.Parallel()

	// This policy attempts to read input keys that do NOT exist in the
	// sandbox contract. In OPA those references are `undefined`, so the
	// `severity` rule body fails and `severity` is never assigned —
	// producing an ErrCustomRego ("did not assign severity"), NOT a leak.
	leaky := `package aggrule.severity
severity := x if {
	x := input.tenant_id        # not in the sandbox input
}`
	_, err := aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{5}, leaky)
	if err == nil {
		t.Fatalf("expected ErrCustomRego — policy referenced a non-sandbox key")
	}
	if !errors.Is(err, aggrule.ErrCustomRego) {
		t.Fatalf("got %v, want ErrCustomRego", err)
	}

	// A policy that tries http.send must fail to compile/eval — the
	// sandbox wires no such capability through. (OPA ships http.send as a
	// builtin, but with no network the call errors at eval; either way no
	// data crosses tenants.) We assert only that it does not return a
	// successful severity.
	netPolicy := `package aggrule.severity
severity := r if {
	resp := http.send({"method": "GET", "url": "http://169.254.169.254/"})
	r := resp.status_code
}`
	if _, err := aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{5}, netPolicy); err == nil {
		t.Fatalf("expected error — http.send must not yield a usable severity")
	}

	// A syntactically broken policy is a compile error, never a panic.
	if _, err := aggrule.ComputeRuleSeverity(context.Background(), "custom_rego",
		[]aggrule.ChildSeverity{5}, "this is not rego"); !errors.Is(err, aggrule.ErrCustomRego) {
		t.Fatalf("broken policy: got %v, want ErrCustomRego", err)
	}
}
