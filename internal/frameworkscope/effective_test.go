package frameworkscope_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// TestEffectiveScope_Intersection — AC-11 unit-level: given an applicability
// set + a framework predicate, the intersection returns only cells in BOTH.
func TestEffectiveScope_Intersection(t *testing.T) {
	applicability := []scope.Cell{
		{ID: uuid.New(), Label: "prod-restricted", Dimensions: map[string]string{"environment": "prod", "data_classification": "restricted"}},
		{ID: uuid.New(), Label: "staging-confidential", Dimensions: map[string]string{"environment": "staging", "data_classification": "confidential"}},
		{ID: uuid.New(), Label: "prod-public", Dimensions: map[string]string{"environment": "prod", "data_classification": "public"}},
	}
	// Framework predicate: only restricted + confidential are in scope.
	pred := []byte(`{"op":"in","dim":"data_classification","values":["restricted","confidential"]}`)
	canon, _, err := frameworkscope.Canonicalize(pred)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	cells, err := frameworkscope.EffectiveScope(context.Background(), applicability, canon)
	if err != nil {
		t.Fatalf("EffectiveScope: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("effective_scope len = %d; want 2", len(cells))
	}
	for _, c := range cells {
		dc := c.Dimensions["data_classification"]
		if dc != "restricted" && dc != "confidential" {
			t.Fatalf("unexpected dc %q in effective_scope", dc)
		}
	}
}

// TestEffectiveScope_OutOfScopeReturnsEmpty — AC-11: when the predicate
// matches none of the control's applicability cells, return an empty
// slice (never nil) so callers can range without nil-checks.
func TestEffectiveScope_OutOfScopeReturnsEmpty(t *testing.T) {
	applicability := []scope.Cell{
		{ID: uuid.New(), Label: "prod-public", Dimensions: map[string]string{"environment": "prod", "data_classification": "public"}},
	}
	pred := []byte(`{"op":"eq","dim":"data_classification","value":"restricted"}`)
	canon, _, _ := frameworkscope.Canonicalize(pred)
	cells, err := frameworkscope.EffectiveScope(context.Background(), applicability, canon)
	if err != nil {
		t.Fatalf("EffectiveScope: %v", err)
	}
	if cells == nil {
		t.Fatalf("EffectiveScope returned nil; want empty non-nil slice")
	}
	if len(cells) != 0 {
		t.Fatalf("EffectiveScope len = %d; want 0", len(cells))
	}
}

// TestEffectiveScope_TruePredicateReturnsApplicability — sanity: a `true`
// framework predicate is equivalent to "no narrowing"; the result is the
// full applicability set. This is the SOC 2 default-seed behaviour (AC-12).
func TestEffectiveScope_TruePredicateReturnsApplicability(t *testing.T) {
	applicability := []scope.Cell{
		{ID: uuid.New(), Label: "x", Dimensions: map[string]string{"environment": "prod"}},
		{ID: uuid.New(), Label: "y", Dimensions: map[string]string{"environment": "staging"}},
	}
	canon, _, _ := frameworkscope.Canonicalize([]byte(`{"op":"true"}`))
	cells, err := frameworkscope.EffectiveScope(context.Background(), applicability, canon)
	if err != nil {
		t.Fatalf("EffectiveScope: %v", err)
	}
	if len(cells) != len(applicability) {
		t.Fatalf("len = %d; want %d", len(cells), len(applicability))
	}
}
