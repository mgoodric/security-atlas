// Pure-Go unit coverage of the crosswalk mapping-tier state machine (AC-8 /
// slice-353 Q-2 fast-loop convention): no Postgres, no build tag. Exercises
// every branch of ValidateTransition / CanTransition / ParseTier so the legality
// rules — including the load-bearing "no draft -> verified skip" guard (P0-483)
// — are pinned independent of the DB.
package crosswalktier

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestParseTier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    Tier
		wantErr bool
	}{
		{"draft", TierDraft, false},
		{"under_review", TierUnderReview, false},
		{"verified", TierVerified, false},
		{"rejected", TierRejected, false},
		{"", "", true},
		{"VERIFIED", "", true}, // case-sensitive
		{"approved", "", true}, // not a tier
		{"draftt", "", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTier(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrUnknownTier) {
					t.Fatalf("ParseTier(%q): want ErrUnknownTier, got %v", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTier(%q): unexpected err %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseTier(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTierIsValid(t *testing.T) {
	t.Parallel()
	valid := []Tier{TierDraft, TierUnderReview, TierVerified, TierRejected}
	for _, tr := range valid {
		if !tr.IsValid() {
			t.Errorf("%q should be valid", tr)
		}
	}
	for _, bad := range []Tier{"", "approved", "DRAFT"} {
		if bad.IsValid() {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestValidateTransition_LegalMoves(t *testing.T) {
	t.Parallel()
	legal := []struct{ from, to Tier }{
		{TierDraft, TierUnderReview},
		{TierDraft, TierRejected},
		{TierUnderReview, TierVerified},
		{TierUnderReview, TierRejected},
	}
	for _, m := range legal {
		m := m
		t.Run(string(m.from)+"->"+string(m.to), func(t *testing.T) {
			t.Parallel()
			if err := ValidateTransition(m.from, m.to); err != nil {
				t.Fatalf("ValidateTransition(%s,%s): want nil, got %v", m.from, m.to, err)
			}
			if !CanTransition(m.from, m.to) {
				t.Fatalf("CanTransition(%s,%s): want true", m.from, m.to)
			}
		})
	}
}

func TestValidateTransition_IllegalMoves(t *testing.T) {
	t.Parallel()
	illegal := []struct{ from, to Tier }{
		// The load-bearing guard: a community draft must NOT skip to verified.
		{TierDraft, TierVerified},
		// No move out of the terminal rejected tier.
		{TierRejected, TierDraft},
		{TierRejected, TierUnderReview},
		{TierRejected, TierVerified},
		// verified is not demoted via this API.
		{TierVerified, TierDraft},
		{TierVerified, TierUnderReview},
		{TierVerified, TierRejected},
		// Backwards / skip moves.
		{TierUnderReview, TierDraft},
		// No-op self-transitions write an empty audit row with no change.
		{TierDraft, TierDraft},
		{TierUnderReview, TierUnderReview},
		{TierVerified, TierVerified},
		{TierRejected, TierRejected},
	}
	for _, m := range illegal {
		m := m
		t.Run(string(m.from)+"->"+string(m.to), func(t *testing.T) {
			t.Parallel()
			err := ValidateTransition(m.from, m.to)
			if !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("ValidateTransition(%s,%s): want ErrIllegalTransition, got %v", m.from, m.to, err)
			}
			if CanTransition(m.from, m.to) {
				t.Fatalf("CanTransition(%s,%s): want false", m.from, m.to)
			}
		})
	}
}

func TestValidateTransition_UnknownTier(t *testing.T) {
	t.Parallel()
	if err := ValidateTransition("bogus", TierVerified); !errors.Is(err, ErrUnknownTier) {
		t.Fatalf("from=bogus: want ErrUnknownTier, got %v", err)
	}
	if err := ValidateTransition(TierDraft, "bogus"); !errors.Is(err, ErrUnknownTier) {
		t.Fatalf("to=bogus: want ErrUnknownTier, got %v", err)
	}
	if CanTransition("bogus", "bogus") {
		t.Fatal("CanTransition with bogus tiers should be false")
	}
}

func TestTierDBRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tr := range []Tier{TierDraft, TierUnderReview, TierVerified, TierRejected} {
		db := tr.DBTier()
		if got := TierFromDB(db); got != tr {
			t.Fatalf("round-trip %q: got %q", tr, got)
		}
	}
	// The dbx enum constants must agree with the domain values.
	if TierFromDB(dbx.CrosswalkMappingTierVerified) != TierVerified {
		t.Fatal("dbx verified constant mismatch")
	}
	if TierVerified.DBTier() != dbx.CrosswalkMappingTierVerified {
		t.Fatal("domain verified -> dbx mismatch")
	}
}
