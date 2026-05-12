// Package idem derives idempotency keys for the osquery/Fleet connector.
//
// One emitter, one key shape (parallel to slices 044 / 045 / 046):
//
//	HostPostureKey: sha256("osquery.host_posture|<host_uuid>|<hour>")
//
// The hour-truncation is the observation_window: two runs that observe the
// same host within the same UTC hour collapse to the same key, so the
// ledger dedupes without losing information about temporal change.
//
// Anti-criterion P0: every push from this connector derives its
// idempotency_key here. The cmd layer never invents one ad-hoc and never
// pushes with an empty key. The test pins (a) determinism within the
// hour, (b) rotation across the hour boundary, (c) hex shape (64 chars).
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// HostPostureKey returns the idempotency key for one (host_uuid,
// observed_at) pair. Truncated to the hour in UTC so two runs within the
// same hour produce identical keys.
//
// Empty host_uuid returns the empty string — the caller MUST reject so a
// missing-uuid record never reaches the ledger with a fabricated key.
func HostPostureKey(hostUUID string, observedAt time.Time) string {
	if hostUUID == "" {
		return ""
	}
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("osquery.host_posture|" + hostUUID + "|" + hour))
	return hex.EncodeToString(sum[:])
}
