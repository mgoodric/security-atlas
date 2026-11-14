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
	"unicode/utf8"
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
	// AnchorFit states how well SCFAnchorID fits the control's intent:
	// exact (the default when omitted), partial, or none (issue #1736).
	// A non-exact fit needs AnchorFitNote; `none` also allows an empty
	// SCFAnchorID, for a control with no SCF equivalent at all.
	AnchorFit          string `yaml:"anchor_fit,omitempty"      json:"anchor_fit,omitempty"`
	AnchorFitNote      string `yaml:"anchor_fit_note,omitempty" json:"anchor_fit_note,omitempty"`
	ControlFamily      string `yaml:"control_family,omitempty" json:"control_family,omitempty"`
	ImplementationType string `yaml:"implementation_type"    json:"implementation_type"`
	OwnerRole          string `yaml:"owner_role,omitempty"   json:"owner_role,omitempty"`
	LifecycleState     string `yaml:"lifecycle_state,omitempty" json:"lifecycle_state,omitempty"`
	FreshnessClass     string `yaml:"freshness_class,omitempty" json:"freshness_class,omitempty"`

	// ApplicabilityExpr is a JSON-AST scope predicate (slice 017 shape). The
	// parser decodes the YAML mapping into a generic interface and re-marshals
	// to JSON bytes so the slice-017 validator (Evaluate / validate node) can
	// consume it without a YAML dependency.
	ApplicabilityExpr map[string]any `yaml:"applicability_expr,omitempty" json:"applicability_expr,omitempty"`

	LinkedPolicyIDs      []string        `yaml:"linked_policy_ids,omitempty" json:"linked_policy_ids,omitempty"`
	EvidenceQueries      []EvidenceQuery `yaml:"evidence_queries,omitempty"  json:"evidence_queries,omitempty"`
	ManualEvidenceSchema map[string]any  `yaml:"manual_evidence_schema,omitempty" json:"manual_evidence_schema,omitempty"`

	// ExternalRefs traces this control to identifiers outside the platform:
	// the organisation's own control number, an auditor's reference, or the
	// framework requirement it was tested under (#1737).
	ExternalRefs []ExternalRef `yaml:"external_refs,omitempty" json:"external_refs,omitempty"`
}

// ExternalRef is one entry in `external_refs[]`.
//
// Kind is "reference" (the default when omitted) for a free-form identifier
// such as an auditor's control number, or "tested_under" for a framework
// requirement the control was audited against. For tested_under, System is
// the framework slug (e.g. "soc2") and ID the requirement code (e.g.
// "CC1.1"); the #1735 "audited under" report reads those pairs.
type ExternalRef struct {
	System string `yaml:"system"         json:"system"`
	Kind   string `yaml:"kind,omitempty" json:"kind,omitempty"`
	ID     string `yaml:"id"             json:"id"`
	Note   string `yaml:"note,omitempty" json:"note,omitempty"`
}

// External reference kinds accepted in `external_refs[].kind`.
const (
	ExternalRefKindReference   = "reference"
	ExternalRefKindTestedUnder = "tested_under"
)

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

// Anchor-fit values (issue #1736). AnchorFitExact is the default.
const (
	AnchorFitExact   = "exact"
	AnchorFitPartial = "partial"
	AnchorFitNone    = "none"
)

var allowedAnchorFits = map[string]struct{}{
	AnchorFitExact:   {},
	AnchorFitPartial: {},
	AnchorFitNone:    {},
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
	// refSystemPattern admits framework slugs ("soc2", "iso27001") and short
	// organisation labels ("internal", "acme-audit").
	refSystemPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// Caps on external_refs. Lengths count characters (runes), not bytes.
const (
	maxExternalRefs       = 100
	maxExternalRefIDLen   = 200
	maxExternalRefNoteLen = 500
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

	if m.AnchorFit != "" {
		if _, ok := allowedAnchorFits[m.AnchorFit]; !ok {
			return fmt.Errorf("control bundle: anchor_fit %q is not one of exact|partial|none", m.AnchorFit)
		}
	}
	if m.AnchorFitOrDefault() != AnchorFitExact && strings.TrimSpace(m.AnchorFitNote) == "" {
		return fmt.Errorf("control bundle: anchor_fit_note is required when anchor_fit is %q (say why the SCF anchor does not fully fit)", m.AnchorFit)
	}

	// AC-4: missing required metadata (scf_anchor_id is the canonical
	// example) must reject with an error pointing at the field. The one
	// exception is anchor_fit: none, ADR 0013's honest "unmapped": no SCF
	// control fits, so the control may stand unanchored.
	if m.SCFAnchorID == "" && m.AnchorFit != AnchorFitNone {
		return fmt.Errorf("control bundle: scf_anchor_id is required (canvas invariant 7: every control must be anchored; if no SCF control fits, set anchor_fit: none with an anchor_fit_note)")
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

	return m.validateExternalRefs()
}

// validateExternalRefs checks every external_refs entry and normalises an
// omitted kind to "reference", so the stored JSON is the same whether the
// author wrote the default or left it out.
func (m *Manifest) validateExternalRefs() error {
	if len(m.ExternalRefs) > maxExternalRefs {
		return fmt.Errorf("control bundle: external_refs has %d entries; at most %d are allowed", len(m.ExternalRefs), maxExternalRefs)
	}
	seen := make(map[[3]string]struct{}, len(m.ExternalRefs))
	for i := range m.ExternalRefs {
		r := &m.ExternalRefs[i]
		if r.System == "" {
			return fmt.Errorf("control bundle: external_refs[%d].system is required", i)
		}
		if !refSystemPattern.MatchString(r.System) {
			return fmt.Errorf("control bundle: external_refs[%d].system %q must match %s", i, r.System, refSystemPattern.String())
		}
		if r.Kind == "" {
			r.Kind = ExternalRefKindReference
		}
		if r.Kind != ExternalRefKindReference && r.Kind != ExternalRefKindTestedUnder {
			return fmt.Errorf("control bundle: external_refs[%d].kind %q is not one of reference|tested_under", i, r.Kind)
		}
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("control bundle: external_refs[%d].id is required and must be non-empty", i)
		}
		if utf8.RuneCountInString(r.ID) > maxExternalRefIDLen {
			return fmt.Errorf("control bundle: external_refs[%d].id exceeds %d characters", i, maxExternalRefIDLen)
		}
		if utf8.RuneCountInString(r.Note) > maxExternalRefNoteLen {
			return fmt.Errorf("control bundle: external_refs[%d].note exceeds %d characters", i, maxExternalRefNoteLen)
		}
		key := [3]string{r.System, r.Kind, r.ID}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("control bundle: external_refs[%d] duplicates system %q kind %q id %q", i, r.System, r.Kind, r.ID)
		}
		seen[key] = struct{}{}
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

// AnchorFitOrDefault returns the manifest's anchor_fit, or "exact" when
// unset, so a bundle that omits it is stored as an exact fit.
func (m *Manifest) AnchorFitOrDefault() string {
	if m.AnchorFit == "" {
		return AnchorFitExact
	}
	return m.AnchorFit
}

// AnchorFitNotePtr returns the trimmed note for the nullable DB column,
// or nil when there is none.
func (m *Manifest) AnchorFitNotePtr() *string {
	n := strings.TrimSpace(m.AnchorFitNote)
	if n == "" {
		return nil
	}
	return &n
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

// ExternalRefsJSON returns the refs as a JSON array for the NOT NULL JSONB
// column. An empty/absent list serialises as "[]".
func (m *Manifest) ExternalRefsJSON() ([]byte, error) {
	if len(m.ExternalRefs) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(m.ExternalRefs)
}
