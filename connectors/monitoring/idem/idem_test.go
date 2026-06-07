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
