// Package idem derives idempotency keys for the MDM connector family (Jamf +
// Intune, slice 490). One key shape across both vendors, parallel to the slice
// 004 / 486 / 487 / 488 idem packages.
//
//	DevicePostureKey:      sha256("endpoint.device_posture|<mdm>/<device_id>|<hour>")
//	SoftwareInventoryKey:  sha256("endpoint.software_inventory|<mdm>/<device_id>|<hour>")
//	ConfigProfileKey:      sha256("endpoint.config_profile|<mdm>/<device_id>|<hour>")
//
// The mdm + device_id together uniquely identify a managed device; truncating
// observed_at to the UTC hour collapses same-device re-runs within the hour
// into one ledger row. The three key shapes are namespaced by their evidence-
// kind prefix so a device's posture record (slice 490), its software-inventory
// record (slice 555), and its config-profile record (slice 556) never collide
// on the same idempotency key for a given device + hour.
//
// Anti-criterion: every push from either MDM connector derives its
// idempotency_key here. The cmd layer never invents one ad-hoc and never pushes
// with an empty key.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// DevicePostureKey returns the idempotency key for one managed device's posture
// record.
func DevicePostureKey(mdm, deviceID string, observedAt time.Time) string {
	return key("endpoint.device_posture", mdm, deviceID, observedAt)
}

// SoftwareInventoryKey returns the idempotency key for one managed device's
// installed-software inventory record (slice 555). Namespaced separately from
// DevicePostureKey so the two kinds never collide for the same device + hour.
func SoftwareInventoryKey(mdm, deviceID string, observedAt time.Time) string {
	return key("endpoint.software_inventory", mdm, deviceID, observedAt)
}

// ConfigProfileKey returns the idempotency key for one managed device's
// configuration-profile detail record (slice 556). Namespaced separately from
// DevicePostureKey and SoftwareInventoryKey so the three kinds never collide
// for the same device + hour.
func ConfigProfileKey(mdm, deviceID string, observedAt time.Time) string {
	return key("endpoint.config_profile", mdm, deviceID, observedAt)
}

func key(kindPrefix, mdm, deviceID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(kindPrefix + "|" + mdm + "/" + deviceID + "|" + hour))
	return hex.EncodeToString(sum[:])
}
