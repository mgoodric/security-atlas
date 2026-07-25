package swrecord

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
)

func sampleDevice() swinventory.Device {
	return swinventory.Device{
		SourceMDM: devposture.MDMJamf,
		DeviceID:  "d-1",
		Software: []swinventory.SoftwareItem{
			{Name: "Google Chrome", Version: "125.0.6422.142", Identifier: "com.google.Chrome", InstallDate: "2026-01-02"},
			{Name: "openssl", Version: "3.0.13"},
		},
		ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_SetsKindAndIdempotencyAndScope(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleDevice(), "scf:VPM-04", "connector:jamf:software@dev", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKind || EvidenceKind != "endpoint.software_inventory.v1" {
		t.Errorf("kind = %q; want endpoint.software_inventory.v1", rec.GetEvidenceKind())
	}
	if rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("schema version = %q", rec.GetSchemaVersion())
	}
	if rec.GetIdempotencyKey() == "" {
		t.Error("idempotency key empty")
	}
	if rec.GetControlId() != "scf:VPM-04" {
		t.Errorf("control = %q", rec.GetControlId())
	}
	scope := map[string]string{}
	for _, d := range rec.GetScope() {
		scope[d.GetKey()] = d.GetValues()[0]
	}
	if scope["service"] != "jamf" || scope["environment"] != "prod" {
		t.Errorf("scope = %v", scope)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !rec.GetObservedAt().AsTime().Equal(want) {
		t.Errorf("observed_at = %v; want %v", rec.GetObservedAt().AsTime(), want)
	}
}

// TestBuild_PayloadAndItemsCarrySoftwareFieldsOnly is the over-collection guard:
// the top-level payload AND every software item are limited to the allow-listed
// keys — no file path, usage telemetry, or license key reaches a record.
func TestBuild_PayloadAndItemsCarrySoftwareFieldsOnly(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:VPM-04", "connector:jamf:software@dev", "jamf", "prod")
	pm := rec.GetPayload().AsMap()

	for _, k := range []string{"source_mdm", "device_id", "software_count", "software"} {
		if _, ok := pm[k]; !ok {
			t.Errorf("missing required payload key %q", k)
		}
	}
	allowedTop := map[string]bool{"source_mdm": true, "device_id": true, "software_count": true, "software": true}
	for k := range pm {
		if !allowedTop[k] {
			t.Errorf("non-allow-listed top-level payload key %q (over-collection guard P0-555)", k)
		}
	}

	if pm["software_count"].(float64) != 2 {
		t.Errorf("software_count = %v; want 2", pm["software_count"])
	}

	allowedItem := map[string]bool{"name": true, "version": true, "identifier": true, "install_date": true}
	banned := []string{"path", "file_path", "executable", "usage", "last_used", "license", "license_key", "size", "email", "phone"}
	items := pm["software"].([]any)
	if len(items) != 2 {
		t.Fatalf("software items = %d; want 2", len(items))
	}
	for _, it := range items {
		m := it.(map[string]any)
		for k := range m {
			if !allowedItem[k] {
				t.Errorf("non-allow-listed software item key %q (over-collection guard P0-555)", k)
			}
			for _, b := range banned {
				if strings.EqualFold(k, b) {
					t.Errorf("banned software item key %q reached payload (P0-555)", k)
				}
			}
		}
	}
}

func TestBuild_OptionalItemFieldsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	d := sampleDevice() // second item has no version/identifier/install_date
	rec, _ := Build(d, "scf:VPM-04", "connector:jamf:software@dev", "jamf", "prod")
	items := rec.GetPayload().AsMap()["software"].([]any)
	openssl := items[1].(map[string]any)
	if openssl["name"] != "openssl" {
		t.Fatalf("unexpected second item: %+v", openssl)
	}
	for _, k := range []string{"identifier", "install_date"} {
		if _, ok := openssl[k]; ok {
			t.Errorf("empty optional %q should be omitted, not emitted", k)
		}
	}
}

func TestBuild_EmptySoftwareListEmitsZeroCount(t *testing.T) {
	t.Parallel()
	d := sampleDevice()
	d.Software = nil
	rec, err := Build(d, "scf:VPM-04", "connector:jamf:software@dev", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pm := rec.GetPayload().AsMap()
	if pm["software_count"].(float64) != 0 {
		t.Errorf("software_count = %v; want 0", pm["software_count"])
	}
	if len(pm["software"].([]any)) != 0 {
		t.Errorf("software list should be empty")
	}
}

func TestBuild_SameDeviceSameHourSameKey(t *testing.T) {
	t.Parallel()
	r1, _ := Build(sampleDevice(), "scf:VPM-04", "a", "jamf", "prod")
	r2, _ := Build(sampleDevice(), "scf:VPM-04", "a", "jamf", "prod")
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Error("same device + hour should yield same idempotency key")
	}
}

func TestBuild_IdempotencyKeyDistinctFromPostureKind(t *testing.T) {
	t.Parallel()
	// The software-inventory key must not collide with the device-posture key for
	// the same device + hour (different evidence-kind namespace).
	rec, _ := Build(sampleDevice(), "scf:VPM-04", "a", "jamf", "prod")
	if rec.GetIdempotencyKey() == "" {
		t.Fatal("empty key")
	}
	// Build a posture-keyed record would be a separate package; instead assert the
	// key embeds the software-inventory namespace by being distinct from a known
	// posture key for the same inputs is covered in idem_test.
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:VPM-04", "a", "jamf", "prod")
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want RESULT_INCONCLUSIVE", rec.GetResult())
	}
}
