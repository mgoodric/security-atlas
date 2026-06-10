package devices

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// graphNotificationBatch is the SUMMARY-ONLY view of a Microsoft Graph
// change-notification delivery. Graph posts a batch `{"value": [ ... ]}`; each
// entry carries the changed resource id (in resourceData.id) and, when the
// subscription requested rich notifications, a bounded set of resourceData
// properties. Only posture-summary + the owner ASSIGNMENT identity are modelled
// here — there is intentionally NO field for device geolocation, the detectedApps
// inventory, device contents, or owner personal contact detail (P0-490-3 /
// threat-model I), so json.Unmarshal discards those keys at the decode boundary
// and they never enter memory as connector data.
//
// The platform-side wire is push (invariant #3); this parser runs inside the
// connector on a webhook the connector receives FROM Graph.
type graphNotificationBatch struct {
	Value []graphNotification `json:"value"`
}

type graphNotification struct {
	// ClientState is the per-subscription secret Graph echoes; the
	// ClientStateVerifier checks it. Carried here so a single decode serves both
	// verification and parsing.
	ClientState string `json:"clientState"`
	// ChangeType is created / updated / deleted. The connector acts on
	// created/updated (a posture change); deleted carries no posture to assess.
	ChangeType string `json:"changeType"`
	// ResourceData carries the changed managed-device id and (rich notifications)
	// a bounded set of posture properties.
	ResourceData struct {
		ID                string `json:"id"`
		DeviceName        string `json:"deviceName"`
		OSVersion         string `json:"osVersion"`
		OperatingSystem   string `json:"operatingSystem"`
		IsEncrypted       *bool  `json:"isEncrypted"`
		ComplianceState   string `json:"complianceState"`
		ManagementAgent   string `json:"managementAgent"`
		UserPrincipalName string `json:"userPrincipalName"`
		UserDisplayName   string `json:"userDisplayName"`
	} `json:"resourceData"`
}

// ExtractClientState pulls the clientState the delivery carries for the
// ClientStateVerifier. It returns the value of the FIRST notification, and false
// if the body is unparseable or carries no notification — but it ALSO requires
// every notification in the batch to carry the SAME clientState, returning false
// if any differs (a batch must be uniformly authentic; a partially-forged batch
// is rejected wholesale). The shared verifier maps a false/mismatch to a bare 401.
func ExtractClientState(body []byte) (string, bool) {
	var batch graphNotificationBatch
	if err := json.Unmarshal(body, &batch); err != nil || len(batch.Value) == 0 {
		return "", false
	}
	first := strings.TrimSpace(batch.Value[0].ClientState)
	if first == "" {
		return "", false
	}
	for _, n := range batch.Value[1:] {
		if strings.TrimSpace(n.ClientState) != first {
			return "", false
		}
	}
	return first, true
}

// ParseChangeNotification maps a verified Graph change-notification batch into the
// PII-bounded devposture.RawDevices it reports (one per created/updated device
// notification carrying a device id). It maps the posture fields Graph's
// resourceData carries (rich notifications); when only the id is present the
// device is emitted with just id + descriptive defaults — the connector NEVER
// re-reads beyond the posture-summary field set to enrich it (over-collection
// guard). A malformed body returns an error (→ 400); a batch with no
// posture-bearing device returns an empty slice (→ 200 ack, no record).
func ParseChangeNotification(body []byte) ([]devposture.RawDevice, error) {
	var batch graphNotificationBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, fmt.Errorf("decode graph change notification: %w", err)
	}
	out := make([]devposture.RawDevice, 0, len(batch.Value))
	for _, n := range batch.Value {
		if strings.EqualFold(strings.TrimSpace(n.ChangeType), "deleted") {
			continue // a deleted device carries no posture to assess
		}
		id := strings.TrimSpace(n.ResourceData.ID)
		if id == "" {
			continue
		}
		rd := n.ResourceData
		dev := devposture.RawDevice{
			DeviceID:          id,
			DeviceName:        strings.TrimSpace(rd.DeviceName),
			OSVersion:         strings.TrimSpace(rd.OSVersion),
			Platform:          strings.TrimSpace(rd.OperatingSystem),
			Enrolled:          true, // present in a managedDevices notification => enrolled
			Compliance:        notificationCompliance(rd.ComplianceState),
			OwnerAssignmentID: strings.TrimSpace(rd.UserPrincipalName),
			OwnerDisplayName:  strings.TrimSpace(rd.UserDisplayName),
		}
		if rd.IsEncrypted != nil {
			dev.DiskEncryptionEnabled = *rd.IsEncrypted
		}
		// Intune folds passcode/screen-lock into the overall complianceState; a
		// compliant device satisfies the configured passcode policy (mirrors the
		// pull path's RawDevice mapping).
		dev.ScreenLockEnabled = strings.EqualFold(strings.TrimSpace(rd.ComplianceState), "compliant")
		// Managed: a device present in a managedDevices notification is managed
		// unless the agent explicitly reports "none". When the rich-notification
		// field is absent, default to managed (it is in managedDevices) —
		// descriptive; the evaluator owns the verdict.
		dev.Managed = !strings.EqualFold(strings.TrimSpace(rd.ManagementAgent), "none")
		out = append(out, dev)
	}
	return out, nil
}

// notificationCompliance maps Graph's complianceState string to the shared
// ComplianceResult enum, mirroring the pull path. An empty/unknown value maps to
// ComplianceUnknown rather than a false "noncompliant".
func notificationCompliance(s string) devposture.ComplianceResult {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "compliant":
		return devposture.ComplianceCompliant
	case "noncompliant", "non-compliant", "notcompliant":
		return devposture.ComplianceNonCompliant
	default:
		// Graph "inGracePeriod" / "configManager" / "unknown" / "" are not a
		// definite verdict — report unknown rather than a false noncompliant.
		return devposture.ComplianceUnknown
	}
}
