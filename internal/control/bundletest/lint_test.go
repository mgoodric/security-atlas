package bundletest

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/eval"
)

func TestKindlessRecordWarnings(t *testing.T) {
	t.Parallel()
	twoKinds := []eval.FixtureQuery{
		{Language: "rego", EvidenceKind: "a.v1"},
		{Language: "rego", EvidenceKind: "b.v1"},
	}
	kinded := []FixtureRecord{{Result: "pass", EvidenceKind: "a.v1"}, {Result: "pass", EvidenceKind: "b.v1"}}
	mixed := []FixtureRecord{{Result: "pass", EvidenceKind: "a.v1"}, {Result: "pass"}, {Result: "fail"}}

	tests := []struct {
		name    string
		queries []eval.FixtureQuery
		cases   []TestCase
		want    []string // substrings, one per warning
	}{
		{"two kinds, every record kinded", twoKinds, []TestCase{{Name: "c", Records: kinded}}, nil},
		{"two kinds, kindless records", twoKinds, []TestCase{{Name: "c", Records: mixed}}, []string{`test case "c": records[1, 2]`}},
		{"one warning per case", twoKinds, []TestCase{{Name: "c1", Records: mixed}, {Name: "c2", Records: kinded}, {Name: "c3", Records: mixed[1:]}},
			[]string{`"c1": records[1, 2]`, `"c3": records[0, 1]`}},
		{"same kind twice is one kind", []eval.FixtureQuery{{EvidenceKind: "a.v1"}, {EvidenceKind: "a.v1"}}, []TestCase{{Name: "c", Records: mixed}}, nil},
		{"one kinded and one kindless query", []eval.FixtureQuery{{EvidenceKind: "a.v1"}, {}}, []TestCase{{Name: "c", Records: mixed}}, nil},
		{"no queries", nil, []TestCase{{Name: "c", Records: mixed}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := KindlessRecordWarnings(tc.queries, tc.cases)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d warnings %q, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) || !strings.Contains(got[i], "a.v1, b.v1") {
					t.Errorf("warning %d = %q, want it to contain %q and name the kinds a.v1, b.v1", i, got[i], w)
				}
			}
		})
	}
}

// The warning reaches the report both entry points return.
func TestRunFromFiles_ReportsKindlessRecordWarnings(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{"t.yaml": []byte(`cases:
  - name: kindless
    expected_state: pass
    records:
      - result: pass
`)}
	queries := []eval.FixtureQuery{
		{Language: "rego", EvidenceKind: "a.v1", Expression: rollupRego},
		{Language: "rego", EvidenceKind: "b.v1", Expression: rollupRego},
	}
	rep, err := RunFromFiles(t.Context(), "b", "quarterly", queries, files, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], `"kindless": records[0]`) {
		t.Fatalf("warnings = %q, want one naming case kindless records[0]", rep.Warnings)
	}
}

const rollupRego = `package evidence.query

result := "pass" if {
	some r in input.records
	r.result == "pass"
} else := "inconclusive"
`
