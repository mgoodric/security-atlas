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

// #2714: equivalent spellings of one request hash the same: the
// predicate's key order, compensating_controls omitted / null / [], and
// an omitted predicate against {} (its stored spelling).
func TestRequestHashCanonical(t *testing.T) {
	t.Parallel()
	ctrl := uuid.New()
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	base := createReq{
		ScopeCellPredicate: json.RawMessage(`{"op":"eq","dim":"env","value":"prod"}`),
		Justification:      "vendor blocker",
		ExpiresAt:          &exp,
	}
	h := requestHash(ctrl, base)

	reordered := base
	reordered.ScopeCellPredicate = json.RawMessage(`{"value":"prod","op":"eq","dim":"env"}`)
	if got := requestHash(ctrl, reordered); got != h {
		t.Errorf("predicate key order changed the hash: %s vs %s", got, h)
	}

	emptyList := base
	emptyList.CompensatingControls = []string{}
	if got := requestHash(ctrl, emptyList); got != h {
		t.Errorf("compensating_controls [] hashed differently from omitted: %s vs %s", got, h)
	}

	var decoded createReq
	body := `{"control_id":"` + ctrl.String() + `","justification":"vendor blocker",` +
		`"compensating_controls":null,"expires_at":"2027-01-02T03:04:05Z",` +
		`"scope_cell_predicate":{"dim":"env","value":"prod","op":"eq"}}`
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := requestHash(ctrl, decoded); got != h {
		t.Errorf("a decoded body with null controls and reordered keys hashed differently: %s vs %s", got, h)
	}

	omitted := base
	omitted.ScopeCellPredicate = nil
	whole := base
	whole.ScopeCellPredicate = json.RawMessage(`{}`)
	if requestHash(ctrl, omitted) != requestHash(ctrl, whole) {
		t.Error("an omitted predicate hashed differently from {}")
	}

	// The order of compensating controls and the predicate's values still
	// count.
	two := base
	two.CompensatingControls = []string{"a", "b"}
	swapped := base
	swapped.CompensatingControls = []string{"b", "a"}
	if requestHash(ctrl, two) == requestHash(ctrl, swapped) {
		t.Error("compensating controls in another order hashed the same")
	}
	otherValue := base
	otherValue.ScopeCellPredicate = json.RawMessage(`{"op":"eq","dim":"env","value":"dev"}`)
	if requestHash(ctrl, otherValue) == h {
		t.Error("a different predicate value hashed the same")
	}

	// Numbers keep their full precision.
	big1 := base
	big1.ScopeCellPredicate = json.RawMessage(`{"n":12345678901234567890}`)
	big2 := base
	big2.ScopeCellPredicate = json.RawMessage(`{"n":12345678901234567891}`)
	if requestHash(ctrl, big1) == requestHash(ctrl, big2) {
		t.Error("two large numbers that differ in the last digit hashed the same")
	}
}
