package controls

// Issue #1741 — pure-Go branches of GET /v1/controls?include=state: the
// include parser, the pager, and the row-to-wire conversion. No Postgres.

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestIncludesState(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"/v1/controls":                           false,
		"/v1/controls?include=state":             true,
		"/v1/controls?include=coverage,%20state": true,
		"/v1/controls?include=stateful":          false,
	}
	for target, want := range cases {
		if got := includesState(httptest.NewRequest("GET", target, nil)); got != want {
			t.Errorf("%s: includesState = %v; want %v", target, got, want)
		}
	}
}

func TestListPagination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query                 string
		wantLimit, wantOffset int32
	}{
		{"", 100, 0},
		{"limit=25&offset=50", 25, 50},
		{"limit=10000", 500, 0},
		{"limit=-1&offset=-5", 100, 0},
		{"limit=abc&offset=xyz", 100, 0},
	}
	for _, c := range cases {
		limit, offset := listPagination(httptest.NewRequest("GET", "/v1/controls?"+c.query, nil))
		if limit != c.wantLimit || offset != c.wantOffset {
			t.Errorf("%q: got (%d, %d); want (%d, %d)", c.query, limit, offset, c.wantLimit, c.wantOffset)
		}
	}
}

func TestControlWithStateWireFrom(t *testing.T) {
	t.Parallel()
	id, anchor := uuid.New(), uuid.New()
	scf := "IAC-06"
	result := dbx.EvidenceResultFail
	fresh := "stale"
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	bare := controlWithStateWireFrom(dbx.ListActiveControlsWithStateRow{
		ID:       pgtype.UUID{Bytes: id, Valid: true},
		BundleID: "b1",
		Title:    "No anchor, no evaluation",
	})
	if bare.State != nil || bare.SCFAnchorID != nil || bare.SCFID != "" {
		t.Fatalf("bare row = %+v; want null state, null anchor, empty scf_id", bare)
	}
	if bare.Frameworks == nil || len(bare.Frameworks) != 0 {
		t.Fatalf("bare frameworks = %#v; want empty non-nil slice", bare.Frameworks)
	}

	full := controlWithStateWireFrom(dbx.ListActiveControlsWithStateRow{
		ID:                   pgtype.UUID{Bytes: id, Valid: true},
		ScfID:                &scf,
		ScfAnchorID:          pgtype.UUID{Bytes: anchor, Valid: true},
		StateResult:          &result,
		StateFreshnessStatus: &fresh,
		StateLastObservedAt:  pgtype.Timestamptz{Time: at, Valid: true},
		StateEvaluatedAt:     pgtype.Timestamptz{Time: at, Valid: true},
		FrameworkSlugs:       []string{"soc2"},
	})
	if full.ID != id.String() || full.SCFID != scf {
		t.Fatalf("identity = %s/%s; want %s/%s", full.ID, full.SCFID, id, scf)
	}
	if full.SCFAnchorID == nil || *full.SCFAnchorID != anchor.String() {
		t.Fatalf("scf_anchor_id = %v; want %s", full.SCFAnchorID, anchor)
	}
	if full.State == nil || full.State.Result != "fail" || full.State.FreshnessStatus != "stale" {
		t.Fatalf("state = %+v; want fail/stale", full.State)
	}
	if full.State.LastObservedAt == nil || *full.State.LastObservedAt != at.Format(time.RFC3339Nano) {
		t.Fatalf("last_observed_at = %v; want %s", full.State.LastObservedAt, at.Format(time.RFC3339Nano))
	}
	if len(full.Frameworks) != 1 {
		t.Fatalf("frameworks = %v; want one display code", full.Frameworks)
	}
}
