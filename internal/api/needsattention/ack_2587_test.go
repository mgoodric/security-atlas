package needsattention

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/policy"
)

// #2587: a pending acknowledgement is the caller's item. A lapsed ack is
// overdue from the day it lapsed; a policy never acknowledged is due soon
// until policy.AckGraceDays after its effective date (#2736).
func TestAckItems2587(t *testing.T) {
	t.Parallel()
	caller := uuid.NewString()
	effective := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	acked := time.Date(2025, 10, 1, 9, 0, 0, 0, time.UTC)
	never := policy.Pending{PolicyID: uuid.New(), Title: "never acknowledged", EffectiveDate: &effective}
	lapsed := policy.Pending{PolicyID: uuid.New(), Title: "lapsed", LastAcknowledgedAt: &acked}

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	got := AckItems([]policy.Pending{never, lapsed}, caller, now)
	if len(got) != 2 {
		t.Fatalf("AckItems: %d items; want 2", len(got))
	}
	n, l := got[0], got[1]
	if want := effective.AddDate(0, 0, policy.AckGraceDays); n.Band != BandDueSoon || n.DueAt == nil || !n.DueAt.Equal(want) {
		t.Errorf("never acknowledged: band %q due %v; want due_soon, due %s", n.Band, n.DueAt, want)
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

// An ack that would lapse after now is not a lapse of this version: the
// item is never acknowledged, due on the effective date plus the grace,
// not overdue with the lapse's future due date.
func TestAckItems2587_UnlapsedAckIsNone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)
	effective := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	got := AckItems([]policy.Pending{{PolicyID: uuid.New(), Title: "re-published", LastAcknowledgedAt: &recent, EffectiveDate: &effective}}, uuid.NewString(), now)
	want := time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
	if len(got) != 1 || got[0].Band != BandDueSoon || got[0].DueAt == nil || !got[0].DueAt.Equal(want) {
		t.Errorf("unlapsed ack: %+v; want one due_soon item, due %s", got, want)
	}
}

// #2736: a policy ignored past its grace period is overdue, due on the
// effective date plus the grace, and ranks ahead of one still in its grace
// period: doing nothing no longer ranks better than lapsing.
func TestAckItems2736_IgnoredIsOverdue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ignored := time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)
	edge := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC) // grace day is today: due soon
	past := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) // grace day was yesterday: overdue
	got := AckItems([]policy.Pending{
		{PolicyID: uuid.New(), Title: "edge", EffectiveDate: &edge},
		{PolicyID: uuid.New(), Title: "ignored", EffectiveDate: &ignored},
		{PolicyID: uuid.New(), Title: "past", EffectiveDate: &past},
	}, uuid.NewString(), now)
	want := map[string]struct {
		band string
		due  time.Time
	}{
		"edge":    {BandDueSoon, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)},
		"ignored": {BandOverdue, time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)},
		"past":    {BandOverdue, time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)},
	}
	for _, it := range got {
		w := want[it.Title]
		if it.Band != w.band || it.DueAt == nil || !it.DueAt.Equal(w.due) {
			t.Errorf("%s: band %q due %v; want %s, due %s", it.Title, it.Band, it.DueAt, w.band, w.due)
		}
	}
	ranked := Rank(got, ForAll, nil)
	if ranked[0].Title != "ignored" || ranked[1].Title != "past" || ranked[2].Title != "edge" {
		t.Errorf("rank: %s, %s, %s; want ignored, past, edge", ranked[0].Title, ranked[1].Title, ranked[2].Title)
	}
}

// A credential with no person behind it (bootstrap admin, an unbound key,
// a machine token) acknowledges nothing, so it gets no item. ackItems
// returns before touching the database: the Handler here has none.
func TestAckItems2587_MachineCredentialHasNone(t *testing.T) {
	t.Parallel()
	h := New(nil, nil, nil)
	for _, cred := range []credstore.Credential{
		{ID: "bootstrap", TenantID: tenant2134, UserID: "bootstrap-admin", IsAdmin: true},
		{ID: "key_abc", TenantID: tenant2134, UserID: "key_abc", OwnerRoles: []string{"grc_engineer"}},
		{ID: "jwt:m", TenantID: tenant2134, UserID: "oauth_client:" + uuid.NewString(), TenantRoles: []string{"grc_engineer"}},
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/needs-attention", nil)
		got, err := h.ackItems(r, cred)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %d items, err %v; want none", cred.UserID, len(got), err)
		}
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
