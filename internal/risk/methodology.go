// Package risk implements the slice-019 Risk register: CRUD with pluggable
// methodology, per-methodology JSON Schema validation of inherent_score, and
// per-treatment required-field validation.
//
// The methodology default is `nist_800_30` (open question #04, resolved by
// slice 019's narrative). The platform supports five methodologies; each
// declares its own JSON Schema for `inherent_score` so risks across the
// register stay comparable within methodology and inspectable across.
package risk

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrInvalidMethodology is returned when the caller supplies a value outside
// the closed set in the dbx.RiskMethodology enum.
var ErrInvalidMethodology = errors.New("risk: methodology not in allowed set")

// ErrInherentScoreInvalid is returned when inherent_score fails its
// methodology's JSON Schema. The wrapping error message embeds the schema's
// failure message so the API's 400 response is actionable.
var ErrInherentScoreInvalid = errors.New("risk: inherent_score failed methodology schema")

// methodologySchemas maps each methodology to its compiled JSON Schema.
// The schemas are inlined here (not loaded from disk) because they are
// load-bearing for AC-2 and would otherwise drift from the spec; embedding
// them keeps the slice self-contained and easy to audit.
//
// nist_800_30 + qualitative_5x5 share the (likelihood, impact) 1..5 shape.
// fair uses loss_event_frequency + loss_magnitude and stores the derived
// annualized_loss_exposure (all numeric). The legacy lef/lm aliases are still
// accepted on input and normalised before persistence.
// cis_ram and iso_27005 accept a more permissive object until orgs that use
// them onboard — the v1 schema enforces only "type=object with at least one
// numeric field" so a placeholder cannot ship as `null`.
var methodologySchemas map[dbx.RiskMethodology]*jsonschema.Schema

func init() {
	methodologySchemas = map[dbx.RiskMethodology]*jsonschema.Schema{
		dbx.RiskMethodologyNist80030:      mustCompile(nist80030Schema),
		dbx.RiskMethodologyQualitative5x5: mustCompile(qualitative5x5Schema),
		dbx.RiskMethodologyFair:           mustCompile(fairSchema),
		dbx.RiskMethodologyCisRam:         mustCompile(permissiveSchema),
		dbx.RiskMethodologyIso27005:       mustCompile(permissiveSchema),
	}
}

const nist80030Schema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["likelihood", "impact"],
  "additionalProperties": true,
  "properties": {
    "likelihood": { "type": "integer", "minimum": 1, "maximum": 5 },
    "impact":     { "type": "integer", "minimum": 1, "maximum": 5 },
    "impact_dollars_low":  { "type": "number", "minimum": 0 },
    "impact_dollars_high": { "type": "number", "minimum": 0 }
  }
}`

const qualitative5x5Schema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["likelihood", "impact"],
  "additionalProperties": true,
  "properties": {
    "likelihood": { "type": "integer", "minimum": 1, "maximum": 5 },
    "impact":     { "type": "integer", "minimum": 1, "maximum": 5 }
  }
}`

const fairSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": true,
  "anyOf": [
    { "required": ["loss_event_frequency", "loss_magnitude"] },
    { "required": ["lef", "lm"] }
  ],
  "properties": {
    "loss_event_frequency": { "type": "number", "minimum": 0 },
    "loss_magnitude": { "type": "number", "minimum": 0 },
    "annualized_loss_exposure": { "type": "number", "minimum": 0 },
    "lef": { "type": "number", "minimum": 0 },
    "lm":  { "type": "number", "minimum": 0 }
  }
}`

// permissiveSchema is the v1 fallback for cis_ram + iso_27005: object with at
// least one numeric property. Tightens later if orgs onboard those methodologies.
const permissiveSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "minProperties": 1
}`

func mustCompile(raw string) *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		panic(fmt.Sprintf("risk: parse builtin schema: %v", err))
	}
	if err := c.AddResource("inline.json", doc); err != nil {
		panic(fmt.Sprintf("risk: add builtin schema: %v", err))
	}
	s, err := c.Compile("inline.json")
	if err != nil {
		panic(fmt.Sprintf("risk: compile builtin schema: %v", err))
	}
	return s
}

// ValidateInherentScore checks the supplied JSONB-encoded inherent_score
// against the schema registered for `methodology`. Returns
// ErrInvalidMethodology if the methodology is not in the enum,
// ErrInherentScoreInvalid (wrapped) if the score fails validation.
//
// Callers should treat any error as a 400. The application validation is the
// primary path; the DB has no equivalent CHECK because Postgres cannot evaluate
// JSON Schema natively.
func ValidateInherentScore(methodology dbx.RiskMethodology, inherentScore []byte) error {
	schema, ok := methodologySchemas[methodology]
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidMethodology, methodology)
	}
	if len(inherentScore) == 0 {
		return fmt.Errorf("%w: empty inherent_score", ErrInherentScoreInvalid)
	}
	var parsed any
	if err := json.Unmarshal(inherentScore, &parsed); err != nil {
		return fmt.Errorf("%w: %v", ErrInherentScoreInvalid, err)
	}
	if err := schema.Validate(parsed); err != nil {
		return fmt.Errorf("%w: %v", ErrInherentScoreInvalid, err)
	}
	if methodology == dbx.RiskMethodologyFair {
		if _, err := FairScoreFromJSON(inherentScore); err != nil {
			return fmt.Errorf("%w: %v", ErrInherentScoreInvalid, err)
		}
	}
	return nil
}

// FairScore is the first-class FAIR quantitative score shape stored in
// inherent_score and residual_score. AnnualizedLossExposure is derived as
// LossEventFrequency * LossMagnitude and represented in dollars per year.
type FairScore struct {
	LossEventFrequency     float64 `json:"loss_event_frequency"`
	LossMagnitude          float64 `json:"loss_magnitude"`
	AnnualizedLossExposure float64 `json:"annualized_loss_exposure"`
}

// FairScoreFromJSON parses either the canonical FAIR shape or the legacy
// {lef,lm} aliases and returns the canonical derived score.
func FairScoreFromJSON(raw []byte) (FairScore, error) {
	if len(raw) == 0 {
		return FairScore{}, errors.New("empty fair score")
	}
	var score struct {
		LossEventFrequency     *float64 `json:"loss_event_frequency"`
		LossMagnitude          *float64 `json:"loss_magnitude"`
		AnnualizedLossExposure *float64 `json:"annualized_loss_exposure"`
		LEF                    *float64 `json:"lef"`
		LM                     *float64 `json:"lm"`
	}
	if err := json.Unmarshal(raw, &score); err != nil {
		return FairScore{}, err
	}
	lef := score.LossEventFrequency
	if lef == nil {
		lef = score.LEF
	}
	lm := score.LossMagnitude
	if lm == nil {
		lm = score.LM
	}
	if lef == nil || lm == nil {
		return FairScore{}, errors.New("missing loss_event_frequency/loss_magnitude")
	}
	if !finiteNonNegative(*lef) || !finiteNonNegative(*lm) {
		return FairScore{}, errors.New("loss_event_frequency and loss_magnitude must be finite non-negative numbers")
	}
	ale := *lef * *lm
	if !finiteNonNegative(ale) {
		return FairScore{}, errors.New("annualized_loss_exposure is not finite")
	}
	if score.AnnualizedLossExposure != nil && math.Abs(*score.AnnualizedLossExposure-ale) > 0.01 {
		return FairScore{}, fmt.Errorf("annualized_loss_exposure must equal loss_event_frequency × loss_magnitude (got %.2f, want %.2f)", *score.AnnualizedLossExposure, ale)
	}
	return FairScore{
		LossEventFrequency:     *lef,
		LossMagnitude:          *lm,
		AnnualizedLossExposure: ale,
	}, nil
}

// NormalizeFairScoreJSON returns the canonical persisted FAIR score shape.
func NormalizeFairScoreJSON(raw []byte) ([]byte, error) {
	score, err := FairScoreFromJSON(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(score)
}

func finiteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// AllowedMethodologies returns the closed set of methodology values the
// platform recognizes. Useful for the OpenAPI generator and the frontend's
// dropdown.
func AllowedMethodologies() []dbx.RiskMethodology {
	return []dbx.RiskMethodology{
		dbx.RiskMethodologyNist80030,
		dbx.RiskMethodologyFair,
		dbx.RiskMethodologyCisRam,
		dbx.RiskMethodologyIso27005,
		dbx.RiskMethodologyQualitative5x5,
	}
}

// DefaultMethodology is the per-risk default if the caller omits the field.
// Locked by open question #04 (resolved by slice 019).
const DefaultMethodology = dbx.RiskMethodologyNist80030
