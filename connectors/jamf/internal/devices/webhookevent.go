package devices

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// wireEvent is the SUMMARY-ONLY view of a Jamf Pro webhook delivery. Jamf posts a
// `{"webhook": {...}, "event": {...}}` envelope; the `event` block carries the
// computer's posture fields for the device-lifecycle events the connector acts on
// (ComputerCheckIn / ComputerInventoryCompleted / ComputerPolicyFinished /
// ComputerAdded). Only posture-summary + the owner ASSIGNMENT identity are
// modelled here — there is intentionally NO field for device geolocation, the
// applications inventory, device contents, or owner personal contact detail
// (P0-490-3 / threat-model I), so json.Unmarshal discards those keys at the decode
// boundary and they never enter memory as connector data.
//
// Field names follow Jamf's webhook event payload (jssID / deviceName / model /
// osVersion / ...). Jamf's webhook posture fields are sparser than the full
// inventory read; absent fields default to false/empty, which is honest
// under-reporting via the stable-optional convention — never over-collection.
type wireEvent struct {
	Webhook struct {
		WebhookEvent string `json:"webhookEvent"`
	} `json:"webhook"`
	Event struct {
		JSSID            int64  `json:"jssID"`
		UDID             string `json:"udid"`
		DeviceName       string `json:"deviceName"`
		OSVersion        string `json:"osVersion"`
		FileVaultEnabled bool   `json:"fileVaultEnabled"`
		PasscodeStatus   string `json:"passcodeStatus"`
		Managed          bool   `json:"managed"`
		Supervised       bool   `json:"supervised"`
		Enrolled         bool   `json:"enrolled"`
		ComplianceStatus string `json:"complianceStatus"`
		Username         string `json:"username"`
		RealName         string `json:"realName"`
	} `json:"event"`
}

// deviceEventPrefix-free allow-list: the Jamf webhook events the connector treats
// as a posture-bearing device change. A delivery for any other event is authentic
// but acts on no device (the parser returns an empty slice → 200 ack, no record).
var jamfPostureEvents = map[string]bool{
	"ComputerCheckIn":               true,
	"ComputerInventoryCompleted":    true,
	"ComputerPolicyFinished":        true,
	"ComputerAdded":                 true,
	"ComputerPushCapabilityChanged": true,
}

// ParseWebhookEvent maps a verified Jamf webhook body into the PII-bounded
// devposture.RawDevices the delivery reports (a Jamf delivery describes a single
// computer, so the slice is 0 or 1). It mirrors the pull path's RawComputer ->
// RawDevice mapping so a webhook-emitted record is shape- and idempotency-key-
// identical to the pull-emitted one (cross-profile dedup). A malformed body
// returns an error (→ 400); a non-posture event or an id-less event returns an
// empty slice (→ 200 ack, no record).
func ParseWebhookEvent(body []byte) ([]devposture.RawDevice, error) {
	var ev wireEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("decode jamf webhook event: %w", err)
	}
	if !jamfPostureEvents[strings.TrimSpace(ev.Webhook.WebhookEvent)] {
		return nil, nil // authentic, not a posture-bearing event
	}
	id := webhookDeviceID(ev)
	if id == "" {
		return nil, nil // no stable device id to attribute / dedup against
	}
	return []devposture.RawDevice{{
		DeviceID:              id,
		DeviceName:            strings.TrimSpace(ev.Event.DeviceName),
		OSVersion:             strings.TrimSpace(ev.Event.OSVersion),
		Platform:              "macOS",
		DiskEncryptionEnabled: ev.Event.FileVaultEnabled,
		ScreenLockEnabled:     strings.EqualFold(strings.TrimSpace(ev.Event.PasscodeStatus), "compliant"),
		Managed:               ev.Event.Managed || ev.Event.Supervised,
		Enrolled:              ev.Event.Enrolled,
		Compliance:            webhookCompliance(ev.Event.ComplianceStatus),
		OwnerAssignmentID:     strings.TrimSpace(ev.Event.Username),
		OwnerDisplayName:      strings.TrimSpace(ev.Event.RealName),
	}}, nil
}

// webhookDeviceID returns the device id the connector attributes + dedups against.
// It MUST match the PULL profile's RawComputer.ID for the same device, or a
// webhook-emitted record and a pull-emitted record will not collapse in the
// ledger. The pull path uses the numeric Jamf REST `id` (the JSS computer id), so
// the webhook path prefers `jssID` (which is that same numeric id) and only falls
// back to the UDID when jssID is absent. (D-JAMF-DEDUP, decisions log.)
func webhookDeviceID(ev wireEvent) string {
	if ev.Event.JSSID > 0 {
		return fmt.Sprintf("%d", ev.Event.JSSID)
	}
	return strings.TrimSpace(ev.Event.UDID)
}

// webhookCompliance maps Jamf's webhook complianceStatus string to the shared
// ComplianceResult enum, mirroring the pull path's complianceOf. An empty/unknown
// status maps to ComplianceUnknown rather than a false "noncompliant".
func webhookCompliance(s string) devposture.ComplianceResult {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "compliant":
		return devposture.ComplianceCompliant
	case "noncompliant", "non-compliant", "notcompliant":
		return devposture.ComplianceNonCompliant
	default:
		return devposture.ComplianceUnknown
	}
}
