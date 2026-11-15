package eval

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// #2112: the pure exception overlay. Inside the predicate's cell while in
// force -> excepted; outside the cell, before effective_from or at/after
// expires_at -> not excepted.
func TestExceptedIn(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)
	prod := &scope.Cell{ID: uuid.New(), Dimensions: map[string]string{"environment": "prod"}}
	staging := &scope.Cell{ID: uuid.New(), Dimensions: map[string]string{"environment": "staging"}}
	prodOnly := []byte(`{"op":"eq","dim":"environment","value":"prod"}`)

	cases := []struct {
		name string
		exs  []activeException
		cell *scope.Cell
		want bool
	}{
		{"no exceptions", nil, prod, false},
		{"inside the cell, in force", []activeException{{predicate: prodOnly, effectiveFrom: &past, expiresAt: future}}, prod, true},
		{"outside the cell", []activeException{{predicate: prodOnly, effectiveFrom: &past, expiresAt: future}}, staging, false},
		{"after expiry", []activeException{{predicate: prodOnly, effectiveFrom: &past, expiresAt: now}}, prod, false},
		{"before effective_from", []activeException{{predicate: prodOnly, effectiveFrom: &future, expiresAt: future.Add(time.Hour)}}, prod, false},
		{"null effective_from is in force", []activeException{{predicate: prodOnly, expiresAt: future}}, prod, true},
		{"empty predicate covers every cell", []activeException{{predicate: []byte(`{}`), expiresAt: future}}, staging, true},
		{"one expired, one in force", []activeException{
			{predicate: prodOnly, expiresAt: past},
			{predicate: []byte(`{"op":"true"}`), expiresAt: future},
		}, prod, true},
		{"whole-tenant row, always-true predicate", []activeException{{predicate: []byte(`{"op":"true"}`), expiresAt: future}}, nil, true},
		{"whole-tenant row, narrow predicate", []activeException{{predicate: prodOnly, expiresAt: future}}, nil, false},
		{"whole-tenant row, negated predicate", []activeException{{predicate: []byte(`{"op":"not","arg":{"op":"eq","dim":"environment","value":"prod"}}`), expiresAt: future}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := exceptedIn(tc.exs, tc.cell, now)
			if err != nil {
				t.Fatalf("exceptedIn: %v", err)
			}
			if got != tc.want {
				t.Fatalf("exceptedIn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExceptedIn_MalformedPredicateIsAnError(t *testing.T) {
	t.Parallel()
	now := time.Now()
	bad := []activeException{{predicate: []byte(`{"op":"nope"}`), expiresAt: now.Add(time.Hour)}}
	cell := &scope.Cell{ID: uuid.New(), Dimensions: map[string]string{"environment": "prod"}}
	if _, err := exceptedIn(bad, cell, now); err == nil {
		t.Fatal("want an error for a malformed predicate on a cell")
	}
	notJSON := []activeException{{predicate: []byte(`not json`), expiresAt: now.Add(time.Hour)}}
	if _, err := exceptedIn(notJSON, nil, now); err == nil {
		t.Fatal("want an error for a malformed predicate on the whole-tenant row")
	}
}
