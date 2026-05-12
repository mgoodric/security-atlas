package idem_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/idem"
)

func TestTicketKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	if a, b := idem.TicketKey("PROJ-123", t1), idem.TicketKey("PROJ-123", t2); a != b {
		t.Fatalf("keys differ within hour: %s vs %s", a, b)
	}
}

func TestTicketKey_RotatesOnHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	if idem.TicketKey("PROJ-123", t1) == idem.TicketKey("PROJ-123", t2) {
		t.Fatal("keys identical across hour boundary")
	}
}

func TestTicketKey_DistinctPerTicket(t *testing.T) {
	now := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	if idem.TicketKey("PROJ-1", now) == idem.TicketKey("PROJ-2", now) {
		t.Fatal("different tickets collided on key")
	}
}

func TestTicketKey_IsHex(t *testing.T) {
	k := idem.TicketKey("PROJ-1", time.Now())
	if len(k) != 64 {
		t.Fatalf("key length = %d; want 64 hex chars", len(k))
	}
	if strings.ContainsAny(k, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("key not hex: %s", k)
	}
}

// Anti-criterion P0: the orchestrator pins the idempotency derivation as
// sha256("jira.ticket_evidence" + ticket_id + hour). The platform shape
// matters too — Jira and Linear both emit the same evidence_kind, so
// passing the *kind* in the hash domain (not the *platform*) keeps a
// PROJ-1 in Jira from colliding with a PROJ-1 in Linear only when the
// caller supplies platform-prefixed ticket keys. Tests pin the exact
// hash so future refactors can't silently drift.
func TestTicketKey_PinnedHash(t *testing.T) {
	tm := time.Date(2026, 5, 11, 14, 30, 0, 0, time.UTC)
	got := idem.TicketKey("PROJ-123", tm)
	// Verbatim sha256 of "jira.ticket_evidence" + "PROJ-123" + "2026-05-11T14:00:00Z".
	const want = "73e14bf42f4498da6259ab2eb15d552fa4c1f8f86c0f8b8d26aa57e37f90bffa"
	if got != want {
		t.Fatalf("hash drift: got %s want %s", got, want)
	}
}
