package metrics

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

type fakeAPI struct {
	out []RawTiming
	err error
}

func (f fakeAPI) ListIncidentTimings(_ context.Context, _, _ time.Time) ([]RawTiming, error) {
	return f.out, f.err
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error for nil API")
	}
}

func TestCollect_PropagatesAPIError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	_, err := Collect(context.Background(), fakeAPI{err: sentinel}, time.Time{}, time.Time{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_AggregatesByService(t *testing.T) {
	t.Parallel()
	created := "2026-06-01T00:00:00Z"
	raw := []RawTiming{
		// Service A: two incidents, both ack'd + resolved.
		{
			ServiceID:  "SVCA",
			CreatedAt:  ts(created),
			Acks:       []RawAck{{At: ts("2026-06-01T00:01:00Z")}}, // 60s
			ResolvedAt: ts("2026-06-01T00:10:00Z"),                 // 600s
		},
		{
			ServiceID:  "SVCA",
			CreatedAt:  ts(created),
			Acks:       []RawAck{{At: ts("2026-06-01T00:03:00Z")}}, // 180s
			ResolvedAt: ts("2026-06-01T00:30:00Z"),                 // 1800s
		},
		// Service B: one incident, ack'd but unresolved.
		{
			ServiceID: "SVCB",
			CreatedAt: ts(created),
			Acks:      []RawAck{{At: ts("2026-06-01T00:02:00Z")}}, // 120s
		},
	}
	got, err := Collect(context.Background(), fakeAPI{out: raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 service aggregates; got %d", len(got))
	}
	// Deterministic order: SVCA before SVCB.
	a := got[0]
	if a.ServiceID != "SVCA" {
		t.Fatalf("first aggregate = %q; want SVCA", a.ServiceID)
	}
	if a.IncidentCount != 2 || a.AcknowledgedCount != 2 || a.ResolvedCount != 2 {
		t.Errorf("SVCA counts = inc:%d ack:%d res:%d; want 2/2/2", a.IncidentCount, a.AcknowledgedCount, a.ResolvedCount)
	}
	if a.MTTASecondsMean != 120 { // (60+180)/2
		t.Errorf("SVCA MTTA mean = %d; want 120", a.MTTASecondsMean)
	}
	if a.MTTRSecondsMean != 1200 { // (600+1800)/2
		t.Errorf("SVCA MTTR mean = %d; want 1200", a.MTTRSecondsMean)
	}
	b := got[1]
	if b.ServiceID != "SVCB" || b.IncidentCount != 1 || b.AcknowledgedCount != 1 || b.ResolvedCount != 0 {
		t.Errorf("SVCB = %+v; want 1 incident, 1 ack, 0 resolved", b)
	}
	if b.MTTASecondsMean != 120 {
		t.Errorf("SVCB MTTA mean = %d; want 120", b.MTTASecondsMean)
	}
	if b.MTTRSecondsMean != 0 || b.MTTRSecondsP50 != 0 {
		t.Errorf("SVCB MTTR aggregates should be 0 with no resolved incidents; got mean %d p50 %d", b.MTTRSecondsMean, b.MTTRSecondsP50)
	}
}

func TestCollect_DropsIncidentsMissingGrainOrAnchor(t *testing.T) {
	t.Parallel()
	raw := []RawTiming{
		{ServiceID: "", CreatedAt: ts("2026-06-01T00:00:00Z")},    // no service grain
		{ServiceID: "SVC", CreatedAt: time.Time{}},                // no created-at anchor
		{ServiceID: "  ", CreatedAt: ts("2026-06-01T00:00:00Z")},  // whitespace grain
		{ServiceID: "SVC", CreatedAt: ts("2026-06-01T00:00:00Z")}, // valid, no ack/resolve
	}
	got, err := Collect(context.Background(), fakeAPI{out: raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 1 || got[0].ServiceID != "SVC" {
		t.Fatalf("want exactly the one valid SVC aggregate; got %+v", got)
	}
	if got[0].IncidentCount != 1 || got[0].AcknowledgedCount != 0 || got[0].ResolvedCount != 0 {
		t.Errorf("SVC = %+v; want 1 incident, 0 ack, 0 resolved", got[0])
	}
}

func TestCollect_IgnoresNegativeAndZeroDurations(t *testing.T) {
	t.Parallel()
	raw := []RawTiming{
		{
			ServiceID:  "SVC",
			CreatedAt:  ts("2026-06-01T00:10:00Z"),
			Acks:       []RawAck{{At: ts("2026-06-01T00:05:00Z")}}, // BEFORE created — clock skew; drop
			ResolvedAt: ts("2026-06-01T00:00:00Z"),                 // BEFORE created — drop
		},
	}
	got, err := Collect(context.Background(), fakeAPI{out: raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 aggregate; got %d", len(got))
	}
	// The incident counts toward IncidentCount but contributes no negative
	// timing sample.
	if got[0].IncidentCount != 1 || got[0].AcknowledgedCount != 0 || got[0].ResolvedCount != 0 {
		t.Errorf("got %+v; negative-duration samples must be dropped", got[0])
	}
}

func TestCollect_FirstAckWins(t *testing.T) {
	t.Parallel()
	raw := []RawTiming{
		{
			ServiceID: "SVC",
			CreatedAt: ts("2026-06-01T00:00:00Z"),
			Acks: []RawAck{
				{At: ts("2026-06-01T00:05:00Z")}, // later
				{At: ts("2026-06-01T00:01:00Z")}, // earliest — 60s
				{At: time.Time{}},                // zero — ignored
			},
		},
	}
	got, err := Collect(context.Background(), fakeAPI{out: raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got[0].MTTASecondsMean != 60 {
		t.Errorf("MTTA mean = %d; want 60 (first ack wins)", got[0].MTTASecondsMean)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	t.Parallel()
	xs := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		p    int
		want int64
	}{
		{50, 50},
		{90, 90},
		{95, 100},
		{100, 100},
	}
	for _, c := range cases {
		if got := percentile(xs, c.p); got != c.want {
			t.Errorf("percentile(p%d) = %d; want %d", c.p, got, c.want)
		}
	}
	if percentile(nil, 50) != 0 {
		t.Error("percentile of empty set must be 0")
	}
}

func TestPercentile_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	xs := []int64{30, 10, 20}
	_ = percentile(xs, 50)
	if xs[0] != 30 || xs[1] != 10 || xs[2] != 20 {
		t.Errorf("percentile mutated its input: %v", xs)
	}
}

func TestMean_Empty(t *testing.T) {
	t.Parallel()
	if mean(nil) != 0 {
		t.Error("mean of empty set must be 0")
	}
}

func TestCollect_CapsAtMaxServices(t *testing.T) {
	t.Parallel()
	raw := make([]RawTiming, 0, MaxServices+50)
	for i := 0; i < MaxServices+50; i++ {
		raw = append(raw, RawTiming{
			ServiceID: "SVC-" + strconv.Itoa(i), // each service id distinct
			CreatedAt: ts("2026-06-01T00:00:00Z"),
		})
	}
	got, err := Collect(context.Background(), fakeAPI{out: raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) > MaxServices {
		t.Errorf("got %d aggregates; must be capped at MaxServices=%d", len(got), MaxServices)
	}
}
