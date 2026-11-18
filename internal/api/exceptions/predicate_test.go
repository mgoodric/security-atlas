package exceptions

import (
	"encoding/json"
	"testing"
)

// #2113: a null scope predicate is the picker's "incomplete" value, and
// the evaluator reads a stored null as every cell. The create handler
// refuses it, and anything else that is not an object; an omitted field
// and the explicit always-true predicate pass.
func TestCheckPredicate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		raw    string
		refuse bool
	}{
		{"", false},
		{`{"op":"true"}`, false},
		{`{"op":"eq","dim":"environment","value":"prod"}`, false},
		{` {"op":"true"} `, false},
		{"null", true},
		{" null ", true},
		{"[]", true},
		{`"everything"`, true},
		{"true", true},
		// #2343: the predicate must be a well-formed scope expression.
		{"{}", false},
		{`{"op":"and","args":[{"op":"in","dim":"cloud","values":["aws"]},{"op":"not","arg":{"op":"eq","dim":"environment","value":"dev"}}]}`, false},
		{`{"op":"bogus"}`, true},
		{`{"dim":"environment"}`, true},
		{`{"op":"eq","dim":"environment"}`, true},
		{`{"op":"in","dim":"environment","values":[]}`, true},
		{`{"op":"and","args":[]}`, true},
		{`{"op":"not"}`, true},
		{`{"op":"true","extra":1}`, true},
		{`{"op":"true"}{"op":"true"}`, true},
	} {
		got := checkPredicate(json.RawMessage(c.raw))
		if (got != "") != c.refuse {
			t.Errorf("checkPredicate(%q) = %q, refuse want %v", c.raw, got, c.refuse)
		}
	}
}
