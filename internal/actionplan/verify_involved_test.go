// Pure-Go unit tests for the #2434 owner rule: who the audit trail
// refuses as a plan's verifier. No Postgres, no build tag.

package actionplan

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func trailRow(actor uuid.UUID, action string, before, after map[string]string) dbx.ActionPlanAuditLog {
	row := dbx.ActionPlanAuditLog{ID: pgUUID(uuid.New()), ActionType: action}
	if actor != uuid.Nil {
		row.ActorID = pgUUID(actor)
	}
	if before != nil {
		row.BeforeState, _ = json.Marshal(before)
	}
	if after != nil {
		row.AfterState, _ = json.Marshal(after)
	}
	return row
}

func st(status string, owner uuid.UUID) map[string]string {
	return map[string]string{"status": status, "owner_id": owner.String()}
}

func TestInvolvedSinceCompletion(t *testing.T) {
	t.Parallel()
	owner, colleague, completer, earlyOwner, bystander := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	trail := []dbx.ActionPlanAuditLog{
		trailRow(earlyOwner, ActionCreated, nil, st(StatusDraft, earlyOwner)),
		// Owned before completion only: not refused.
		trailRow(earlyOwner, ActionUpdated, st(StatusDraft, earlyOwner), st(StatusDraft, owner)),
		trailRow(owner, ActionStatusChanged, st(StatusDraft, owner), st(StatusInProgress, owner)),
		trailRow(completer, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
		// A link row carries a target, not an owner.
		trailRow(owner, ActionRiskLinked, nil, map[string]string{"target_id": uuid.NewString()}),
		// The bypass: hand to a colleague, and back.
		trailRow(owner, ActionUpdated, st(StatusCompleted, owner), st(StatusCompleted, colleague)),
		trailRow(colleague, ActionUpdated, st(StatusCompleted, colleague), st(StatusCompleted, owner)),
	}
	got, err := involvedSinceCompletion(trail)
	if err != nil {
		t.Fatalf("involvedSinceCompletion: %v", err)
	}
	for name, u := range map[string]uuid.UUID{"owner": owner, "colleague": colleague, "completer": completer} {
		if !got[u] {
			t.Errorf("%s not refused", name)
		}
	}
	for name, u := range map[string]uuid.UUID{"early owner": earlyOwner, "bystander": bystander} {
		if got[u] {
			t.Errorf("%s refused, want allowed", name)
		}
	}
}

// A system unverify (verified -> completed) is not a completion: the
// window still opens at the person's move.
func TestInvolvedSinceCompletion_SystemUnverifyIsNotACompletion(t *testing.T) {
	t.Parallel()
	owner, completer := uuid.New(), uuid.New()
	got, err := involvedSinceCompletion([]dbx.ActionPlanAuditLog{
		trailRow(completer, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
		trailRow(uuid.Nil, ActionStatusChanged, st(StatusCompleted, owner), st(StatusVerified, owner)),
		trailRow(uuid.Nil, ActionStatusChanged, st(StatusVerified, owner), st(StatusCompleted, owner)),
	})
	if err != nil {
		t.Fatalf("involvedSinceCompletion: %v", err)
	}
	if !got[completer] || !got[owner] {
		t.Fatalf("completer/owner not refused: %v", got)
	}
}

func TestInvolvedSinceCompletion_FailsClosed(t *testing.T) {
	t.Parallel()
	owner := uuid.New()
	bad := trailRow(owner, ActionUpdated, nil, nil)
	bad.AfterState = []byte("{not json")
	badOwner := trailRow(owner, ActionUpdated, nil, map[string]string{"owner_id": "nope"})
	cases := map[string][]dbx.ActionPlanAuditLog{
		"empty trail":   nil,
		"no completion": {trailRow(owner, ActionCreated, nil, st(StatusDraft, owner))},
		"completion with no person": {
			trailRow(uuid.Nil, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
		},
		"unparseable state": {
			trailRow(owner, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
			bad,
		},
		"unparseable owner": {
			trailRow(owner, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
			badOwner,
		},
	}
	for name, trail := range cases {
		if _, err := involvedSinceCompletion(trail); !errors.Is(err, ErrVerifierInvolved) {
			t.Errorf("%s: err = %v, want ErrVerifierInvolved", name, err)
		}
	}
}

// #2639: the trail orders by created_at, and a row can sort before the
// completion it followed. An owner on a row whose before- or after-status
// is completed or verified is refused in any order.
func TestInvolvedSinceCompletion_OrderIndependent(t *testing.T) {
	t.Parallel()
	owner, colleague, completer := uuid.New(), uuid.New(), uuid.New()
	completion := trailRow(completer, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner))
	handOff := trailRow(owner, ActionUpdated, st(StatusCompleted, owner), st(StatusCompleted, colleague))
	handBack := trailRow(colleague, ActionUpdated, st(StatusCompleted, colleague), st(StatusCompleted, owner))
	orders := map[string][]dbx.ActionPlanAuditLog{
		"commit order":          {completion, handOff, handBack},
		"owner changes first":   {handOff, handBack, completion},
		"completion in between": {handOff, completion, handBack},
	}
	for name, trail := range orders {
		got, err := involvedSinceCompletion(trail)
		if err != nil {
			t.Fatalf("%s: involvedSinceCompletion: %v", name, err)
		}
		for who, u := range map[string]uuid.UUID{"owner": owner, "colleague": colleague, "completer": completer} {
			if !got[u] {
				t.Errorf("%s: %s not refused", name, who)
			}
		}
	}
}

// An owner on a row before the first completion that never touched
// completed or verified is still allowed: the rule widens the window only
// by closed rows.
func TestInvolvedSinceCompletion_OpenRowsBeforeCompletionNotCounted(t *testing.T) {
	t.Parallel()
	early, owner, completer := uuid.New(), uuid.New(), uuid.New()
	got, err := involvedSinceCompletion([]dbx.ActionPlanAuditLog{
		trailRow(early, ActionUpdated, st(StatusInProgress, early), st(StatusInProgress, owner)),
		trailRow(completer, ActionStatusChanged, st(StatusInProgress, owner), st(StatusCompleted, owner)),
	})
	if err != nil {
		t.Fatalf("involvedSinceCompletion: %v", err)
	}
	if got[early] {
		t.Fatal("pre-completion owner refused, want allowed")
	}
}
