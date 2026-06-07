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
