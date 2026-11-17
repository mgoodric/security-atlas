package risk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrScoreNotNumeric is returned when a non-FAIR inherent_score or
// residual_score carries a score field that is not a JSON number in its
// range (#2490). The HTTP layer renders it as a 422.
//
// The readers (ScoreValue, AssessedSeverity and the register's SQL) read
// only JSON numbers, so a stored {"score": "20"} is read as unscored. This
// refusal keeps that shape out rather than teaching every reader a second
// rule.
var ErrScoreNotNumeric = errors.New("risk: score field must be a JSON number in range")

// scoreFieldRanges are the score fields a reader takes a number from, with
// the range each may hold: `score` (the residual deriver's) and `severity`
// on the 0..25 scale, `likelihood` and `impact` on the 1..5 grid.
var scoreFieldRanges = []struct {
	key    string
	lo, hi float64
}{
	{"score", 0, SeverityScaleMax},
	{"severity", 0, SeverityScaleMax},
	{"likelihood", 1, 5},
	{"impact", 1, 5},
}

// ValidateScoreNumbers checks the score fields of a non-FAIR risk's
// inherent and residual scores on write (#2490). FAIR keeps its own
// validation (FairScoreFromJSON) and is not checked here.
//
// Every score field present in either blob must be a JSON number in its
// range. The residual may be empty, `null` or `{}`, which is unscored (the
// platform's default); any other residual must be an object that carries a
// value ScoreValue reads, so a residual is either unscored or scored, never
// a blob that looks scored and reads as unscored.
func ValidateScoreNumbers(methodology dbx.RiskMethodology, inherent, residual []byte) error {
	if Monetary(methodology) {
		return nil
	}
	if err := checkScoreFields("inherent_score", inherent); err != nil {
		return err
	}
	if unscoredResidual(residual) {
		return nil
	}
	if err := checkScoreFields("residual_score", residual); err != nil {
		return err
	}
	if _, ok := ScoreValue(residual); !ok {
		return fmt.Errorf("%w: residual_score must carry score, severity, or likelihood and impact", ErrScoreNotNumeric)
	}
	return nil
}

func unscoredResidual(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null")) || bytes.Equal(t, []byte("{}"))
}

// checkScoreFields refuses a blob that is not an object, or whose score
// fields are not numbers in range. An empty blob has no fields to check;
// the methodology schema decides whether it may be empty.
func checkScoreFields(name string, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("%w: %s must be a JSON object", ErrScoreNotNumeric, name)
	}
	for _, f := range scoreFieldRanges {
		v, present := m[f.key]
		if !present {
			continue
		}
		var n float64
		if err := json.Unmarshal(v, &n); err != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("%w: %s.%s must be a JSON number, got %s", ErrScoreNotNumeric, name, f.key, v)
		}
		if n < f.lo || n > f.hi {
			return fmt.Errorf("%w: %s.%s must be between %g and %g, got %g", ErrScoreNotNumeric, name, f.key, f.lo, f.hi, n)
		}
	}
	return nil
}
