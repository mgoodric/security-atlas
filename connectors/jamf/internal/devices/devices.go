// Package devices pulls Jamf Pro managed-computer posture via the read-only
// Jamf Pro API (GET /api/v1/computers-inventory, requires only read-inventory
// privileges).
//
// The load-bearing guard (P0-490-3 / threat-model I): the client decodes ONLY
// each computer's id, name, OS version, FileVault (disk-encryption) state,
// passcode/screen-lock-compliance state, managed/supervised state, and the
// assigned user's OPAQUE id + display name. It NEVER materializes or emits
// device geolocation (Jamf "location" GPS), the installed-applications section,
// device contents, or the owner's personal email / phone / address — those
// inventory sections are simply not requested (the `section=` query asks for the
// posture-relevant sections only) and are not decoded into RawComputer.
package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Jamf Pro API calls; tests pass a fake. v0 reads the first
// bounded page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListComputers(ctx context.Context) ([]RawComputer, error)
}

// RawComputer is the narrow, PII-bounded view the Jamf client returns for one
// managed computer. The HTTP client maps the Jamf inventory response into this
// shape, discarding geolocation, the applications section, and owner contact
// detail at the decode boundary. Tests construct it directly.
//
// There is intentionally no Geolocation / InstalledApps / OwnerEmail /
// OwnerPhone field on this struct (P0-490-3).
type RawComputer struct {
	ID                string
	Name              string
	OSVersion         string
	FileVaultEnabled  bool
	PasscodeCompliant bool
	Managed           bool
	Supervised        bool
	Enrolled          bool
	Compliant         bool
	// HasCompliance reports whether the source provided a compliance verdict at
	// all; false maps to ComplianceUnknown rather than falsely "noncompliant".
	HasCompliance     bool
	OwnerAssignmentID string
	OwnerDisplayName  string
}

// Collect lists every visible managed computer and returns PII-bounded
// RawDevices ready for devposture.Normalize. Separated from normalization so
// the cmd layer owns the observed-at clock.
func Collect(ctx context.Context, api API) ([]devposture.RawDevice, error) {
	if api == nil {
		return nil, errors.New("devices: API is nil")
	}
	raw, err := api.ListComputers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list jamf computers: %w", err)
	}
	out := make([]devposture.RawDevice, 0, len(raw))
	for _, c := range raw {
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		out = append(out, devposture.RawDevice{
			DeviceID:              c.ID,
			DeviceName:            c.Name,
			OSVersion:             c.OSVersion,
			Platform:              "macOS",
			DiskEncryptionEnabled: c.FileVaultEnabled,
			ScreenLockEnabled:     c.PasscodeCompliant,
			Managed:               c.Managed || c.Supervised,
			Enrolled:              c.Enrolled,
			Compliance:            complianceOf(c),
			OwnerAssignmentID:     c.OwnerAssignmentID,
			OwnerDisplayName:      c.OwnerDisplayName,
		})
	}
	return out, nil
}

func complianceOf(c RawComputer) devposture.ComplianceResult {
	if !c.HasCompliance {
		return devposture.ComplianceUnknown
	}
	if c.Compliant {
		return devposture.ComplianceCompliant
	}
	return devposture.ComplianceNonCompliant
}
