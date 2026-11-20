package needsattention

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/policy"
)

// #2587: a pending acknowledgement is the caller's item. A lapsed ack is
// overdue from the day it lapsed; a policy never acknowledged is due soon,
// with no due date, dated from its effective date.
func TestAckItems2587(t *testing.T) {
	t.Parallel()
	caller := uuid.NewString()
	effective := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	acked := time.Date(2025, 10, 1, 9, 0, 0, 0, time.UTC)
	never := policy.Pending{PolicyID: uuid.New(), Title: "never acknowledged", EffectiveDate: &effective}
	lapsed := policy.Pending{PolicyID: uuid.New(), Title: "lapsed", LastAcknowledgedAt: &acked}

	got := AckItems([]policy.Pending{never, lapsed}, caller)
	if len(got) != 2 {
		t.Fatalf("AckItems: %d items; want 2", len(got))
	}
	n, l := got[0], got[1]
	if n.Band != BandDueSoon || n.DueAt != nil || n.Since == nil || !n.Since.Equal(effective) {
		t.Errorf("never acknowledged: band %q due %v since %v; want due_soon, no due date, since %s", n.Band, n.DueAt, n.Since, effective)
	}
	if want := acked.Add(policy.AcknowledgmentFreshness); l.Band != BandOverdue || l.DueAt == nil || !l.DueAt.Equal(want) {
		t.Errorf("lapsed: band %q due %v; want overdue, due %s", l.Band, l.DueAt, want)
	}
	for _, it := range got {
		if it.Type != TypePolicyAck || it.Action != "Acknowledge" {
			t.Errorf("%s: type %q action %q; want %q, Acknowledge", it.Title, it.Type, it.Action, TypePolicyAck)
		}
		if it.Link != "/policies/"+it.ID {
			t.Errorf("%s: link %q; want /policies/%s", it.Title, it.Link, it.ID)
		}
		if it.Owner == nil || it.Owner.UserID != caller {
			t.Errorf("%s: owner %+v; want the caller", it.Title, it.Owner)
		}
	}

	// The item is the caller's in the for-me view, and nobody else's.
	cred := credstore.Credential{ID: "jwt:x", TenantID: tenant2134, UserID: "user:" + caller}
	other := credstore.Credential{ID: "jwt:y", TenantID: tenant2134, UserID: "user:" + uuid.NewString()}
	if len(Rank(got, ForMe, mineFor(cred))) != 2 {
		t.Errorf("for=me: the caller's own acknowledgements are not theirs")
	}
	if len(Rank(got, ForMe, mineFor(other))) != 0 {
		t.Errorf("for=me: another member sees the caller's acknowledgements")
	}
}

// ?type=policy_ack is a known filter.
func TestParseTypes2587_PolicyAck(t *testing.T) {
	t.Parallel()
	got, err := parseTypes([]string{"policy_ack"})
	if err != nil || len(got) != 1 || got[0] != TypePolicyAck {
		t.Errorf("parseTypes(policy_ack) = %v, %v", got, err)
	}
}
