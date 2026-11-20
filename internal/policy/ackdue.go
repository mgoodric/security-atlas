package policy

import (
	"time"

	"github.com/mgoodric/security-atlas/internal/duedate"
)

// AckGraceDays is how long, in whole UTC days after a policy's effective
// date, a first acknowledgement may wait before it is overdue (#2736). It
// is the compliance calendar's due-soon window (calendar.DueSoonDays).
const AckGraceDays = 14

// Acknowledgement due states, as GET /v1/me/acknowledgments reports them.
const (
	AckOverdue = "overdue"
	AckDueSoon = "due_soon"
)

// Due reports when the caller's acknowledgement of p is due and whether it
// is overdue at now. GET /v1/me/acknowledgments and GET /v1/needs-attention
// both read it, so the two agree (#2736).
//
//   - A lapsed ack (the last one is older than AcknowledgmentFreshness) is
//     due the moment it lapsed, and overdue from then on.
//   - A policy never acknowledged is due AckGraceDays after its effective
//     date. It is overdue once that due day has ended (duedate.IsOverdue,
//     the rule date-valued due dates use), due soon before then. Without
//     the escalation, ignoring a policy ranked better than letting an ack
//     lapse.
//
// The store matches acks to the current version only (a re-publish is a
// new policies row), so a pending row's ack is always lapsed. An ack that
// would lapse after now is not a lapse of this version and counts as none.
//
// due is nil only for a pending row with no effective date, which a
// published policy cannot be (policies_effective_date_set_when_published);
// such a row is due soon.
func (p Pending) Due(now time.Time) (due *time.Time, state string) {
	if p.LastAcknowledgedAt != nil {
		lapsed := p.LastAcknowledgedAt.UTC().Add(AcknowledgmentFreshness)
		if !lapsed.After(now) {
			return &lapsed, AckOverdue
		}
	}
	if p.EffectiveDate == nil {
		return nil, AckDueSoon
	}
	d := duedate.UTCDay(*p.EffectiveDate).AddDate(0, 0, AckGraceDays)
	if duedate.IsOverdue(d, now) {
		return &d, AckOverdue
	}
	return &d, AckDueSoon
}
