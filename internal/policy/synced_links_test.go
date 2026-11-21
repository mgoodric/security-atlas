package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDedupeUUIDs(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	got := dedupeUUIDs([]uuid.UUID{a, b, a, b, a})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("dedupeUUIDs = %v; want [%s %s]", got, a, b)
	}
	if got := dedupeUUIDs(nil); got == nil || len(got) != 0 {
		t.Errorf("dedupeUUIDs(nil) = %#v; want an empty, non-nil slice", got)
	}
}

func TestSameUUIDSet(t *testing.T) {
	t.Parallel()
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name string
		x, y []uuid.UUID
		want bool
	}{
		{"both empty", nil, []uuid.UUID{}, true},
		{"same order", []uuid.UUID{a, b}, []uuid.UUID{a, b}, true},
		{"other order", []uuid.UUID{a, b}, []uuid.UUID{b, a}, true},
		{"duplicates count once", []uuid.UUID{a, a, b}, []uuid.UUID{b, a}, true},
		{"one added", []uuid.UUID{a}, []uuid.UUID{a, b}, false},
		{"one removed", []uuid.UUID{a, b}, []uuid.UUID{a}, false},
		{"same size, other id", []uuid.UUID{a, b}, []uuid.UUID{a, c}, false},
	} {
		if got := sameUUIDSet(tc.x, tc.y); got != tc.want {
			t.Errorf("%s: sameUUIDSet = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// SetSyncedLinks refuses a missing actor before it opens a transaction, so
// a nil pool is never touched.
func TestSetSyncedLinks_ActorRequired(t *testing.T) {
	t.Parallel()
	_, err := (&Store{}).SetSyncedLinks(context.Background(), uuid.New(), nil, "")
	if !errors.Is(err, ErrCreatedByRequired) {
		t.Errorf("err = %v; want ErrCreatedByRequired", err)
	}
}
