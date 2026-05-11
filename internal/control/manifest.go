// Package control implements the slice-009 control-as-code bundle format:
// the YAML manifest parser, the tarball handler, and the upload Store that
// persists the parsed bundle as a new version row in `controls`.
//
// The package's public surface is intentionally small:
//
//   - Manifest: the parsed YAML, with no DB or transport concern.
//   - ParseDirectory / ParseTarball: turn bytes into a Manifest.
//   - Validate: structural + cross-field checks that do not require a DB.
//   - Store.Upload: persist a Manifest as a new control row (initial or
//     supersession).
//
// Execution of evidence queries lives in slice 012 — slice 009 only STORES
// queries verbatim. The OSCAL component-definition path is out of scope for
// slice 009 (canvas v2 work).
package control

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// BundleSchemaVersion is the only accepted manifest schema version in slice
// 009. Forward-compat additions to the manifest stay within "1". Breaking
// changes will bump to "2" and the parser will reject older bundles.
const BundleSchemaVersion = "1"

// Manifest is the parsed control.yaml. Field types are deliberately concrete
// (no `any` blobs) so the validator can give field-pointing error messages.
//
// JSON tags drive both YAML parsing (gopkg.in/yaml.v3 honours JSON tags via
// the inline option) and the JSON wire shape on POST /v1/controls:upload-bundle.
type Manifest struct {
	BundleSchemaVersion string `yaml:"bundle_schema_version" json:"bundle_schema_version"`
	BundleID            string `yaml:"bundle_id"              json:"bundle_id"`
	Title               string `yaml:"title"                  json:"title"`
	Description         string `yaml:"description,omitempty"  json:"description,omitempty"`
	SCFAnchorID         string `yaml:"scf_anchor_id"          json:"scf_anchor_id"`
	ControlFamily       string `yaml:"control_family,omitempty" json:"control_family,omitempty"`
	ImplementationType  string `yaml:"implementation_type"    json:"implementation_type"`
	OwnerRole           string `yaml:"owner_role,omitempty"   json:"owner_role,omitempty"`
	LifecycleState      string `yaml:"lifecycle_state,omitempty" json:"lifecycle_state,omitempty"`
	FreshnessClass      string `yaml:"freshness_class,omitempty" json:"freshness_class,omitempty"`

	// ApplicabilityExpr is a JSON-AST scope predicate (slice 017 shape). The
	// parser decodes the YAML mapping into a generic interface and re-marshals
	// to JSON bytes so the slice-017 validator (Evaluate / validate node) can
	// consume it without a YAML dependency.
	ApplicabilityExpr map[string]any `yaml:"applicability_expr,omitempty" json:"applicability_expr,omitempty"`

	LinkedPolicyIDs      []string        `yaml:"linked_policy_ids,omitempty" json:"linked_policy_ids,omitempty"`
	EvidenceQueries      []EvidenceQuery `yaml:"evidence_queries,omitempty"  json:"evidence_queries,omitempty"`
	ManualEvidenceSchema map[string]any  `yaml:"manual_evidence_schema,omitempty" json:"manual_evidence_schema,omitempty"`
}

// EvidenceQuery is one entry in `evidence_queries[]`. Slice 009 stores these
// verbatim; slice 012 will dispatch on Language to invoke the appropriate
// evaluator (OPA for rego, sqlc-shaped reader for sql, jsonpath lib for
// jsonpath).
type EvidenceQuery struct {
	ID           string `yaml:"id"                       json:"id"`
	Language     string `yaml:"language"                 json:"language"`
	Expression   string `yaml:"expression"               json:"expression"`
	EvidenceKind string `yaml:"evidence_kind,omitempty"  json:"evidence_kind,omitempty"`
	Description  string `yaml:"description,omitempty"    json:"description,omitempty"`
}

// allowedImplementationTypes lists the four lifecycle types from canvas §2.1.
// We mirror them as application-level strings even though the DB enum already
// constrains them; failing at parse time (vs INSERT time) yields a clearer
// error pointing at the YAML field.
var allowedImplementationTypes = map[string]struct{}{
	"automated":       {},
	"semi_automated":  {},
	"manual_attested": {},
	"manual_periodic": {},
}

var allowedLifecycleStates = map[string]struct{}{
	"draft":      {},
	"proposed":   {},
	"active":     {},
	"deprecated": {},
	"retired":    {},
}

var allowedFreshnessClasses = map[string]struct{}{
	"realtime":  {},
	"hourly":    {},
	"daily":     {},
	"weekly":    {},
	"monthly":   {},
	"quarterly": {},
	"annual":    {},
}

var allowedQueryLanguages = map[string]struct{}{
	"rego":     {},
	"sql":      {},
	"jsonpath": {},
}

var (
	bundleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)
	queryIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,63}$`)
)

// ValidateStructural runs every check that does not require a database call:
// shape, required fields, enum membership, applicability_expr well-formedness.
// SCF-anchor existence + evidence_kind registration are deferred to Store.Upload
// because they need DB / registry access.
//
// Errors include the path to the offending field so the CLI can point the
// author straight at it (AC-2: clear schema error reporting).
func (m *Manifest) ValidateStructural() error {
	if m.BundleSchemaVersion == "" {
		return fmt.Errorf("control bundle: bundle_schema_version is required")
	}
	if m.BundleSchemaVersion != BundleSchemaVersion {
		return fmt.Errorf("control bundle: unsupported bundle_schema_version %q (only %q is supported)", m.BundleSchemaVersion, BundleSchemaVersion)
	}

	if m.BundleID == "" {
		return fmt.Errorf("control bundle: bundle_id is required")
	}
	if !bundleIDPattern.MatchString(m.BundleID) {
		return fmt.Errorf("control bundle: bundle_id %q must match pattern %s", m.BundleID, bundleIDPattern.String())
	}

	if m.Title == "" {
		return fmt.Errorf("control bundle: title is required")
	}
	if len(m.Title) > 200 {
		return fmt.Errorf("control bundle: title exceeds 200 characters")
	}

	// AC-4: missing required metadata (scf_anchor_id is the canonical
	// example) must reject with an error pointing at the field.
	if m.SCFAnchorID == "" {
		return fmt.Errorf("control bundle: scf_anchor_id is required (canvas invariant 7: every control must be anchored)")
	}

	if m.ImplementationType == "" {
		return fmt.Errorf("control bundle: implementation_type is required")
	}
	if _, ok := allowedImplementationTypes[m.ImplementationType]; !ok {
		return fmt.Errorf("control bundle: implementation_type %q is not one of automated|semi_automated|manual_attested|manual_periodic", m.ImplementationType)
	}

	if m.LifecycleState != "" {
		if _, ok := allowedLifecycleStates[m.LifecycleState]; !ok {
			return fmt.Errorf("control bundle: lifecycle_state %q is not one of draft|proposed|active|deprecated|retired", m.LifecycleState)
		}
	}

	if m.FreshnessClass != "" {
		if _, ok := allowedFreshnessClasses[m.FreshnessClass]; !ok {
			return fmt.Errorf("control bundle: freshness_class %q is not a known class", m.FreshnessClass)
		}
	}

	// Evidence queries — every entry must have id+language+expression and
	// no two entries may share an id.
	seen := make(map[string]struct{}, len(m.EvidenceQueries))
	for i, q := range m.EvidenceQueries {
		if q.ID == "" {
			return fmt.Errorf("control bundle: evidence_queries[%d].id is required", i)
		}
		if !queryIDPattern.MatchString(q.ID) {
			return fmt.Errorf("control bundle: evidence_queries[%d].id %q must match %s", i, q.ID, queryIDPattern.String())
		}
		if _, dup := seen[q.ID]; dup {
			return fmt.Errorf("control bundle: evidence_queries[%d].id %q is duplicated", i, q.ID)
		}
		seen[q.ID] = struct{}{}

		if q.Language == "" {
			return fmt.Errorf("control bundle: evidence_queries[%d].language is required", i)
		}
		if _, ok := allowedQueryLanguages[q.Language]; !ok {
			return fmt.Errorf("control bundle: evidence_queries[%d].language %q is not one of rego|sql|jsonpath", i, q.Language)
		}
		if strings.TrimSpace(q.Expression) == "" {
			return fmt.Errorf("control bundle: evidence_queries[%d].expression is required and must be non-empty", i)
		}
	}

	return nil
}

// LifecycleStateOrDefault returns the manifest's lifecycle_state, or "draft"
// when unset. The DB column has NOT NULL DEFAULT 'draft' so empty would work
// too, but the application layer picks a value explicitly so the wire
// response (and the stored manifest hash) is deterministic.
func (m *Manifest) LifecycleStateOrDefault() string {
	if m.LifecycleState == "" {
		return "draft"
	}
	return m.LifecycleState
}

// ApplicabilityExprJSON marshals m.ApplicabilityExpr to compact JSON bytes
// suitable for the slice-017 validator. Returns []byte("null") when the
// expression is nil/empty — that's slice-017's "match every cell" sentinel.
func (m *Manifest) ApplicabilityExprJSON() ([]byte, error) {
	if len(m.ApplicabilityExpr) == 0 {
		return []byte("null"), nil
	}
	b, err := json.Marshal(m.ApplicabilityExpr)
	if err != nil {
		return nil, fmt.Errorf("control bundle: marshal applicability_expr: %w", err)
	}
	return b, nil
}

// EvidenceQueriesJSON returns the queries as a JSON array, suitable for the
// JSONB column. An empty/absent list serialises as "[]" rather than "null" so
// the DB never sees the null literal in a NOT NULL JSONB column.
func (m *Manifest) EvidenceQueriesJSON() ([]byte, error) {
	if len(m.EvidenceQueries) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(m.EvidenceQueries)
}

// ManualEvidenceSchemaJSON returns the schema as JSON bytes, or nil when the
// manifest omits it. The DB column is JSONB NULL.
func (m *Manifest) ManualEvidenceSchemaJSON() ([]byte, error) {
	if len(m.ManualEvidenceSchema) == 0 {
		return nil, nil
	}
	return json.Marshal(m.ManualEvidenceSchema)
}
