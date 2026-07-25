package worker

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 34, 0, 0, time.UTC) }
}

func TestNormalize_StampsHRISAndTruncatesObservedAt(t *testing.T) {
	t.Parallel()
	got := Normalize(HRISRippling, []RawWorker{
		{WorkerID: "w1", Status: StatusActive, Title: "SWE", Department: "Eng"},
	}, fixedClock())
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	w := got[0]
	if w.SourceHRIS != HRISRippling {
		t.Errorf("SourceHRIS = %q", w.SourceHRIS)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !w.ObservedAt.Equal(want) {
		t.Errorf("ObservedAt = %v; want %v (hour-truncated)", w.ObservedAt, want)
	}
}

func TestNormalize_DropsWorkersMissingID(t *testing.T) {
	t.Parallel()
	got := Normalize(HRISBambooHR, []RawWorker{
		{WorkerID: ""},
		{WorkerID: "  "},
		{WorkerID: "keep"},
	}, fixedClock())
	if len(got) != 1 || got[0].WorkerID != "keep" {
		t.Fatalf("got %+v; want only [keep]", got)
	}
}

func TestNormalize_SortsByWorkerID(t *testing.T) {
	t.Parallel()
	got := Normalize(HRISRippling, []RawWorker{
		{WorkerID: "c"}, {WorkerID: "a"}, {WorkerID: "b"},
	}, fixedClock())
	if got[0].WorkerID != "a" || got[1].WorkerID != "b" || got[2].WorkerID != "c" {
		t.Errorf("not sorted: %v", []string{got[0].WorkerID, got[1].WorkerID, got[2].WorkerID})
	}
}

func TestNormalize_StatusFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   EmploymentStatus
		want EmploymentStatus
	}{
		{StatusActive, StatusActive},
		{StatusTerminated, StatusTerminated},
		{StatusOnLeave, StatusOnLeave},
		{StatusPending, StatusPending},
		{StatusUnknown, StatusUnknown},
		{"", StatusUnknown},
		{"garbage", StatusUnknown},
	}
	for _, c := range cases {
		got := Normalize(HRISRippling, []RawWorker{{WorkerID: "x", Status: c.in}}, fixedClock())
		if got[0].Status != c.want {
			t.Errorf("status %q -> %q; want %q", c.in, got[0].Status, c.want)
		}
	}
}

func TestNormalize_TrimsWhitespaceOptionalFields(t *testing.T) {
	t.Parallel()
	got := Normalize(HRISBambooHR, []RawWorker{
		{WorkerID: " w1 ", Title: " SWE ", Department: " Eng ", ManagerAssignmentID: " m-1 ", WorkEmail: " a@corp.example "},
	}, fixedClock())
	w := got[0]
	if w.WorkerID != "w1" || w.Title != "SWE" || w.Department != "Eng" || w.ManagerAssignmentID != "m-1" || w.WorkEmail != "a@corp.example" {
		t.Errorf("whitespace not trimmed: %+v", w)
	}
}

func TestNormalize_NilClockUsesWallClock(t *testing.T) {
	t.Parallel()
	got := Normalize(HRISRippling, []RawWorker{{WorkerID: "w1"}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("ObservedAt should be set from wall clock")
	}
}

func TestNormalize_DatesNormalizedToUTC(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("X", 5*3600)
	got := Normalize(HRISRippling, []RawWorker{
		{WorkerID: "w1", StartDate: time.Date(2024, 1, 2, 0, 0, 0, 0, loc), EndDate: time.Date(2025, 6, 1, 0, 0, 0, 0, loc)},
	}, fixedClock())
	if got[0].StartDate.Location() != time.UTC || got[0].EndDate.Location() != time.UTC {
		t.Errorf("dates not normalized to UTC: %+v", got[0])
	}
}

// TestRawWorker_HasNoSensitivePIIField is the structural over-collection guard
// (P0-491-3): a banned-PII field would be a compile error, but to make the
// guarantee an executable assertion we reflect over the struct and fail if any
// field name matches a sensitive-PII concept. A new field accidentally named
// "Salary" / "Ssn" / "HomeAddress" trips this immediately.
func TestRawWorker_HasNoSensitivePIIField(t *testing.T) {
	t.Parallel()
	banned := []string{
		"ssn", "nationalid", "national_id", "salary", "compensation", "comp",
		"pay", "wage", "bonus", "bank", "account", "routing", "iban",
		"address", "street", "zip", "postal", "benefit", "health", "insurance",
		"performance", "rating", "review", "dob", "birth", "gender", "ethnicity",
		"race", "personalphone", "personal_phone", "homephone", "home_phone",
		"mobile", "cellphone",
	}
	assertNoBannedFieldNames(t, reflect.TypeOf(RawWorker{}), banned)
	assertNoBannedFieldNames(t, reflect.TypeOf(Worker{}), banned)
}

func assertNoBannedFieldNames(t *testing.T, typ reflect.Type, banned []string) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, b := range banned {
			if strings.Contains(name, b) {
				t.Errorf("%s has field %q matching banned sensitive-PII concept %q (P0-491-3)", typ.Name(), typ.Field(i).Name, b)
			}
		}
	}
}
