// Package idem derives idempotency keys for the Jira/Linear connector.
//
// The orchestrator pins the derivation to
//
//	sha256("jira.ticket_evidence" + ticket_id + hour)
//
// — the evidence_kind goes in the hash domain (not the platform) because
// Jira and Linear both emit jira.ticket_evidence.v1. Callers are
// responsible for supplying disambiguated ticket keys when a Jira project
// and a Linear team could collide on the bare numeric id; the run loop
// composes platform-prefixed keys (e.g. "JIRA:PROJ-123",
// "LINEAR:TEAM-456") before calling in.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// TicketKey returns the idempotency key for a (ticket_id, observed_at)
// pair. observed_at is truncated to the hour in UTC so two runs within
// the same hour produce identical keys (the ledger then dedupes).
func TicketKey(ticketID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("jira.ticket_evidence" + ticketID + hour))
	return hex.EncodeToString(sum[:])
}
