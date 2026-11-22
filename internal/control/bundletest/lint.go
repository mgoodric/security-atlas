// lint.go — warnings about a bundle's test cases that run but cannot show
// what the author means them to (#2882).
package bundletest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mgoodric/security-atlas/internal/eval"
)

// KindlessRecordWarnings returns one warning per test case that has a record
// without evidence_kind, when the queries declare two or more distinct kinds.
//
// A fixture record without a kind counts as every kind (decisions log 2818
// D4), while a live record without one reaches only a kindless query (D3).
// In a bundle whose queries read two kinds, such a record reaches both
// queries in the test and neither live, so the case never exercises the
// per-query kind split and can pass where a live evaluation would differ.
// A bundle with one kind has no split to miss, so it gets no warning.
func KindlessRecordWarnings(queries []eval.FixtureQuery, cases []TestCase) []string {
	kinds := map[string]struct{}{}
	for _, q := range queries {
		if q.EvidenceKind != "" {
			kinds[q.EvidenceKind] = struct{}{}
		}
	}
	if len(kinds) < 2 {
		return nil
	}
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)

	var out []string
	for i := range cases {
		var idx []string
		for j, r := range cases[i].Records {
			if r.EvidenceKind == "" {
				idx = append(idx, strconv.Itoa(j))
			}
		}
		if len(idx) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("test case %q: records[%s] omit evidence_kind; the queries read %s, and a fixture record without a kind reaches every query while a live one reaches none of them, so set evidence_kind on each record",
			cases[i].Name, strings.Join(idx, ", "), strings.Join(names, ", ")))
	}
	return out
}
