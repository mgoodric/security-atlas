package policy

import (
	"time"

	"github.com/mgoodric/security-atlas/internal/duedate"
)

// ReviewDueSoonDays is how many days ahead of its due day a published
// policy counts as due soon (#1806). The calendar query's policy branch
// (internal/db/queries/calendar.sql) uses the same 30 days, counted in
// whole UTC days on both sides so the list and the calendar agree at the
// boundary (#2009).
const ReviewDueSoonDays = 30

// Review states reported for a published policy with a review date. The
// strings match the compliance calendar's event status values.
const (
	ReviewOverdue  = duedate.Overdue
	ReviewDueSoon  = duedate.DueSoon
	ReviewUpcoming = duedate.Upcoming
)

// ReviewStatus reports where p stands against its review date at now.
//
// The due day is the UTC calendar date of next_review_at (a date-only
// value is stored as 00:00 UTC). A policy is ReviewOverdue only once its
// due day has ended, that is from the next UTC day on; it is
// ReviewDueSoon from ReviewDueSoonDays days before its due day through
// the due day itself, and ReviewUpcoming before that (#2009). The rule
// itself is duedate.Status, shared with the calendar's risk-review and
// action-plan events (#2130).
//
// It is "" when p has no review date or is not published, since only the
// published chain tip has a review due.
func (p Policy) ReviewStatus(now time.Time) string {
	return ReviewStatusOf(p.Status, p.NextReviewAt, now)
}

// ReviewStatusOf is ReviewStatus for callers holding the raw columns
// rather than a Policy (the joined ack-rate list row).
func ReviewStatusOf(status string, next *time.Time, now time.Time) string {
	if status != StatePublished || next == nil {
		return ""
	}
	return duedate.Status(*next, now, ReviewDueSoonDays)
}
