package controls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeRemapLister returns the first f.Limit of rows and records the filter.
type fakeRemapLister struct {
	rows []dbx.ListSCFRemapProposalsRow
	got  control.RemapProposalFilter
}

func (f *fakeRemapLister) ListRemapProposals(_ context.Context, in control.RemapProposalFilter) ([]dbx.ListSCFRemapProposalsRow, int64, error) {
	f.got = in
	return f.rows[:min(int(in.Limit), len(f.rows))], int64(len(f.rows)), nil
}

func remapRow(bundle string, at time.Time) dbx.ListSCFRemapProposalsRow {
	return dbx.ListSCFRemapProposalsRow{
		ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ControlID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		BundleID:  bundle,
		CreatedAt: pgtype.Timestamptz{Time: at, Valid: true},
	}
}

func listRemapProposals(t *testing.T, f *fakeRemapLister, query string) (*httptest.ResponseRecorder, RemapProposalsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/remap-proposals"+query, nil)
	ctx, err := tenancy.WithTenant(req.Context(), remapTestTenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rr := httptest.NewRecorder()
	NewRemapProposalsHandler(f).List(rr, req.WithContext(ctx))
	var out RemapProposalsResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rr, out
}

func TestRemapProposalsList_PagesWithACursorTheNextCallDecodes(t *testing.T) {
	at := time.Date(2026, 11, 17, 12, 0, 0, 123456000, time.UTC)
	f := &fakeRemapLister{rows: []dbx.ListSCFRemapProposalsRow{remapRow("a", at), remapRow("b", at), remapRow("c", at)}}

	rr, page := listRemapProposals(t, f, "?limit=2&class_not=unchanged&status=rebound")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if f.got.Limit != 3 || f.got.After != nil || *f.got.ClassNot != "unchanged" || *f.got.Status != "rebound" {
		t.Errorf("filter %+v; want limit 3 (one past the page), no cursor, class_not and status set", f.got)
	}
	if page.Count != 2 || page.Total != 3 || page.NextCursor == "" {
		t.Fatalf("page count %d total %d next %q; want 2 of 3 with a cursor", page.Count, page.Total, page.NextCursor)
	}

	_, _ = listRemapProposals(t, f, "?limit=2&cursor="+page.NextCursor)
	want := control.RemapProposalCursor{CreatedAt: at, BundleID: "b", ID: uuid.UUID(f.rows[1].ID.Bytes)}
	if f.got.After == nil || !f.got.After.CreatedAt.Equal(want.CreatedAt) || f.got.After.BundleID != want.BundleID || f.got.After.ID != want.ID {
		t.Errorf("cursor decoded to %+v; want %+v", f.got.After, want)
	}

	if _, last := listRemapProposals(t, f, "?limit=5"); last.NextCursor != "" || last.Count != 3 {
		t.Errorf("last page next %q count %d; want no cursor and 3 rows", last.NextCursor, last.Count)
	}
	if _, def := listRemapProposals(t, f, ""); f.got.Limit != stateListDefaultLimit+1 || def.Count != 3 {
		t.Errorf("default limit %d; want %d", f.got.Limit, stateListDefaultLimit+1)
	}
	if _, _ = listRemapProposals(t, f, "?limit=100000"); f.got.Limit != stateListMaxLimit+1 {
		t.Errorf("capped limit %d; want %d", f.got.Limit, stateListMaxLimit+1)
	}
}

func TestRemapProposalsList_RejectsUnknownFiltersAndForeignCursors(t *testing.T) {
	f := &fakeRemapLister{}
	for _, q := range []string{"?class_not=moved", "?cursor=not*base64", "?cursor=bm90LWpzb24", "?cursor=e30"} {
		if rr, _ := listRemapProposals(t, f, q); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d; want 400", q, rr.Code)
		}
	}
}
