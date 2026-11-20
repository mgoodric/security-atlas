package adminauditlog

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// riskOff is a switch store with the Risk module switched off.
type riskOff struct{}

func (riskOff) Get(_ context.Context, key string) (featureflag.Flag, error) {
	return featureflag.Flag{Key: key, Enabled: key != "risk.enabled"}, nil
}

func riskOffSet(t *testing.T) moduleaccess.ModuleSet {
	t.Helper()
	set, err := moduleaccess.NewViews(nil, riskOff{}).Switches(httptest.NewRequest("GET", "/v1/activity/unified", nil))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// fakeLog serves rows newest first, resuming after the cursor's row, as
// unifiedlog.Query does.
func fakeLog(rows []unifiedlog.Entry, reads *int) func(unifiedlog.QueryParams) ([]unifiedlog.Entry, error) {
	return func(p unifiedlog.QueryParams) ([]unifiedlog.Entry, error) {
		*reads++
		start := 0
		if p.Cursor != nil {
			for i, e := range rows {
				if e.RowID == p.Cursor.RowID {
					start = i + 1
				}
			}
		}
		end := min(start+p.Limit, len(rows))
		return rows[start:end], nil
	}
}

// #2590: Activity pages are cut after hidden rows are skipped, so every
// page but the last is full and only the last has no cursor.
func TestActivityPage_FillsPagesPastHiddenRows(t *testing.T) {
	t.Parallel()
	set := riskOffSet(t)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var rows []unifiedlog.Entry
	for i := range 12 {
		kind := unifiedlog.KindEvidence
		if i%3 != 0 {
			kind = unifiedlog.KindRisk
		}
		rows = append(rows, unifiedlog.Entry{Kind: kind, RowID: uuid.New(), OccurredAt: base.Add(-time.Duration(i) * time.Minute)})
	}
	const pageSize = 2
	p := unifiedlog.QueryParams{Limit: pageSize + 1}
	var got []unifiedlog.Entry
	for page := 0; page < 10; page++ {
		reads := 0
		entries, next, err := activityPage(p, pageSize, set, fakeLog(rows, &reads))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, entries...)
		if next == nil {
			break
		}
		if len(entries) != pageSize {
			t.Fatalf("page %d: %d rows with a cursor; want %d", page, len(entries), pageSize)
		}
		p.Cursor = next
	}
	if len(got) != 4 {
		t.Fatalf("paged %d rows; want the 4 evidence rows", len(got))
	}
	for _, e := range got {
		if e.Kind != unifiedlog.KindEvidence {
			t.Errorf("risk row shown with Risk switched off")
		}
	}
}

// A run of hidden rows longer than the scan cap ends the page early,
// empty, with a cursor past what it read.
func TestActivityPage_ScanCap(t *testing.T) {
	t.Parallel()
	hidden := make([]unifiedlog.Entry, 0, 100)
	for range 100 {
		hidden = append(hidden, unifiedlog.Entry{Kind: unifiedlog.KindRisk, RowID: uuid.New()})
	}
	reads := 0
	entries, next, err := activityPage(unifiedlog.QueryParams{Limit: 3}, 2, riskOffSet(t), fakeLog(hidden, &reads))
	if err != nil || len(entries) != 0 || next == nil || reads != maxActivityScans {
		t.Errorf("all hidden: %d rows, cursor %v, %d reads, err %v; want 0, a cursor, %d reads",
			len(entries), next, reads, err, maxActivityScans)
	}
	if next != nil && next.RowID != hidden[3*maxActivityScans-1].RowID {
		t.Errorf("cursor is not at the last row read")
	}
}

// #2590: the admin log marks rows whose module is switched off and
// leaves the rest unmarked.
func TestMarkSwitchedOff(t *testing.T) {
	t.Parallel()
	got := markSwitchedOff([]unifiedlog.Entry{
		{Kind: unifiedlog.KindRisk},
		{Kind: unifiedlog.KindEvidence},
		{Kind: unifiedlog.KindDecision, TargetType: "risks"},
		{Kind: unifiedlog.KindVendor},
	}, riskOffSet(t))
	if want := []bool{true, false, true, false}; !slices.Equal(got, want) {
		t.Errorf("markSwitchedOff = %v; want %v", got, want)
	}
}
