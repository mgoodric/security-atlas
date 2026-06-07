// Package devposture is the shared normalization layer for the MDM connector
// family (Jamf + Intune, slice 490). Both MDMs' managed-device posture reduces
// to the same shape — a device with a stable id, an OS version, a
// disk-encryption state, a screen-lock/passcode-policy compliance flag, an
// overall compliance result, a managed/enrollment state, and the device→owner
// ASSIGNMENT identity needed to attribute the device — so they share one
// evidence kind (endpoint.device_posture.v1) and one normalizer.
//
// The load-bearing guard (P0-490-3 / threat-model I): a Device carries
// posture/compliance SUMMARY + the owner ASSIGNMENT identity (opaque id +
// optional display name) only. It deliberately has NO field for device
// geolocation, installed-app inventory, device contents, browsing data, or
// owner personal contact detail (phone / personal email / address). The type
// system itself is the first line of the over-collection defence: there is
// nowhere to put geolocation, an app list, or owner PII-beyond-assignment. The
// vendor clients decode only the listed fields at the API boundary, so the
// unwanted fields never enter memory as connector data. The cmd-layer test
// asserts no banned key/substring reaches an emitted record.
package devposture

import (
	"sort"
	"strings"
	"time"
)

// MDM identifies the management source. Maps 1:1 to the schema's source_mdm
// enum.
type MDM string

const (
	// MDMJamf is the Jamf Pro MDM connector (managed Macs).
	MDMJamf MDM = "jamf"
	// MDMIntune is the Microsoft Intune MDM connector (Windows / cross-platform).
	MDMIntune MDM = "intune"
)

// ComplianceResult is the overall device-compliance verdict the MDM reports.
// Descriptive — the platform evaluator owns the final pass/fail per
// (control, scope); this is the source's own assessment.
type ComplianceResult string

const (
	// ComplianceCompliant means the MDM evaluates the device as meeting its
	// compliance policy.
	ComplianceCompliant ComplianceResult = "compliant"
	// ComplianceNonCompliant means the MDM evaluates the device as failing its
	// compliance policy.
	ComplianceNonCompliant ComplianceResult = "noncompliant"
	// ComplianceUnknown means the MDM has no compliance verdict (not evaluated,
	// grace period, or the source did not report one).
	ComplianceUnknown ComplianceResult = "unknown"
)

// RawDevice is the narrow, PII-bounded view a vendor client returns for one
// managed device. The vendor clients map their API response into this shape,
// discarding geolocation, installed-app inventory, device contents, and owner
// personal contact detail at the decode boundary. Tests construct it directly.
//
// There is intentionally no Geolocation / InstalledApps / OwnerEmail /
// OwnerPhone field on this struct (P0-490-3).
type RawDevice struct {
	// DeviceID is the MDM-native stable identifier (opaque, non-secret).
	DeviceID string
	// DeviceName is the human-readable device name (asset label, e.g.
	// "ENG-MBP-014"). NOT owner personal contact detail.
	DeviceName string
	// OSVersion is the operating-system version string (e.g. "14.5", "10.0.22631").
	OSVersion string
	// Platform is the OS platform family (e.g. "macOS", "Windows"). Descriptive.
	Platform string
	// DiskEncryptionEnabled is the FileVault (Jamf) / BitLocker (Intune)
	// disk-encryption state.
	DiskEncryptionEnabled bool
	// ScreenLockEnabled is whether a screen-lock / passcode policy is enforced
	// and compliant on the device.
	ScreenLockEnabled bool
	// Managed is whether the device is currently managed/supervised by the MDM
	// (Jamf supervised/managed; Intune managed).
	Managed bool
	// Enrolled is whether the device is currently enrolled in the MDM.
	Enrolled bool
	// Compliance is the MDM's overall compliance verdict. Empty falls back to
	// ComplianceUnknown.
	Compliance ComplianceResult
	// OwnerAssignmentID is the OPAQUE id of the device's assigned user — the
	// minimum identity needed to attribute the device to an owner for an access
	// review. NEVER the owner's personal email, phone, or address.
	OwnerAssignmentID string
	// OwnerDisplayName is the optional human-readable display name of the
	// assigned user (e.g. "A. Engineer"). Assignment identity, NOT contact
	// detail. Optional — empty is fine.
	OwnerDisplayName string
}

// Device is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the endpoint.device_posture.v1 schema.
type Device struct {
	SourceMDM             MDM
	DeviceID              string
	DeviceName            string
	OSVersion             string
	Platform              string
	DiskEncryptionEnabled bool
	ScreenLockEnabled     bool
	Managed               bool
	Enrolled              bool
	Compliance            ComplianceResult
	OwnerAssignmentID     string
	OwnerDisplayName      string
	ObservedAt            time.Time
}

// Normalize converts a vendor's raw devices into normalized Devices, stamping
// the source MDM + a single observed-at. now is injectable for deterministic
// tests (nil -> time.Now UTC). Devices missing an id are dropped (the schema
// requires it) rather than emitting an invalid record. An unset/unknown
// compliance value normalizes to ComplianceUnknown.
func Normalize(mdm MDM, raw []RawDevice, now func() time.Time) []Device {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)
	out := make([]Device, 0, len(raw))
	for _, d := range raw {
		id := strings.TrimSpace(d.DeviceID)
		if id == "" {
			continue
		}
		out = append(out, Device{
			SourceMDM:             mdm,
			DeviceID:              id,
			DeviceName:            strings.TrimSpace(d.DeviceName),
			OSVersion:             strings.TrimSpace(d.OSVersion),
			Platform:              strings.TrimSpace(d.Platform),
			DiskEncryptionEnabled: d.DiskEncryptionEnabled,
			ScreenLockEnabled:     d.ScreenLockEnabled,
			Managed:               d.Managed,
			Enrolled:              d.Enrolled,
			Compliance:            normalizeCompliance(d.Compliance),
			OwnerAssignmentID:     strings.TrimSpace(d.OwnerAssignmentID),
			OwnerDisplayName:      strings.TrimSpace(d.OwnerDisplayName),
			ObservedAt:            observedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func normalizeCompliance(c ComplianceResult) ComplianceResult {
	switch c {
	case ComplianceCompliant, ComplianceNonCompliant:
		return c
	default:
		return ComplianceUnknown
	}
}
