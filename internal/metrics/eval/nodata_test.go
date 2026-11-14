package eval

import "testing"

// #2064: every evaluator that can run with no inputs returns the empty
// marker and a reason, never a number that reads as a pass or a fail.
// These cover the pure result builders; integration_test.go covers the
// SQL that feeds them.

func TestEvaluators_NoInputsSetEmptyMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  Result
	}{
		{"critical_findings_sla", criticalFindingsSLAResult(0)},
		{"program_effectiveness", programEffectivenessResult(0, 0)},
		{"evidence_freshness_pct", evidenceFreshnessPctResult(0, 0)},
		{"audit_readiness_score/no_scope", auditReadinessScoreResult(0, 0, 5, 5)},
		{"audit_readiness_score/no_freshness", auditReadinessScoreResult(2, 2, 0, 0)},
		{"open_risk_financial_exposure/no_risks", openRiskFinancialExposureResult(0, 0, 0)},
		{"open_risk_financial_exposure/unscored", openRiskFinancialExposureResult(0, 3, 0)},
		{"policy_attestation_rate", policyAttestationRateResult(0, 0)},
		{"training_completion_rate", trainingCompletionRateResult(0, 0)},
		{"vendor_risk_concentration", vendorRiskConcentrationResult(0, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !c.got.IsNoData() {
				t.Fatalf("dims = %v; want %s=%s", c.got.Dimensions, SampleKey, SampleEmpty)
			}
			if c.got.Dimensions[NoDataReasonKey] == "" {
				t.Errorf("dims = %v; want a non-empty %s", c.got.Dimensions, NoDataReasonKey)
			}
			if c.got.Value != 0 {
				t.Errorf("Value = %v; want 0 (an empty result carries no value)", c.got.Value)
			}
		})
	}
}

func TestEvaluators_InputsPresentCarryNoEmptyMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		got   Result
		value float64
	}{
		{"critical_findings_sla", criticalFindingsSLAResult(2), 0},
		{"program_effectiveness", programEffectivenessResult(4, 3), 0.75},
		{"evidence_freshness_pct", evidenceFreshnessPctResult(4, 1), 0.25},
		{"audit_readiness_score", auditReadinessScoreResult(2, 1, 4, 4), 0.5},
		{"open_risk_financial_exposure", openRiskFinancialExposureResult(11, 2, 2), 11},
		{"policy_attestation_rate", policyAttestationRateResult(2, 1), 0.5},
		{"training_completion_rate", trainingCompletionRateResult(4, 4), 1},
		{"vendor_risk_concentration", vendorRiskConcentrationResult(3, 1), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.got.IsNoData() {
				t.Fatalf("dims = %v; want no empty marker when inputs exist", c.got.Dimensions)
			}
			if c.got.Value != c.value {
				t.Errorf("Value = %v; want %v", c.got.Value, c.value)
			}
		})
	}
}

// The SLA evaluator used to return 1.0 for "no findings", which the
// metrics page rendered as a 100% pass.
func TestCriticalFindingsSLA_NoFindingsIsNotAPass(t *testing.T) {
	t.Parallel()
	r := criticalFindingsSLAResult(0)
	if r.Value == 1.0 {
		t.Fatal("Value = 1.0; no findings must not read as a 100% pass")
	}
	if got, want := r.Dimensions[NoDataReasonKey], "No critical findings recorded in the last 90 days"; got != want {
		t.Errorf("reason = %q; want %q", got, want)
	}
	if r.Dimensions["window_days"] != "90" {
		t.Errorf("dims = %v; want window_days kept alongside the marker", r.Dimensions)
	}
}
