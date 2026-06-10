package firingevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

type fakeAPI struct {
	events   []firing.RawFiring
	err      error
	gotSince time.Time
}

func (f *fakeAPI) ListMonitorEvents(_ context.Context, since time.Time) ([]firing.RawFiring, error) {
	f.gotSince = since
	return f.events, f.err
}

func fixedClock() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	fired := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	api := &fakeAPI{events: []firing.RawFiring{{
		RuleID: "12345", State: "error", FiredAt: fired, TargetHandle: "slack-sec", TargetKind: "slack",
	}}}
	got, err := Collect(context.Background(), api, 24*time.Hour, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != "12345" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got[0].SourceVendor != firing.VendorDatadog {
		t.Errorf("vendor = %q; want datadog", got[0].SourceVendor)
	}
	if got[0].State != firing.StateAlerting {
		t.Errorf("error not folded to alerting: %q", got[0].State)
	}
	if !api.gotSince.Equal(time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("since = %v; want now-24h", api.gotSince)
	}
}

func TestCollect_LookbackDefaultsTo24h(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, 0, fixedClock); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("default lookback not 24h: since=%v", api.gotSince)
	}
}

func TestCollect_CustomLookback(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, 6*time.Hour, fixedClock); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(time.Date(2026, 6, 7, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("custom lookback not honored: since=%v", api.gotSince)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, time.Hour, fixedClock); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_NilClockUsesNow(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, time.Hour, nil); err != nil {
		t.Fatalf("Collect with nil clock: %v", err)
	}
	if api.gotSince.IsZero() {
		t.Error("since not set with nil clock")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403 forbidden")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}, time.Hour, fixedClock); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}
