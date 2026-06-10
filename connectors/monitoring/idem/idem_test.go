package idem

import (
	"testing"
	"time"
)

func TestAlertConfigKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := AlertConfigKey("datadog", "mon-1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := AlertConfigKey("datadog", "mon-1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Errorf("same hour should collapse: %q vs %q", a, b)
	}
}

func TestAlertConfigKey_DiffersAcrossHour(t *testing.T) {
	t.Parallel()
	a := AlertConfigKey("datadog", "mon-1", time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	b := AlertConfigKey("datadog", "mon-1", time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC))
	if a == b {
		t.Error("different hour should differ")
	}
}

func TestAlertConfigKey_DiffersAcrossVendorAndRule(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if AlertConfigKey("datadog", "r1", ts) == AlertConfigKey("grafana", "r1", ts) {
		t.Error("different vendor should differ")
	}
	if AlertConfigKey("datadog", "r1", ts) == AlertConfigKey("datadog", "r2", ts) {
		t.Error("different rule should differ")
	}
}

func TestAlertConfigKey_NonEmpty(t *testing.T) {
	t.Parallel()
	if AlertConfigKey("grafana", "r", time.Now()) == "" {
		t.Error("key should be non-empty")
	}
}

func TestAlertFiringKey_StableForSameFiring(t *testing.T) {
	t.Parallel()
	fired := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	// An overlapping-window re-read within the same observed hour must collapse
	// to one ledger row for the same (vendor, rule, fired_at) — the dedup JUDGMENT.
	a := AlertFiringKey("datadog", "mon-1", fired, time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := AlertFiringKey("datadog", "mon-1", fired, time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Errorf("same firing within the hour should collapse: %q vs %q", a, b)
	}
}

func TestAlertFiringKey_DiffersAcrossFiredAt(t *testing.T) {
	t.Parallel()
	obs := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	// A busy rule fires many distinct times; each firing is its own audit event.
	a := AlertFiringKey("datadog", "mon-1", time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC), obs)
	b := AlertFiringKey("datadog", "mon-1", time.Date(2026, 6, 7, 10, 5, 0, 0, time.UTC), obs)
	if a == b {
		t.Error("different fired_at should differ — each firing is a distinct event")
	}
}

func TestAlertFiringKey_DiffersAcrossVendorRuleHour(t *testing.T) {
	t.Parallel()
	fired := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	obs := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if AlertFiringKey("datadog", "r1", fired, obs) == AlertFiringKey("grafana", "r1", fired, obs) {
		t.Error("different vendor should differ")
	}
	if AlertFiringKey("datadog", "r1", fired, obs) == AlertFiringKey("datadog", "r2", fired, obs) {
		t.Error("different rule should differ")
	}
	nextHour := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if AlertFiringKey("datadog", "r1", fired, obs) == AlertFiringKey("datadog", "r1", fired, nextHour) {
		t.Error("different observed hour should differ")
	}
}

func TestAlertFiringKey_NonEmpty(t *testing.T) {
	t.Parallel()
	if AlertFiringKey("grafana", "r", time.Now(), time.Now()) == "" {
		t.Error("key should be non-empty")
	}
}
