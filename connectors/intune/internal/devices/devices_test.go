package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

type fakeAPI struct {
	devices []RawDevice
	err     error
}

func (f *fakeAPI) ListManagedDevices(_ context.Context) ([]RawDevice, error) {
	return f.devices, f.err
}

func TestCollect_MapsPostureFields(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{devices: []RawDevice{
		{ID: "d-1", Name: "ENG-PC", OSVersion: "10.0.22631", OS: "Windows", Encrypted: true, PasscodeCompliant: true,
			ComplianceState: "compliant", ManagementState: "mdm", Enrolled: true,
			OwnerAssignmentID: "user@tenant.example", OwnerDisplayName: "A. Eng"},
	}}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	d := got[0]
	if d.DeviceID != "d-1" || d.Platform != "Windows" || !d.DiskEncryptionEnabled || !d.ScreenLockEnabled || !d.Managed || !d.Enrolled {
		t.Errorf("posture mapped wrong: %+v", d)
	}
	if d.Compliance != devposture.ComplianceCompliant {
		t.Errorf("compliance = %q", d.Compliance)
	}
	if d.OwnerAssignmentID != "user@tenant.example" {
		t.Errorf("owner assignment id (UPN) mapped wrong: %+v", d)
	}
}

func TestCollect_ComplianceMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state string
		want  devposture.ComplianceResult
	}{
		{"compliant", devposture.ComplianceCompliant},
		{"noncompliant", devposture.ComplianceNonCompliant},
		{"inGracePeriod", devposture.ComplianceNonCompliant},
		{"conflict", devposture.ComplianceNonCompliant},
		{"error", devposture.ComplianceNonCompliant},
		{"unknown", devposture.ComplianceUnknown},
		{"", devposture.ComplianceUnknown},
	}
	for _, c := range cases {
		got, _ := Collect(context.Background(), &fakeAPI{devices: []RawDevice{{ID: "1", ComplianceState: c.state}}})
		if got[0].Compliance != c.want {
			t.Errorf("state %q -> %q; want %q", c.state, got[0].Compliance, c.want)
		}
	}
}

func TestCollect_ManagedFromManagementState(t *testing.T) {
	t.Parallel()
	got, _ := Collect(context.Background(), &fakeAPI{devices: []RawDevice{{ID: "1", ManagementState: "unknown"}}})
	if got[0].Managed {
		t.Error("management state 'unknown' should not be Managed")
	}
	got, _ = Collect(context.Background(), &fakeAPI{devices: []RawDevice{{ID: "1", ManagementState: "mdm"}}})
	if !got[0].Managed {
		t.Error("management state 'mdm' should be Managed")
	}
}

func TestCollect_DropsEmptyID(t *testing.T) {
	t.Parallel()
	got, _ := Collect(context.Background(), &fakeAPI{devices: []RawDevice{{ID: ""}, {ID: "keep"}}})
	if len(got) != 1 || got[0].DeviceID != "keep" {
		t.Fatalf("got %+v", got)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	_, err := Collect(context.Background(), &fakeAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}
