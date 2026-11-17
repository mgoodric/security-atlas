package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// Every kind's owner is a module or feature key the registry lists, so a
// renamed key cannot silently stop hiding a kind.
func TestKindModule_KeysAreInRegistry(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, m := range featureflag.Modules {
		known[m.Key] = !m.Core
		for _, f := range m.Features {
			known[f.Key] = !m.Core
		}
	}
	for kind, key := range kindModule {
		if optional, ok := known[key]; !ok || !optional {
			t.Errorf("kind %q maps to %q, not an optional module or its feature", kind, key)
		}
	}
}

// reachFake reaches every key except those listed; err fails every read.
type reachFake struct {
	off map[string]bool
	err error
}

func (r reachFake) Reaches(_ context.Context, _ uuid.UUID, _, key string) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return !r.off[key], nil
}

func TestHiddenKindsAndDropKinds(t *testing.T) {
	t.Parallel()
	hidden, err := HiddenKinds(context.Background(), reachFake{off: map[string]bool{"policy_acknowledgments": true}}, uuid.New(), "u1")
	if err != nil {
		t.Fatalf("HiddenKinds: %v", err)
	}
	if !hidden["policy_ack_due"] || hidden["risk_review_overdue"] || hidden["evidence.staleness"] {
		t.Fatalf("hidden = %v; want only policy_ack_due", hidden)
	}
	counts, total := DropKinds(map[string]int{"policy_ack_due": 3, "evidence.staleness": 2, "control.drift": 0}, hidden)
	if total != 2 || len(counts) != 1 || counts["evidence.staleness"] != 2 {
		t.Fatalf("DropKinds = %v, %d; want evidence.staleness=2", counts, total)
	}
}

// A failed access read is returned, so the sink sends nothing.
func TestHiddenKinds_ErrorIsReturned(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	if _, err := HiddenKinds(context.Background(), reachFake{err: boom}, uuid.New(), "u1"); !errors.Is(err, boom) {
		t.Fatalf("err = %v; want boom", err)
	}
}

// A nil Reacher hides nothing.
func TestHiddenKinds_Nil(t *testing.T) {
	t.Parallel()
	hidden, err := HiddenKinds(context.Background(), nil, uuid.New(), "u1")
	if err != nil || len(hidden) != 0 {
		t.Fatalf("HiddenKinds(nil) = %v, %v", hidden, err)
	}
}
