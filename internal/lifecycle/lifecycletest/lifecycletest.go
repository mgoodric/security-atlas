// Package lifecycletest checks a declared lifecycle.Table against the real
// thing (#2146). A lifecycle's integration test supplies a Harness over its
// store and a real Postgres; EveryPair then tries every move out of every
// state and fails on any disagreement with the table. TriggerPairs does the
// same for a database trigger, with a raw UPDATE per from -> to pair.
package lifecycletest

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/lifecycle"
)

// Harness drives one lifecycle's store.
type Harness struct {
	// Seed returns the id of a fresh row in state, reached through the
	// store's own moves wherever one exists.
	Seed func(t *testing.T, state string) string
	// Moves maps each action the table names to the call that attempts it
	// on a row. It must cover the table's actions exactly.
	Moves map[string]func(t *testing.T, id string) error
	// State reads a row's current state.
	State func(t *testing.T, id string) string
}

// EveryPair attempts every move out of every declared state. A move the
// table lists must succeed and land in its to-state; any other must leave
// the row where it was. The moves are the only write paths into the state
// column, so a from -> to pair no move reaches is refused by construction;
// EveryPair checks the pairs the moves reached are exactly the table's.
func EveryPair(t *testing.T, tab lifecycle.Table, h Harness) {
	t.Helper()
	actions := tab.Actions()
	for _, a := range actions {
		if h.Moves[a] == nil {
			t.Fatalf("%s: harness has no move for action %q", tab.Name, a)
		}
	}
	for a := range h.Moves {
		if !slices.Contains(actions, a) {
			t.Fatalf("%s: harness move %q is not an action in the table", tab.Name, a)
		}
	}
	reached := map[[2]string]bool{}
	for _, from := range tab.States {
		for _, action := range actions {
			id := h.Seed(t, from)
			if got := h.State(t, id); got != from {
				t.Fatalf("%s: seeded a %s row, got %s", tab.Name, from, got)
			}
			err := h.Moves[action](t, id)
			got := h.State(t, id)
			if tr, ok := tab.Move(from, action); ok {
				if err != nil {
					t.Errorf("%s: %s out of %s: table allows -> %s, store refused: %v", tab.Name, action, from, tr.To, err)
					continue
				}
				if got != tr.To {
					t.Errorf("%s: %s out of %s: table says -> %s, row is %s", tab.Name, action, from, tr.To, got)
				}
			} else if got != from {
				t.Errorf("%s: %s out of %s: table forbids it, row moved to %s (err=%v)", tab.Name, action, from, got, err)
			}
			if got != from {
				reached[[2]string{from, got}] = true
			}
		}
	}
	for _, from := range tab.States {
		for _, to := range tab.States {
			if from == to {
				continue
			}
			if allowed, did := tab.Allows(from, to), reached[[2]string{from, to}]; allowed != did {
				t.Errorf("%s: %s -> %s: table allows=%v, a move reached it=%v", tab.Name, from, to, allowed, did)
			}
		}
	}
}

// TriggerPairs checks a database trigger against the table. For every
// from-state the trigger covers and every other state, it seeds a row,
// sets the state column directly, and expects success exactly when the
// table allows the pair; a refusal must be a check_violation.
func TriggerPairs(
	t *testing.T,
	tab lifecycle.Table,
	seed func(t *testing.T, state string) string,
	set func(t *testing.T, id, to string) error,
	state func(t *testing.T, id string) string,
) {
	t.Helper()
	if tab.Trigger == nil {
		t.Fatalf("%s: table declares no trigger", tab.Name)
	}
	for _, from := range tab.Trigger.From {
		for _, to := range tab.States {
			if from == to {
				continue
			}
			id := seed(t, from)
			err := set(t, id, to)
			got := state(t, id)
			if tab.Allows(from, to) {
				if err != nil || got != to {
					t.Errorf("%s: %s refused %s -> %s, which the table allows: %v", tab.Name, tab.Trigger.Name, from, to, err)
				}
				continue
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("%s: %s -> %s: want a check_violation from %s, got %v", tab.Name, from, to, tab.Trigger.Name, err)
			}
			if got != from {
				t.Errorf("%s: %s -> %s: table forbids it, row is %s", tab.Name, from, to, got)
			}
		}
	}
}
