// Package duedate holds the estate's one rule for whether a dated item is
// overdue, due soon or upcoming.
//
// The due day is the UTC calendar date of the due timestamp (a date-only
// value is stored as 00:00 UTC). An item is Overdue only once its due day
// has ended, that is from the next UTC day on; it is DueSoon from soonDays
// days before its due day through the due day itself, and Upcoming before
// that. This is the policy-review rule (#2009, #2075), which risk reviews
// (#2059) and the compliance calendar (#2130) share.
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
	case d.Before(today):
		return Overdue
	case !d.After(today.AddDate(0, 0, soonDays)):
		return DueSoon
	default:
		return Upcoming
	}
}

// UTCDay is the start of t's UTC calendar day.
func UTCDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
