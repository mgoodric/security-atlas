package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

type fakeAPI struct {
	computers []RawComputer
	err       error
}

func (f *fakeAPI) ListComputers(_ context.Context) ([]RawComputer, error) {
	return f.computers, f.err
}

func TestCollect_MapsPostureFields(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{computers: []RawComputer{
		{ID: "1", Name: "ENG-MBP", OSVersion: "14.5", FileVaultEnabled: true, PasscodeCompliant: true,
			Managed: true, Supervised: true, Enrolled: true, Compliant: true, HasCompliance: true,
			OwnerAssignmentID: "u-1", OwnerDisplayName: "A. Eng"},
	}}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	d := got[0]
	if d.DeviceID != "1" || d.Platform != "macOS" || !d.DiskEncryptionEnabled || !d.ScreenLockEnabled || !d.Managed || !d.Enrolled {
		t.Errorf("posture mapped wrong: %+v", d)
	}
	if d.Compliance != devposture.ComplianceCompliant {
		t.Errorf("compliance = %q", d.Compliance)
	}
	if d.OwnerAssignmentID != "u-1" || d.OwnerDisplayName != "A. Eng" {
		t.Errorf("owner assignment mapped wrong: %+v", d)
	}
}

func TestCollect_ManagedFromSupervisedOnly(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{computers: []RawComputer{{ID: "1", Managed: false, Supervised: true}}}
	got, _ := Collect(context.Background(), api)
	if !got[0].Managed {
		t.Error("supervised-only device should be Managed")
	}
}

func TestCollect_NoComplianceVerdictIsUnknown(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{computers: []RawComputer{{ID: "1", HasCompliance: false}}}
	got, _ := Collect(context.Background(), api)
	if got[0].Compliance != devposture.ComplianceUnknown {
		t.Errorf("no verdict should be unknown; got %q", got[0].Compliance)
	}
}

func TestCollect_NonCompliant(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{computers: []RawComputer{{ID: "1", HasCompliance: true, Compliant: false}}}
	got, _ := Collect(context.Background(), api)
	if got[0].Compliance != devposture.ComplianceNonCompliant {
		t.Errorf("got %q; want noncompliant", got[0].Compliance)
	}
}

func TestCollect_DropsEmptyID(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{computers: []RawComputer{{ID: ""}, {ID: "keep"}}}
	got, _ := Collect(context.Background(), api)
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
