package lifecycle

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sample() Table {
	return Table{
		Name:        "sample",
		Enforcement: EnforcedByDatabase,
		States:      []string{"a", "b", "c"},
		Initial:     []Entry{{State: "a", Action: "create", Roles: []string{"admin"}}},
		Terminal:    []string{"c"},
		Transitions: []Transition{
			{From: "a", To: "b", Action: "go", Actor: ActorPerson, Roles: []string{"admin"}},
			{From: "b", To: "c", Action: "go", Actor: ActorSystem},
		},
		Trigger: &Trigger{Name: "t", From: []string{"b"}},
	}
}

func TestTable_Queries(t *testing.T) {
	t.Parallel()
	tab := sample()
	if !tab.Allows("a", "b") || tab.Allows("a", "c") || tab.Allows("b", "a") {
		t.Fatal("Allows disagrees with the transitions")
	}
	if tr, ok := tab.Move("b", "go"); !ok || tr.To != "c" {
		t.Fatalf("Move(b, go) = %+v, %v", tr, ok)
	}
	if _, ok := tab.Move("c", "go"); ok {
		t.Fatal("Move out of a terminal state found a transition")
	}
	if got := tab.Actions(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("Actions = %v", got)
	}
}

func TestTable_Validate(t *testing.T) {
	t.Parallel()
	if err := sample().Validate(); err != nil {
		t.Fatalf("valid table: %v", err)
	}
	cases := map[string]struct {
		edit func(*Table)
		want string
	}{
		"no name":              {func(t *Table) { t.Name = "" }, "required"},
		"no initial":           {func(t *Table) { t.Initial = nil }, "no initial"},
		"unknown initial":      {func(t *Table) { t.Initial[0].State = "z" }, "initial state"},
		"unknown terminal":     {func(t *Table) { t.Terminal = []string{"z"} }, "terminal state"},
		"unknown state":        {func(t *Table) { t.Transitions[0].To = "z" }, "undeclared"},
		"stays in place":       {func(t *Table) { t.Transitions[0].To = "a" }, "stays in place"},
		"terminal outbound":    {func(t *Table) { t.Terminal = []string{"b"} }, "outbound"},
		"no action":            {func(t *Table) { t.Transitions[0].Action = "" }, "no action"},
		"bad actor":            {func(t *Table) { t.Transitions[0].Actor = "robot" }, "actor"},
		"person without roles": {func(t *Table) { t.Transitions[0].Roles = nil }, "no role"},
		"opt-in alone":         {func(t *Table) { t.Transitions[0].SelfApprovalOptIn = true }, "self-approval"},
		"pair twice": {func(t *Table) {
			t.Transitions = append(t.Transitions, Transition{From: "a", To: "b", Action: "again", Actor: ActorSystem})
		}, "listed twice"},
		"action twice": {func(t *Table) {
			t.Transitions = append(t.Transitions, Transition{From: "a", To: "c", Action: "go", Actor: ActorSystem})
		}, "listed twice"},
		"unreachable":          {func(t *Table) { t.States = append(t.States, "d") }, "unreachable"},
		"trigger unknown":      {func(t *Table) { t.Trigger.From = []string{"z"} }, "trigger"},
		"no states at all":     {func(t *Table) { t.States = nil }, "required"},
		"no enforcement":       {func(t *Table) { t.Enforcement = "" }, "enforcement"},
		"database, no trigger": {func(t *Table) { t.Trigger = nil }, "no trigger"},
		"store with trigger":   {func(t *Table) { t.Enforcement = EnforcedByStore }, "names trigger"},
		"derived person move":  {func(t *Table) { t.Enforcement = EnforcedByDerivation; t.Trigger = nil }, "not the system"},
	}
	for name, c := range cases {
		tab := sample()
		tab.Initial = append([]Entry(nil), tab.Initial...)
		tab.Transitions = append([]Transition(nil), tab.Transitions...)
		tr := *tab.Trigger
		tab.Trigger = &tr
		c.edit(&tab)
		err := tab.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Validate = %v, want an error containing %q", name, err, c.want)
		}
	}
}

func TestTable_JSONRoundTrips(t *testing.T) {
	t.Parallel()
	b, err := sample().JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "}\n") {
		t.Fatal("document does not end in a newline")
	}
	var back Table
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, sample()) {
		t.Fatalf("round trip changed the table:\n%+v\n%+v", back, sample())
	}
}
