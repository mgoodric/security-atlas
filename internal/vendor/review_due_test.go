package vendor

import (
	"testing"
	"time"
)

// TestReviewDueDate pins the Go mirror of the SQL vendor_review_due_date
// (#2232): calendar months clamped to the month's end, the first-review
// date for a vendor never reviewed, nil when neither is known.
func TestReviewDueDate(t *testing.T) {
	t.Parallel()
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	cases := []struct {
		name string
		v    Vendor
		want *time.Time
	}{
		{"never reviewed, no first review", Vendor{ReviewCadence: CadenceAnnual}, nil},
		{"never reviewed", Vendor{FirstReviewDue: ptr(d(2026, 6, 10))}, ptr(d(2026, 6, 10))},
		{"monthly", Vendor{LastReviewDate: ptr(d(2026, 4, 10)), ReviewCadence: CadenceMonthly}, ptr(d(2026, 5, 10))},
		{"monthly from Jan 31", Vendor{LastReviewDate: ptr(d(2026, 1, 31)), ReviewCadence: CadenceMonthly}, ptr(d(2026, 2, 28))},
		{"monthly from Jan 31, leap year", Vendor{LastReviewDate: ptr(d(2028, 1, 31)), ReviewCadence: CadenceMonthly}, ptr(d(2028, 2, 29))},
		{"quarterly from Nov 30", Vendor{LastReviewDate: ptr(d(2025, 11, 30)), ReviewCadence: CadenceQuarterly}, ptr(d(2026, 2, 28))},
		{"biannual from Aug 31", Vendor{LastReviewDate: ptr(d(2026, 8, 31)), ReviewCadence: CadenceBiannual}, ptr(d(2027, 2, 28))},
		{"annual from Feb 29", Vendor{LastReviewDate: ptr(d(2024, 2, 29)), ReviewCadence: CadenceAnnual}, ptr(d(2025, 2, 28))},
		{"review date wins over first review", Vendor{LastReviewDate: ptr(d(2026, 4, 10)), FirstReviewDue: ptr(d(2026, 1, 1)), ReviewCadence: CadenceMonthly}, ptr(d(2026, 5, 10))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.v.ReviewDueDate()
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil || !got.Equal(*tc.want):
				t.Fatalf("ReviewDueDate() = %v, want %v", got, tc.want)
			}
		})
	}
}
