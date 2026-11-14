package risk

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func child(methodology dbx.RiskMethodology, inherent string) LinkedRisk {
	return LinkedRisk{Methodology: methodology, inherentScore: []byte(inherent)}
}

// The roll-up uses the existing severity functions: the same numbers
// ComputeSeverity gives for the same children (#1748).
func TestComputeRollUp_MatchesSeverityFunctions(t *testing.T) {
	t.Parallel()
	children := []LinkedRisk{
		child(dbx.RiskMethodologyNist80030, `{"likelihood":3,"impact":4}`),      // 12
		child(dbx.RiskMethodologyQualitative5x5, `{"likelihood":2,"impact":3}`), // 6
		child(dbx.RiskMethodologyNist80030, `{"likelihood":1,"impact":2}`),      // 2
	}
	scores := []ChildScore{{3, 4}, {2, 3}, {1, 2}}

	cases := []struct {
		name     string
		parent   string
		wantFn   SeverityFunction
		assessed int
	}{
		{"no function stored defaults to max", `{"likelihood":2,"impact":2}`, SeverityFunctionMax, 4},
		{"stored max", `{"severity":20,"severity_function":"max"}`, SeverityFunctionMax, 20},
		{"stored weighted_max", `{"severity":9,"severity_function":"weighted_max"}`, SeverityFunctionWeightedMax, 9},
		{"stored sum", `{"severity":30,"severity_function":"sum"}`, SeverityFunctionSum, 30},
		{"unknown function falls back to max", `{"severity":1,"severity_function":"median"}`, SeverityFunctionMax, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeRollUp(dbx.RiskMethodologyQualitative5x5, []byte(tc.parent), children)
			if got == nil {
				t.Fatal("ComputeRollUp = nil, want a roll-up")
			}
			want, err := ComputeSeverity(tc.wantFn, scores)
			if err != nil {
				t.Fatalf("ComputeSeverity: %v", err)
			}
			if got.Function != tc.wantFn || got.Severity != want || got.ChildCount != 3 {
				t.Fatalf("roll-up = %+v, want %s severity %d over 3 children", *got, tc.wantFn, want)
			}
			if got.AssessedSeverity != tc.assessed {
				t.Fatalf("assessed = %d, want %d", got.AssessedSeverity, tc.assessed)
			}
			if got.ExceedsAssessed != (want > tc.assessed) {
				t.Fatalf("exceeds_assessed = %v with roll-up %d and assessed %d", got.ExceedsAssessed, want, tc.assessed)
			}
		})
	}
}

func TestComputeRollUp_SkipsUnscoredChildren(t *testing.T) {
	t.Parallel()
	children := []LinkedRisk{
		child(dbx.RiskMethodologyFair, `{"annualized_loss_exposure":1000000}`),
		child(dbx.RiskMethodologyNist80030, `{}`),
		child(dbx.RiskMethodologyNist80030, `{"likelihood":4,"impact":5}`),
	}
	got := ComputeRollUp(dbx.RiskMethodologyNist80030, []byte(`{"likelihood":5,"impact":5}`), children)
	if got == nil || got.ChildCount != 1 || got.Severity != 20 {
		t.Fatalf("roll-up = %+v, want severity 20 from the one scored child", got)
	}
	if got.ExceedsAssessed {
		t.Fatalf("exceeds_assessed set with roll-up 20 below assessed 25")
	}
	if ComputeRollUp(dbx.RiskMethodologyNist80030, nil, children[:2]) != nil {
		t.Fatal("roll-up over no scored child should be nil")
	}
	if ComputeRollUp(dbx.RiskMethodologyNist80030, nil, nil) != nil {
		t.Fatal("roll-up over no children should be nil")
	}
}

// A FAIR parent's dollar exposure is not comparable to the 5x5 roll-up, so
// the higher-than-assessed flag stays off.
func TestComputeRollUp_FairParentNeverExceeds(t *testing.T) {
	t.Parallel()
	children := []LinkedRisk{child(dbx.RiskMethodologyNist80030, `{"likelihood":5,"impact":5}`)}
	got := ComputeRollUp(dbx.RiskMethodologyFair, []byte(`{"annualized_loss_exposure":5}`), children)
	if got == nil || got.Severity != 25 || got.ExceedsAssessed {
		t.Fatalf("roll-up = %+v, want severity 25 with exceeds_assessed false", got)
	}
}

func TestLevelRankOrdersHierarchy(t *testing.T) {
	t.Parallel()
	if levelRank(dbx.RiskLevelTeam) >= levelRank(dbx.RiskLevelOrg) ||
		levelRank(dbx.RiskLevelOrg) >= levelRank(dbx.RiskLevelCompany) {
		t.Fatal("want team < org < company")
	}
	if levelRank("") != 0 {
		t.Fatal("unknown level should rank 0")
	}
}

func TestLinkedRiskSource(t *testing.T) {
	t.Parallel()
	var l LinkedRisk
	if l.Source() != LinkSourceManual {
		t.Fatalf("source with no rule = %q, want manual", l.Source())
	}
	id := l.ID
	l.RuleID = &id
	if l.Source() != LinkSourceRule {
		t.Fatalf("source with a rule = %q, want rule", l.Source())
	}
}

func TestAssessedSeverity(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{
		``:                               0,
		`not json`:                       0,
		`{"likelihood":3,"impact":4}`:    12,
		`{"severity":7,"likelihood":1}`:  7,
		`{"annualized_loss_exposure":9}`: 0,
	} {
		if got := AssessedSeverity([]byte(in)); got != want {
			t.Errorf("AssessedSeverity(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestIsChildLinkValidation(t *testing.T) {
	t.Parallel()
	for _, err := range []error{ErrChildLevel, ErrIncompatibleMethodology, ErrEmptyChildren} {
		if !IsChildLinkValidation(err) {
			t.Errorf("IsChildLinkValidation(%v) = false, want true", err)
		}
	}
	for _, err := range []error{ErrAggregationCycle, ErrNotFound, ErrLinkNotFound} {
		if IsChildLinkValidation(err) {
			t.Errorf("IsChildLinkValidation(%v) = true, want false", err)
		}
	}
}
