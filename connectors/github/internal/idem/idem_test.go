package idem_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/idem"
)

func TestRepoProtectionKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	if a, b := idem.RepoProtectionKey("mgoodric/security-atlas", t1), idem.RepoProtectionKey("mgoodric/security-atlas", t2); a != b {
		t.Fatalf("keys differ within hour: %s vs %s", a, b)
	}
}

func TestRepoProtectionKey_RotatesOnHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	if idem.RepoProtectionKey("mgoodric/security-atlas", t1) == idem.RepoProtectionKey("mgoodric/security-atlas", t2) {
		t.Fatal("keys identical across hour boundary")
	}
}

func TestSCIMUserKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 5, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 10, 0, 0, time.UTC)
	if a, b := idem.SCIMUserKey("scim-uuid-1", t1), idem.SCIMUserKey("scim-uuid-1", t2); a != b {
		t.Fatalf("scim keys differ within hour: %s vs %s", a, b)
	}
}

func TestSCIMUserKey_DistinctPerUser(t *testing.T) {
	now := time.Now().UTC()
	if idem.SCIMUserKey("u1", now) == idem.SCIMUserKey("u2", now) {
		t.Fatal("different scim users collided on key")
	}
}

func TestDeliveryKey_VerbatimUUID(t *testing.T) {
	const deliveryID = "72d3162e-cc78-11e3-81ab-4c9367dc0958"
	if got := idem.DeliveryKey(deliveryID); got != deliveryID {
		t.Fatalf("DeliveryKey rewrote header: got %q want %q", got, deliveryID)
	}
}

func TestDeliveryKey_TrimsSurroundingSpace(t *testing.T) {
	if got := idem.DeliveryKey("  abc \n"); got != "abc" {
		t.Fatalf("DeliveryKey trim wrong: %q", got)
	}
}

func TestDeliveryKey_EmptyOnBlank(t *testing.T) {
	if got := idem.DeliveryKey("   "); got != "" {
		t.Fatalf("DeliveryKey on blank returned %q; want empty", got)
	}
}

func TestRepoProtectionKey_IsHex(t *testing.T) {
	k := idem.RepoProtectionKey("a/b", time.Now())
	if len(k) != 64 {
		t.Fatalf("key length = %d; want 64 hex chars", len(k))
	}
	if strings.ContainsAny(k, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("key not hex: %s", k)
	}
}
