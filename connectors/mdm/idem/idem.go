// Package idem derives idempotency keys for the MDM connector family (Jamf +
// Intune, slice 490). One key shape across both vendors, parallel to the slice
// 004 / 486 / 487 / 488 idem packages.
//
//	DevicePostureKey: sha256("endpoint.device_posture|<mdm>/<device_id>|<hour>")
//
// The mdm + device_id together uniquely identify a managed device; truncating
// observed_at to the UTC hour collapses same-device re-runs within the hour
// into one ledger row.
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
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("endpoint.device_posture|" + mdm + "/" + deviceID + "|" + hour))
	return hex.EncodeToString(sum[:])
}
