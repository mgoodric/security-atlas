// Package duedate holds the rule for whether a dated item is overdue, due
// soon or upcoming.
//
// The due day is the UTC calendar date of the due timestamp (a date-only
// value is stored as 00:00 UTC). An item is Overdue only once its due day
// has ended, that is from the next UTC day on; it is DueSoon from soonDays
// days before its due day through the due day itself, and Upcoming before
// that. It is the policy-review rule (#2009, #2075). Policy reviews,
// vendor reviews (vendor.IsOverdueAsOf, and in SQL the vendor queries'
// vendor_review_due_date < cutoff) and every calendar event type whose status
// the calendar store fills (#2130, #2232) use it.
package duedate

import "time"

// Status values. The strings match the compliance calendar's event status
// values.
const (
	Overdue  = "overdue"
	DueSoon  = "due-soon"
	Upcoming = "upcoming"
)

// Status reports where an item due at due stands at now, with a due-soon
// window of soonDays whole UTC days.
func Status(due, now time.Time, soonDays int) string {
	d, today := UTCDay(due), UTCDay(now)
	switch {
	case IsOverdue(due, now):
		return Overdue
	case !d.After(today.AddDate(0, 0, soonDays)):
		return DueSoon
	default:
		return Upcoming
	}
}

// IsOverdue reports whether an item due at due is overdue at now: its UTC
// due day has ended.
func IsOverdue(due, now time.Time) bool {
	return UTCDay(due).Before(UTCDay(now))
}

// UTCDay is the start of t's UTC calendar day.
func UTCDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
