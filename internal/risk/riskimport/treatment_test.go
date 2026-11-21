package riskimport

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestParseCSV_Treatment(t *testing.T) {
	t.Parallel()
	csv := "Risk ID,Title,Category,Treatment,Justification,Accepted Until,Accepted By,Instrument,Linked Controls\n" +
		"R-1,Accepted,operational,Accepted,Cost of fix exceeds exposure,2027-06-30,Jo CISO,,\n" +
		"R-2,Accepted no why,operational,accept,,2027-06-30,,,\n" +
		"R-3,Accepted no until,operational,accept,Because,,,,\n" +
		"R-4,Bad until,operational,accept,Because,next year,,,\n" +
		"R-5,Bad treatment,operational,ignore,,,,,\n" +
		"R-6,Mitigated,operational,Mitigate,,,,,\"AC-1; ac-1, 6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839\"\n" +
		"R-7,Transferred,operational,transfer,,,,POL-9,\n" +
		"R-8,Blank,operational,,,,,,\n"
	got, err := ParseCSVWith([]byte(csv), ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCSVWith: %v", err)
	}
	if r := rejections(got.Rejected); !reflect.DeepEqual(r, map[int]string{
		3: RejectInvalidAcceptance, 4: RejectInvalidAcceptance, 5: RejectInvalidAcceptance, 6: RejectInvalidTreatment,
	}) {
		t.Errorf("rejected = %v (%+v)", r, got.Rejected)
	}
	if len(got.UnmappedColumns) != 0 {
		t.Errorf("unmapped columns = %q", got.UnmappedColumns)
	}
	byRef := map[string]StagedRisk{}
	for _, r := range got.Risks {
		byRef[r.ExternalRef] = r
	}
	until := time.Date(2027, 6, 30, 0, 0, 0, 0, time.UTC)
	if r := byRef["R-1"]; r.Treatment != dbx.RiskTreatmentAccept || r.Justification != "Cost of fix exceeds exposure" ||
		r.ProposedUntil == nil || !r.ProposedUntil.Equal(until) || r.HistoricalAccepter != "Jo CISO" {
		t.Errorf("R-1 = %+v", r)
	}
	if r := byRef["R-6"]; r.Treatment != dbx.RiskTreatmentMitigate ||
		!reflect.DeepEqual(r.ControlRefs, []string{"AC-1", "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839"}) {
		t.Errorf("R-6 = %+v", r)
	}
	if r := byRef["R-7"]; r.Treatment != dbx.RiskTreatmentTransfer || r.InstrumentReference != "POL-9" {
		t.Errorf("R-7 = %+v", r)
	}
	if r := byRef["R-8"]; r.Treatment != dbx.RiskTreatmentUntreated {
		t.Errorf("R-8 = %+v", r)
	}
}

func TestApplyTreatmentRules(t *testing.T) {
	t.Parallel()
	ctl := uuid.MustParse("6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2839")
	resolved := map[string]uuid.UUID{"AC-1": ctl, ctl.String(): ctl}
	for _, tc := range []struct {
		name                 string
		treatment            dbx.RiskTreatment
		instrument           string
		refs                 []string
		wantTreatment        string
		wantRequested        string
		wantLinks, wantUnres []string
		wantFlags            []string
	}{
		{name: "mitigate with a resolved link keeps mitigate", treatment: dbx.RiskTreatmentMitigate,
			refs: []string{"AC-1", ctl.String(), "NOPE"}, wantTreatment: "mitigate",
			wantLinks: []string{ctl.String()}, wantUnres: []string{"NOPE"}},
		{name: "mitigate with no resolved link is downgraded", treatment: dbx.RiskTreatmentMitigate,
			refs: []string{"NOPE"}, wantTreatment: "untreated", wantRequested: "mitigate",
			wantUnres: []string{"NOPE"}, wantFlags: []string{FlagTreatmentDowngraded}},
		{name: "mitigate with no links is downgraded", treatment: dbx.RiskTreatmentMitigate,
			wantTreatment: "untreated", wantRequested: "mitigate", wantFlags: []string{FlagTreatmentDowngraded}},
		{name: "transfer with an instrument keeps transfer", treatment: dbx.RiskTreatmentTransfer,
			instrument: "POL-9", wantTreatment: "transfer"},
		{name: "transfer without an instrument is downgraded", treatment: dbx.RiskTreatmentTransfer,
			wantTreatment: "untreated", wantRequested: "transfer", wantFlags: []string{FlagTreatmentDowngraded}},
		{name: "accept is unchanged", treatment: dbx.RiskTreatmentAccept, wantTreatment: "accept"},
		{name: "avoid links its controls", treatment: dbx.RiskTreatmentAvoid, refs: []string{"AC-1"},
			wantTreatment: "avoid", wantLinks: []string{ctl.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row := BatchRow{InstrumentReference: tc.instrument}
			applyTreatmentRules(&row, tc.treatment, tc.refs, resolved)
			if row.Treatment != tc.wantTreatment || row.RequestedTreatment != tc.wantRequested ||
				!reflect.DeepEqual(row.LinkedControlIDs, tc.wantLinks) ||
				!reflect.DeepEqual(row.UnresolvedControls, tc.wantUnres) ||
				!reflect.DeepEqual(row.Flags, tc.wantFlags) {
				t.Errorf("row = %+v", row)
			}
		})
	}
}

func TestProposalOf(t *testing.T) {
	t.Parallel()
	until := time.Date(2027, 6, 30, 0, 0, 0, 0, time.UTC)
	if s, j, u := proposalOf(dbx.RiskTreatmentAccept, "why", &until); s != dbx.RiskAcceptanceStatusProposed || j != "why" || !u.Equal(until) {
		t.Errorf("accept = %q %q %v", s, j, u)
	}
	// A justification on a row that is not accepted is not sent.
	if s, j, u := proposalOf(dbx.RiskTreatmentMitigate, "why", &until); s != "" || j != "" || !u.IsZero() {
		t.Errorf("mitigate = %q %q %v", s, j, u)
	}
}
