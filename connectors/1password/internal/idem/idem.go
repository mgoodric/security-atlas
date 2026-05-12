// Package idem derives idempotency keys for the 1Password connector.
//
// Slice 046 emits one evidence kind (1password.org_policy.v1), so this
// package exposes a single key derivation: OrgPolicyKey. The shape
// matches the slice 044 convention — sha256 hex over
// `kind|<resource>|<hour>` so two runs within the same hour collapse to
// the same evidence record and runs in different hours don't.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// OrgPolicyKey returns the idempotency key for a (org_id, observed_at)
// pair. Truncated to the hour in UTC so two runs within the same hour
// produce identical keys, and runs that cross an hour boundary do not.
func OrgPolicyKey(orgID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("1password.org_policy|" + orgID + "|" + hour))
	return hex.EncodeToString(sum[:])
}
