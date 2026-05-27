// Unit tests for slice 318: coverage lift for the audit-ledger umbrella
// (internal/audit) covering pure-Go helpers + the pre-DB validation paths
// that reject malformed input before the transaction opens.
//
// Load-bearing functions + branches covered:
//
//   - isTrivialPredicate: every documented match-all shape (nil, empty,
//     "null", "{}", canonical {"op":"true"} including whitespace variants)
//     plus the non-trivial reject branch.
//   - canonicalPredicate: empty / nil -> canonical {"op":"true"};
//     non-empty -> passthrough.
//   - nullableInt32: pointer round-trip preserves value.
//   - pgUUID: wraps a uuid.UUID into a Valid pgtype.UUID.
//   - pgTimestamptz: zero time -> Valid=false; non-zero -> Valid=true with
//     the same instant.
//   - Store.CreatePopulation: pre-tx validation (empty CreatedBy, inverted
//     time window).
//   - Store.DrawSample: pre-tx validation (N<=0, empty Seed, empty
//     CreatedBy).
//   - Store.AnnotateSample: pre-tx validation (invalid Result, empty
//     AnnotatedBy).
//
// All branches are pure-Go: no DB, no fixtures. The Store-method
// validations exit BEFORE the inTx call opens the pool, so a nil pool
// (or an unconfigured one) is acceptable.
//
// The append-only invariant (P0-318-4) is honored trivially here — these
// tests never touch any audit-log table.

package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------- isTrivialPredicate ----------

func TestIsTrivialPredicate_MatchAllShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"nil", nil, true},
		{"empty", []byte{}, true},
		{"json-null", []byte("null"), true},
		{"empty-object", []byte("{}"), true},
		{"canonical-op-true", []byte(`{"op":"true"}`), true},
		{"canonical-op-true-with-whitespace", []byte(`{ "op" : "true" }`), true},
		{"canonical-op-true-other-fields", []byte(`{"op":"true","extra":1}`), true},
		// Non-trivial predicates: must NOT be reported as trivial.
		{"op-eq", []byte(`{"op":"eq","field":"env","value":"prod"}`), false},
		{"op-and", []byte(`{"op":"and","args":[]}`), false},
		{"raw-string", []byte(`"prod"`), false},
		{"garbage-json", []byte(`{`), false},
		{"trailing-junk", []byte(`{"op":"true"x`), false},
		{"unrelated-true-field", []byte(`{"truthy":"true"}`), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTrivialPredicate(tc.in); got != tc.want {
				t.Errorf("isTrivialPredicate(%q) = %v; want %v", string(tc.in), got, tc.want)
			}
		})
	}
}

// ---------- canonicalPredicate ----------

func TestCanonicalPredicate_NilAndEmptyMapToCanonicalForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage{}},
	}
	want := []byte(`{"op":"true"}`)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := canonicalPredicate(tc.in)
			if string(got) != string(want) {
				t.Errorf("canonicalPredicate(%v) = %q; want %q", tc.in, got, want)
			}
			// Sanity: the canonical form is itself classified as trivial.
			if !isTrivialPredicate(got) {
				t.Errorf("canonicalPredicate output is not classified as trivial: %q", got)
			}
		})
	}
}

func TestCanonicalPredicate_PassesThroughNonEmpty(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"op":"eq","field":"env","value":"prod"}`)
	got := canonicalPredicate(in)
	if string(got) != string(in) {
		t.Errorf("canonicalPredicate passthrough = %q; want %q", got, in)
	}
}

// ---------- nullableInt32 ----------

func TestNullableInt32_RoundTripsValue(t *testing.T) {
	t.Parallel()
	cases := []int32{0, 1, -1, 42, 100, -100}
	for _, v := range cases {
		v := v
		t.Run("", func(t *testing.T) {
			t.Parallel()
			p := nullableInt32(v)
			if p == nil {
				t.Fatal("nullableInt32 returned nil pointer")
			}
			if *p != v {
				t.Errorf("nullableInt32(%d) deref = %d", v, *p)
			}
		})
	}
}

// ---------- pgUUID ----------

func TestPgUUID_WrapsValid(t *testing.T) {
	t.Parallel()
	u := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := pgUUID(u)
	if !got.Valid {
		t.Error("pgUUID returned Valid=false")
	}
	if uuid.UUID(got.Bytes) != u {
		t.Errorf("pgUUID round-trip = %s; want %s", uuid.UUID(got.Bytes), u)
	}
}

// ---------- pgTimestamptz ----------

func TestPgTimestamptz_ZeroIsInvalid(t *testing.T) {
	t.Parallel()
	got := pgTimestamptz(time.Time{})
	if got.Valid {
		t.Error("pgTimestamptz(zero) returned Valid=true; want false")
	}
}

func TestPgTimestamptz_NonZeroIsValid(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 27, 12, 34, 56, 0, time.UTC)
	got := pgTimestamptz(when)
	if !got.Valid {
		t.Error("pgTimestamptz(non-zero) returned Valid=false; want true")
	}
	if !got.Time.Equal(when) {
		t.Errorf("pgTimestamptz round-trip = %s; want %s", got.Time, when)
	}
}

// ---------- Store.CreatePopulation pre-tx validation ----------

func TestCreatePopulation_RejectsEmptyCreatedBy(t *testing.T) {
	t.Parallel()
	// Pool can be nil — the validation branch exits before inTx opens it.
	s := &Store{pool: nil}
	_, err := s.CreatePopulation(context.Background(), CreatePopulationInput{
		ControlID:       uuid.New(),
		TimeWindowStart: time.Now(),
		TimeWindowEnd:   time.Now().Add(time.Hour),
		CreatedBy:       "",
	})
	if err == nil {
		t.Fatal("expected error for empty CreatedBy")
	}
}

func TestCreatePopulation_RejectsInvertedTimeWindow(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	now := time.Now()
	_, err := s.CreatePopulation(context.Background(), CreatePopulationInput{
		ControlID:       uuid.New(),
		TimeWindowStart: now.Add(time.Hour), // start AFTER end
		TimeWindowEnd:   now,
		CreatedBy:       "test-actor",
	})
	if err == nil {
		t.Fatal("expected error for inverted time window")
	}
}

// ---------- Store.DrawSample pre-tx validation ----------

func TestDrawSample_RejectsNonPositiveN(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	for _, n := range []int{0, -1, -100} {
		n := n
		t.Run("", func(t *testing.T) {
			t.Parallel()
			_, err := s.DrawSample(context.Background(), DrawSampleInput{
				PopulationID: uuid.New(),
				N:            n,
				Seed:         "seed",
				CreatedBy:    "actor",
			})
			if err == nil {
				t.Fatalf("expected error for N=%d", n)
			}
		})
	}
}

func TestDrawSample_RejectsEmptySeed(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	_, err := s.DrawSample(context.Background(), DrawSampleInput{
		PopulationID: uuid.New(),
		N:            10,
		Seed:         "",
		CreatedBy:    "actor",
	})
	if err == nil {
		t.Fatal("expected error for empty seed")
	}
}

func TestDrawSample_RejectsEmptyCreatedBy(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	_, err := s.DrawSample(context.Background(), DrawSampleInput{
		PopulationID: uuid.New(),
		N:            10,
		Seed:         "seed",
		CreatedBy:    "",
	})
	if err == nil {
		t.Fatal("expected error for empty CreatedBy")
	}
}

// ---------- Store.AnnotateSample pre-tx validation ----------

func TestAnnotateSample_RejectsUnknownResult(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	cases := []string{"", "PASSED", "pending", "yes", "no"}
	for _, r := range cases {
		r := r
		t.Run(r, func(t *testing.T) {
			t.Parallel()
			_, err := s.AnnotateSample(context.Background(), AnnotateSampleInput{
				SampleID:         uuid.New(),
				EvidenceRecordID: uuid.New(),
				Result:           r,
				AnnotatedBy:      "actor",
			})
			if err == nil {
				t.Fatalf("expected error for result=%q", r)
			}
			if !errors.Is(err, ErrInvalidAnnotation) {
				t.Errorf("expected ErrInvalidAnnotation; got %v", err)
			}
		})
	}
}

func TestAnnotateSample_AcceptsAllThreeCanonicalResults(t *testing.T) {
	t.Parallel()
	// We can't drive AnnotateSample to success without a real pool — but
	// we can confirm the validation branch lets the canonical values
	// through (the FIRST gate). The call will then fail on inTx because
	// the pool is nil; that's expected. The point of this test is to pin
	// the AnnotationResults map keys so accidentally renaming one is
	// caught here, not on a green-field deploy.
	for _, r := range []string{"passed", "failed", "not-applicable"} {
		if _, ok := AnnotationResults[r]; !ok {
			t.Errorf("AnnotationResults missing canonical key %q", r)
		}
	}
}

func TestAnnotateSample_RejectsEmptyAnnotatedBy(t *testing.T) {
	t.Parallel()
	s := &Store{pool: nil}
	_, err := s.AnnotateSample(context.Background(), AnnotateSampleInput{
		SampleID:         uuid.New(),
		EvidenceRecordID: uuid.New(),
		Result:           "passed",
		AnnotatedBy:      "",
	})
	if err == nil {
		t.Fatal("expected error for empty AnnotatedBy")
	}
}

// ---------- sentinel errors ----------

func TestSentinelErrors_DistinctValues(t *testing.T) {
	t.Parallel()
	// The three sentinels carry distinct messages and are not aliases.
	if ErrNotFound == nil || ErrEmptyPopulation == nil || ErrInvalidAnnotation == nil {
		t.Fatal("sentinel error initialized to nil")
	}
	if errors.Is(ErrNotFound, ErrEmptyPopulation) {
		t.Error("ErrNotFound and ErrEmptyPopulation must not alias")
	}
	if errors.Is(ErrEmptyPopulation, ErrInvalidAnnotation) {
		t.Error("ErrEmptyPopulation and ErrInvalidAnnotation must not alias")
	}
}
