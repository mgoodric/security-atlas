package scope

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Credential scope predicates (security-atlas#2435).
//
// An API key's scope_predicate (api_keys.scope_predicate, JSONB, default
// '{}') restricts which evidence the key may push. It is written in the
// same grammar as applicability_expr (see the package doc): the scope
// builder (#2088), exceptions and framework scopes all share it.
//
//	""  / "{}" / "null"                         unrestricted
//	{"op":"eq","dim":"cloud_account","value":"aws:111122223333"}
//	{"op":"in","dim":"environment","values":["prod","staging"]}
//	{"op":"and","args":[ ... ]}   also "or", "not", "true"
//
// A pushed record's scope is a list of dimensions, each with one or more
// values. The record is expanded into every cell its values span (the
// cross product), and the predicate must hold for every one of them: a
// key scoped to environment=prod cannot push a record that also names
// staging. A record that names no dimensions satisfies only an
// unrestricted predicate, so a restricted key never pushes unscoped
// evidence. Anything that is not a valid expression is denied, never
// treated as unrestricted.
//
// Legacy form. Before #2435 ingest read the predicate as
// "key=value[,key=value...]". NormalizeCredentialPredicate maps that form
// to an "and" of "eq" nodes so in-memory and proto callers keep working.
// No api_keys row can hold it: the column is JSONB and the string is not
// JSON, so such an insert has always failed.

// MaxPredicateCells caps the cross product a record's scope expands to.
// A record past it is denied rather than evaluated partially.
const MaxPredicateCells = 4096

// MaxExprDepth and MaxExprNodes cap a parsed expression's nesting and
// size (#2600). JSON decoding alone bounds neither below Go's 10,000-level
// nesting limit and the request body, and validate, match and implies all
// recurse over the tree. Real predicates are a few levels deep.
const (
	MaxExprDepth = 32
	MaxExprNodes = 1000
)

// ErrBadPredicate wraps every malformed-predicate error.
var ErrBadPredicate = errors.New("scope: invalid scope predicate")

// IsUnrestrictedPredicate reports whether predicate places no restriction.
func IsUnrestrictedPredicate(predicate string) bool {
	return isEmptyExpr([]byte(predicate))
}

// NormalizeCredentialPredicate validates predicate and returns its stored
// form: "{}" when unrestricted, the expression JSON otherwise, and the
// legacy key=value form converted to the grammar. An invalid predicate
// returns an error wrapping ErrBadPredicate.
func NormalizeCredentialPredicate(predicate string) (string, error) {
	if IsUnrestrictedPredicate(predicate) {
		return "{}", nil
	}
	trimmed := strings.TrimSpace(predicate)
	if !strings.HasPrefix(trimmed, "{") {
		return legacyToExpr(trimmed)
	}
	if _, err := parsePredicate([]byte(trimmed)); err != nil {
		return "", err
	}
	return trimmed, nil
}

// MatchRecordScope reports whether a record with the given scope (dimension
// key to its values) may be pushed under predicate. A malformed predicate
// returns false and an error wrapping ErrBadPredicate.
func MatchRecordScope(predicate string, dims map[string][]string) (bool, error) {
	if IsUnrestrictedPredicate(predicate) {
		return true, nil
	}
	norm, err := NormalizeCredentialPredicate(predicate)
	if err != nil {
		return false, err
	}
	n, err := parsePredicate([]byte(norm))
	if err != nil {
		return false, err
	}
	cells, err := expandCells(dims)
	if err != nil {
		return false, err
	}
	if len(cells) == 0 {
		return false, nil
	}
	for _, c := range cells {
		ok, err := match(n, c)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func parsePredicate(b []byte) (*node, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var n node
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPredicate, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing data after expression", ErrBadPredicate)
	}
	if err := checkSize(&n); err != nil {
		return nil, err
	}
	if err := validate(&n); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPredicate, err)
	}
	if err := rejectEmptyIn(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

// checkSize refuses a tree deeper than MaxExprDepth or with more than
// MaxExprNodes nodes. It walks iteratively, so the check itself cannot be
// the deep recursion it guards against.
func checkSize(root *node) error {
	type item struct {
		n     *node
		depth int
	}
	stack := []item{{root, 1}}
	count := 0
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		if count > MaxExprNodes {
			return fmt.Errorf("%w: expression has more than %d nodes", ErrBadPredicate, MaxExprNodes)
		}
		if it.depth > MaxExprDepth {
			return fmt.Errorf("%w: expression nests deeper than %d levels", ErrBadPredicate, MaxExprDepth)
		}
		for i := range it.n.Args {
			stack = append(stack, item{&it.n.Args[i], it.depth + 1})
		}
		if it.n.Arg != nil {
			stack = append(stack, item{it.n.Arg, it.depth + 1})
		}
	}
	return nil
}

// rejectEmptyIn refuses an "in" with no values anywhere in the tree. It
// would match nothing, so a key carrying one looks scoped but can never
// push; refusing it at issue surfaces the mistake. (An empty "and"/"or"
// is already refused by validate.)
func rejectEmptyIn(n *node) error {
	if n.Op == "in" && len(n.Values) == 0 {
		return fmt.Errorf("%w: in on %q requires at least one value", ErrBadPredicate, n.Dim)
	}
	for i := range n.Args {
		if err := rejectEmptyIn(&n.Args[i]); err != nil {
			return err
		}
	}
	if n.Arg != nil {
		return rejectEmptyIn(n.Arg)
	}
	return nil
}

// legacyToExpr converts "k=v[,k=v...]" into {"op":"and","args":[eq...]}.
func legacyToExpr(s string) (string, error) {
	var args []map[string]string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return "", fmt.Errorf("%w: %q is neither a JSON expression nor key=value", ErrBadPredicate, s)
		}
		k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		if k == "" || v == "" {
			return "", fmt.Errorf("%w: empty key or value in %q", ErrBadPredicate, part)
		}
		args = append(args, map[string]string{"op": "eq", "dim": k, "value": v})
	}
	if len(args) == 0 {
		return "", fmt.Errorf("%w: %q has no key=value pairs", ErrBadPredicate, s)
	}
	b, err := json.Marshal(map[string]any{"op": "and", "args": args})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadPredicate, err)
	}
	return string(b), nil
}

// expandCells returns the cross product of dims as single-valued cells.
// No dimensions yields no cells.
func expandCells(dims map[string][]string) ([]Cell, error) {
	if len(dims) == 0 {
		return nil, nil
	}
	cells := []map[string]string{{}}
	for k, vals := range dims {
		if len(vals) == 0 {
			return nil, fmt.Errorf("%w: record scope dimension %q has no values", ErrBadPredicate, k)
		}
		if len(cells)*len(vals) > MaxPredicateCells {
			return nil, fmt.Errorf("%w: record scope spans more than %d cells", ErrBadPredicate, MaxPredicateCells)
		}
		next := make([]map[string]string, 0, len(cells)*len(vals))
		for _, c := range cells {
			for _, v := range vals {
				m := make(map[string]string, len(c)+1)
				for ck, cv := range c {
					m[ck] = cv
				}
				m[k] = v
				next = append(next, m)
			}
		}
		cells = next
	}
	out := make([]Cell, len(cells))
	for i, m := range cells {
		out[i] = Cell{Dimensions: m}
	}
	return out, nil
}

// ValidateExpr checks that b is a well-formed expression in the scope
// grammar, by the strict rules credential predicates use: no unknown
// fields, no unknown op, no empty and/or/in. It does not accept the empty
// forms ("", null, {}); a caller that allows those checks for them first.
// The error wraps ErrBadPredicate (#2343).
func ValidateExpr(b []byte) error {
	_, err := parsePredicate(b)
	return err
}

// PredicateWithin reports whether every record child admits is also
// admitted by parent, so a key scoped by child is no wider than one scoped
// by parent (#2656). It is sound, not complete: false can mean "not
// provably within". It proves containment from the structure alone:
//
//   - parent unrestricted, or parent {"op":"true"} with child restricted;
//   - parent "and": child within every arg;
//   - child "or": every arg within parent;
//   - child "and": some arg within parent;
//   - parent "or": child within some arg;
//   - eq and in on one dim, when child's values are a subset of parent's;
//   - otherwise only a structurally equal expression ("not" included).
//
// An unrestricted child is within an unrestricted parent only. Either
// predicate malformed returns an error wrapping ErrBadPredicate.
func PredicateWithin(child, parent string) (bool, error) {
	c, err := NormalizeCredentialPredicate(child)
	if err != nil {
		return false, err
	}
	p, err := NormalizeCredentialPredicate(parent)
	if err != nil {
		return false, err
	}
	if IsUnrestrictedPredicate(p) {
		return true, nil
	}
	if IsUnrestrictedPredicate(c) {
		return false, nil
	}
	cn, err := parsePredicate([]byte(c))
	if err != nil {
		return false, err
	}
	pn, err := parsePredicate([]byte(p))
	if err != nil {
		return false, err
	}
	return implies(cn, pn), nil
}

// implies reports whether c holding on a cell means p holds on it, by the
// rules PredicateWithin lists.
func implies(c, p *node) bool {
	if p.Op == "true" {
		return true
	}
	if p.Op == "and" {
		for i := range p.Args {
			if !implies(c, &p.Args[i]) {
				return false
			}
		}
		return true
	}
	if c.Op == "or" {
		for i := range c.Args {
			if !implies(&c.Args[i], p) {
				return false
			}
		}
		return true
	}
	if c.Op == "and" {
		for i := range c.Args {
			if implies(&c.Args[i], p) {
				return true
			}
		}
	}
	if p.Op == "or" {
		for i := range p.Args {
			if implies(c, &p.Args[i]) {
				return true
			}
		}
	}
	if cv, ok := leafValues(c); ok {
		if pv, ok := leafValues(p); ok && c.Dim == p.Dim {
			return subsetOf(cv, pv)
		}
	}
	return reflect.DeepEqual(c, p)
}

// leafValues returns the values an eq or in node admits on its dim.
func leafValues(n *node) ([]string, bool) {
	switch n.Op {
	case "eq":
		return []string{*n.Value}, true
	case "in":
		return n.Values, true
	}
	return nil, false
}

// subsetOf reports whether every value in a is in b.
func subsetOf(a, b []string) bool {
	for _, v := range a {
		found := false
		for _, w := range b {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
