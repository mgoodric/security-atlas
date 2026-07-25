package devposture

import (
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 34, 0, 0, time.UTC) }
}

func TestNormalize_StampsMDMAndTruncatesObservedAt(t *testing.T) {
	t.Parallel()
	got := Normalize(MDMJamf, []RawDevice{
		{DeviceID: "d1", OSVersion: "14.5", DiskEncryptionEnabled: true, ScreenLockEnabled: true, Managed: true, Enrolled: true, Compliance: ComplianceCompliant},
	}, fixedClock())
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	d := got[0]
	if d.SourceMDM != MDMJamf {
		t.Errorf("SourceMDM = %q", d.SourceMDM)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !d.ObservedAt.Equal(want) {
		t.Errorf("ObservedAt = %v; want %v (hour-truncated)", d.ObservedAt, want)
	}
}

func TestNormalize_DropsDevicesMissingID(t *testing.T) {
	t.Parallel()
	got := Normalize(MDMIntune, []RawDevice{
		{DeviceID: ""},
		{DeviceID: "  "},
		{DeviceID: "keep"},
	}, fixedClock())
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only [keep]", got)
	}
}

func TestNormalize_SortsByDeviceID(t *testing.T) {
	t.Parallel()
	got := Normalize(MDMJamf, []RawDevice{
		{DeviceID: "c"}, {DeviceID: "a"}, {DeviceID: "b"},
	}, fixedClock())
	if got[0].DeviceID != "a" || got[1].DeviceID != "b" || got[2].DeviceID != "c" {
		t.Errorf("not sorted: %v", []string{got[0].DeviceID, got[1].DeviceID, got[2].DeviceID})
	}
}

func TestNormalize_ComplianceFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   ComplianceResult
		want ComplianceResult
	}{
		{ComplianceCompliant, ComplianceCompliant},
		{ComplianceNonCompliant, ComplianceNonCompliant},
		{ComplianceUnknown, ComplianceUnknown},
		{"", ComplianceUnknown},
		{"garbage", ComplianceUnknown},
	}
	for _, c := range cases {
		got := Normalize(MDMJamf, []RawDevice{{DeviceID: "x", Compliance: c.in}}, fixedClock())
		if got[0].Compliance != c.want {
			t.Errorf("compliance %q -> %q; want %q", c.in, got[0].Compliance, c.want)
		}
	}
}

func TestNormalize_TrimsWhitespaceOptionalFields(t *testing.T) {
	t.Parallel()
	got := Normalize(MDMIntune, []RawDevice{
		{DeviceID: " d1 ", DeviceName: " ENG-PC ", OSVersion: " 10.0 ", Platform: " Windows ", OwnerAssignmentID: " u-1 ", OwnerDisplayName: " A. Eng "},
	}, fixedClock())
	d := got[0]
	if d.DeviceID != "d1" || d.DeviceName != "ENG-PC" || d.OSVersion != "10.0" || d.Platform != "Windows" || d.OwnerAssignmentID != "u-1" || d.OwnerDisplayName != "A. Eng" {
		t.Errorf("whitespace not trimmed: %+v", d)
	}
}

func TestNormalize_NilClockUsesWallClock(t *testing.T) {
	t.Parallel()
	got := Normalize(MDMJamf, []RawDevice{{DeviceID: "d1"}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("ObservedAt should be set from wall clock")
	}
}
