// Package needsattention serves GET /v1/needs-attention (#2134): one
// ranked list of what needs someone's attention, for the dashboard's
// front door (#2072).
//
// Items come in five bands, in this order:
//
//  1. failing: in-scope controls whose rolled-up state is fail;
//  2. approval: records waiting for the caller's approval, read through
//     the approvals handler (#2119), so the band lists exactly what
//     GET /v1/approvals lists;
//  3. overdue: policy, risk and vendor reviews, action plans and periodic
//     control reviews the compliance calendar marks overdue;
//  4. due_soon: the same calendar items, plus expiring exceptions, due
//     within DueSoonDays;
//  5. ownership_gap: in-scope controls with no assigned owner and open
//     risks with no owner, listed for `for=all` only.
//
// Within a band the oldest item comes first (earliest due date, else
// earliest "since"), then type, then id, so offset paging is stable.
//
// "For you" (`for=me`) keeps the items the caller owns (the owner is a
// user, read from live data), the policies whose owner role the caller
// holds as a live role, and every approval item. `for=all` (admins and
// GRC leads, authz.IsTenantApprover) keeps everything.
package needsattention

import (
	"sort"
	"time"
)

// The five bands, in rank order.
const (
	BandFailing      = "failing"
	BandApproval     = "approval"
	BandOverdue      = "overdue"
	BandDueSoon      = "due_soon"
	BandOwnershipGap = "ownership_gap"
)

// Bands lists the bands in rank order.
var Bands = []string{BandFailing, BandApproval, BandOverdue, BandDueSoon, BandOwnershipGap}

var bandRank = map[string]int{
	BandFailing: 1, BandApproval: 2, BandOverdue: 3, BandDueSoon: 4, BandOwnershipGap: 5,
}

// The two views.
const (
	ForMe  = "me"
	ForAll = "all"
)

// Owner is who an item belongs to: a person (UserID, Name) or, for a
// policy, an owner role.
type Owner struct {
	UserID string `json:"user_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Role   string `json:"role,omitempty"`
}

// Item is one needs-attention row.
type Item struct {
	Band string `json:"band"`
	// Type is the record's kind: control, policy, risk, vendor,
	// action_plan, exception, or an approval type (exception,
	// risk_acceptance, policy, framework_scope).
	Type   string `json:"type"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Link   string `json:"link"`
	Action string `json:"action"`
	// DueAt is set on the due-based bands: the date the calendar shows.
	DueAt *time.Time `json:"due_at,omitempty"`
	// Since is set on the other bands: when the control was last
	// evaluated failing, when the approval was requested, when the
	// unowned record was created.
	Since *time.Time `json:"since,omitempty"`
	// Owner is nil when the record has none (an ownership gap).
	Owner *Owner `json:"owner,omitempty"`
}

func (it Item) at() *time.Time {
	if it.DueAt != nil {
		return it.DueAt
	}
	return it.Since
}

// Rank keeps the items in view and orders them: band, then oldest first
// (undated last), then type, then id. mine reports whether an item is the
// caller's; it is consulted for `for=me` only, which also never lists an
// ownership gap.
func Rank(items []Item, view string, mine func(Item) bool) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if view == ForMe && (it.Band == BandOwnershipGap || !mine(it)) {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return before(out[i], out[j]) })
	return out
}

func before(a, b Item) bool {
	if ra, rb := bandRank[a.Band], bandRank[b.Band]; ra != rb {
		return ra < rb
	}
	ta, tb := a.at(), b.at()
	switch {
	case ta != nil && tb != nil && !ta.Equal(*tb):
		return ta.Before(*tb)
	case (ta == nil) != (tb == nil):
		return ta != nil
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.ID < b.ID
}

// Response is the GET /v1/needs-attention shape. Total and Counts cover
// every ranked item; Items is the requested page.
type Response struct {
	For    string         `json:"for"`
	Items  []Item         `json:"items"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// Page builds the response for one page of ranked items.
func Page(ranked []Item, view string, limit, offset int) Response {
	counts := make(map[string]int, len(Bands))
	for _, b := range Bands {
		counts[b] = 0
	}
	for _, it := range ranked {
		counts[it.Band]++
	}
	page := []Item{}
	if offset < len(ranked) {
		end := min(offset+limit, len(ranked))
		page = append(page, ranked[offset:end]...)
	}
	return Response{For: view, Items: page, Total: len(ranked), Counts: counts, Limit: limit, Offset: offset}
}
