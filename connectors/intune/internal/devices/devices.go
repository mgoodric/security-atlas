// Package devices pulls Microsoft Intune managed-device compliance posture via
// the read-only Microsoft Graph device-management API
// (GET /deviceManagement/managedDevices, requires only
// DeviceManagementManagedDevices.Read.All).
//
// The load-bearing guard (P0-490-3 / threat-model I): the client requests an
// explicit $select of posture-relevant properties only and decodes ONLY each
// device's id, name, OS version, BitLocker (disk-encryption) state, compliance
// state, management/enrollment state, and the assigned user's OPAQUE principal
// name + display name. It NEVER requests or decodes device geolocation, the
// detectedApps inventory, device contents, or the owner's personal phone /
// email — those are not in the $select and have no field on RawDevice.
package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Graph API calls; tests pass a fake. v0 reads the first
// bounded page; cursor pagination (@odata.nextLink) is a documented follow-on
// (threat-model D).
type API interface {
	ListManagedDevices(ctx context.Context) ([]RawDevice, error)
}

// RawDevice is the narrow, PII-bounded view the Graph client returns for one
// Intune managed device. The HTTP client maps the Graph response into this
// shape, discarding geolocation, the detectedApps inventory, and owner contact
// detail at the decode boundary. Tests construct it directly.
//
// There is intentionally no Geolocation / DetectedApps / OwnerPhone field on
// this struct (P0-490-3).
type RawDevice struct {
	ID                string
	Name              string
	OSVersion         string
	OS                string
	Encrypted         bool
	PasscodeCompliant bool
	ComplianceState   string // Graph: compliant / noncompliant / unknown / ...
	ManagementState   string // Graph managementAgent / state
	Enrolled          bool
	OwnerAssignmentID string // userPrincipalName (opaque identity) — NOT a contact email used for messaging
	OwnerDisplayName  string
}

// Collect lists every visible Intune managed device and returns PII-bounded
// devposture.RawDevices ready for devposture.Normalize. Separated from
// normalization so the cmd layer owns the observed-at clock.
func Collect(ctx context.Context, api API) ([]devposture.RawDevice, error) {
	if api == nil {
		return nil, errors.New("devices: API is nil")
	}
	raw, err := api.ListManagedDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list intune managed devices: %w", err)
	}
	out := make([]devposture.RawDevice, 0, len(raw))
	for _, d := range raw {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		out = append(out, devposture.RawDevice{
			DeviceID:              d.ID,
			DeviceName:            d.Name,
			OSVersion:             d.OSVersion,
			Platform:              platformOf(d.OS),
			DiskEncryptionEnabled: d.Encrypted,
			ScreenLockEnabled:     d.PasscodeCompliant,
			Managed:               d.ManagementState != "" && d.ManagementState != "unknown",
			Enrolled:              d.Enrolled,
			Compliance:            complianceOf(d.ComplianceState),
			OwnerAssignmentID:     d.OwnerAssignmentID,
			OwnerDisplayName:      d.OwnerDisplayName,
		})
	}
	return out, nil
}

func platformOf(os string) string {
	o := strings.TrimSpace(os)
	if o == "" {
		return ""
	}
	return o
}

func complianceOf(state string) devposture.ComplianceResult {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "compliant":
		return devposture.ComplianceCompliant
	case "noncompliant", "non-compliant", "ingraceperiod", "in_grace_period", "conflict", "error":
		return devposture.ComplianceNonCompliant
	default:
		return devposture.ComplianceUnknown
	}
}
