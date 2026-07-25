package incidents

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAPI struct {
	incidents []RawIncident
	err       error
	gotSince  time.Time
	gotUntil  time.Time
}

func (f *fakeAPI) ListIncidents(_ context.Context, since, until time.Time) ([]RawIncident, error) {
	f.gotSince, f.gotUntil = since, until
	return f.incidents, f.err
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, time.Now(), time.Now()); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("429")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}, time.Now(), time.Now()); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_NormalizesAndPassesWindow(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	resolved := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)
	api := &fakeAPI{incidents: []RawIncident{
		{ID: "INC1", Number: 42, Status: "RESOLVED", Urgency: "High", ServiceID: "SVC1", ServiceName: "API", CreatedAt: created, ResolvedAt: resolved},
		{ID: "INC2", Number: 43, Status: "weird", Urgency: "low", CreatedAt: created},
		{ID: "", Status: "triggered"}, // dropped: blank id
	}}
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	got, err := Collect(context.Background(), api, since, until)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(since) || !api.gotUntil.Equal(until) {
		t.Errorf("window not threaded: %v..%v", api.gotSince, api.gotUntil)
	}
	if len(got) != 2 {
		t.Fatalf("got %d; want 2 (blank-id dropped)", len(got))
	}
	if got[0].Status != "resolved" || got[0].Urgency != "high" {
		t.Errorf("inc0 = %+v", got[0])
	}
	// Unknown status coerces to "triggered"; unknown urgency to "high".
	if got[1].Status != "triggered" || got[1].Urgency != "low" {
		t.Errorf("inc1 = %+v", got[1])
	}
	if got[1].ResolvedAt.IsZero() != true {
		t.Errorf("inc1 should have zero ResolvedAt")
	}
}

func TestNormalizeStatusUrgency(t *testing.T) {
	t.Parallel()
	if normalizeStatus("ACKNOWLEDGED") != "acknowledged" {
		t.Error("ack")
	}
	if normalizeStatus("") != "triggered" {
		t.Error("default status")
	}
	if normalizeUrgency("LOW") != "low" {
		t.Error("low")
	}
	if normalizeUrgency("") != "high" {
		t.Error("default urgency")
	}
}
