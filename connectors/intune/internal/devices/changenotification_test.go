// Tests for the Graph change-notification parser + clientState extractor. No live
// Graph — synthetic deliveries; neutral test strings.
package devices

import (
	"testing"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

const testClientState = "test-client-state-not-a-real-value"

func TestParseChangeNotification_RichPosture(t *testing.T) {
	body := []byte(`{"value":[{
      "clientState": "test-client-state-not-a-real-value",
      "changeType": "updated",
      "resourceData": {
        "id": "device-guid-1",
        "deviceName": "WIN-LAPTOP-22",
        "osVersion": "10.0.22631",
        "operatingSystem": "Windows",
        "isEncrypted": true,
        "complianceState": "compliant",
        "managementAgent": "mdm",
        "userPrincipalName": "user-123",
        "userDisplayName": "A. Engineer"
      }
    }]}`)
	devs, err := ParseChangeNotification(body)
	if err != nil {
		t.Fatalf("ParseChangeNotification: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("got %d devices, want 1", len(devs))
	}
	d := devs[0]
	if d.DeviceID != "device-guid-1" {
		t.Errorf("DeviceID = %q", d.DeviceID)
	}
	if !d.DiskEncryptionEnabled || !d.ScreenLockEnabled || !d.Managed || !d.Enrolled {
		t.Errorf("posture flags not mapped: %+v", d)
	}
	if d.Compliance != devposture.ComplianceCompliant {
		t.Errorf("Compliance = %q, want compliant", d.Compliance)
	}
	if d.Platform != "Windows" {
		t.Errorf("Platform = %q, want Windows", d.Platform)
	}
	if d.OwnerAssignmentID != "user-123" {
		t.Errorf("owner assignment not mapped: %+v", d)
	}
}

// A bare notification (id only, no rich posture) emits a device with just the id
// and descriptive defaults — the connector never re-reads to enrich it.
func TestParseChangeNotification_IDOnly(t *testing.T) {
	body := []byte(`{"value":[{"clientState":"x","changeType":"updated","resourceData":{"id":"device-guid-2"}}]}`)
	devs, err := ParseChangeNotification(body)
	if err != nil {
		t.Fatalf("ParseChangeNotification: %v", err)
	}
	if len(devs) != 1 || devs[0].DeviceID != "device-guid-2" {
		t.Fatalf("got %v, want one device id=device-guid-2", devs)
	}
	if devs[0].DiskEncryptionEnabled {
		t.Error("id-only notification must not assert encryption")
	}
	if devs[0].Compliance != devposture.ComplianceUnknown {
		t.Errorf("id-only compliance = %q, want unknown", devs[0].Compliance)
	}
}

func TestParseChangeNotification_DeletedSkipped(t *testing.T) {
	body := []byte(`{"value":[{"clientState":"x","changeType":"deleted","resourceData":{"id":"gone"}}]}`)
	devs, err := ParseChangeNotification(body)
	if err != nil {
		t.Fatalf("ParseChangeNotification: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("deleted change must yield 0 devices; got %d", len(devs))
	}
}

func TestParseChangeNotification_MultipleDevices(t *testing.T) {
	body := []byte(`{"value":[
      {"clientState":"x","changeType":"updated","resourceData":{"id":"d1"}},
      {"clientState":"x","changeType":"created","resourceData":{"id":"d2"}}
    ]}`)
	devs, err := ParseChangeNotification(body)
	if err != nil {
		t.Fatalf("ParseChangeNotification: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2", len(devs))
	}
}

func TestParseChangeNotification_Malformed_Error(t *testing.T) {
	if _, err := ParseChangeNotification([]byte(`not json`)); err == nil {
		t.Fatal("malformed body must error")
	}
}

func TestExtractClientState(t *testing.T) {
	good := []byte(`{"value":[{"clientState":"test-client-state-not-a-real-value","resourceData":{"id":"d1"}}]}`)
	got, ok := ExtractClientState(good)
	if !ok || got != testClientState {
		t.Fatalf("ExtractClientState = %q,%v; want %q,true", got, ok, testClientState)
	}
}

func TestExtractClientState_EmptyBatch(t *testing.T) {
	if _, ok := ExtractClientState([]byte(`{"value":[]}`)); ok {
		t.Error("empty batch must extract no clientState")
	}
}

func TestExtractClientState_NoClientState(t *testing.T) {
	if _, ok := ExtractClientState([]byte(`{"value":[{"resourceData":{"id":"d1"}}]}`)); ok {
		t.Error("notification without clientState must extract false")
	}
}

// A batch with a mismatched clientState in any entry is rejected wholesale.
func TestExtractClientState_MixedBatchRejected(t *testing.T) {
	body := []byte(`{"value":[
      {"clientState":"test-good-state","resourceData":{"id":"d1"}},
      {"clientState":"test-forged-state","resourceData":{"id":"d2"}}
    ]}`)
	if _, ok := ExtractClientState(body); ok {
		t.Error("mixed-clientState batch must extract false (partial forgery)")
	}
}

func TestExtractClientState_Malformed(t *testing.T) {
	if _, ok := ExtractClientState([]byte(`not json`)); ok {
		t.Error("malformed body must extract false")
	}
}

func TestNotificationCompliance(t *testing.T) {
	cases := map[string]devposture.ComplianceResult{
		"compliant":     devposture.ComplianceCompliant,
		"noncompliant":  devposture.ComplianceNonCompliant,
		"inGracePeriod": devposture.ComplianceUnknown,
		"":              devposture.ComplianceUnknown,
	}
	for in, want := range cases {
		if got := notificationCompliance(in); got != want {
			t.Errorf("notificationCompliance(%q) = %q, want %q", in, got, want)
		}
	}
}
