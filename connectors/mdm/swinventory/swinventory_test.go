package swinventory

import (
	"strconv"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

func fixedClock(ts time.Time) func() time.Time { return func() time.Time { return ts } }

func TestNormalize_StampsSourceAndHourTruncatesObservedAt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 45, 30, 0, time.UTC)
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{
		{DeviceID: "d-1", Software: []RawSoftwareItem{{Name: "Chrome", Version: "125"}}},
	}, fixedClock(at))
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	if got[0].SourceMDM != devposture.MDMJamf {
		t.Errorf("source = %q", got[0].SourceMDM)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !got[0].ObservedAt.Equal(want) {
		t.Errorf("observed_at = %v; want %v", got[0].ObservedAt, want)
	}
}

func TestNormalize_DropsDeviceMissingID(t *testing.T) {
	t.Parallel()
	got := Normalize(devposture.MDMIntune, []RawDeviceSoftware{
		{DeviceID: "  ", Software: []RawSoftwareItem{{Name: "X"}}},
		{DeviceID: "keep", Software: []RawSoftwareItem{{Name: "Y"}}},
	}, fixedClock(time.Now()))
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only keep", got)
	}
}

func TestNormalize_DropsSoftwareMissingNameAndTrims(t *testing.T) {
	t.Parallel()
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{
		{DeviceID: "d", Software: []RawSoftwareItem{
			{Name: "  "},
			{Name: "  Slack ", Version: " 4.2 ", Identifier: " com.tinyspeck.slack ", InstallDate: " 2026-01-01 "},
		}},
	}, fixedClock(time.Now()))
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	sw := got[0].Software
	if len(sw) != 1 {
		t.Fatalf("software len = %d; want 1 (empty-name dropped)", len(sw))
	}
	if sw[0].Name != "Slack" || sw[0].Version != "4.2" || sw[0].Identifier != "com.tinyspeck.slack" || sw[0].InstallDate != "2026-01-01" {
		t.Errorf("trim wrong: %+v", sw[0])
	}
}

func TestNormalize_SortsSoftwareStably(t *testing.T) {
	t.Parallel()
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{
		{DeviceID: "d", Software: []RawSoftwareItem{
			{Name: "Zoom", Version: "5"},
			{Name: "Atom", Version: "2"},
			{Name: "Atom", Version: "1"},
		}},
	}, fixedClock(time.Now()))
	sw := got[0].Software
	if sw[0].Name != "Atom" || sw[0].Version != "1" || sw[1].Version != "2" || sw[2].Name != "Zoom" {
		t.Errorf("sort wrong: %+v", sw)
	}
}

func TestNormalize_SortsDevicesByID(t *testing.T) {
	t.Parallel()
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{
		{DeviceID: "b", Software: []RawSoftwareItem{{Name: "X"}}},
		{DeviceID: "a", Software: []RawSoftwareItem{{Name: "Y"}}},
	}, fixedClock(time.Now()))
	if got[0].DeviceID != "a" || got[1].DeviceID != "b" {
		t.Errorf("device sort wrong: %+v", got)
	}
}

func TestNormalize_BoundsSoftwarePerDevice(t *testing.T) {
	t.Parallel()
	raw := make([]RawSoftwareItem, MaxSoftwarePerDevice+50)
	for i := range raw {
		// zero-padded name so the stable sort is deterministic and the bound is
		// applied after sorting.
		raw[i] = RawSoftwareItem{Name: "app-" + pad(i)}
	}
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{{DeviceID: "d", Software: raw}}, fixedClock(time.Now()))
	if len(got[0].Software) != MaxSoftwarePerDevice {
		t.Errorf("software len = %d; want bound %d", len(got[0].Software), MaxSoftwarePerDevice)
	}
}

func TestNormalize_NilClockUsesNow(t *testing.T) {
	t.Parallel()
	got := Normalize(devposture.MDMJamf, []RawDeviceSoftware{{DeviceID: "d", Software: []RawSoftwareItem{{Name: "X"}}}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observed_at should be set from time.Now when clock is nil")
	}
}

func pad(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}
