// Package needsattention serves GET /v1/needs-attention (#2134): one
// ranked list of what needs someone's attention, for the dashboard's
// front door (#2072).
//
// Items come in seven bands, in this order:
//
//  1. failing: in-scope controls whose rolled-up state is fail;
//  2. approval: records waiting for the caller's approval, read through
//     the approvals handler (#2119), so the band lists exactly what
//     GET /v1/approvals lists;
//  3. source_missing: published synced policies whose page a policy sync
//     run found absent, because it lost its label or was deleted (#2853).
//     The policy stays in force; the item says the source has gone;
//  4. inconclusive: in-scope controls whose rolled-up state has been
//     inconclusive for at least InconclusiveDays (#2915), so a control
//     with no evidence it can read is not left silent. Since is when the
//     run began;
//  5. overdue: policy, risk and vendor reviews, action plans and periodic
//     control reviews the compliance calendar marks overdue;
//  6. due_soon: the same calendar items, plus expiring exceptions, due
//     within DueSoonDays, and the caller's own policy acknowledgements
//     (TypePolicyAck, #2587): a lapsed one is overdue, one never made is
//     due soon until policy.AckGraceDays after the policy took effect and
//     overdue after (#2736). They come from the ack store, not the
//     calendar, whose ack lapse events are one per policy and day for
//     many people;
//  7. ownership_gap: in-scope controls with no assigned owner and open
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

// The seven bands, in rank order.
const (
	BandFailing       = "failing"
	BandApproval      = "approval"
	BandSourceMissing = "source_missing"
	BandInconclusive  = "inconclusive"
	BandOverdue       = "overdue"
	BandDueSoon       = "due_soon"
	BandOwnershipGap  = "ownership_gap"
)

// Bands lists the bands in rank order.
var Bands = []string{BandFailing, BandApproval, BandSourceMissing, BandInconclusive, BandOverdue, BandDueSoon, BandOwnershipGap}

var bandRank = map[string]int{
	BandFailing: 1, BandApproval: 2, BandSourceMissing: 3, BandInconclusive: 4,
	BandOverdue: 5, BandDueSoon: 6, BandOwnershipGap: 7,
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
	// evaluated failing, when it first went inconclusive, when the
	// approval was requested, when the unowned record was created.
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
	// TypeCounts counts the view's items by type before any `?type=`
	// filter, so the full page's type filters can show what each holds
	// (#2135).
	TypeCounts map[string]int `json:"type_counts"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
}

// TypePolicyAck is a policy waiting for the caller's acknowledgement.
const TypePolicyAck = "policy_ack"

// ItemTypes lists every item type a `?type=` filter may name: the record
// types of the due-based and control bands, the caller's policy
// acknowledgements, and the approval types.
var ItemTypes = []string{
	"control", "policy", "risk", "vendor", "action_plan", "exception",
	TypePolicyAck, "risk_acceptance", "framework_scope",
}

// CountTypes counts ranked items by type.
func CountTypes(ranked []Item) map[string]int {
	out := map[string]int{}
	for _, it := range ranked {
		out[it.Type]++
	}
	return out
}

// FilterTypes keeps the items whose type is in types. An empty types
// keeps everything.
func FilterTypes(ranked []Item, types []string) []Item {
	if len(types) == 0 {
		return ranked
	}
	keep := make(map[string]bool, len(types))
	for _, t := range types {
		keep[t] = true
	}
	out := make([]Item, 0, len(ranked))
	for _, it := range ranked {
		if keep[it.Type] {
			out = append(out, it)
		}
	}
	return out
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
	return Response{For: view, Items: page, Total: len(ranked), Counts: counts, TypeCounts: CountTypes(ranked), Limit: limit, Offset: offset}
}
