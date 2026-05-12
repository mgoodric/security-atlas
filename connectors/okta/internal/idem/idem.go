// Package idem derives idempotency keys for the Okta connector.
//
// Three emitters, three key shapes (parallel to slice 044's idem
// package). All three collapse same-resource pushes within the same hour
// into the same key, so a re-run within the hour does not double-write
// the ledger.
//
//   - MFAPolicyKey:      sha256("okta.mfa_policy|<policy_id>|<hour>")
//   - AppAssignmentKey:  sha256("okta.app_assignment|<app_id>|<hour>")
//   - UserLifecycleKey:  sha256("okta.user_lifecycle|<user_id>|<hour>")
//
// Anti-criterion P0: every push from this connector derives its
// idempotency_key here. The cmd layer never invents one ad-hoc and never
// pushes with an empty key.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// MFAPolicyKey returns the idempotency key for one (policy_id,
// observed_at) pair. Truncated to the hour in UTC so two runs within the
// same hour produce identical keys.
func MFAPolicyKey(policyID string, observedAt time.Time) string {
	return hashKey("okta.mfa_policy", policyID, observedAt)
}

// AppAssignmentKey returns the idempotency key for one (app_id,
// observed_at) pair.
func AppAssignmentKey(appID string, observedAt time.Time) string {
	return hashKey("okta.app_assignment", appID, observedAt)
}

// UserLifecycleKey returns the idempotency key for one (user_id,
// observed_at) pair.
func UserLifecycleKey(userID string, observedAt time.Time) string {
	return hashKey("okta.user_lifecycle", userID, observedAt)
}

func hashKey(prefix, id string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(prefix + "|" + id + "|" + hour))
	return hex.EncodeToString(sum[:])
}
