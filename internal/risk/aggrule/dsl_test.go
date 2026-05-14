package aggrule_test

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

// validRuleJSON is a well-formed rule used as the baseline for the
// negative cases below.
const validRuleJSON = `{
  "rule_id": "ownership-cross-team",
  "target_theme": "ownership",
  "min_risks": 3,
  "min_teams": 2,
  "window_days": 90,
  "parent_level": "org",
  "severity_function": "max",
  "title_template": "Cross-team {theme} pattern",
  "custom_rego": ""
}`

const validRuleYAML = `
rule_id: ownership-cross-team
target_theme: ownership
min_risks: 3
min_teams: 2
window_days: 90
parent_level: org
severity_function: max
title_template: "Cross-team {theme} pattern"
custom_rego: ""
`

// ISC-1: ParseRule accepts application/json bytes into Rule struct.
func TestParseRule_JSON(t *testing.T) {
	t.Parallel()
	r, err := aggrule.ParseRule([]byte(validRuleJSON), aggrule.FormatJSON)
	if err != nil {
		t.Fatalf("ParseRule JSON: %v", err)
	}
	if r.RuleID != "ownership-cross-team" {
		t.Errorf("rule_id: got %q, want ownership-cross-team", r.RuleID)
	}
	if r.MinRisks != 3 || r.MinTeams != 2 || r.WindowDays != 90 {
		t.Errorf("thresholds: got (%d,%d,%d), want (3,2,90)", r.MinRisks, r.MinTeams, r.WindowDays)
	}
	if r.ParentLevel != "org" || r.SeverityFunction != "max" {
		t.Errorf("parent_level/severity_function: got (%q,%q)", r.ParentLevel, r.SeverityFunction)
	}
}

// ISC-2: ParseRule accepts application/yaml bytes into the same Rule struct.
func TestParseRule_YAML(t *testing.T) {
	t.Parallel()
	r, err := aggrule.ParseRule([]byte(validRuleYAML), aggrule.FormatYAML)
	if err != nil {
		t.Fatalf("ParseRule YAML: %v", err)
	}
	// Parse the JSON form too and compare — both wire formats must produce
	// the identical struct.
	rj, err := aggrule.ParseRule([]byte(validRuleJSON), aggrule.FormatJSON)
	if err != nil {
		t.Fatalf("ParseRule JSON: %v", err)
	}
	if r != rj {
		t.Errorf("YAML and JSON produced different structs:\n yaml=%+v\n json=%+v", r, rj)
	}
}

// ISC-3: ParseRule rejects malformed JSON/YAML with a parse error.
func TestParseRule_Malformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		raw    string
		format aggrule.Format
	}{
		{"broken json", `{"rule_id": `, aggrule.FormatJSON},
		{"unknown json field", `{"rule_id":"x","bogus_field":1}`, aggrule.FormatJSON},
		{"broken yaml", "rule_id: x\n  bad: : indent", aggrule.FormatYAML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := aggrule.ParseRule([]byte(tc.raw), tc.format)
			if err == nil {
				t.Fatalf("expected parse error, got nil")
			}
			if !errors.Is(err, aggrule.ErrParse) {
				t.Errorf("error is not ErrParse: %v", err)
			}
		})
	}
}

// fieldErrors runs Validate and returns the field names that failed.
func fieldErrors(t *testing.T, r aggrule.Rule) map[string]string {
	t.Helper()
	err := r.Validate()
	if err == nil {
		return nil
	}
	var ve *aggrule.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate returned non-ValidationError: %v", err)
	}
	out := make(map[string]string, len(ve.Errors))
	for _, fe := range ve.Errors {
		out[fe.Field] = fe.Message
	}
	return out
}

func baseRule() aggrule.Rule {
	r, _ := aggrule.ParseRule([]byte(validRuleJSON), aggrule.FormatJSON)
	return r
}

// ISC-4: Validate rejects empty rule_id with a field-level error.
func TestValidate_EmptyRuleID(t *testing.T) {
	t.Parallel()
	r := baseRule()
	r.RuleID = ""
	errs := fieldErrors(t, r)
	if _, ok := errs["rule_id"]; !ok {
		t.Fatalf("expected rule_id field error, got %v", errs)
	}
}

// ISC-5: Validate rejects empty target_theme with a field-level error.
func TestValidate_EmptyTargetTheme(t *testing.T) {
	t.Parallel()
	r := baseRule()
	r.TargetTheme = "   "
	errs := fieldErrors(t, r)
	if _, ok := errs["target_theme"]; !ok {
		t.Fatalf("expected target_theme field error, got %v", errs)
	}
}

// ISC-6: Validate rejects min_risks/min_teams/window_days <= 0.
func TestValidate_NonPositiveThresholds(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"min_risks", "min_teams", "window_days"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			r := baseRule()
			switch field {
			case "min_risks":
				r.MinRisks = 0
			case "min_teams":
				r.MinTeams = -1
			case "window_days":
				r.WindowDays = 0
			}
			errs := fieldErrors(t, r)
			if _, ok := errs[field]; !ok {
				t.Fatalf("expected %s field error, got %v", field, errs)
			}
		})
	}
}

// ISC-7: Validate rejects severity_function outside the closed set.
func TestValidate_UnknownSeverityFunction(t *testing.T) {
	t.Parallel()
	r := baseRule()
	r.SeverityFunction = "median"
	errs := fieldErrors(t, r)
	if _, ok := errs["severity_function"]; !ok {
		t.Fatalf("expected severity_function field error, got %v", errs)
	}
}

// ISC-8: Validate rejects parent_level outside team/org/company.
func TestValidate_UnknownParentLevel(t *testing.T) {
	t.Parallel()
	r := baseRule()
	r.ParentLevel = "division"
	errs := fieldErrors(t, r)
	if _, ok := errs["parent_level"]; !ok {
		t.Fatalf("expected parent_level field error, got %v", errs)
	}
}

// ISC-9: Validate rejects custom_rego function with an empty custom_rego policy,
// and rejects a non-empty custom_rego on a non-custom_rego function.
func TestValidate_CustomRegoCoherence(t *testing.T) {
	t.Parallel()

	t.Run("custom_rego function, empty policy", func(t *testing.T) {
		t.Parallel()
		r := baseRule()
		r.SeverityFunction = "custom_rego"
		r.CustomRego = ""
		errs := fieldErrors(t, r)
		if _, ok := errs["custom_rego"]; !ok {
			t.Fatalf("expected custom_rego field error, got %v", errs)
		}
	})

	t.Run("max function, non-empty policy", func(t *testing.T) {
		t.Parallel()
		r := baseRule()
		r.SeverityFunction = "max"
		r.CustomRego = "package x\nseverity := 1"
		errs := fieldErrors(t, r)
		if _, ok := errs["custom_rego"]; !ok {
			t.Fatalf("expected custom_rego field error, got %v", errs)
		}
	})

	t.Run("custom_rego function, non-empty policy is valid", func(t *testing.T) {
		t.Parallel()
		r := baseRule()
		r.SeverityFunction = "custom_rego"
		r.CustomRego = "package aggrule.severity\nseverity := 7"
		if err := r.Validate(); err != nil {
			t.Fatalf("expected valid, got %v", err)
		}
	})
}

// ISC-10: Validate rejects a self-referential rule (rule_id appears in body refs).
func TestValidate_SelfReferential(t *testing.T) {
	t.Parallel()
	r := baseRule()
	r.RuleID = "loop-rule"
	r.TitleTemplate = "pattern from loop-rule"
	errs := fieldErrors(t, r)
	if _, ok := errs["rule_id"]; !ok {
		t.Fatalf("expected rule_id self-referential field error, got %v", errs)
	}
}

// ISC-11: Validate accepts a well-formed rule with no errors.
func TestValidate_WellFormed(t *testing.T) {
	t.Parallel()
	if err := baseRule().Validate(); err != nil {
		t.Fatalf("expected well-formed rule to validate, got %v", err)
	}
}

func TestRule_Title(t *testing.T) {
	t.Parallel()
	r := baseRule()
	if got := r.Title(); got != "Cross-team ownership pattern" {
		t.Errorf("Title: got %q", got)
	}
	r.TitleTemplate = ""
	if got := r.Title(); got != "Aggregated ownership pattern" {
		t.Errorf("default Title: got %q", got)
	}
}
