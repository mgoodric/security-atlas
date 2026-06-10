// Tests for the Jamf webhook-event parser. No live Jamf — synthetic deliveries
// only; neutral test strings.
package devices

import (
	"testing"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

func TestParseWebhookEvent_PostureEvent(t *testing.T) {
	body := []byte(`{
      "webhook": {"webhookEvent": "ComputerCheckIn"},
      "event": {
        "jssID": 42,
        "udid": "TEST-UDID-0001",
        "deviceName": "ENG-MBP-014",
        "osVersion": "14.5",
        "fileVaultEnabled": true,
        "passcodeStatus": "compliant",
        "managed": true,
        "enrolled": true,
        "complianceStatus": "compliant",
        "username": "user-123",
        "realName": "A. Engineer"
      }
    }`)
	devs, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("got %d devices, want 1", len(devs))
	}
	d := devs[0]
	// jssID is preferred (it == the pull profile's numeric REST id, so the dedup
	// key collides). UDID is only the fallback when jssID is absent.
	if d.DeviceID != "42" {
		t.Errorf("DeviceID = %q, want 42 (jssID preferred for pull-dedup)", d.DeviceID)
	}
	if !d.DiskEncryptionEnabled || !d.ScreenLockEnabled || !d.Managed || !d.Enrolled {
		t.Errorf("posture flags not mapped: %+v", d)
	}
	if d.Compliance != devposture.ComplianceCompliant {
		t.Errorf("Compliance = %q, want compliant", d.Compliance)
	}
	if d.Platform != "macOS" {
		t.Errorf("Platform = %q, want macOS", d.Platform)
	}
	if d.OwnerAssignmentID != "user-123" || d.OwnerDisplayName != "A. Engineer" {
		t.Errorf("owner assignment not mapped: %+v", d)
	}
}

func TestParseWebhookEvent_FallsBackToUDID(t *testing.T) {
	body := []byte(`{"webhook":{"webhookEvent":"ComputerInventoryCompleted"},"event":{"udid":"TEST-UDID-9"}}`)
	devs, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if len(devs) != 1 || devs[0].DeviceID != "TEST-UDID-9" {
		t.Fatalf("got %v, want one device id=TEST-UDID-9 (udid fallback)", devs)
	}
}

func TestParseWebhookEvent_NonPostureEvent_Empty(t *testing.T) {
	body := []byte(`{"webhook":{"webhookEvent":"RestApiOperation"},"event":{"udid":"X"}}`)
	devs, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("non-posture event must yield 0 devices; got %d", len(devs))
	}
}

func TestParseWebhookEvent_NoDeviceID_Empty(t *testing.T) {
	body := []byte(`{"webhook":{"webhookEvent":"ComputerCheckIn"},"event":{"deviceName":"x"}}`)
	devs, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("id-less event must yield 0 devices; got %d", len(devs))
	}
}

func TestParseWebhookEvent_Malformed_Error(t *testing.T) {
	if _, err := ParseWebhookEvent([]byte(`not json`)); err == nil {
		t.Fatal("malformed body must error")
	}
}

func TestParseWebhookEvent_ComplianceMapping(t *testing.T) {
	cases := map[string]devposture.ComplianceResult{
		"compliant":    devposture.ComplianceCompliant,
		"noncompliant": devposture.ComplianceNonCompliant,
		"":             devposture.ComplianceUnknown,
		"weird":        devposture.ComplianceUnknown,
	}
	for in, want := range cases {
		if got := webhookCompliance(in); got != want {
			t.Errorf("webhookCompliance(%q) = %q, want %q", in, got, want)
		}
	}
}

// Over-collection guard: a Jamf delivery carrying geolocation / app inventory /
// owner contact PII has nowhere to land them — the parser maps only posture +
// assignment, and devposture.RawDevice has no such field. This pins that a body
// with those keys still yields exactly the posture fields.
func TestParseWebhookEvent_DropsOverCollectedFields(t *testing.T) {
	body := []byte(`{
      "webhook": {"webhookEvent": "ComputerCheckIn"},
      "event": {
        "udid": "TEST-UDID-2",
        "fileVaultEnabled": true,
        "location": {"lat": 1.0, "lon": 2.0},
        "applications": [{"name": "Slack"}],
        "email": "person@example.com",
        "phone": "555-0100"
      }
    }`)
	devs, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("got %d devices, want 1", len(devs))
	}
	d := devs[0]
	// The owner assignment id must NOT have been populated from the email field.
	if d.OwnerAssignmentID == "person@example.com" {
		t.Error("owner email leaked into OwnerAssignmentID")
	}
}
