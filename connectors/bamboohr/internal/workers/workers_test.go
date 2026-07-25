package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

type fakeAPI struct {
	out []RawWorker
	err error
}

func (f *fakeAPI) ListWorkers(context.Context) ([]RawWorker, error) { return f.out, f.err }

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_WrapsAPIError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("401")
	_, err := Collect(context.Background(), &fakeAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_MapsToSharedRawWorker(t *testing.T) {
	t.Parallel()
	out, err := Collect(context.Background(), &fakeAPI{out: []RawWorker{
		{ID: "42", Status: "Active", HireDate: "2024-01-15", JobTitle: "SWE", Department: "Eng", ManagerAssignmentID: "7", WorkEmail: "a@corp.example"},
		{ID: "", Status: "Active"}, // dropped
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d; want 1 (empty id dropped)", len(out))
	}
	w := out[0]
	if w.WorkerID != "42" || w.Status != worker.StatusActive || w.Title != "SWE" || w.WorkEmail != "a@corp.example" {
		t.Errorf("mapped worker wrong: %+v", w)
	}
	if w.StartDate.IsZero() {
		t.Error("hire date should parse")
	}
}

func TestMapStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status, termDate string
		want             worker.EmploymentStatus
	}{
		{"Active", "", worker.StatusActive},
		{"active", "", worker.StatusActive},
		{"Inactive", "2026-05-31", worker.StatusTerminated},
		{"Inactive", "", worker.StatusOnLeave},
		{"Inactive", "0000-00-00", worker.StatusOnLeave}, // sentinel = no term date stored, but raw value present
		{"garbage", "", worker.StatusUnknown},
		{"", "", worker.StatusUnknown},
	}
	for _, c := range cases {
		if got := mapStatus(c.status, c.termDate); got != c.want {
			t.Errorf("mapStatus(%q, %q) = %q; want %q", c.status, c.termDate, got, c.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	if parseDate("2024-01-15").IsZero() {
		t.Error("ISO date should parse")
	}
	if !parseDate("0000-00-00").IsZero() {
		t.Error("BambooHR sentinel should be zero")
	}
	if !parseDate("").IsZero() {
		t.Error("empty should be zero")
	}
	if !parseDate("not-a-date").IsZero() {
		t.Error("garbage should be zero")
	}
}
