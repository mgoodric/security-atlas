package frameworkscope

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// Validate parses the canonicalized predicate JSON using slice-017's
// scope-engine AST. It accepts the same operator surface
// (`true | eq | in | and | or | not`) and rejects unknown / malformed
// shapes loudly — silent rejection would let a bad predicate sneak past the
// approval gate.
//
// Validate does NOT need a cell universe — it runs the validator path of
// scope.Evaluate against an empty universe. If the predicate is well-formed
// the call returns nil; otherwise wrap with ErrPredicateMalformed.
func Validate(canonicalPredicate []byte) error {
	// scope.Evaluate handles its own empty-form check, but we want
	// Canonicalize to have already collapsed those — defense in depth.
	if _, err := scope.Evaluate(canonicalPredicate, nil); err != nil {
		return fmt.Errorf("%w: %v", ErrPredicateMalformed, err)
	}
	return nil
}

// EffectiveScope returns the cells in `universe` that satisfy BOTH the
// control's applicability_expr AND the framework_scope's predicate. This
// is the canvas-§5.5 intersection:
//
//	effective_scope(control, framework) =
//	    control.applicability_expr ∩ framework_scope.predicate
//
// Callers (the slice-018 HTTP handler at GET /v1/controls/{id}/effective-scope)
// pass the control's resolved applicability set (already filtered through
// scope.Evaluate) plus the framework_scope predicate; this function applies
// the predicate to those cells and returns the intersection.
//
// An out-of-scope control (zero cells after intersection) returns an empty
// slice — never nil — so callers can range without nil-checks. Downstream
// coverage computation interprets the empty set as `n/a`, not `fail` (per
// AC-11 anti-criterion in docs/issues/018-…).
func EffectiveScope(_ context.Context, applicability []scope.Cell, frameworkPredicate []byte) ([]scope.Cell, error) {
	// scope.Evaluate is stateless — safe to reuse here. Pass the control's
	// applicability set as the universe so we never widen scope past the
	// control's own engineering-reality bounds.
	matches, err := scope.Evaluate(frameworkPredicate, applicability)
	if err != nil {
		return nil, fmt.Errorf("frameworkscope: evaluate predicate: %w", err)
	}
	if matches == nil {
		matches = []scope.Cell{}
	}
	return matches, nil
}

// Compile-time guard: Validate exists and matches the contract.
var _ = func(b []byte) error { return Validate(b) }

// errInternalBug is for unreachable code paths — kept as a sentinel so test
// builds don't break the var-block. Not exported; package-internal only.
var errInternalBug = errors.New("frameworkscope: internal bug")

// silence the unused warning while keeping the symbol available for future
// internal asserts.
var _ = errInternalBug
