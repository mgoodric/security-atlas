package exceptions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2618: the Idempotency-Key shape the create handler admits.
func TestValidIdempotencyKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key  string
		want bool
	}{
		{"", false},
		{uuid.New().String(), true},
		{"a", true},
		{strings.Repeat("k", 255), true},
		{strings.Repeat("k", 256), false},
		{"has space", false},
		{"tab\tkey", false},
		{"café", false},
	} {
		if got := validIdempotencyKey(c.key); got != c.want {
			t.Errorf("validIdempotencyKey(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

// #2618: one request hashes the same however it is formatted, and any
// change to its content changes the hash.
func TestRequestHash(t *testing.T) {
	t.Parallel()
	ctrl := uuid.New()
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	base := createReq{
		ScopeCellPredicate:   json.RawMessage(`{"op":"true"}`),
		Justification:        "vendor blocker",
		CompensatingControls: []string{"manual review"},
		ExpiresAt:            &exp,
	}
	h := requestHash(ctrl, base)

	spaced := base
	spaced.ScopeCellPredicate = json.RawMessage("{ \"op\" : \"true\" }")
	otherZone := exp.In(time.FixedZone("x", 3600))
	spaced.ExpiresAt = &otherZone
	if got := requestHash(ctrl, spaced); got != h {
		t.Errorf("formatting changed the hash: %s vs %s", got, h)
	}

	changed := base
	changed.Justification = "another reason"
	if requestHash(ctrl, changed) == h {
		t.Error("a different justification hashed the same")
	}
	if requestHash(uuid.New(), base) == h {
		t.Error("a different control hashed the same")
	}
	later := exp.Add(time.Hour)
	changed = base
	changed.ExpiresAt = &later
	if requestHash(ctrl, changed) == h {
		t.Error("a different expiry hashed the same")
	}
}
