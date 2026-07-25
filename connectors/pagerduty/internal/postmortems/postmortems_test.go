package postmortems

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAPI struct {
	postmortems []RawPostmortem
	err         error
	gotSince    time.Time
	gotUntil    time.Time
}

func (f *fakeAPI) ListPostmortems(_ context.Context, since, until time.Time) ([]RawPostmortem, error) {
	f.gotSince, f.gotUntil = since, until
	return f.postmortems, f.err
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

func TestCollect_NormalizesRollupAndWindow(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	published := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	api := &fakeAPI{postmortems: []RawPostmortem{
		{
			ID: "PM1", IncidentID: "INC1", Status: "PUBLISHED",
			CreatedAt: created, PublishedAt: published,
			ActionItems: []RawActionItem{{Completed: true}, {Completed: false}, {Completed: true}},
		},
		{ID: "PM2", IncidentID: "INC2", Status: "weird", CreatedAt: created}, // unknown status -> not_started; no items
		{ID: "", IncidentID: "INC3", Status: "published"},                    // dropped: blank postmortem id
		{ID: "PM4", IncidentID: "", Status: "published"},                     // dropped: no linked incident
	}}
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	got, err := Collect(context.Background(), api, since, until)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(since) || !api.gotUntil.Equal(until) {
		t.Errorf("window not threaded: %v..%v", api.gotSince, api.gotUntil)
	}
	if len(got) != 2 {
		t.Fatalf("got %d; want 2 (blank-id + no-incident dropped)", len(got))
	}
	pm0 := got[0]
	if pm0.Status != "published" {
		t.Errorf("pm0 status = %q", pm0.Status)
	}
	if pm0.ActionItemCount != 3 || pm0.ActionItemsDone != 2 || pm0.ActionItemsOpen != 1 {
		t.Errorf("pm0 rollup = count=%d done=%d open=%d; want 3/2/1", pm0.ActionItemCount, pm0.ActionItemsDone, pm0.ActionItemsOpen)
	}
	if !pm0.PublishedAt.Equal(published) {
		t.Errorf("pm0 published_at = %v", pm0.PublishedAt)
	}
	pm1 := got[1]
	if pm1.Status != "not_started" {
		t.Errorf("pm1 status (unknown coerced) = %q; want not_started", pm1.Status)
	}
	if pm1.ActionItemCount != 0 || pm1.ActionItemsDone != 0 || pm1.ActionItemsOpen != 0 {
		t.Errorf("pm1 rollup = count=%d done=%d open=%d; want 0/0/0", pm1.ActionItemCount, pm1.ActionItemsDone, pm1.ActionItemsOpen)
	}
	if !pm1.PublishedAt.IsZero() {
		t.Errorf("pm1 published_at should be zero (unpublished); got %v", pm1.PublishedAt)
	}
}

func TestCollect_HardCap(t *testing.T) {
	t.Parallel()
	// Feed more than the run cap; Collect must stop at MaxRecords (DoS guard).
	raw := make([]RawPostmortem, MaxRecords+50)
	for i := range raw {
		raw[i] = RawPostmortem{ID: "PM", IncidentID: "INC", Status: "published"}
	}
	got, err := Collect(context.Background(), &fakeAPI{postmortems: raw}, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != MaxRecords {
		t.Fatalf("got %d; want hard cap %d", len(got), MaxRecords)
	}
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"published":   "published",
		"PUBLISHED":   "published",
		"in_review":   "in_review",
		"review":      "in_review",
		"in_progress": "in_progress",
		"draft":       "in_progress",
		"started":     "in_progress",
		"":            "not_started",
		"nonsense":    "not_started",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q; want %q", in, got, want)
		}
	}
}
