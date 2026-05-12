package idem_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/idem"
)

func TestHostPostureKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	if a, b := idem.HostPostureKey("host-uuid-1", t1), idem.HostPostureKey("host-uuid-1", t2); a != b {
		t.Fatalf("keys differ within hour: %s vs %s", a, b)
	}
}

func TestHostPostureKey_RotatesOnHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	if idem.HostPostureKey("host-uuid-1", t1) == idem.HostPostureKey("host-uuid-1", t2) {
		t.Fatal("keys identical across hour boundary")
	}
}

func TestHostPostureKey_DistinctPerHost(t *testing.T) {
	now := time.Now().UTC()
	if idem.HostPostureKey("h1", now) == idem.HostPostureKey("h2", now) {
		t.Fatal("different hosts collided on key")
	}
}

func TestHostPostureKey_IsHex(t *testing.T) {
	k := idem.HostPostureKey("h1", time.Now())
	if len(k) != 64 {
		t.Fatalf("key length = %d; want 64 hex chars", len(k))
	}
	if strings.ContainsAny(k, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("key not hex: %s", k)
	}
}

// Anti-criterion P0: empty host_uuid MUST return empty key so the caller
// has an unambiguous "reject" signal — the connector never fabricates an
// idempotency_key for a record missing its primary identifier.
func TestHostPostureKey_EmptyOnMissingHostUUID(t *testing.T) {
	if got := idem.HostPostureKey("", time.Now()); got != "" {
		t.Fatalf("HostPostureKey('') = %q; want empty", got)
	}
}
