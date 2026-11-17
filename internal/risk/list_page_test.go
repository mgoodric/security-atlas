package risk

import (
	"errors"
	"testing"
	"time"
)

func TestParsePageSort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw  string
		want PageSort
	}{
		{"", PageSort{Key: PageSortResidual, Desc: true}},
		{"residual", PageSort{Key: PageSortResidual}},
		{"-residual", PageSort{Key: PageSortResidual, Desc: true}},
		{"title", PageSort{Key: PageSortTitle}},
		{"-review_due", PageSort{Key: PageSortReviewDue, Desc: true}},
		{"org_unit", PageSort{Key: PageSortOrgUnit}},
		{"residual,age", PageSort{Key: PageSortResidualAge}},
	} {
		got, err := ParsePageSort(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("ParsePageSort(%q) = %+v, %v; want %+v", tc.raw, got, err, tc.want)
		}
	}
	for _, raw := range []string{"bogus", "--title", "-residual,age", "residual_age", "Title", " title"} {
		if _, err := ParsePageSort(raw); !errors.Is(err, ErrInvalidPageSort) {
			t.Errorf("ParsePageSort(%q) err = %v; want ErrInvalidPageSort", raw, err)
		}
	}
}

func TestPageFilterDueBounds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 16, 15, 30, 0, 0, time.UTC)
	today := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	day := func(d int) *time.Time { v := today.AddDate(0, 0, d); return &v }
	for _, tc := range []struct {
		name         string
		f            PageFilter
		from, before *time.Time
	}{
		{"none", PageFilter{}, nil, nil},
		// Overdue: the UTC due day has ended, so due before today.
		{"overdue", PageFilter{ReviewDue: ReviewDueOverdue}, nil, day(0)},
		// Due soon: today through today+14, inclusive.
		{"due soon", PageFilter{ReviewDue: ReviewDueSoon}, day(0), day(15)},
		// A date range is inclusive of both days.
		{"range", PageFilter{DueFrom: day(3), DueTo: day(5)}, day(3), day(6)},
		// Composed, the bounds intersect.
		{"due soon and from", PageFilter{ReviewDue: ReviewDueSoon, DueFrom: day(8)}, day(8), day(15)},
		{"overdue and to", PageFilter{ReviewDue: ReviewDueOverdue, DueTo: day(-10)}, nil, day(-9)},
	} {
		from, before := tc.f.dueBounds(now)
		check := func(what string, got bool, gotT time.Time, want *time.Time) {
			if (want == nil) != !got || (want != nil && !gotT.Equal(*want)) {
				t.Errorf("%s: %s = %v (%v); want %v", tc.name, what, gotT, got, want)
			}
		}
		check("from", from.Valid, from.Time, tc.from)
		check("before", before.Valid, before.Time, tc.before)
	}
}
