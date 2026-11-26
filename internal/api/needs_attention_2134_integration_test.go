//go:build integration

// #2134 — GET /v1/needs-attention end to end through the production router
// (JWT, tenancy, OPA with the live user_roles resolver, the module gates)
// on real Postgres under RLS, over the attention fixture: two tenants, two
// users in each, and every needs-attention source.
//
// In each tenant the fixture's Owner is a grc_engineer (a GRC lead, so an
// approver) who owns the controls, risks, plans and vendor; Other is an
// admin who owns nothing but holds the overdue policy's owner role. The
// matrix pins:
//
//   - for=me is exactly the caller's owned, owner-role and approval items;
//   - for=all is everything, plus the ownership gaps;
//   - band order, oldest first within a band, and stable paging;
//   - a viewer asking for for=all is refused, and gets an empty for=me;
//   - neither tenant's items reach the other;
//   - the approvals band is GET /v1/approvals, and the due-based bands
//     agree with GET /v1/calendar item for item;
//   - a member narrowed out of the Risk module sees no risk or plan item,
//     and the total drops by exactly those.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

type naItem2134 struct {
	Band   string     `json:"band"`
	Type   string     `json:"type"`
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Link   string     `json:"link"`
	Action string     `json:"action"`
	DueAt  *time.Time `json:"due_at"`
	Since  *time.Time `json:"since"`
	Owner  *struct {
		UserID string `json:"user_id"`
		Name   string `json:"name"`
		Role   string `json:"role"`
	} `json:"owner"`
}

type naResponse2134 struct {
	For    string         `json:"for"`
	Items  []naItem2134   `json:"items"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

type naEnv2134 struct {
	*combinedViewsEnv
	owner, other, ownerB, otherB string // bearers
}

// newNAEnv2134 grants the fixture's users their roles in both tenants and
// mints their sessions.
func newNAEnv2134(t *testing.T) *naEnv2134 {
	t.Helper()
	e := newCombinedViewsEnv(t)
	ctx := context.Background()
	grant := func(tn attention.Tenant, u uuid.UUID, role string) string {
		t.Helper()
		if _, err := e.migrate.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2134')`,
			tn.ID, u.String(), role); err != nil {
			t.Fatalf("grant %s: %v", role, err)
		}
		c := testjwt.OwnerFor(uuid.MustParse(tn.ID), []string{role})
		c.Subject = "user:" + u.String()
		return e.srv.IssueTestJWT(t, c)
	}
	t.Cleanup(func() {
		for _, table := range []string{"decision_audit_log", "user_roles"} {
			_, _ = e.migrate.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1", e.fx.B.ID)
		}
		_, _ = e.migrate.Exec(ctx, "DELETE FROM user_roles WHERE tenant_id = $1", e.fx.A.ID)
	})
	return &naEnv2134{
		combinedViewsEnv: e,
		owner:            grant(e.fx.A, e.fx.A.Owner, "grc_engineer"),
		other:            grant(e.fx.A, e.fx.A.Other, "admin"),
		ownerB:           grant(e.fx.B, e.fx.B.Owner, "grc_engineer"),
		otherB:           grant(e.fx.B, e.fx.B.Other, "admin"),
	}
}

func (e *naEnv2134) list(t *testing.T, bearer, query string) (int, naResponse2134) {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/needs-attention"+query, bearer, nil)
	var out naResponse2134
	if got.status == http.StatusOK {
		if err := json.Unmarshal([]byte(got.body), &out); err != nil {
			t.Fatalf("decode needs-attention: %v; body=%s", err, got.body)
		}
	}
	return got.status, out
}

func (e *naEnv2134) mustList(t *testing.T, bearer, query string) naResponse2134 {
	t.Helper()
	st, out := e.list(t, bearer, query)
	if st != http.StatusOK {
		t.Fatalf("GET /v1/needs-attention%s: status %d", query, st)
	}
	return out
}

func key2134(band string, id uuid.UUID) string { return band + "/" + id.String() }

func keys2134(items []naItem2134) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		out[it.Band+"/"+it.ID] = true
	}
	return out
}

func sameSet2134(t *testing.T, name string, got map[string]bool, want []string) {
	t.Helper()
	w := map[string]bool{}
	for _, k := range want {
		w[k] = true
		if !got[k] {
			t.Errorf("%s: missing %s", name, k)
		}
	}
	for k := range got {
		if !w[k] {
			t.Errorf("%s: unexpected %s", name, k)
		}
	}
}

// ownerItems2134 is what the fixture's Owner owns or must approve.
func ownerItems2134(tn attention.Tenant) []string {
	return []string{
		key2134("failing", tn.FailingControl),
		key2134("approval", tn.PendingApproval),
		key2134("overdue", tn.OverdueRisk),
		key2134("overdue", tn.OverdueVendor),
		key2134("overdue", tn.OverduePlan),
		key2134("overdue", tn.PeriodicControl),
		key2134("due_soon", tn.DueSoonRisk),
		key2134("due_soon", tn.DueSoonPlan),
		key2134("due_soon", tn.ExpiringWaiver),
	}
}

func TestNeedsAttention2134_ForMeAndForAll(t *testing.T) {
	e := newNAEnv2134(t)
	a := e.fx.A

	// Owner: the records they own, and the approval waiting for them.
	sameSet2134(t, "Owner for=me", keys2134(e.mustList(t, e.owner, "?for=me&limit=100").Items), ownerItems2134(a))

	// Other owns nothing, but holds the policy's owner role and approves.
	other := e.mustList(t, e.other, "?for=me&limit=100")
	sameSet2134(t, "Other for=me", keys2134(other.Items), []string{
		key2134("approval", a.PendingApproval),
		key2134("overdue", a.OverduePolicy),
	})
	for _, it := range other.Items {
		if it.ID == a.OverduePolicy.String() && (it.Owner == nil || it.Owner.Role != "admin") {
			t.Errorf("overdue policy owner = %+v; want owner role admin", it.Owner)
		}
	}

	// Everyone: all of it, plus the ownership gaps.
	all := e.mustList(t, e.other, "?for=all&limit=100")
	want := append(ownerItems2134(a),
		key2134("overdue", a.OverduePolicy),
		key2134("ownership_gap", a.UnownedControl),
		key2134("ownership_gap", a.UnownedRisk),
	)
	sameSet2134(t, "for=all", keys2134(all.Items), want)
	if all.For != "all" || all.Total != len(want) || all.Counts["ownership_gap"] != 2 {
		t.Errorf("for=all: for=%q total=%d counts=%v; want all, %d, 2 gaps", all.For, all.Total, all.Counts, len(want))
	}
	for _, it := range all.Items {
		if it.Link == "" || it.Action == "" || it.Title == "" || (it.DueAt == nil && it.Since == nil) {
			t.Errorf("item %s/%s lacks a link, action, title or date: %+v", it.Band, it.ID, it)
		}
		if it.Band != "ownership_gap" && (it.Owner == nil || (it.Owner.UserID == "" && it.Owner.Role == "")) {
			t.Errorf("item %s/%s has no owner", it.Band, it.ID)
		}
		if it.Owner != nil && it.Owner.UserID == a.Owner.String() && it.Owner.Name != "Owner A" {
			t.Errorf("item %s/%s owner name %q; want Owner A", it.Band, it.ID, it.Owner.Name)
		}
	}

	// With no view named, a caller who has items gets their own.
	if def := e.mustList(t, e.other, ""); def.For != "me" || def.Total != 2 {
		t.Errorf("Other, no view: for=%q total=%d; want me, 2", def.For, def.Total)
	}
}

func TestNeedsAttention2134_OrderAndPaging(t *testing.T) {
	e := newNAEnv2134(t)
	all := e.mustList(t, e.other, "?for=all&limit=100").Items
	rank := map[string]int{"failing": 1, "approval": 2, "overdue": 3, "due_soon": 4, "ownership_gap": 5}
	at := func(it naItem2134) time.Time {
		if it.DueAt != nil {
			return *it.DueAt
		}
		return *it.Since
	}
	for i := 1; i < len(all); i++ {
		p, c := all[i-1], all[i]
		switch {
		case rank[p.Band] > rank[c.Band]:
			t.Errorf("band order: %s before %s", p.Band, c.Band)
		case rank[p.Band] == rank[c.Band] && at(p).After(at(c)):
			t.Errorf("%s band: %s (%s) before older %s (%s)", c.Band, p.ID, at(p), c.ID, at(c))
		}
	}
	// The fixture's overdue items, oldest first: the periodic control
	// (30 days past cadence), the vendor (monthly, reviewed 40 days ago),
	// the policy (5 days), the risk (3) and the plan (2).
	a := e.fx.A
	var overdue []string
	for _, it := range all {
		if it.Band == "overdue" {
			overdue = append(overdue, it.ID)
		}
	}
	wantOverdue := []string{a.PeriodicControl.String(), a.OverdueVendor.String(), a.OverduePolicy.String(), a.OverdueRisk.String(), a.OverduePlan.String()}
	if len(overdue) != len(wantOverdue) {
		t.Fatalf("overdue band = %v; want %v", overdue, wantOverdue)
	}
	for i := range wantOverdue {
		if overdue[i] != wantOverdue[i] {
			t.Errorf("overdue[%d] = %s; want %s", i, overdue[i], wantOverdue[i])
		}
	}

	// Pages of 5 walk the same list, with the same total on each.
	var walked []naItem2134
	for offset := 0; offset < len(all); offset += 5 {
		page := e.mustList(t, e.other, "?for=all&limit=5&offset="+strconv.Itoa(offset))
		if page.Total != len(all) {
			t.Errorf("page at %d: total %d; want %d", offset, page.Total, len(all))
		}
		walked = append(walked, page.Items...)
	}
	if len(walked) != len(all) {
		t.Fatalf("paged walk: %d items; want %d", len(walked), len(all))
	}
	for i := range all {
		if walked[i].Band != all[i].Band || walked[i].ID != all[i].ID {
			t.Errorf("paged walk [%d] = %s/%s; want %s/%s", i, walked[i].Band, walked[i].ID, all[i].Band, all[i].ID)
		}
	}
	if past := e.mustList(t, e.other, "?for=all&offset=500"); len(past.Items) != 0 || past.Total != len(all) {
		t.Errorf("offset past the end: %d items, total %d", len(past.Items), past.Total)
	}
}

func TestNeedsAttention2134_RefusalAndTenantIsolation(t *testing.T) {
	e := newNAEnv2134(t)
	_, viewer := e.member(t, "viewer")
	if st, _ := e.list(t, viewer, "?for=all"); st != http.StatusForbidden {
		t.Errorf("viewer for=all: status %d; want 403", st)
	}
	_, controlOwner := e.member(t, "control_owner")
	if st, _ := e.list(t, controlOwner, "?for=all"); st != http.StatusForbidden {
		t.Errorf("control_owner for=all: status %d; want 403", st)
	}
	// A viewer owns nothing and cannot see everyone: an empty "For you".
	if v := e.mustList(t, viewer, ""); v.For != "me" || v.Total != 0 || len(v.Items) != 0 {
		t.Errorf("viewer, no view: for=%q total=%d; want me, 0", v.For, v.Total)
	}

	ids := func(tn attention.Tenant) map[string]bool {
		out := map[string]bool{}
		for _, u := range []uuid.UUID{
			tn.FailingControl, tn.PendingApproval, tn.OverduePolicy, tn.OverdueRisk, tn.OverdueVendor,
			tn.OverduePlan, tn.PeriodicControl, tn.ExpiringWaiver, tn.UnownedControl, tn.UnownedRisk,
			tn.DueSoonRisk, tn.DueSoonPlan, tn.WaiverControl, tn.Owner, tn.Other,
		} {
			out[u.String()] = true
		}
		return out
	}
	aIDs, bIDs := ids(e.fx.A), ids(e.fx.B)
	allA := e.mustList(t, e.other, "?for=all&limit=100")
	allB := e.mustList(t, e.otherB, "?for=all&limit=100")
	if allA.Total != allB.Total {
		t.Errorf("the tenants seed the same sources: totals A=%d B=%d", allA.Total, allB.Total)
	}
	for _, c := range []struct {
		name    string
		items   []naItem2134
		foreign map[string]bool
	}{
		{"tenant A", allA.Items, bIDs},
		{"tenant B", allB.Items, aIDs},
		{"tenant B owner", e.mustList(t, e.ownerB, "?for=me&limit=100").Items, aIDs},
	} {
		for _, it := range c.items {
			if c.foreign[it.ID] || (it.Owner != nil && c.foreign[it.Owner.UserID]) {
				t.Errorf("%s lists the other tenant's %s/%s", c.name, it.Band, it.ID)
			}
		}
	}
}

// The approvals band is GET /v1/approvals; the due-based bands agree with
// the calendar over the same window.
func TestNeedsAttention2134_AgreesWithApprovalsAndCalendar(t *testing.T) {
	e := newNAEnv2134(t)
	all := e.mustList(t, e.other, "?for=all&limit=100").Items

	got := e.do(t, http.MethodGet, "/v1/approvals", e.other, nil)
	var appr struct {
		Items []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if got.status != http.StatusOK || json.Unmarshal([]byte(got.body), &appr) != nil {
		t.Fatalf("GET /v1/approvals: %d %s", got.status, got.body)
	}
	var wantAppr []string
	for _, it := range appr.Items {
		wantAppr = append(wantAppr, "approval/"+it.ID)
	}
	gotAppr := map[string]bool{}
	for _, it := range all {
		if it.Band == "approval" {
			gotAppr[it.Band+"/"+it.ID] = true
		}
	}
	sameSet2134(t, "approvals band vs GET /v1/approvals", gotAppr, wantAppr)

	now := e.fx.Now
	cal := e.do(t, http.MethodGet, "/v1/calendar?from="+now.AddDate(0, 0, -60).Format("2006-01-02")+
		"&to="+now.AddDate(0, 0, 14).Format("2006-01-02"), e.other, nil)
	var events struct {
		Events []struct {
			Type     string `json:"type"`
			Related  string `json:"related_entity_id"`
			Status   string `json:"status"`
			StartsAt string `json:"starts_at"`
		} `json:"events"`
	}
	if cal.status != http.StatusOK || json.Unmarshal([]byte(cal.body), &events) != nil {
		t.Fatalf("GET /v1/calendar: %d %s", cal.status, cal.body)
	}
	status := map[string]string{}
	starts := map[string]string{}
	for _, ev := range events.Events {
		status[ev.Type+"/"+ev.Related] = ev.Status
		starts[ev.Type+"/"+ev.Related] = ev.StartsAt
	}
	due := map[string]string{}
	for _, it := range all {
		if it.Band != "overdue" && it.Band != "due_soon" {
			continue
		}
		k := it.Type + "/" + it.ID
		due[k] = it.Band
		st, ok := status[k]
		switch {
		case !ok:
			t.Errorf("%s %s is not on the calendar", it.Band, k)
		case it.Band == "overdue" && st != "overdue" && it.Type != "exception":
			t.Errorf("%s is overdue here, %q on the calendar", k, st)
		case it.Band == "due_soon" && st != "due-soon" && it.Type != "exception":
			t.Errorf("%s is due soon here, %q on the calendar", k, st)
		}
		if d, err := time.Parse(time.RFC3339, starts[k]); ok && (err != nil || !d.Equal(it.DueAt.Truncate(time.Second))) {
			t.Errorf("%s due %s here, %s on the calendar", k, it.DueAt, starts[k])
		}
	}
	// Every overdue review on the calendar is in the overdue band.
	for k, st := range status {
		typ, _, _ := strings.Cut(k, "/")
		switch typ {
		case "policy", "risk", "vendor", "control", "action_plan":
			if st == "overdue" && due[k] != "overdue" {
				t.Errorf("calendar has %s overdue; needs attention has %q", k, due[k])
			}
		}
	}
}

// A member narrowed out of the Risk module sees no risk or action-plan
// item, and the counts drop by exactly those (#2173).
func TestNeedsAttention2134_NarrowedMemberSeesNoHiddenModule(t *testing.T) {
	e := newNAEnv2134(t)
	before := e.mustList(t, e.owner, "?for=all&limit=100")
	hidden := 0
	for _, it := range before.Items {
		if it.Type == "risk" || it.Type == "action_plan" {
			hidden++
		}
	}
	if hidden == 0 {
		t.Fatalf("the fixture should put risk and plan items on the list")
	}
	e.setAccess(t, e.fx.A.Owner, everyOptionalBut("risk"))
	after := e.mustList(t, e.owner, "?for=all&limit=100")
	for _, it := range after.Items {
		if it.Type == "risk" || it.Type == "action_plan" {
			t.Errorf("no Risk access: lists %s/%s %s", it.Band, it.Type, it.ID)
		}
	}
	if after.Total != before.Total-hidden {
		t.Errorf("no Risk access: total %d; want %d (%d less)", after.Total, before.Total-hidden, hidden)
	}
}

// #2135: ?type= narrows the page, total and band counts to those types,
// and type_counts still counts every type in the view, so the full
// page's filters can show what each holds.
func TestNeedsAttention2135_TypeFilter(t *testing.T) {
	e := newNAEnv2134(t)
	all := e.mustList(t, e.other, "?for=all&limit=100")
	byType := map[string]int{}
	for _, it := range all.Items {
		byType[it.Type]++
	}
	if byType["control"] == 0 || byType["risk"] == 0 {
		t.Fatalf("fixture view has no controls or risks: %v", byType)
	}

	got := e.do(t, http.MethodGet, "/v1/needs-attention?for=all&limit=100&type=control,risk", e.other, nil)
	if got.status != http.StatusOK {
		t.Fatalf("type=control,risk: status %d body=%s", got.status, got.body)
	}
	var page struct {
		naResponse2134
		TypeCounts map[string]int `json:"type_counts"`
	}
	if err := json.Unmarshal([]byte(got.body), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := byType["control"] + byType["risk"]; page.Total != want || len(page.Items) != want {
		t.Errorf("type=control,risk: total %d, %d items; want %d", page.Total, len(page.Items), want)
	}
	for _, it := range page.Items {
		if it.Type != "control" && it.Type != "risk" {
			t.Errorf("type=control,risk listed a %s", it.Type)
		}
	}
	for typ, n := range byType {
		if page.TypeCounts[typ] != n {
			t.Errorf("type_counts[%s] = %d; want %d (the unfiltered view)", typ, page.TypeCounts[typ], n)
		}
	}

	if st, _ := e.list(t, e.other, "?type=bogus"); st != http.StatusBadRequest {
		t.Errorf("type=bogus: status %d; want 400", st)
	}
}
