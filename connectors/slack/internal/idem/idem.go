// Package idem derives idempotency keys for Slack connector evidence
// records, mirroring the slice-004 AWS connector pattern: a stable per-entity
// anchor combined with an hour-truncated observed_at so two runs within the
// same hour for the same entity collapse to one ledger row, while a run that
// crosses an hour boundary writes a fresh record.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Key returns the idempotency_key for an (anchor, observed_at) pair. anchor
// is the stable per-entity identifier — a Slack user id for a member record,
// the audit entry id for an admin-action record, the team id for a retention
// record. The hour is truncated to UTC so local-time skew never produces
// drift in the dedup window.
func Key(anchor string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(anchor + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// EventKey is the idempotency key for an audit event. The Slack audit entry
// id is globally unique and immutable, so it anchors the key directly — two
// runs that observe the same audit entry (even across hour boundaries)
// collapse to one record. The event's own DateCreate is folded in so a
// re-emitted id (Slack reuse is not contractually impossible) still keys on
// its observed moment.
func EventKey(entryID string, dateCreate int64) string {
	sum := sha256.Sum256([]byte(entryID + "|" + strconv.FormatInt(dateCreate, 10)))
	return hex.EncodeToString(sum[:])
}
