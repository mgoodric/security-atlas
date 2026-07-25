package devrecord

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

func sampleDevice() devposture.Device {
	return devposture.Device{
		SourceMDM:             devposture.MDMJamf,
		DeviceID:              "d-1",
		DeviceName:            "ENG-MBP-014",
		OSVersion:             "14.5",
		Platform:              "macOS",
		DiskEncryptionEnabled: true,
		ScreenLockEnabled:     true,
		Managed:               true,
		Enrolled:              true,
		Compliance:            devposture.ComplianceCompliant,
		OwnerAssignmentID:     "u-7",
		OwnerDisplayName:      "A. Engineer",
		ObservedAt:            time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_SetsKindAndIdempotencyAndScope(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleDevice(), "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKind {
		t.Errorf("kind = %q; want %q", rec.GetEvidenceKind(), EvidenceKind)
	}
	if rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("schema version = %q", rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency key empty")
	}
	if rec.GetControlId() != "scf:END-04" {
		t.Errorf("control = %q", rec.GetControlId())
	}
	scope := map[string]string{}
	for _, d := range rec.GetScope() {
		scope[d.GetKey()] = d.GetValues()[0]
	}
	if scope["service"] != "jamf" || scope["environment"] != "prod" {
		t.Errorf("scope = %v", scope)
	}
	// observed_at hour-truncated.
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !rec.GetObservedAt().AsTime().Equal(want) {
		t.Errorf("observed_at = %v; want %v", rec.GetObservedAt().AsTime(), want)
	}
}

func TestBuild_PayloadCarriesPostureSummaryOnly(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	pm := rec.GetPayload().AsMap()
	// Required posture-summary fields present.
	for _, k := range []string{"source_mdm", "device_id", "disk_encryption_enabled", "screen_lock_enabled", "managed", "enrolled", "compliance_result"} {
		if _, ok := pm[k]; !ok {
			t.Errorf("missing required payload key %q", k)
		}
	}
	// Only the allow-listed keys exist — no geolocation / app-inventory / contact PII.
	allowed := map[string]bool{
		"source_mdm": true, "device_id": true, "device_name": true, "os_version": true,
		"platform": true, "disk_encryption_enabled": true, "screen_lock_enabled": true,
		"managed": true, "enrolled": true, "compliance_result": true,
		"owner_assignment_id": true, "owner_display_name": true,
	}
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q (over-collection guard P0-490-3)", k)
		}
	}
}

func TestBuild_StableOptionalFieldsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	d := sampleDevice()
	d.DeviceName = ""
	d.OSVersion = ""
	d.Platform = ""
	d.OwnerAssignmentID = ""
	d.OwnerDisplayName = ""
	rec, _ := Build(d, "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	pm := rec.GetPayload().AsMap()
	for _, k := range []string{"device_name", "os_version", "platform", "owner_assignment_id", "owner_display_name"} {
		if _, ok := pm[k]; ok {
			t.Errorf("empty optional %q should be omitted, not emitted as empty", k)
		}
	}
}

func TestBuild_SameDeviceSameHourSameKey(t *testing.T) {
	t.Parallel()
	r1, _ := Build(sampleDevice(), "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	r2, _ := Build(sampleDevice(), "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Error("same device + hour should yield same idempotency key")
	}
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:END-04", "connector:jamf:devices@dev", "jamf", "prod")
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want RESULT_INCONCLUSIVE (evaluator decides)", rec.GetResult())
	}
}
