// Package idem derives idempotency keys for GCP connector evidence records,
// mirroring the slice-004 AWS / slice-443 Slack connector pattern: a stable
// per-entity anchor combined with an hour-truncated observed_at so two runs
// within the same hour for the same entity collapse to one ledger row, while
// a run that crosses an hour boundary writes a fresh record.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Key returns the idempotency_key for an (anchor, observed_at) pair. anchor
// is the stable per-entity identifier — for a GCP IAM binding it is the
// "<project>|<role>|<member>" triple; for a Cloud Storage bucket it is the
// globally-unique bucket name. The hour is truncated to UTC so local-time
// skew never produces drift in the dedup window.
func Key(anchor string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(anchor + "|" + hour))
	return hex.EncodeToString(sum[:])
}
