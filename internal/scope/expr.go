// Package scope implements the multidimensional scope model: tenant-configurable
// dimensions, scope cells (tuples over those dimensions), and a JSON-AST
// applicability_expr engine that resolves an expression to the subset of cells
// where it evaluates to true.
//
// The expression language is intentionally a small JSON-encoded boolean AST,
// not a custom DSL (per slice 017 anti-criterion). Operators in v1:
//
//	{"op":"true"}                                            // matches every cell
//	{"op":"eq",  "dim":"environment", "value":"prod"}        // equality on one dim
//	{"op":"in",  "dim":"environment", "values":["prod","staging"]}
//	{"op":"and", "args":[ ... ]}
//	{"op":"or",  "args":[ ... ]}
//	{"op":"not", "arg":  { ... }}
//
// An empty document, the JSON value null, or "{}" all mean "match every cell"
// (AC-4). Any other shape is rejected loudly — we never silently drop cells.
//
// Dimension values are strings only in v1; future numeric/bool types extend
// the value-type allowlist in scope_dimensions.
package scope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Cell is a scope cell — a tuple over the tenant's declared dimensions plus
// platform metadata. Equality between cells is identity-based (ID); two cells
// with the same dimensions in different rows are different cells.
type Cell struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Label      string
	Dimensions map[string]string
}

// Engine is the public contract slice 012 (evaluation engine) imports. Given an
// expression and the tenant's cell universe, it returns the applicability set.
type Engine interface {
	// Applicability returns the subset of cells where exprJSON evaluates true.
	// A nil/empty exprJSON returns every cell unchanged (AC-4).
	Applicability(ctx context.Context, exprJSON []byte, universe []Cell) ([]Cell, error)
}

// DefaultEngine is the stateless evaluator. Construct with NewEngine.
type DefaultEngine struct{}

// NewEngine returns the v1 evaluator.
func NewEngine() *DefaultEngine { return &DefaultEngine{} }

// Applicability satisfies Engine. Stateless — safe for concurrent use.
func (DefaultEngine) Applicability(_ context.Context, exprJSON []byte, universe []Cell) ([]Cell, error) {
	return Evaluate(exprJSON, universe)
}

// Evaluate is the package-level entry point (no receiver) used both by Engine
// callers and by direct test code. It parses exprJSON, walks the AST against
// each cell, and returns the cells where the AST evaluated true.
//
// Pre-allocated returned slice; never nil even on zero matches, so callers can
// range without nil-checks. Returns an error if the expression is malformed —
// dropping cells silently on a bad expression would violate the slice's
// anti-criterion.
func Evaluate(exprJSON []byte, universe []Cell) ([]Cell, error) {
	// AC-4: nil/empty/"{}"/null all mean "match every cell".
	if isEmptyExpr(exprJSON) {
		out := make([]Cell, len(universe))
		copy(out, universe)
		return out, nil
	}

	var node node
	if err := json.Unmarshal(exprJSON, &node); err != nil {
		return nil, fmt.Errorf("scope: parse expression: %w", err)
	}
	if err := validate(&node); err != nil {
		return nil, err
	}

	matches := make([]Cell, 0, len(universe))
	for _, c := range universe {
		ok, err := match(&node, c)
		if err != nil {
			return nil, err
		}
		if ok {
			matches = append(matches, c)
		}
	}
	return matches, nil
}

// isEmptyExpr returns true if the input represents the "always-true" form:
// nil, empty bytes, the literal "null", or "{}".
func isEmptyExpr(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	s := strings.TrimSpace(string(b))
	return s == "null" || s == "{}"
}

// ===== AST =====

// node mirrors every operator's union of fields. We unmarshal once into this
// shape and then walk a typed tree; this keeps the JSON surface small.
type node struct {
	Op     string   `json:"op"`
	Dim    string   `json:"dim,omitempty"`
	Value  *string  `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
	Args   []node   `json:"args,omitempty"`
	Arg    *node    `json:"arg,omitempty"`
}

// validate checks structural well-formedness independent of any cell.
// It rejects unknown operators, missing fields, and zero-argument and/or/not
// constructions. Calling validate before match() guarantees match() never
// observes a malformed node.
func validate(n *node) error {
	switch n.Op {
	case "true":
		return nil
	case "eq":
		if n.Dim == "" {
			return errors.New("scope: eq requires `dim`")
		}
		if n.Value == nil {
			return errors.New("scope: eq requires `value`")
		}
		return nil
	case "in":
		if n.Dim == "" {
			return errors.New("scope: in requires `dim`")
		}
		if n.Values == nil {
			return errors.New("scope: in requires `values` array")
		}
		return nil
	case "and", "or":
		if len(n.Args) == 0 {
			return fmt.Errorf("scope: %s requires non-empty `args`", n.Op)
		}
		for i := range n.Args {
			if err := validate(&n.Args[i]); err != nil {
				return err
			}
		}
		return nil
	case "not":
		if n.Arg == nil {
			return errors.New("scope: not requires `arg`")
		}
		return validate(n.Arg)
	case "":
		return errors.New("scope: missing `op`")
	default:
		return fmt.Errorf("scope: unknown op %q", n.Op)
	}
}

// match returns true if the cell satisfies the AST. validate() must have run
// first; this function does not re-check structural well-formedness.
func match(n *node, c Cell) (bool, error) {
	switch n.Op {
	case "true":
		return true, nil
	case "eq":
		v, ok := c.Dimensions[n.Dim]
		if !ok {
			return false, nil
		}
		return v == *n.Value, nil
	case "in":
		v, ok := c.Dimensions[n.Dim]
		if !ok {
			return false, nil
		}
		for _, candidate := range n.Values {
			if v == candidate {
				return true, nil
			}
		}
		return false, nil
	case "and":
		for i := range n.Args {
			ok, err := match(&n.Args[i], c)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case "or":
		for i := range n.Args {
			ok, err := match(&n.Args[i], c)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case "not":
		ok, err := match(n.Arg, c)
		if err != nil {
			return false, err
		}
		return !ok, nil
	}
	return false, fmt.Errorf("scope: unreachable op %q (validate skipped?)", n.Op)
}

// Compile-time assurance that DefaultEngine satisfies Engine.
var _ Engine = (*DefaultEngine)(nil)
