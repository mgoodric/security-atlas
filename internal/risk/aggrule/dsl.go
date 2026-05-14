// Package aggrule implements the slice 054 declarative aggregation rules
// engine (canvas Plans/canvas/06-risk.md §6.6).
//
// A declarative rule — authored in YAML or JSON — defines when child-level
// risks should generate a parent-level meta-risk:
//
//	rule_id:           ownership-cross-team
//	target_theme:      ownership
//	min_risks:         3
//	min_teams:         2
//	window_days:       90
//	parent_level:      org
//	severity_function: max          # max | weighted_max | sum | custom_rego
//	title_template:    "Cross-team {theme} pattern"
//	custom_rego:       ""            # required when severity_function = custom_rego
//
// The engine re-evaluates every `active` rule on each risk write and
// auto-creates / updates a meta-risk when the rule's thresholds are met.
//
// dsl.go owns the wire contract: ParseRule (JSON + YAML -> the same Rule
// struct) and Rule.Validate (field-level validation + a self-referential
// guard). The HTTP handler parses + validates BEFORE persisting; the
// database CHECK constraints on aggregation_rules are defense-in-depth.
package aggrule

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format selects the wire encoding ParseRule should expect. The HTTP
// handler maps the request Content-Type onto it (application/json ->
// FormatJSON, application/yaml | text/yaml -> FormatYAML).
type Format int

const (
	// FormatJSON parses an application/json body.
	FormatJSON Format = iota
	// FormatYAML parses an application/yaml body.
	FormatYAML
)

// Rule is the canonical in-memory shape of an aggregation rule. Both
// ParseRule branches (JSON, YAML) produce this exact struct so validation
// and persistence never have to care which wire format arrived.
//
// The struct tags are shared by encoding/json and gopkg.in/yaml.v3 — yaml.v3
// honours `json:` tags only when no `yaml:` tag is present, so both are
// declared explicitly for clarity.
type Rule struct {
	// RuleID is the human-authored stable identifier ("ownership-cross-team").
	// Tenant-unique (the DB enforces UNIQUE (tenant_id, rule_id)).
	RuleID string `json:"rule_id" yaml:"rule_id"`
	// TargetTheme is the canvas §6.5 theme this rule watches. A risk write
	// whose themes include this value triggers re-evaluation.
	TargetTheme string `json:"target_theme" yaml:"target_theme"`
	// MinRisks / MinTeams / WindowDays are the canvas §6.6 thresholds.
	MinRisks   int `json:"min_risks" yaml:"min_risks"`
	MinTeams   int `json:"min_teams" yaml:"min_teams"`
	WindowDays int `json:"window_days" yaml:"window_days"`
	// ParentLevel is the level of the meta-risk this rule creates:
	// team | org | company (slice 052's risk_level enum).
	ParentLevel string `json:"parent_level" yaml:"parent_level"`
	// SeverityFunction is one of max | weighted_max | sum | custom_rego.
	SeverityFunction string `json:"severity_function" yaml:"severity_function"`
	// TitleTemplate is the meta-risk title; "{theme}" is substituted with
	// TargetTheme at fire time. Optional — a default is applied when empty.
	TitleTemplate string `json:"title_template" yaml:"title_template"`
	// CustomRego is the OPA Rego policy body. Required (non-empty) when
	// SeverityFunction == "custom_rego"; must be empty otherwise.
	CustomRego string `json:"custom_rego" yaml:"custom_rego"`
}

// Closed sets the DSL accepts. Kept in sync with the migration's CHECK
// constraints on aggregation_rules (severity_function, parent_level).
var (
	validSeverityFunctions = map[string]struct{}{
		"max":          {},
		"weighted_max": {},
		"sum":          {},
		"custom_rego":  {},
	}
	validParentLevels = map[string]struct{}{
		"team":    {},
		"org":     {},
		"company": {},
	}
)

// ErrParse wraps any malformed-input error from ParseRule. Callers use
// errors.Is(err, ErrParse) to map to HTTP 400 without inspecting the
// underlying JSON/YAML decoder error.
var ErrParse = errors.New("aggrule: malformed rule document")

// ParseRule decodes raw bytes in the given Format into a Rule. A decode
// failure is wrapped in ErrParse. ParseRule does NOT validate semantics —
// call Rule.Validate for that.
func ParseRule(raw []byte, format Format) (Rule, error) {
	var r Rule
	switch format {
	case FormatJSON:
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return Rule{}, fmt.Errorf("%w: json: %v", ErrParse, err)
		}
	case FormatYAML:
		if err := yaml.Unmarshal(raw, &r); err != nil {
			return Rule{}, fmt.Errorf("%w: yaml: %v", ErrParse, err)
		}
	default:
		return Rule{}, fmt.Errorf("%w: unknown format %d", ErrParse, format)
	}
	return r, nil
}

// FieldError is one field-level validation failure. The HTTP handler
// renders a slice of these into a 400 body so the caller sees exactly
// which field is wrong and why.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ValidationError aggregates every FieldError found in one Validate pass.
// errors.As(err, *ValidationError) recovers the field list.
type ValidationError struct {
	Errors []FieldError `json:"errors"`
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		parts[i] = fe.Error()
	}
	return "aggrule: rule validation failed: " + strings.Join(parts, "; ")
}

// Validate checks the rule against the canvas §6.6 schema and returns a
// *ValidationError listing every field-level problem (not just the first).
// It also enforces the structural cycle guard: a rule whose body references
// its own rule_id is rejected (defense in depth — the real cycle prevention
// is the engine's runtime exclusion of rule-generated meta-risks).
//
// Returns nil when the rule is well-formed.
func (r Rule) Validate() error {
	var errs []FieldError

	if strings.TrimSpace(r.RuleID) == "" {
		errs = append(errs, FieldError{"rule_id", "must not be empty"})
	}
	if strings.TrimSpace(r.TargetTheme) == "" {
		errs = append(errs, FieldError{"target_theme", "must not be empty"})
	}
	if r.MinRisks <= 0 {
		errs = append(errs, FieldError{"min_risks", "must be a positive integer"})
	}
	if r.MinTeams <= 0 {
		errs = append(errs, FieldError{"min_teams", "must be a positive integer"})
	}
	if r.WindowDays <= 0 {
		errs = append(errs, FieldError{"window_days", "must be a positive integer"})
	}
	if _, ok := validParentLevels[r.ParentLevel]; !ok {
		errs = append(errs, FieldError{"parent_level", "must be one of team, org, company"})
	}
	if _, ok := validSeverityFunctions[r.SeverityFunction]; !ok {
		errs = append(errs, FieldError{"severity_function", "must be one of max, weighted_max, sum, custom_rego"})
	}
	if r.SeverityFunction == "custom_rego" {
		if strings.TrimSpace(r.CustomRego) == "" {
			errs = append(errs, FieldError{"custom_rego", "must be a non-empty Rego policy when severity_function is custom_rego"})
		}
	} else if strings.TrimSpace(r.CustomRego) != "" {
		errs = append(errs, FieldError{"custom_rego", "must be empty unless severity_function is custom_rego"})
	}

	// Structural cycle guard (defense in depth). A rule must not reference
	// its own rule_id anywhere in its body — that would be an obviously
	// self-referential definition. The substantive cycle prevention is the
	// engine's runtime exclusion of rule_generated meta-risks (see engine.go),
	// because a rule-generated meta-risk carries the union of its children's
	// themes, which always includes target_theme — so a static
	// "target_theme matches own output" check would reject every rule.
	if r.RuleID != "" {
		if strings.Contains(r.TargetTheme, r.RuleID) ||
			strings.Contains(r.TitleTemplate, r.RuleID) ||
			strings.Contains(r.CustomRego, r.RuleID) {
			errs = append(errs, FieldError{
				"rule_id",
				"rule is self-referential: rule_id must not appear in target_theme, title_template, or custom_rego",
			})
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// CanonicalJSON returns the rule serialised to canonical JSON. This is what
// the store persists in the aggregation_rules.rule_body JSONB column — the
// typed columns are a denormalised projection of the queryable subset, and
// rule_body is the complete record (the hybrid storage decision).
func (r Rule) CanonicalJSON() ([]byte, error) {
	return json.Marshal(r)
}

// Title renders the meta-risk title for a fired rule, substituting "{theme}"
// in TitleTemplate with TargetTheme. When TitleTemplate is empty a stable
// default is used.
func (r Rule) Title() string {
	tmpl := r.TitleTemplate
	if strings.TrimSpace(tmpl) == "" {
		tmpl = "Aggregated " + r.TargetTheme + " pattern"
	}
	return strings.ReplaceAll(tmpl, "{theme}", r.TargetTheme)
}
