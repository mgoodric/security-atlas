package scope

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMatchRecordScope(t *testing.T) {
	t.Parallel()
	prod := map[string][]string{"environment": {"prod"}}
	prodAWS := map[string][]string{"environment": {"prod"}, "cloud_account": {"aws:1"}}
	prodStaging := map[string][]string{"environment": {"prod", "staging"}}

	cases := []struct {
		name      string
		predicate string
		dims      map[string][]string
		want      bool
		wantErr   bool
	}{
		// Unrestricted shapes.
		{"empty string", "", prod, true, false},
		{"empty object", "{}", prod, true, false},
		{"empty object spaced", " { } ", prod, false, true},
		{"null", "null", prod, true, false},
		{"empty object, unscoped record", "{}", nil, true, false},
		{"op true", `{"op":"true"}`, prod, true, false},

		// eq.
		{"eq match", `{"op":"eq","dim":"environment","value":"prod"}`, prod, true, false},
		{"eq other value", `{"op":"eq","dim":"environment","value":"staging"}`, prod, false, false},
		{"eq dim absent", `{"op":"eq","dim":"cloud_account","value":"aws:1"}`, prod, false, false},
		{"eq every value must match", `{"op":"eq","dim":"environment","value":"prod"}`, prodStaging, false, false},

		// in.
		{"in match", `{"op":"in","dim":"environment","values":["prod","staging"]}`, prodStaging, true, false},
		{"in miss", `{"op":"in","dim":"environment","values":["dev"]}`, prod, false, false},

		// and / or / not.
		{"and match", `{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"cloud_account","value":"aws:1"}]}`, prodAWS, true, false},
		{"and partial", `{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"cloud_account","value":"aws:2"}]}`, prodAWS, false, false},
		{"or match", `{"op":"or","args":[{"op":"eq","dim":"environment","value":"dev"},{"op":"eq","dim":"environment","value":"prod"}]}`, prod, true, false},
		{"not match", `{"op":"not","arg":{"op":"eq","dim":"environment","value":"staging"}}`, prod, true, false},
		{"not miss", `{"op":"not","arg":{"op":"eq","dim":"environment","value":"prod"}}`, prod, false, false},

		// Fail closed: a restricted key never pushes unscoped evidence.
		{"restricted, unscoped record", `{"op":"eq","dim":"environment","value":"prod"}`, nil, false, false},
		{"not, unscoped record", `{"op":"not","arg":{"op":"eq","dim":"environment","value":"prod"}}`, map[string][]string{}, false, false},
		{"empty values", `{"op":"eq","dim":"environment","value":"prod"}`, map[string][]string{"environment": {}}, false, true},

		// Legacy key=value (in-memory and proto callers only).
		{"legacy match", "environment=prod", prod, true, false},
		{"legacy multi", "environment=prod, cloud_account=aws:1", prodAWS, true, false},
		{"legacy miss", "cloud_account=aws:2", prodAWS, false, false},

		// Malformed: denied with an error, never unrestricted.
		{"bare dimension map", `{"environment":"prod"}`, prod, false, true},
		{"unknown op", `{"op":"xor","args":[]}`, prod, false, true},
		// An empty combinator or empty "in" must never read as unrestricted.
		{"empty and", `{"op":"and","args":[]}`, prod, false, true},
		{"empty or", `{"op":"or","args":[]}`, prod, false, true},
		{"empty in", `{"op":"in","dim":"environment","values":[]}`, prod, false, true},
		{"nested empty in", `{"op":"and","args":[{"op":"not","arg":{"op":"in","dim":"environment","values":[]}}]}`, prod, false, true},
		{"unknown field", `{"op":"eq","dim":"environment","value":"prod","extra":1}`, prod, false, true},
		{"missing value", `{"op":"eq","dim":"environment"}`, prod, false, true},
		{"truncated json", `{"op":"eq"`, prod, false, true},
		{"trailing data", `{"op":"true"}{"op":"true"}`, prod, false, true},
		{"json array", `[]`, prod, false, true},
		{"json string", `"{}"`, prod, false, true},
		{"legacy without =", "environment", prod, false, true},
		{"legacy empty value", "environment=", prod, false, true},
		{"commas only", ",,", prod, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchRecordScope(tc.predicate, tc.dims)
			if got != tc.want {
				t.Errorf("MatchRecordScope(%q) = %v, want %v (err=%v)", tc.predicate, got, tc.want, err)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("MatchRecordScope(%q) err = %v, wantErr %v", tc.predicate, err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrBadPredicate) {
				t.Errorf("err %v does not wrap ErrBadPredicate", err)
			}
		})
	}
}

func TestMatchRecordScope_CellCap(t *testing.T) {
	t.Parallel()
	vals := make([]string, 100)
	for i := range vals {
		vals[i] = fmt.Sprint(i)
	}
	dims := map[string][]string{"a": vals, "b": vals}
	ok, err := MatchRecordScope(`{"op":"true"}`, dims)
	if ok || !errors.Is(err, ErrBadPredicate) {
		t.Fatalf("10000-cell record: ok=%v err=%v, want denied with ErrBadPredicate", ok, err)
	}
}

func TestNormalizeCredentialPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"", "{}", false},
		{"{}", "{}", false},
		{"null", "{}", false},
		{` {"op":"true"} `, `{"op":"true"}`, false},
		{"environment=prod", `{"args":[{"dim":"environment","op":"eq","value":"prod"}],"op":"and"}`, false},
		{`{"environment":"prod"}`, "", true},
		{"nonsense", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeCredentialPredicate(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("NormalizeCredentialPredicate(%q) = %q, %v; want %q, err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

// #2343: ValidateExpr is the strict grammar check exception create uses.
func TestValidateExpr(t *testing.T) {
	t.Parallel()
	for in, ok := range map[string]bool{
		`{"op":"true"}`: true,
		`{"op":"in","dim":"environment","values":["prod"]}`: true,
		`{"op":"bogus"}`:      false,
		`{}`:                  false,
		`{"op":"true","x":1}`: false,
		`{"op":"in","dim":"environment","values":[]}`: false,
	} {
		err := ValidateExpr([]byte(in))
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrBadPredicate)) {
			t.Errorf("ValidateExpr(%s) = %v, want ok=%v", in, err, ok)
		}
	}
}

// nestedNot returns depth "not" nodes wrapped around a "true" leaf, so the
// tree is depth+1 levels deep.
func nestedNot(depth int) string {
	return strings.Repeat(`{"op":"not","arg":`, depth) + `{"op":"true"}` + strings.Repeat(`}`, depth)
}

// wideOr returns an "or" with n "true" args: n+1 nodes, two levels deep.
func wideOr(n int) string {
	return `{"op":"or","args":[` + strings.TrimSuffix(strings.Repeat(`{"op":"true"},`, n), ",") + `]}`
}

// #2600: the parser caps depth and node count, for exceptions and for
// credential predicates alike.
func TestValidateExpr_SizeCap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		ok   bool
	}{
		{"at depth cap", nestedNot(MaxExprDepth - 1), true},
		{"past depth cap", nestedNot(MaxExprDepth), false},
		{"far past depth cap", nestedNot(5000), false},
		{"at node cap", wideOr(MaxExprNodes - 1), true},
		{"past node cap", wideOr(MaxExprNodes), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExpr([]byte(tc.expr))
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrBadPredicate)) {
				t.Errorf("ValidateExpr = %v, want ok=%v", err, tc.ok)
			}
			ok, err := MatchRecordScope(tc.expr, map[string][]string{"environment": {"prod"}})
			if tc.ok == (err != nil) {
				t.Errorf("MatchRecordScope err = %v, want ok=%v", err, tc.ok)
			}
			if !tc.ok && ok {
				t.Error("MatchRecordScope admitted an over-cap predicate")
			}
		})
	}
}

// #2656: PredicateWithin bounds a key-minted key's scope by the caller's.
func TestPredicateWithin(t *testing.T) {
	t.Parallel()
	const (
		prod        = `{"op":"eq","dim":"environment","value":"prod"}`
		staging     = `{"op":"eq","dim":"environment","value":"staging"}`
		prodStaging = `{"op":"in","dim":"environment","values":["prod","staging"]}`
		aws1        = `{"op":"eq","dim":"cloud_account","value":"aws:1"}`
		prodAndAWS  = `{"op":"and","args":[` + prod + `,` + aws1 + `]}`
		prodOrStg   = `{"op":"or","args":[` + prod + `,` + staging + `]}`
		notProd     = `{"op":"not","arg":` + prod + `}`
		opTrue      = `{"op":"true"}`
	)
	cases := []struct {
		name          string
		child, parent string
		want          bool
	}{
		{"both unrestricted", "{}", "", true},
		{"anything within unrestricted", prodAndAWS, "{}", true},
		{"unrestricted not within restricted", "{}", prod, false},
		{"unrestricted not within op true", "", opTrue, false},
		{"restricted within op true", prod, opTrue, true},
		{"op true not within eq", opTrue, prod, false},
		{"equal eq", prod, prod, true},
		{"other value", staging, prod, false},
		{"other dim", aws1, prod, false},
		{"eq within in", prod, prodStaging, true},
		{"in not within eq", prodStaging, prod, false},
		{"in within wider in", `{"op":"in","dim":"environment","values":["prod"]}`, prodStaging, true},
		{"and narrows", prodAndAWS, prod, true},
		{"and within and", prodAndAWS, prodAndAWS, true},
		{"eq not within and", prod, prodAndAWS, false},
		{"or within in", prodOrStg, prodStaging, true},
		{"in within or", prodStaging, prodOrStg, false},
		{"or not within eq", prodOrStg, prod, false},
		{"equal not", notProd, notProd, true},
		{"not not within eq", notProd, staging, false},
		{"legacy within its expression", "environment=prod", prod, true},
	}
	for _, tc := range cases {
		got, err := PredicateWithin(tc.child, tc.parent)
		if err != nil || got != tc.want {
			t.Errorf("%s: PredicateWithin(%s, %s) = %v, %v; want %v", tc.name, tc.child, tc.parent, got, err, tc.want)
		}
	}
	if _, err := PredicateWithin("nonsense", prod); !errors.Is(err, ErrBadPredicate) {
		t.Errorf("malformed child: err = %v, want ErrBadPredicate", err)
	}
	if _, err := PredicateWithin(prod, "nonsense"); !errors.Is(err, ErrBadPredicate) {
		t.Errorf("malformed parent: err = %v, want ErrBadPredicate", err)
	}
}
