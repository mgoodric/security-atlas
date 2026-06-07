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
	sentinel := errors.New("403")
	_, err := Collect(context.Background(), &fakeAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_MapsToSharedRawWorker(t *testing.T) {
	t.Parallel()
	out, err := Collect(context.Background(), &fakeAPI{out: []RawWorker{
		{ID: "emp-1", EmploymentStatus: "ACTIVE", StartDate: "2024-01-15", Title: "SWE", Department: "Eng", ManagerAssignmentID: "m-1", WorkEmail: "a@corp.example"},
		{ID: "", EmploymentStatus: "ACTIVE"}, // dropped
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d; want 1 (empty id dropped)", len(out))
	}
	w := out[0]
	if w.WorkerID != "emp-1" || w.Status != worker.StatusActive || w.Title != "SWE" || w.WorkEmail != "a@corp.example" {
		t.Errorf("mapped worker wrong: %+v", w)
	}
	if w.StartDate.IsZero() {
		t.Error("start date should parse")
	}
}

func TestMapStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]worker.EmploymentStatus{
		"ACTIVE":     worker.StatusActive,
		"active":     worker.StatusActive,
		"TERMINATED": worker.StatusTerminated,
		"OFFBOARDED": worker.StatusTerminated,
		"LEAVE":      worker.StatusOnLeave,
		"ON_LEAVE":   worker.StatusOnLeave,
		"PENDING":    worker.StatusPending,
		"PREHIRE":    worker.StatusPending,
		"ACCEPTED":   worker.StatusPending,
		"garbage":    worker.StatusUnknown,
		"":           worker.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	if parseDate("2024-01-15").IsZero() {
		t.Error("ISO date should parse")
	}
	if parseDate("2024-01-15T00:00:00Z").IsZero() {
		t.Error("RFC3339 should parse")
	}
	if !parseDate("").IsZero() {
		t.Error("empty should be zero")
	}
	if !parseDate("not-a-date").IsZero() {
		t.Error("garbage should be zero")
	}
}
