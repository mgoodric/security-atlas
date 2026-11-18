// The action-plan side enforces the mitigate rule with
// actionplan.MitigationHeld, because actionplan cannot import this package
// (risk imports eval, which imports actionplan). This external test package
// imports both and checks the two agree on the mitigate rule (#2449).

package risk_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestMitigationHeldAgreesWithValidateTreatment(t *testing.T) {
	t.Parallel()
	for _, controls := range []int{0, 1, 2} {
		for _, plans := range []int{0, 1, 2} {
			want := risk.ValidateTreatment(risk.TreatmentInput{
				Treatment:             dbx.RiskTreatmentMitigate,
				LinkedControlCount:    controls,
				LinkedActionPlanCount: plans,
			}) == nil
			if got := actionplan.MitigationHeld(dbx.RiskTreatmentMitigate, controls, plans); got != want {
				t.Errorf("mitigate controls=%d plans=%d: MitigationHeld=%v, ValidateTreatment ok=%v", controls, plans, got, want)
			}
		}
	}
	// No other treatment counts links.
	for _, tr := range []dbx.RiskTreatment{
		dbx.RiskTreatmentUntreated, dbx.RiskTreatmentAccept,
		dbx.RiskTreatmentTransfer, dbx.RiskTreatmentAvoid,
	} {
		if !actionplan.MitigationHeld(tr, 0, 0) {
			t.Errorf("%s with no links: MitigationHeld=false, want true", tr)
		}
	}
}
