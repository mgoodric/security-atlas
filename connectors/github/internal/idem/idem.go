// Package idem derives idempotency keys for the GitHub connector.
//
// Three emitters, three key shapes:
//
//   - RepoProtectionKey: same repo + same hour → same key (dedup).
//   - SCIMUserKey: same SCIM user id + same hour → same key.
//   - DeliveryKey: GitHub's X-GitHub-Delivery UUID is already unique per
//     delivery, so we use it directly without hashing. This makes
//     anti-criterion enforcement obvious — the test verifies key == header.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// RepoProtectionKey returns the idempotency key for a (repo_full_name,
// observed_at) pair. Truncated to the hour in UTC so two runs within the
// same hour produce identical keys.
func RepoProtectionKey(repoFullName string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("github.repo_protection|" + repoFullName + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// SCIMUserKey returns the idempotency key for a (scim_user_id, observed_at)
// pair. Same truncation rules.
func SCIMUserKey(scimUserID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("github.scim_user|" + scimUserID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// DeliveryKey returns the idempotency key for a webhook delivery.
// X-GitHub-Delivery is a GitHub-assigned UUID, unique per delivery, so we
// use it verbatim (trimmed of surrounding whitespace) without hashing.
// Empty input returns the empty string — the caller must reject.
func DeliveryKey(xGitHubDelivery string) string {
	return strings.TrimSpace(xGitHubDelivery)
}
