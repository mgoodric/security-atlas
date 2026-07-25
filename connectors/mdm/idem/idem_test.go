package idem

import (
	"testing"
	"time"
)

func TestDevicePostureKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := DevicePostureKey("jamf", "d1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := DevicePostureKey("jamf", "d1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Fatalf("same hour should dedupe: %q != %q", a, b)
	}
}

func TestDevicePostureKey_DiffersAcrossHour(t *testing.T) {
	t.Parallel()
	a := DevicePostureKey("jamf", "d1", time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	b := DevicePostureKey("jamf", "d1", time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC))
	if a == b {
		t.Fatal("different hour should differ")
	}
}

func TestDevicePostureKey_DiffersAcrossMDMAndDevice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if DevicePostureKey("jamf", "d1", at) == DevicePostureKey("intune", "d1", at) {
		t.Error("different mdm should differ")
	}
	if DevicePostureKey("jamf", "d1", at) == DevicePostureKey("jamf", "d2", at) {
		t.Error("different device should differ")
	}
}

func TestDevicePostureKey_HexLength(t *testing.T) {
	t.Parallel()
	if got := DevicePostureKey("jamf", "d1", time.Now()); len(got) != 64 {
		t.Errorf("key length = %d; want 64 (sha256 hex)", len(got))
	}
}

func TestSoftwareInventoryKey_StableWithinHourAndHexLength(t *testing.T) {
	t.Parallel()
	a := SoftwareInventoryKey("jamf", "d1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := SoftwareInventoryKey("jamf", "d1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Fatalf("same hour should dedupe: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("key length = %d; want 64", len(a))
	}
}

func TestSoftwareInventoryKey_DiffersAcrossHourMDMAndDevice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if SoftwareInventoryKey("jamf", "d1", at) == SoftwareInventoryKey("jamf", "d1", at.Add(time.Hour)) {
		t.Error("different hour should differ")
	}
	if SoftwareInventoryKey("jamf", "d1", at) == SoftwareInventoryKey("intune", "d1", at) {
		t.Error("different mdm should differ")
	}
	if SoftwareInventoryKey("jamf", "d1", at) == SoftwareInventoryKey("jamf", "d2", at) {
		t.Error("different device should differ")
	}
}

// TestSoftwareInventoryKey_NamespacedFromPosture is the load-bearing guard: a
// device's software-inventory record and its posture record never collide on the
// same idempotency key for the same device + hour.
func TestSoftwareInventoryKey_NamespacedFromPosture(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if SoftwareInventoryKey("jamf", "d1", at) == DevicePostureKey("jamf", "d1", at) {
		t.Error("software-inventory key must not collide with device-posture key for the same device+hour")
	}
}

func TestConfigProfileKey_StableWithinHourAndHexLength(t *testing.T) {
	t.Parallel()
	a := ConfigProfileKey("jamf", "d1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := ConfigProfileKey("jamf", "d1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Fatalf("same hour should dedupe: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("key length = %d; want 64", len(a))
	}
}

func TestConfigProfileKey_DiffersAcrossHourMDMAndDevice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if ConfigProfileKey("jamf", "d1", at) == ConfigProfileKey("jamf", "d1", at.Add(time.Hour)) {
		t.Error("different hour should differ")
	}
	if ConfigProfileKey("jamf", "d1", at) == ConfigProfileKey("intune", "d1", at) {
		t.Error("different mdm should differ")
	}
	if ConfigProfileKey("jamf", "d1", at) == ConfigProfileKey("jamf", "d2", at) {
		t.Error("different device should differ")
	}
}

// TestConfigProfileKey_NamespacedFromSiblings is the load-bearing guard: a
// device's config-profile record never collides on the same idempotency key with
// either its posture or its software-inventory record for the same device + hour.
func TestConfigProfileKey_NamespacedFromSiblings(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if ConfigProfileKey("jamf", "d1", at) == DevicePostureKey("jamf", "d1", at) {
		t.Error("config-profile key must not collide with device-posture key for the same device+hour")
	}
	if ConfigProfileKey("jamf", "d1", at) == SoftwareInventoryKey("jamf", "d1", at) {
		t.Error("config-profile key must not collide with software-inventory key for the same device+hour")
	}
}
