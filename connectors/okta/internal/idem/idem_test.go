package idem_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/idem"
)

func TestMFAPolicyKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	if a, b := idem.MFAPolicyKey("policy-1", t1), idem.MFAPolicyKey("policy-1", t2); a != b {
		t.Fatalf("keys differ within hour: %s vs %s", a, b)
	}
}

func TestMFAPolicyKey_RotatesOnHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	if idem.MFAPolicyKey("policy-1", t1) == idem.MFAPolicyKey("policy-1", t2) {
		t.Fatal("keys identical across hour boundary")
	}
}

func TestAppAssignmentKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 5, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 50, 0, 0, time.UTC)
	if a, b := idem.AppAssignmentKey("app-1", t1), idem.AppAssignmentKey("app-1", t2); a != b {
		t.Fatalf("app keys differ within hour: %s vs %s", a, b)
	}
}

func TestAppAssignmentKey_DistinctPerApp(t *testing.T) {
	now := time.Now().UTC()
	if idem.AppAssignmentKey("a1", now) == idem.AppAssignmentKey("a2", now) {
		t.Fatal("different apps collided on key")
	}
}

func TestUserLifecycleKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 5, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 50, 0, 0, time.UTC)
	if a, b := idem.UserLifecycleKey("u-1", t1), idem.UserLifecycleKey("u-1", t2); a != b {
		t.Fatalf("user keys differ within hour: %s vs %s", a, b)
	}
}

func TestUserLifecycleKey_DistinctPerUser(t *testing.T) {
	now := time.Now().UTC()
	if idem.UserLifecycleKey("u1", now) == idem.UserLifecycleKey("u2", now) {
		t.Fatal("different users collided on key")
	}
}

func TestMFAPolicyKey_DistinctFromOtherKinds(t *testing.T) {
	now := time.Now().UTC()
	// Same input id + hour across different kind prefixes must produce
	// distinct keys — the prefix is what disambiguates them in the ledger.
	mfa := idem.MFAPolicyKey("x", now)
	app := idem.AppAssignmentKey("x", now)
	usr := idem.UserLifecycleKey("x", now)
	if mfa == app || app == usr || mfa == usr {
		t.Fatalf("cross-kind collision: mfa=%s app=%s user=%s", mfa, app, usr)
	}
}

func TestMFAPolicyKey_IsHex64(t *testing.T) {
	k := idem.MFAPolicyKey("p", time.Now())
	if len(k) != 64 {
		t.Fatalf("key length = %d; want 64 hex chars", len(k))
	}
	if strings.ContainsAny(k, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("key not hex: %s", k)
	}
}
