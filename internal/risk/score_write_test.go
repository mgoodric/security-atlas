package risk

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2490: a non-FAIR score field is a JSON number in range, or the write is
// refused. FAIR is left to its own validation.
func TestValidateScoreNumbers(t *testing.T) {
	t.Parallel()
	nist := dbx.RiskMethodologyNist80030
	cases := []struct {
		name        string
		methodology dbx.RiskMethodology
		inherent    string
		residual    string
		wantErr     bool
	}{
		{"5x5 with no residual", nist, `{"likelihood":4,"impact":5}`, ``, false},
		{"residual null is unscored", nist, `{"likelihood":4,"impact":5}`, `null`, false},
		{"residual {} is unscored", nist, `{"likelihood":4,"impact":5}`, `{}`, false},
		{"residual score", nist, `{"likelihood":4,"impact":5}`, `{"score":12.5}`, false},
		{"residual score 0", nist, `{"likelihood":4,"impact":5}`, `{"score":0}`, false},
		{"residual severity 25", nist, `{"likelihood":4,"impact":5}`, `{"severity":25}`, false},
		{"residual likelihood x impact", nist, `{"likelihood":4,"impact":5}`, `{"likelihood":2,"impact":3}`, false},
		{"residual numeric string", nist, `{"likelihood":4,"impact":5}`, `{"score":"20"}`, true},
		{"residual severity string", nist, `{"likelihood":4,"impact":5}`, `{"severity":"20"}`, true},
		{"residual likelihood string", nist, `{"likelihood":4,"impact":5}`, `{"likelihood":"2","impact":3}`, true},
		{"residual score null", nist, `{"likelihood":4,"impact":5}`, `{"score":null}`, true},
		{"residual score over 25", nist, `{"likelihood":4,"impact":5}`, `{"score":26}`, true},
		{"residual score negative", nist, `{"likelihood":4,"impact":5}`, `{"score":-1}`, true},
		{"residual impact 6", nist, `{"likelihood":4,"impact":5}`, `{"likelihood":2,"impact":6}`, true},
		{"residual bare number", nist, `{"likelihood":4,"impact":5}`, `20`, true},
		{"residual with no score field", nist, `{"likelihood":4,"impact":5}`, `{"note":"x"}`, true},
		{"residual likelihood without impact", nist, `{"likelihood":4,"impact":5}`, `{"likelihood":2}`, true},
		{"inherent severity string", nist, `{"likelihood":4,"impact":5,"severity":"20"}`, ``, true},
		{"cis_ram inherent likelihood string", dbx.RiskMethodologyCisRam, `{"likelihood":"4","impact":5}`, ``, true},
		{"cis_ram inherent other fields", dbx.RiskMethodologyCisRam, `{"rating":"high"}`, ``, false},
		{"fair is not checked here", dbx.RiskMethodologyFair, `{"loss_event_frequency":2,"loss_magnitude":1000}`, `{"score":"20"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateScoreNumbers(tc.methodology, []byte(tc.inherent), []byte(tc.residual))
			if tc.wantErr != (err != nil) {
				t.Fatalf("ValidateScoreNumbers() err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrScoreNotNumeric) {
				t.Fatalf("err = %v, want ErrScoreNotNumeric", err)
			}
		})
	}
}

// #2490: the ?severity_band= ranges are SeverityBand's, so a risk the
// filter puts in a band shows that band.
func TestSeverityBandRangeMatchesSeverityBand(t *testing.T) {
	t.Parallel()
	for _, band := range []string{"high", "medium", "low"} {
		lo, hi, ok := SeverityBandRange(band)
		if !ok {
			t.Fatalf("SeverityBandRange(%q) not ok", band)
		}
		top := 100
		if hi != nil {
			top = *hi
		}
		for sev := 0; sev <= 100; sev++ {
			in := sev >= lo && sev <= top
			want := map[string]string{"high": "High", "medium": "Medium", "low": "Low"}[band]
			if got := SeverityBand(float64(sev)) == want; got != in {
				t.Fatalf("band %q, severity %d: in range %v, SeverityBand %q", band, sev, in, SeverityBand(float64(sev)))
			}
		}
	}
	if _, _, ok := SeverityBandRange("critical"); ok {
		t.Fatal("SeverityBandRange(critical) ok, want refused")
	}
}

func TestPageFilterSeverityBounds(t *testing.T) {
	t.Parallel()
	lo, hi, err := PageFilter{SeverityBand: "high"}.severityBounds()
	if err != nil || lo == nil || *lo != SeverityHighMin || hi != nil {
		t.Fatalf("high: lo=%v hi=%v err=%v", lo, hi, err)
	}
	lo, hi, err = PageFilter{SeverityBand: "medium"}.severityBounds()
	if err != nil || *lo != SeverityMediumMin || hi == nil || *hi != SeverityHighMin-1 {
		t.Fatalf("medium: lo=%v hi=%v err=%v", lo, hi, err)
	}
	lo, hi, err = PageFilter{}.severityBounds()
	if err != nil || lo != nil || hi != nil {
		t.Fatalf("off: lo=%v hi=%v err=%v", lo, hi, err)
	}
	if _, _, err := (PageFilter{SeverityBand: "x"}).severityBounds(); !errors.Is(err, ErrInvalidSeverityBand) {
		t.Fatalf("x: err=%v, want ErrInvalidSeverityBand", err)
	}
}
