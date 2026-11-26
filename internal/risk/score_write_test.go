package risk

import (
	"errors"
	"strings"
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

// #2502: a refused score field names its JSON type; the value itself, of
// any size, is never echoed back.
func TestValidateScoreNumbers_NamesTypeNotValue(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("9", 4096)
	cases := []struct{ value, want string }{
		{`"` + huge + `"`, "got a string"},
		{`{"a":"` + huge + `"}`, "got an object"},
		{`["` + huge + `"]`, "got an array"},
		{`true`, "got a boolean"},
		{`null`, "got null"},
	}
	for _, tc := range cases {
		err := ValidateScoreNumbers(dbx.RiskMethodologyCisRam, []byte(`{"likelihood":2,"impact":3,"severity":`+tc.value+`}`), nil)
		if !errors.Is(err, ErrScoreNotNumeric) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%.20s: err = %v, want %q", tc.value, err, tc.want)
		}
		if strings.Contains(err.Error(), huge) {
			t.Fatalf("%.20s: error echoes the value", tc.value)
		}
	}
}

// #2502: PrepareCreateScores is the create rule Store.Create and the MCP
// create_risk applier share. FAIR is normalised and its empty residual
// takes the inherent score; a non-FAIR residual is left as given.
func TestPrepareCreateScores(t *testing.T) {
	t.Parallel()
	inh, res, err := PrepareCreateScores(dbx.RiskMethodologyFair, []byte(`{"lef":2,"lm":1000}`), nil)
	if err != nil || string(inh) != string(res) || !strings.Contains(string(inh), `"loss_event_frequency":2`) {
		t.Fatalf("fair: %s / %s / %v", inh, res, err)
	}
	if _, _, err := PrepareCreateScores(dbx.RiskMethodologyFair, []byte(`{"lef":2,"lm":1000}`), []byte(`{"lef":"x"}`)); !errors.Is(err, ErrInherentScoreInvalid) {
		t.Fatalf("fair bad residual: %v", err)
	}
	inh, res, err = PrepareCreateScores(dbx.RiskMethodologyNist80030, []byte(`{"likelihood":4,"impact":5}`), nil)
	if err != nil || string(inh) != `{"likelihood":4,"impact":5}` || res != nil {
		t.Fatalf("nist: %s / %s / %v", inh, res, err)
	}
	if _, _, err := PrepareCreateScores(dbx.RiskMethodologyNist80030, []byte(`{"likelihood":4,"impact":5,"score":"20"}`), nil); !errors.Is(err, ErrScoreNotNumeric) {
		t.Fatalf("nist string score: %v", err)
	}
}

// #2511: an omitted inherent score is an unscored risk, stored as {} / {}
// on every create path. The methodology is still checked, and a residual
// without an inherent score is refused.
func TestPrepareCreateScores_Unscored(t *testing.T) {
	t.Parallel()
	for _, m := range []dbx.RiskMethodology{dbx.RiskMethodologyNist80030, dbx.RiskMethodologyFair} {
		for _, omitted := range []string{"", "null", "{}", " {} "} {
			inh, res, err := PrepareCreateScores(m, []byte(omitted), nil)
			if err != nil || string(inh) != `{}` || string(res) != `{}` {
				t.Fatalf("%s %q: %s / %s / %v; want {} / {} / nil", m, omitted, inh, res, err)
			}
			if _, _, err := PrepareCreateScores(m, []byte(omitted), []byte(`{}`)); err != nil {
				t.Fatalf("%s %q with {} residual: %v", m, omitted, err)
			}
		}
	}
	if _, _, err := PrepareCreateScores("octave", nil, nil); !errors.Is(err, ErrInvalidMethodology) {
		t.Fatalf("unknown methodology: %v", err)
	}
	if _, _, err := PrepareCreateScores(dbx.RiskMethodologyNist80030, nil, []byte(`{"score":12}`)); !errors.Is(err, ErrInherentScoreInvalid) {
		t.Fatalf("residual without inherent: %v", err)
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
