package anchors

// Issue #1741 — pure-Go branches of the `?has_control=` filter: the
// parser and the filtered total. No Postgres.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestHasControlParam(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	cases := []struct {
		query    string
		want     *bool
		wantCode int
	}{
		{"", nil, 0},
		{"has_control=true", ptr(true), 0},
		{"has_control=false", ptr(false), 0},
		{"has_control=1", ptr(true), 0},
		{"has_control=maybe", nil, http.StatusBadRequest},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		got, ok := h.hasControl(rr, httptest.NewRequest("GET", "/v1/anchors?"+c.query, nil))
		if c.wantCode != 0 {
			if ok || rr.Code != c.wantCode {
				t.Errorf("%q: ok=%v code=%d; want rejected with %d", c.query, ok, rr.Code, c.wantCode)
			}
			continue
		}
		if !ok {
			t.Errorf("%q: rejected; want accepted", c.query)
			continue
		}
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%q: got %v; want %v", c.query, got, c.want)
		}
	}
}

func TestFilteredAnchorTotal(t *testing.T) {
	t.Parallel()
	counts := dbx.CountSCFAnchorsLatestRow{CatalogTotal: 1400, WithControlTotal: 39}
	if got := filteredAnchorTotal(counts, nil); got != 1400 {
		t.Errorf("no filter: %d; want 1400", got)
	}
	if got := filteredAnchorTotal(counts, ptr(true)); got != 39 {
		t.Errorf("has_control=true: %d; want 39", got)
	}
	if got := filteredAnchorTotal(counts, ptr(false)); got != 1361 {
		t.Errorf("has_control=false: %d; want 1361", got)
	}
}

func ptr[T any](v T) *T { return &v }
