// Package idem derives idempotency keys for AWS connector evidence records.
// Same bucket within the same hour → same key (dedup); new hour → new key
// (fresh record).
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Key returns the idempotency_key for a (bucket_arn, observed_at) pair.
// The hour is truncated to UTC so daylight-savings or local-time skew never
// produces drift in the dedup window.
func Key(bucketARN string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(bucketARN + "|" + hour))
	return hex.EncodeToString(sum[:])
}
