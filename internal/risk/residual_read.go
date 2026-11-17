package risk

import (
	"encoding/json"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ScoreValue is the one reader for the scalar in a risk's residual_score or
// inherent_score JSONB (#2185). The blob is methodology-dependent and the
// platform writes three shapes, read in this order:
//
//   - the residual deriver's `score` (residualBlob, slice 020);
//   - a precomputed `severity` (slice 053 aggregated parents);
//   - a 5x5 {likelihood, impact}, whose scalar is their product
//     (canvas §2.2).
//
// ok is false when no shape carries a number: a malformed or empty blob.
// {score: 0} (fully effective controls) is ok with value 0.
//
// The value's unit depends on the methodology: on a FAIR risk the deriver's
// `score` is a dollar amount, not a 1..25 severity. Read the methodology
// with Monetary before comparing or banding the value.
func ScoreValue(raw []byte) (value float64, ok bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	if v, ok := numField(m, "score"); ok {
		return v, true
	}
	if v, ok := numField(m, "severity"); ok {
		return v, true
	}
	l, lok := numField(m, "likelihood")
	i, iok := numField(m, "impact")
	if lok && iok {
		return l * i, true
	}
	return 0, false
}

// Monetary reports whether a methodology's score scalar is a dollar amount
// rather than a 1..25 severity. Only FAIR is: its residual is an annualized
// loss exposure, which has no Low/Medium/High band and is not comparable
// with a 5x5 severity (OPENENGINE-596: FAIR is not normalized into the 5x5
// rollup).
func Monetary(m dbx.RiskMethodology) bool {
	return m == dbx.RiskMethodologyFair
}

// SeverityHighMin and SeverityMediumMin are the lowest severities of the
// High and Medium bands. SeverityBand and the register's ?severity_band=
// filter (SeverityBandRange) both read them, so the sidebar's
// high-severity count and the band a risk shows cannot disagree (#2490).
const (
	SeverityHighMin   = 15
	SeverityMediumMin = 8
)

// SeverityBandRange is the inclusive severity range of a band named by the
// register's ?severity_band= (high, medium or low); hi is nil for High,
// which has no top, as SeverityBand has none. ok is false for any other
// name. Low starts at 1: the filtered severity is AssessedSeverity, a whole
// number, and 0 is None.
func SeverityBandRange(band string) (lo int, hi *int, ok bool) {
	top := func(n int) *int { return &n }
	switch band {
	case "high":
		return SeverityHighMin, nil, true
	case "medium":
		return SeverityMediumMin, top(SeverityHighMin - 1), true
	case "low":
		return 1, top(SeverityMediumMin - 1), true
	}
	return 0, nil, false
}

// SeverityBand is the qualitative band of a 1..25 severity: Low up to 7,
// Medium 8-14, High 15-25. Any value above 0 is at least Low, so a residual
// of 0.4 (strong controls on a low inherent) is Low, not None. Zero or less
// is None. Never band a Monetary value.
func SeverityBand(severity float64) string {
	switch {
	case severity >= SeverityHighMin:
		return "High"
	case severity >= SeverityMediumMin:
		return "Medium"
	case severity > 0:
		return "Low"
	default:
		return "None"
	}
}

// Rank is one risk's place in a "top risks" order: its residual value and
// the tier the value is ranked in. Unscored means the risk has no readable
// score; each caller decides which scores it reads (#2516).
type Rank struct {
	Unscored bool
	Monetary bool
	Value    float64
}

// Tier is the group a Rank is ordered in: 0 for a scored 5x5 severity, 1 for
// a scored FAIR dollar amount, 2 for an unscored risk of any methodology.
// Tiers never flip with the sort direction; only the value within a tier
// does. The register's residual ORDER BY (ListRisksPage) mirrors it.
func (r Rank) Tier() int {
	switch {
	case r.Unscored:
		return 2
	case r.Monetary:
		return 1
	default:
		return 0
	}
}

// RanksAbove orders two risks for a "top risks" list, highest first.
// 5x5 severities and FAIR dollar amounts are ranked separately, not on one
// scale: every severity-scored risk ranks above every Monetary one, and
// within each group the higher value ranks first. So a FAIR risk's dollar
// figure never outranks a 1..25 severity by raw number (#2185). Unscored
// risks rank after every scored one, FAIR included, and their Value is
// ignored (#2516). It returns false when the two are tied, so callers apply
// their own tiebreak.
func RanksAbove(a, b Rank) bool {
	if at, bt := a.Tier(), b.Tier(); at != bt {
		return at < bt
	}
	if a.Unscored {
		return false
	}
	return a.Value > b.Value
}
