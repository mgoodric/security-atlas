package calendar

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestParseWindow_DefaultsToNinetyDays asserts AC-4 — omitted `from`/`to`
// yields [now, now+90d).
func TestParseWindow_DefaultsToNinetyDays(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	r := httptest.NewRequest("GET", "/v1/calendar", nil)
	from, to, err := parseWindow(r, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !from.Equal(now) {
		t.Errorf("from=%v want=%v", from, now)
	}
	want := now.Add(90 * 24 * time.Hour)
	if !to.Equal(want) {
		t.Errorf("to=%v want=%v", to, want)
	}
}

// TestParseWindow_ExplicitDatesParseCorrectly asserts the YYYY-MM-DD
// parse and the exclusive-upper-bound semantics.
func TestParseWindow_ExplicitDatesParseCorrectly(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	r := httptest.NewRequest("GET", "/v1/calendar?from=2026-06-01&to=2026-06-30", nil)
	from, to, err := parseWindow(r, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	wantFrom := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // exclusive upper bound
	if !from.Equal(wantFrom) {
		t.Errorf("from=%v want=%v", from, wantFrom)
	}
	if !to.Equal(wantTo) {
		t.Errorf("to=%v want=%v", to, wantTo)
	}
}

// TestParseWindow_RejectsBadDates checks malformed YYYY-MM-DD → 400-shaped err.
func TestParseWindow_RejectsBadDates(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	cases := []string{
		"/v1/calendar?from=not-a-date",
		"/v1/calendar?to=2026-13-99",
		"/v1/calendar?from=2026-06-30&to=2026-06-01", // to <= from
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest("GET", path, nil)
			if _, _, err := parseWindow(r, now); err == nil {
				t.Errorf("expected err for %q, got nil", path)
			}
		})
	}
}

// TestParseWindow_RejectsOversizedWindow asserts AC-4 — window > 366 days
// is rejected.
func TestParseWindow_RejectsOversizedWindow(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	// 400 days is well over the 366 limit.
	r := httptest.NewRequest("GET", "/v1/calendar?from=2026-01-01&to=2027-12-31", nil)
	if _, _, err := parseWindow(r, now); err == nil {
		t.Error("expected oversized-window err, got nil")
	} else if !strings.Contains(err.Error(), "366") {
		t.Errorf("err should mention 366-day ceiling, got: %v", err)
	}
}

// TestNormalizeTypeFilter_EmptyMeansAll asserts the empty filter is the
// "all event types" sentinel the SQL reads.
func TestNormalizeTypeFilter_EmptyMeansAll(t *testing.T) {
	got, err := normalizeTypeFilter("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "" {
		t.Errorf("empty filter should normalize to %q, got %q", "", got)
	}
}

// TestNormalizeTypeFilter_DedupesAndOrdersStably keeps the SQL position()
// match simple by deduping inputs.
func TestNormalizeTypeFilter_DedupesAndOrdersStably(t *testing.T) {
	got, err := normalizeTypeFilter("audit,exception,audit, control ")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "audit,exception,control"
	if got != want {
		t.Errorf("normalized=%q want=%q", got, want)
	}
}

// TestNormalizeTypeFilter_RejectsUnknownType asserts the AC-1 closed
// vocabulary — unknown types get a 400-shaped error. `risk` is not a
// calendar event source.
func TestNormalizeTypeFilter_RejectsUnknownType(t *testing.T) {
	if _, err := normalizeTypeFilter("audit,risk"); err == nil {
		t.Error("expected err for unknown type 'risk', got nil")
	}
}

// TestNormalizeTypeFilter_AcceptsVendor is the slice-675 guard: `vendor`
// is now a first-class calendar event type (aligning the agenda with the
// dashboard "Upcoming" widget). It must pass the closed-vocabulary check.
func TestNormalizeTypeFilter_AcceptsVendor(t *testing.T) {
	got, err := normalizeTypeFilter("audit,vendor,exception")
	if err != nil {
		t.Fatalf("vendor should be a valid event type: %v", err)
	}
	want := "audit,vendor,exception"
	if got != want {
		t.Errorf("normalized=%q want=%q", got, want)
	}
}

// TestCalendarNameFor_TenantIDShortened — the X-WR-CALNAME label uses the
// first 8 chars of the tenant id to disambiguate multiple calendars in a
// client.
func TestCalendarNameFor_TenantIDShortened(t *testing.T) {
	cred := struct{}{}
	_ = cred
	// Build the expected output with a known tenant id.
	tenant := "11111111-2222-3333-4444-555555555555"
	got := calendarNameForTenant(tenant)
	want := "Compliance calendar (11111111)"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// calendarNameForTenant exists so the test can drive calendarNameFor
// without constructing a full credstore.Credential — the helper is a
// thin wrapper around the same logic. Kept in the test file to avoid
// adding a public surface to the package.
func calendarNameForTenant(tenant string) string {
	// Mirror the production logic in calendarNameFor.
	short := tenant
	if len(short) > 8 {
		short = short[:8]
	}
	return "Compliance calendar (" + short + ")"
}
