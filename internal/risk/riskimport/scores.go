package riskimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// A batch declares one scoring methodology (spec #2812, Q16; #2841). Every
// row's inherent and residual scores are read from its columns in the shape
// that methodology already validates on a single create: likelihood and
// impact for qualitative_5x5 and nist_800_30, loss event frequency and loss
// magnitude for fair. A row with no score columns filled imports unscored.

// ErrInvalidMethodology is an upload naming a methodology the import does
// not read scores for.
var ErrInvalidMethodology = errors.New("riskimport: invalid methodology")

// Score fields. Like the other fields they are column-map keys; each one
// applies only to the methodologies whose shape it belongs to.
const (
	FieldInherentLikelihood         = "inherent_likelihood"
	FieldInherentImpact             = "inherent_impact"
	FieldResidualLikelihood         = "residual_likelihood"
	FieldResidualImpact             = "residual_impact"
	FieldInherentLossEventFrequency = "inherent_loss_event_frequency"
	FieldInherentLossMagnitude      = "inherent_loss_magnitude"
	FieldResidualLossEventFrequency = "residual_loss_event_frequency"
	FieldResidualLossMagnitude      = "residual_loss_magnitude"
)

// FieldInherentScore and FieldResidualScore are whole scores as JSON, the
// shape the risk export writes them in (#2898). They apply to every
// methodology: the JSON is put through the batch methodology's create
// rule, so a score of another shape is rejected rather than read.
const (
	FieldInherentScore = "inherent_score"
	FieldResidualScore = "residual_score"
)

// RejectInvalidScore is a row whose score cells do not make a score its
// batch's methodology accepts.
const RejectInvalidScore = "invalid_score"

// maxQuotedCell caps how much of a cell a rejection detail repeats.
const maxQuotedCell = 40

// ImportMethodologies are the methodologies an import batch may declare.
// cis_ram and iso_27005 have no defined score shape to read columns into,
// so they are not offered.
func ImportMethodologies() []dbx.RiskMethodology {
	return []dbx.RiskMethodology{
		dbx.RiskMethodologyNist80030,
		dbx.RiskMethodologyQualitative5x5,
		dbx.RiskMethodologyFair,
	}
}

// ResolveMethodology returns the batch methodology for an upload's value:
// the platform default (risk.DefaultMethodology) when empty, as on a single
// create, and ErrInvalidMethodology for one the import does not offer.
func ResolveMethodology(raw string) (dbx.RiskMethodology, error) {
	m := dbx.RiskMethodology(strings.TrimSpace(raw))
	if m == "" {
		return risk.DefaultMethodology, nil
	}
	if !slices.Contains(ImportMethodologies(), m) {
		names := make([]string, 0, len(ImportMethodologies()))
		for _, v := range ImportMethodologies() {
			names = append(names, string(v))
		}
		return "", fmt.Errorf("%w: %q (want one of %s)", ErrInvalidMethodology, raw, strings.Join(names, ", "))
	}
	return m, nil
}

// scoreShape is how one methodology's scores are read from a row.
type scoreShape struct {
	inherent [2]string // the inherent pair's fields
	residual [2]string // the residual pair's fields
	keys     [2]string // the JSON keys each pair is stored under
	want     string    // what a cell must hold, for a rejection detail
	cell     func(raw string) (any, bool)
}

var gridShape = scoreShape{
	inherent: [2]string{FieldInherentLikelihood, FieldInherentImpact},
	residual: [2]string{FieldResidualLikelihood, FieldResidualImpact},
	keys:     [2]string{"likelihood", "impact"},
	want:     "a whole number from 1 to 5",
	cell:     gridCell,
}

var fairShape = scoreShape{
	inherent: [2]string{FieldInherentLossEventFrequency, FieldInherentLossMagnitude},
	residual: [2]string{FieldResidualLossEventFrequency, FieldResidualLossMagnitude},
	keys:     [2]string{"loss_event_frequency", "loss_magnitude"},
	want:     "a plain non-negative number such as 0.5 or 250000",
	cell:     fairCell,
}

func shapeFor(m dbx.RiskMethodology) scoreShape {
	if m == dbx.RiskMethodologyFair {
		return fairShape
	}
	return gridShape
}

// scoreFieldApplies reports whether field is read under methodology m. A
// field that is not a score field applies to every methodology.
func scoreFieldApplies(field string, m dbx.RiskMethodology) bool {
	if !isScoreField(field) {
		return true
	}
	s := shapeFor(m)
	return slices.Contains(s.inherent[:], field) || slices.Contains(s.residual[:], field)
}

func isScoreField(field string) bool {
	for _, s := range []scoreShape{gridShape, fairShape} {
		if slices.Contains(s.inherent[:], field) || slices.Contains(s.residual[:], field) {
			return true
		}
	}
	return false
}

func gridCell(raw string) (any, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 5 {
		return nil, false
	}
	return n, true
}

// fairCell reads a plain decimal number. A currency sign or a thousands
// separator is refused rather than guessed at: "1,5" is 1.5 in some locales
// and 15 in others.
func fairCell(raw string) (any, bool) {
	if strings.ContainsAny(raw, "xXpP_") {
		return nil, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return nil, false
	}
	return f, true
}

// scoresFromCells builds a row's inherent and residual scores from its score
// cells. Both are nil for an unscored row. Each score is given either as a
// pair of cells or as one JSON cell, the export's shape (#2898), not both.
// A pair is given whole or not at all, and a residual needs an inherent, as
// on a single create. The scores are then put through
// risk.PrepareCreateScores, the create path's own rule, so a staged score is
// one approval will accept, and what is staged is what the risk will hold (a
// FAIR score with its derived annualized loss exposure). A JSON score of
// another methodology's shape fails that rule. ok false carries the
// rejection detail.
func scoresFromCells(m dbx.RiskMethodology, cells map[string]string) (inherent, residual json.RawMessage, detail string, ok bool) {
	shape := shapeFor(m)
	inhJSON, detail, ok := scoreJSON(shape, shape.inherent, FieldInherentScore, cells)
	if !ok {
		return nil, nil, detail, false
	}
	resJSON, detail, ok := scoreJSON(shape, shape.residual, FieldResidualScore, cells)
	if !ok {
		return nil, nil, detail, false
	}
	if inhJSON == nil && resJSON != nil {
		return nil, nil, fmt.Sprintf("residual scores need inherent scores: give %s and %s, or %s, too",
			fieldLabel(shape.inherent[0]), fieldLabel(shape.inherent[1]), fieldLabel(FieldInherentScore)), false
	}
	if inhJSON == nil {
		return nil, nil, "", true
	}
	prepInh, prepRes, err := risk.PrepareCreateScores(m, inhJSON, resJSON)
	if err != nil {
		return nil, nil, strings.TrimPrefix(err.Error(), "risk: "), false
	}
	return json.RawMessage(prepInh), scoredOrNil(prepRes), "", true
}

// scoreJSON reads one score, from its pair of cells or its JSON cell, as
// JSON; nil is no score. A JSON cell holding an unscored blob (null or {},
// as the export writes an unscored risk) is no score.
func scoreJSON(shape scoreShape, fields [2]string, jsonField string, cells map[string]string) ([]byte, string, bool) {
	pair, detail, ok := scorePair(shape, fields, cells)
	if !ok {
		return nil, detail, false
	}
	raw := cells[jsonField]
	if raw != "" && pair != nil {
		return nil, fmt.Sprintf("give the score as %s or as %s and %s, not both",
			fieldLabel(jsonField), fieldLabel(fields[0]), fieldLabel(fields[1])), false
	}
	if raw != "" {
		if scoredOrNil(json.RawMessage(raw)) == nil {
			return nil, "", true
		}
		return []byte(raw), "", true
	}
	if pair == nil {
		return nil, "", true
	}
	out, err := json.Marshal(pair)
	if err != nil {
		return nil, fieldLabel(jsonField) + " could not be encoded", false
	}
	return out, "", true
}

// scorePair reads one pair of cells into {key: value}. Both blank is nil
// (no score); one blank, or a cell that is not a number in range, is a
// rejection.
func scorePair(shape scoreShape, fields [2]string, cells map[string]string) (map[string]any, string, bool) {
	a, b := cells[fields[0]], cells[fields[1]]
	if a == "" && b == "" {
		return nil, "", true
	}
	for i, v := range []string{a, b} {
		if v == "" {
			return nil, fmt.Sprintf("%s is missing: give both %s and %s, or neither",
				fieldLabel(fields[i]), fieldLabel(fields[0]), fieldLabel(fields[1])), false
		}
	}
	out := make(map[string]any, 2)
	for i, v := range []string{a, b} {
		n, ok := shape.cell(v)
		if !ok {
			return nil, fmt.Sprintf("%s %s is not %s", fieldLabel(fields[i]), quoteCell(v), shape.want), false
		}
		out[shape.keys[i]] = n
	}
	return out, "", true
}

// scoredOrNil maps the create path's unscored residual (empty or {}) to nil.
func scoredOrNil(raw []byte) json.RawMessage {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "{}" || t == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func fieldLabel(field string) string { return strings.ReplaceAll(field, "_", " ") }

func quoteCell(v string) string {
	r := []rune(v)
	if len(r) > maxQuotedCell {
		return strconv.Quote(string(r[:maxQuotedCell]) + "…")
	}
	return strconv.Quote(v)
}
