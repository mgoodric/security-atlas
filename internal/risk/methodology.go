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
// fair uses LEF + LM (loss-event frequency and loss magnitude, both numeric).
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
  "required": ["lef", "lm"],
  "additionalProperties": true,
  "properties": {
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
	return nil
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
