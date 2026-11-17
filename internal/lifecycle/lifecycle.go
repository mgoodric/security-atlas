// Package lifecycle declares the shape of a lifecycle's transition table:
// its states, the moves allowed between them, and who may make each move
// (#2146). Each domain package declares its own table next to the code that
// performs the moves (exception.Lifecycle, policy.Lifecycle, ...); this
// package holds only the shape and the checks every table must pass.
//
// The tables describe what the product does today. They do not drive it:
// the stores and the database guard the moves, and each package's
// lifecycle integration test proves the table and those guards agree, pair
// by pair, against a real Postgres. cmd/atlas-lifecycles exports every
// table as JSON (docs/lifecycles/) for the in-app help's diagram check
// (#2148).
package lifecycle

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Actor kinds. A person acts through an authenticated request; the system
// acts on its own (a scheduled sweep, or a side effect of another move).
const (
	ActorPerson = "person"
	ActorSystem = "system"
)

// Enforcement kinds: what refuses a move the table does not list (#2147).
const (
	// EnforcedByDatabase: a database trigger refuses moves out of the
	// states Trigger covers, on top of the stores' own checks.
	EnforcedByDatabase = "database"
	// EnforcedByStore: only the Go store refuses a move, through its
	// state checks and guarded UPDATEs. No trigger guards the column.
	EnforcedByStore = "store"
	// EnforcedByDerivation: nothing refuses a move row by row. The state
	// is recomputed from its inputs (evidence, exceptions) each time, so
	// the table says which inputs produce which state, and every move is
	// the system's.
	EnforcedByDerivation = "derived"
)

// Table is one lifecycle's declared transition table.
type Table struct {
	// Name is the lifecycle id, also the export's file name
	// (docs/lifecycles/<name>.json).
	Name string `json:"name"`
	// Title is the human label.
	Title string `json:"title"`
	// Entity is the database table and Column the state column on it.
	Entity string `json:"entity"`
	Column string `json:"column"`
	// Enforcement is EnforcedByDatabase, EnforcedByStore or
	// EnforcedByDerivation.
	Enforcement string `json:"enforcement"`
	// States lists every value the state column may hold, in lifecycle
	// order.
	States []string `json:"states"`
	// Initial lists the states a new row may be created in.
	Initial []Entry `json:"initial"`
	// Terminal lists the states with no outbound move.
	Terminal []string `json:"terminal"`
	// Transitions lists every allowed from -> to move. A pair not listed
	// is refused.
	Transitions []Transition `json:"transitions"`
	// Trigger, when set, names the database trigger that also refuses
	// moves, and the from-states it covers. Moves out of any other state
	// are refused only by the stores' guarded UPDATEs.
	Trigger *Trigger `json:"trigger,omitempty"`
}

// Entry is a way a row comes into existence.
type Entry struct {
	State  string   `json:"state"`
	Action string   `json:"action"`
	Routes []string `json:"routes,omitempty"`
	Roles  []string `json:"roles"`
}

// Transition is one allowed move.
type Transition struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Action string `json:"action"`
	// Actor is ActorPerson or ActorSystem.
	Actor string `json:"actor"`
	// Routes are the HTTP routes that make the move. Empty for a system
	// move with no route.
	Routes []string `json:"routes,omitempty"`
	// Roles are the roles the authorization bundle admits on Routes.
	// Empty for a system move.
	Roles []string `json:"roles,omitempty"`
	// RequiresApprover is the handler's extra gate: the credential must
	// carry the approver flag (admin implies it).
	RequiresApprover bool `json:"requires_approver"`
	// PeopleOnly: a machine credential never makes this move (#2101).
	PeopleOnly bool `json:"people_only"`
	// SecondPerson: the person making the move must differ from the one
	// who requested or submitted it. SelfApprovalOptIn: unless the
	// organisation's Allow self-approval setting is on, in which case the
	// move is flagged self_approved (#2101).
	SecondPerson      bool `json:"second_person"`
	SelfApprovalOptIn bool `json:"self_approval_opt_in"`
	// Note says anything the columns above do not.
	Note string `json:"note,omitempty"`
}

// Trigger names a database trigger that refuses moves out of From.
type Trigger struct {
	Name string   `json:"name"`
	From []string `json:"from"`
}

// Allows reports whether the table lists a from -> to move.
func (t Table) Allows(from, to string) bool {
	for _, tr := range t.Transitions {
		if tr.From == from && tr.To == to {
			return true
		}
	}
	return false
}

// Move returns the transition that action makes out of from, if any.
func (t Table) Move(from, action string) (Transition, bool) {
	for _, tr := range t.Transitions {
		if tr.From == from && tr.Action == action {
			return tr, true
		}
	}
	return Transition{}, false
}

// Actions returns the distinct move actions, in first-seen order.
func (t Table) Actions() []string {
	var out []string
	for _, tr := range t.Transitions {
		if !slices.Contains(out, tr.Action) {
			out = append(out, tr.Action)
		}
	}
	return out
}

// Validate checks the table is well formed: every state it names is
// declared, no pair or (from, action) appears twice, no move stays in
// place, terminal states have no outbound move, and every non-initial
// state is reachable.
func (t Table) Validate() error {
	if t.Name == "" || len(t.States) == 0 {
		return fmt.Errorf("lifecycle %q: name and states are required", t.Name)
	}
	known := func(s string) bool { return slices.Contains(t.States, s) }
	switch t.Enforcement {
	case EnforcedByDatabase:
		if t.Trigger == nil {
			return fmt.Errorf("lifecycle %s: enforced by the database but names no trigger", t.Name)
		}
	case EnforcedByStore, EnforcedByDerivation:
		if t.Trigger != nil {
			return fmt.Errorf("lifecycle %s: names trigger %s but is enforced by %s", t.Name, t.Trigger.Name, t.Enforcement)
		}
	default:
		return fmt.Errorf("lifecycle %s: enforcement %q", t.Name, t.Enforcement)
	}
	if len(t.Initial) == 0 {
		return fmt.Errorf("lifecycle %s: no initial state", t.Name)
	}
	for _, e := range t.Initial {
		if !known(e.State) {
			return fmt.Errorf("lifecycle %s: initial state %q is not declared", t.Name, e.State)
		}
	}
	for _, s := range t.Terminal {
		if !known(s) {
			return fmt.Errorf("lifecycle %s: terminal state %q is not declared", t.Name, s)
		}
	}
	pairs := map[[2]string]bool{}
	moves := map[[2]string]bool{}
	reached := map[string]bool{}
	for _, e := range t.Initial {
		reached[e.State] = true
	}
	for _, tr := range t.Transitions {
		if !known(tr.From) || !known(tr.To) {
			return fmt.Errorf("lifecycle %s: %s -> %s names an undeclared state", t.Name, tr.From, tr.To)
		}
		if tr.From == tr.To {
			return fmt.Errorf("lifecycle %s: %s -> %s stays in place", t.Name, tr.From, tr.To)
		}
		if slices.Contains(t.Terminal, tr.From) {
			return fmt.Errorf("lifecycle %s: terminal state %s has an outbound move", t.Name, tr.From)
		}
		if tr.Action == "" {
			return fmt.Errorf("lifecycle %s: %s -> %s has no action", t.Name, tr.From, tr.To)
		}
		if tr.Actor != ActorPerson && tr.Actor != ActorSystem {
			return fmt.Errorf("lifecycle %s: %s -> %s has actor %q", t.Name, tr.From, tr.To, tr.Actor)
		}
		if t.Enforcement == EnforcedByDerivation && tr.Actor != ActorSystem {
			return fmt.Errorf("lifecycle %s: derived move %s -> %s is not the system's", t.Name, tr.From, tr.To)
		}
		if tr.Actor == ActorPerson && len(tr.Roles) == 0 {
			return fmt.Errorf("lifecycle %s: person move %s -> %s names no role", t.Name, tr.From, tr.To)
		}
		if tr.SelfApprovalOptIn && !tr.SecondPerson {
			return fmt.Errorf("lifecycle %s: %s -> %s opts in to self-approval without a second-person rule", t.Name, tr.From, tr.To)
		}
		p := [2]string{tr.From, tr.To}
		if pairs[p] {
			return fmt.Errorf("lifecycle %s: %s -> %s is listed twice", t.Name, tr.From, tr.To)
		}
		pairs[p] = true
		m := [2]string{tr.From, tr.Action}
		if moves[m] {
			return fmt.Errorf("lifecycle %s: action %s out of %s is listed twice", t.Name, tr.Action, tr.From)
		}
		moves[m] = true
		reached[tr.To] = true
	}
	for _, s := range t.States {
		if !reached[s] {
			return fmt.Errorf("lifecycle %s: state %s is unreachable", t.Name, s)
		}
	}
	if t.Trigger != nil {
		for _, s := range t.Trigger.From {
			if !known(s) {
				return fmt.Errorf("lifecycle %s: trigger covers undeclared state %q", t.Name, s)
			}
		}
	}
	return nil
}

// JSON renders the table as the exported document: indented, with a
// trailing newline, so the committed file diffs cleanly.
func (t Table) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
