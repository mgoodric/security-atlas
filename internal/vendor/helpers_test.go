// Unit tests for pure-Go helpers in internal/vendor — the branches that
// the slice-024 integration_test.go does not exercise.
//
// Load-bearing functions covered here (slice 287):
//
//   - validateInput — every error branch (empty name, bad criticality,
//     bad cadence, DPA-signed without date, contract_end before
//     contract_start), plus the happy path.
//   - ReviewCadence.Duration — the default case (unknown cadence falls
//     back to annual so the system fails CLOSED, not open).
//   - normalizeDomain — nil, whitespace-only, trim+lower happy path.
//   - normalizeOpt — nil, whitespace-only, trim happy path.
//   - onTime — zero-total sentinel (returns 1.0), partial, full coverage.
//   - pgDate / fromPgDate — round-trip + nil pointer handling.
//   - pgUUID — wraps + flags Valid=true.
//   - pgCriticality — nil passthrough + non-nil pointer-of-enum.
//
// These are pure functions (no DB, no context) so they live as a
// stand-alone _test.go file under the same package. The integration tests
// in integration_test.go cover the SQL-touching branches; this file
// covers the helpers the integration tests never have a reason to call
// with the corner-case inputs.

package vendor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestValidateInput_AllBranches exercises every error path + happy path of
// validateInput. The function is the API-boundary guard that keeps malformed
// payloads out of the SQL layer; every branch is a real 400-shaped rejection.
func TestValidateInput_AllBranches(t *testing.T) {
	parse := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name      string
		in        CreateVendorInput
		wantErrIs error // nil = expect no error
		wantSub   string
	}{
		{
			name: "happy_path",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityMedium,
				ReviewCadence: CadenceAnnual,
			},
			wantErrIs: nil,
		},
		{
			name: "empty_name_rejected",
			in: CreateVendorInput{
				Name:          "",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "name is required",
		},
		{
			name: "whitespace_only_name_rejected",
			in: CreateVendorInput{
				Name:          "   \t  ",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "name is required",
		},
		{
			name: "bad_criticality_rejected",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   Criticality("extreme"),
				ReviewCadence: CadenceAnnual,
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "criticality",
		},
		{
			name: "bad_cadence_rejected",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityLow,
				ReviewCadence: ReviewCadence("decadal"),
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "review_cadence",
		},
		{
			name: "dpa_signed_without_date_rejected",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
				DPASigned:     true,
				DPASignedAt:   nil,
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "dpa_signed_at",
		},
		{
			name: "contract_end_before_start_rejected",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
				ContractStart: ptr(parse("2026-01-01")),
				ContractEnd:   ptr(parse("2025-01-01")),
			},
			wantErrIs: ErrInvalidInput,
			wantSub:   "contract_end is before contract_start",
		},
		{
			name: "contract_end_equal_to_start_accepted",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
				ContractStart: ptr(parse("2026-01-01")),
				ContractEnd:   ptr(parse("2026-01-01")),
			},
			wantErrIs: nil,
		},
		{
			name: "dpa_signed_with_date_accepted",
			in: CreateVendorInput{
				Name:          "Acme",
				Criticality:   CriticalityLow,
				ReviewCadence: CadenceAnnual,
				DPASigned:     true,
				DPASignedAt:   ptr(parse("2026-01-15")),
			},
			wantErrIs: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInput(tc.in)
			if tc.wantErrIs == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("want errors.Is(%v); got %v", tc.wantErrIs, err)
			}
			if tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err %q should contain %q", err, tc.wantSub)
			}
		})
	}
}

// TestReviewCadence_Duration_DefaultCase locks the fail-CLOSED contract for
// an unknown cadence: it returns the annual duration so an unknown enum
// eventually surfaces as overdue rather than silently never-overdue.
func TestReviewCadence_Duration_DefaultCase(t *testing.T) {
	if got, want := ReviewCadence("weekly").Duration(), 365*24*time.Hour; got != want {
		t.Fatalf("unknown cadence Duration: got %v, want %v (fail-closed = annual)", got, want)
	}
	if got, want := ReviewCadence("").Duration(), 365*24*time.Hour; got != want {
		t.Fatalf("empty cadence Duration: got %v, want %v", got, want)
	}
}

// TestReviewCadence_Duration_KnownCases keeps the day-count mapping a real
// regression guard — the SQL CASE uses Postgres INTERVAL math; this Go
// mapping is the fallback for callers computing overdue in memory. A bug
// in one without the other is exactly the silent-drift class of failure
// the slice-024 contract calls out.
func TestReviewCadence_Duration_KnownCases(t *testing.T) {
	cases := map[ReviewCadence]time.Duration{
		CadenceMonthly:   30 * 24 * time.Hour,
		CadenceQuarterly: 91 * 24 * time.Hour,
		CadenceBiannual:  182 * 24 * time.Hour,
		CadenceAnnual:    365 * 24 * time.Hour,
	}
	for c, want := range cases {
		if got := c.Duration(); got != want {
			t.Fatalf("%s.Duration() = %v, want %v", c, got, want)
		}
	}
}

// TestNormalizeDomain covers the nil / empty / trim+lower branches —
// the function is what makes the (tenant_id, lower(domain)) unique
// index DRY: a "  EXAMPLE.com  " input and an "example.com" input must
// collide. This test is the contract for that collision.
func TestNormalizeDomain(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		in   *string
		want *string
	}{
		{name: "nil_returns_nil", in: nil, want: nil},
		{name: "empty_returns_nil", in: str(""), want: nil},
		{name: "whitespace_returns_nil", in: str("   "), want: nil},
		{name: "lowercased", in: str("Example.com"), want: str("example.com")},
		{name: "trimmed", in: str("  example.com  "), want: str("example.com")},
		{name: "trim_and_lower", in: str("  EXAMPLE.com\t"), want: str("example.com")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeDomain(tc.in)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("want nil; got %q", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("want %q; got nil", *tc.want)
			case tc.want != nil && got != nil && *tc.want != *got:
				t.Fatalf("want %q; got %q", *tc.want, *got)
			}
		})
	}
}

// TestNormalizeOpt mirrors normalizeDomain but without the lowercasing —
// it trims whitespace and collapses empty to nil. SOW URIs flow through it.
func TestNormalizeOpt(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		in   *string
		want *string
	}{
		{name: "nil_returns_nil", in: nil, want: nil},
		{name: "empty_returns_nil", in: str(""), want: nil},
		{name: "whitespace_returns_nil", in: str("\t \n"), want: nil},
		{name: "preserves_case", in: str("  S3://Contracts/X.pdf  "), want: str("S3://Contracts/X.pdf")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOpt(tc.in)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("want nil; got %q", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("want %q; got nil", *tc.want)
			case tc.want != nil && got != nil && *tc.want != *got:
				t.Fatalf("want %q; got %q", *tc.want, *got)
			}
		})
	}
}

// TestOnTime covers all three branches: zero-total sentinel (1.0), full
// coverage (1.0), and partial (fraction). The burndown query depends on
// these — when a band has zero vendors the dashboard cannot show NaN.
func TestOnTime(t *testing.T) {
	tests := []struct {
		name             string
		total, overdue   int64
		wantFraction     float64
		wantExactlyEqual bool
	}{
		{name: "zero_total_returns_one", total: 0, overdue: 0, wantFraction: 1.0, wantExactlyEqual: true},
		{name: "all_on_time", total: 10, overdue: 0, wantFraction: 1.0, wantExactlyEqual: true},
		{name: "all_overdue", total: 10, overdue: 10, wantFraction: 0.0, wantExactlyEqual: true},
		{name: "half_overdue", total: 4, overdue: 2, wantFraction: 0.5, wantExactlyEqual: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := onTime(tc.total, tc.overdue)
			if tc.wantExactlyEqual && got != tc.wantFraction {
				t.Fatalf("onTime(%d, %d) = %v; want %v",
					tc.total, tc.overdue, got, tc.wantFraction)
			}
		})
	}
}

// TestPgDate_RoundTrip covers the pgDate/fromPgDate symmetry. nil in →
// invalid date out; valued in → same value out. The store relies on this
// to round-trip *time.Time through sqlc-generated pgtype.Date columns.
func TestPgDate_RoundTrip(t *testing.T) {
	t.Run("nil_in_invalid_out", func(t *testing.T) {
		d := pgDate(nil)
		if d.Valid {
			t.Fatalf("pgDate(nil) should be invalid; got %+v", d)
		}
		back := fromPgDate(d)
		if back != nil {
			t.Fatalf("fromPgDate(invalid) should be nil; got %v", *back)
		}
	})
	t.Run("value_round_trip", func(t *testing.T) {
		when := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
		d := pgDate(&when)
		if !d.Valid {
			t.Fatalf("pgDate(non-nil) should be valid; got %+v", d)
		}
		if !d.Time.Equal(when) {
			t.Fatalf("pgDate(%v) embedded Time = %v", when, d.Time)
		}
		back := fromPgDate(d)
		if back == nil {
			t.Fatalf("fromPgDate(valid) should not be nil")
		}
		if !back.Equal(when) {
			t.Fatalf("round trip lost value: %v vs %v", when, *back)
		}
	})
}

// TestPgUUID locks the wrap contract: bytes preserved, Valid=true. The
// store sends every UUID through pgUUID before it crosses into sqlc.
func TestPgUUID(t *testing.T) {
	id := uuid.New()
	p := pgUUID(id)
	if !p.Valid {
		t.Fatalf("pgUUID should be Valid=true")
	}
	if p.Bytes != id {
		t.Fatalf("pgUUID bytes mismatch: %v vs %v", p.Bytes, id)
	}
}

// TestPgCriticality covers both arms of the nil-passthrough helper. The
// SQL filter accepts NULL to mean "no filter" and a non-NULL pointer to
// mean "match this band"; pgCriticality is what bridges the Go *Crit to
// the dbx *VendorCriticality.
func TestPgCriticality(t *testing.T) {
	t.Run("nil_in_nil_out", func(t *testing.T) {
		if got := pgCriticality(nil); got != nil {
			t.Fatalf("pgCriticality(nil) = %v; want nil", *got)
		}
	})
	t.Run("non_nil_in_pointer_out", func(t *testing.T) {
		c := CriticalityHigh
		got := pgCriticality(&c)
		if got == nil {
			t.Fatalf("pgCriticality(&high) = nil; want pointer")
		}
		if dbx.VendorCriticality(*got) != "high" {
			t.Fatalf("pgCriticality(&high) = %q; want \"high\"", *got)
		}
	})
}

// TestFromPgDate_InvalidReturnsNil is the dedicated nil-arm coverage for
// fromPgDate — the pgDate round-trip test above also covers it, but the
// API contract is important enough to assert separately on a hand-crafted
// pgtype.Date with Valid=false.
func TestFromPgDate_InvalidReturnsNil(t *testing.T) {
	d := pgtype.Date{Valid: false}
	if got := fromPgDate(d); got != nil {
		t.Fatalf("fromPgDate({Valid:false}) = %v; want nil", *got)
	}
}
