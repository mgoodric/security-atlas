package controls

import (
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// #2131 — the `?include=state` filters and sort are allow-listed before
// any SQL runs. The Postgres-backed behaviour is in
// list_filter_sort_integration_test.go.
func TestParseListFilter(t *testing.T) {
	t.Parallel()
	ok := []struct {
		query string
		check func(f control.ListFilter) bool
	}{
		{"", func(f control.ListFilter) bool { return f.Sort == control.SortDefault && !f.SortDesc }},
		{"sort=name", func(f control.ListFilter) bool { return f.Sort == control.SortName && !f.SortDesc }},
		{"sort=-last_observed_at", func(f control.ListFilter) bool { return f.Sort == control.SortLastObservedAt && f.SortDesc }},
		{"owner=none", func(f control.ListFilter) bool { return f.Unowned && !f.Owner.Valid }},
		{"owner=6f1c1f0e-0000-4000-8000-000000000001", func(f control.ListFilter) bool { return f.Owner.Valid && !f.Unowned }},
		{"result=excepted&freshness=no_evidence", func(f control.ListFilter) bool {
			return f.Result == "excepted" && f.Freshness == "no_evidence"
		}},
		{"family=IAC&framework=soc2", func(f control.ListFilter) bool { return f.Family == "IAC" && f.Framework == "soc2" }},
	}
	for _, tc := range ok {
		r := httptest.NewRequest("GET", "/v1/controls?include=state&"+tc.query, nil)
		f, msg := parseListFilter(r)
		if msg != "" {
			t.Errorf("%q: refused: %s", tc.query, msg)
			continue
		}
		if !tc.check(f) {
			t.Errorf("%q: parsed %+v", tc.query, f)
		}
	}

	for _, query := range []string{
		"sort=title",
		"sort=--name",
		"sort=-",
		"sort=name%20desc",
		"result=insufficient_evidence",
		"freshness=old",
		"owner=me",
		"scope=not-a-uuid",
		"anchor_fit=loose",
	} {
		r := httptest.NewRequest("GET", "/v1/controls?include=state&"+query, nil)
		if _, msg := parseListFilter(r); msg == "" {
			t.Errorf("%q: accepted, want refused", query)
		}
	}
}
