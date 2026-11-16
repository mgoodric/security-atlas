package scope

import (
	"errors"
	"fmt"
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
