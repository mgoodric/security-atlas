package risk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
	if unscoredScore(residual) {
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

// PrepareCreateScores validates and normalises a new risk's inherent and
// residual scores. It is the one score rule for every create path:
// POST /v1/risks (Store.Create) and the MCP create_risk applier (#2502).
//
// The inherent score must pass its methodology's schema. A FAIR score is
// normalised to its canonical shape, and an empty FAIR residual takes the
// normalised inherent. A non-FAIR score's fields must then be JSON numbers
// in range (ValidateScoreNumbers). The returned blobs are what to persist.
//
// An omitted inherent score (empty, `null` or `{}`) is an unscored risk,
// logged before it is assessed (#2511): both blobs are stored as `{}` and
// read as Unscored. The methodology is still checked, and a residual
// without an inherent score is refused.
func PrepareCreateScores(methodology dbx.RiskMethodology, inherent, residual []byte) ([]byte, []byte, error) {
	if unscoredScore(inherent) {
		if !slices.Contains(AllowedMethodologies(), methodology) {
			return nil, nil, fmt.Errorf("%w: %q", ErrInvalidMethodology, methodology)
		}
		if !unscoredScore(residual) {
			return nil, nil, fmt.Errorf("%w: residual_score requires an inherent_score", ErrInherentScoreInvalid)
		}
		return []byte(`{}`), []byte(`{}`), nil
	}
	if err := ValidateInherentScore(methodology, inherent); err != nil {
		return nil, nil, err
	}
	if methodology == dbx.RiskMethodologyFair {
		normalized, err := NormalizeFairScoreJSON(inherent)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInherentScoreInvalid, err)
		}
		inherent = normalized
		if len(residual) == 0 {
			residual = normalized
		} else {
			normalizedResidual, err := NormalizeFairScoreJSON(residual)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: residual_score: %v", ErrInherentScoreInvalid, err)
			}
			residual = normalizedResidual
		}
	}
	if err := ValidateScoreNumbers(methodology, inherent, residual); err != nil {
		return nil, nil, err
	}
	return inherent, residual, nil
}

// jsonKind names the JSON type of a score field that is not a number in
// range, so a refusal says what was sent without echoing a value of any
// size back to the caller (#2502).
func jsonKind(v json.RawMessage) string {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return "nothing"
	}
	switch t[0] {
	case '"':
		return "a string"
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	default:
		return "a malformed number"
	}
}

// unscoredScore reports whether a score blob is unscored: empty, `null`
// or `{}`.
func unscoredScore(raw []byte) bool {
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
			return fmt.Errorf("%w: %s.%s must be a JSON number, got %s", ErrScoreNotNumeric, name, f.key, jsonKind(v))
		}
		if n < f.lo || n > f.hi {
			return fmt.Errorf("%w: %s.%s must be between %g and %g, got %g", ErrScoreNotNumeric, name, f.key, f.lo, f.hi, n)
		}
	}
	return nil
}
