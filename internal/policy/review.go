package policy

import "time"

// ReviewDueSoonWindow is how far ahead of next_review_at a published policy
// counts as due soon (#1806). The calendar query's policy branch
// (internal/db/queries/calendar.sql) uses the same 30 days.
const ReviewDueSoonWindow = 30 * 24 * time.Hour

// Review states reported for a published policy with a review date. The
// strings match the compliance calendar's event status values.
const (
	ReviewOverdue  = "overdue"
	ReviewDueSoon  = "due-soon"
	ReviewUpcoming = "upcoming"
)

// ReviewStatus reports where p stands against its review date at now:
// ReviewOverdue once next_review_at has passed, ReviewDueSoon within
// ReviewDueSoonWindow, otherwise ReviewUpcoming. It is "" when p has no
// review date or is not published, since only the published chain tip
// has a review due.
func (p Policy) ReviewStatus(now time.Time) string {
	return ReviewStatusOf(p.Status, p.NextReviewAt, now)
}

// ReviewStatusOf is ReviewStatus for callers holding the raw columns
// rather than a Policy (the joined ack-rate list row).
func ReviewStatusOf(status string, next *time.Time, now time.Time) string {
	if status != StatePublished || next == nil {
		return ""
	}
	switch {
	case next.Before(now):
		return ReviewOverdue
	case !next.After(now.Add(ReviewDueSoonWindow)):
		return ReviewDueSoon
	default:
		return ReviewUpcoming
	}
}
