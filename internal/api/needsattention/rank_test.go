package needsattention

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

var now2134 = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func days(n int) *time.Time {
	t := now2134.AddDate(0, 0, n)
	return &t
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Band + "/" + it.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRank_BandOrderThenOldestThenID(t *testing.T) {
	t.Parallel()
	items := []Item{
		{Band: BandOwnershipGap, Type: "risk", ID: "gap"},
		{Band: BandDueSoon, Type: "risk", ID: "soon-late", DueAt: days(9)},
		{Band: BandDueSoon, Type: "risk", ID: "soon-early", DueAt: days(2)},
		{Band: BandOverdue, Type: "policy", ID: "over", DueAt: days(-5)},
		{Band: BandApproval, Type: "exception", ID: "appr-undated"},
		{Band: BandApproval, Type: "exception", ID: "appr-dated", Since: days(-1)},
		{Band: BandFailing, Type: "control", ID: "fail-b", Since: days(-2)},
		{Band: BandFailing, Type: "control", ID: "fail-a", Since: days(-2)},
	}
	got := ids(Rank(items, ForAll, func(Item) bool { return false }))
	want := []string{
		"failing/fail-a", "failing/fail-b",
		"approval/appr-dated", "approval/appr-undated",
		"overdue/over",
		"due_soon/soon-early", "due_soon/soon-late",
		"ownership_gap/gap",
	}
	if !equal(got, want) {
		t.Fatalf("Rank = %v\nwant   %v", got, want)
	}
}

func TestRank_ForMeKeepsMineAndNeverGaps(t *testing.T) {
	t.Parallel()
	items := []Item{
		{Band: BandOverdue, Type: "risk", ID: "mine"},
		{Band: BandOverdue, Type: "risk", ID: "theirs"},
		{Band: BandOwnershipGap, Type: "risk", ID: "gap"},
	}
	mine := func(it Item) bool { return it.ID != "theirs" }
	if got := ids(Rank(items, ForMe, mine)); !equal(got, []string{"overdue/mine"}) {
		t.Fatalf("for=me = %v", got)
	}
}

func TestMineFor(t *testing.T) {
	t.Parallel()
	user := uuid.NewString()
	session := credstore.Credential{ID: "jwt:s", UserID: user, TenantRoles: []string{"grc_engineer"}, OwnerRoles: []string{"admin"}}
	machine := credstore.Credential{ID: "key_m", UserID: "key_m", OwnerRoles: []string{"security_lead"}}
	owned := Item{Band: BandOverdue, Owner: &Owner{UserID: user}}
	other := Item{Band: BandOverdue, Owner: &Owner{UserID: uuid.NewString()}}
	liveRole := Item{Band: BandOverdue, Type: "policy", Owner: &Owner{Role: "grc_engineer"}}
	claimRole := Item{Band: BandOverdue, Type: "policy", Owner: &Owner{Role: "admin"}}
	keyRole := Item{Band: BandOverdue, Type: "policy", Owner: &Owner{Role: "security_lead"}}
	approval := Item{Band: BandApproval}
	unowned := Item{Band: BandDueSoon}
	for _, c := range []struct {
		name string
		cred credstore.Credential
		it   Item
		want bool
	}{
		{"owner", session, owned, true},
		{"someone else's", session, other, false},
		{"live owner role", session, liveRole, true},
		// A JWT role claim outlives a revoke (#2381); only live roles count.
		{"JWT claim role", session, claimRole, false},
		{"approval", session, approval, true},
		{"no owner", session, unowned, false},
		// An API key's own label is its grant, as for policy acks.
		{"key label role", machine, keyRole, true},
		{"key owns no person's record", machine, owned, false},
	} {
		if got := mineFor(c.cred)(c.it); got != c.want {
			t.Errorf("%s: mine = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPage(t *testing.T) {
	t.Parallel()
	ranked := []Item{
		{Band: BandFailing, ID: "1"}, {Band: BandOverdue, ID: "2"}, {Band: BandOverdue, ID: "3"},
	}
	p := Page(ranked, ForAll, 2, 1)
	if got := ids(p.Items); !equal(got, []string{"overdue/2", "overdue/3"}) {
		t.Fatalf("page items = %v", got)
	}
	if p.Total != 3 || p.Counts[BandOverdue] != 2 || p.Counts[BandOwnershipGap] != 0 || p.For != ForAll {
		t.Fatalf("page = %+v", p)
	}
	if past := Page(ranked, ForMe, 10, 5); len(past.Items) != 0 || past.Items == nil || past.Total != 3 {
		t.Fatalf("offset past the end = %+v; want an empty, non-nil page", past)
	}
}

func TestParsePage(t *testing.T) {
	t.Parallel()
	if l, o, err := parsePage("", ""); err != nil || l != DefaultLimit || o != 0 {
		t.Fatalf("defaults = %d %d %v", l, o, err)
	}
	if l, o, err := parsePage("25", "50"); err != nil || l != 25 || o != 50 {
		t.Fatalf("25/50 = %d %d %v", l, o, err)
	}
	for _, c := range [][2]string{{"0", ""}, {"101", ""}, {"x", ""}, {"", "-1"}, {"", "y"}} {
		if _, _, err := parsePage(c[0], c[1]); err == nil {
			t.Errorf("parsePage(%q, %q) accepted", c[0], c[1])
		}
	}
}

func calRow(id, typ, status string, due time.Time) dbx.ListCalendarEventsRow {
	return dbx.ListCalendarEventsRow{
		EventID: id, EventType: typ, Title: typ + " " + id, Status: status,
		StartsAt: pgtype.Timestamptz{Time: due, Valid: true}, RelatedEntityID: id,
	}
}

func TestDueItems(t *testing.T) {
	t.Parallel()
	rows := []dbx.ListCalendarEventsRow{
		calRow("risk-over", "risk", "overdue", *days(-3)),
		calRow("risk-soon", "risk", "due-soon", *days(7)),
		calRow("risk-later", "risk", "upcoming", *days(30)),
		// A policy's due-soon window is 30 days; day 20 is outside band 4.
		calRow("policy-20", "policy", "due-soon", *days(20)),
		calRow("policy-10", "policy", "due-soon", *days(10)),
		// An exception's calendar status is its lifecycle state.
		calRow("exc-10", "exception", "active", *days(10)),
		calRow("exc-past", "exception", "active", *days(-1)),
		calRow("exc-later", "exception", "active", *days(40)),
		calRow("renewal-v1", "vendor", "active", *days(3)),
	}
	got := map[string]string{}
	for _, it := range DueItems(rows, now2134) {
		got[it.ID] = it.Band
		if it.Link == "" || it.Action == "" || it.DueAt == nil {
			t.Errorf("%s: link %q action %q due %v", it.ID, it.Link, it.Action, it.DueAt)
		}
	}
	want := map[string]string{
		"risk-over": BandOverdue, "risk-soon": BandDueSoon, "policy-10": BandDueSoon,
		"exc-10": BandDueSoon, "exc-past": BandOverdue,
	}
	if len(got) != len(want) {
		t.Fatalf("DueItems = %v\nwant      %v", got, want)
	}
	for id, band := range want {
		if got[id] != band {
			t.Errorf("%s: band %q, want %q", id, got[id], band)
		}
	}
}

func TestControlItems(t *testing.T) {
	t.Parallel()
	fail, pass := dbx.EvidenceResultFail, dbx.EvidenceResultPass
	owner := uuid.New()
	ctl := func(result *dbx.EvidenceResult, owned bool, state dbx.ControlLifecycleState) dbx.ListActiveControlsWithStateRow {
		r := dbx.ListActiveControlsWithStateRow{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Title: "c", LifecycleState: state, StateResult: result,
			StateEvaluatedAt: pgtype.Timestamptz{Time: now2134, Valid: true},
		}
		if owned {
			r.OwnerUserID = pgtype.UUID{Bytes: owner, Valid: true}
		}
		return r
	}
	rows := []dbx.ListActiveControlsWithStateRow{
		ctl(&fail, true, dbx.ControlLifecycleStateActive),  // failing
		ctl(&fail, false, dbx.ControlLifecycleStateActive), // failing and a gap
		ctl(&pass, true, dbx.ControlLifecycleStateActive),  // neither
		ctl(nil, false, dbx.ControlLifecycleStateActive),   // a gap
		ctl(&fail, false, dbx.ControlLifecycleStateDraft),  // not in scope
	}
	bands := map[string]int{}
	for _, it := range ControlItems(rows, nil) {
		bands[it.Band]++
		if it.Band == BandFailing && it.Since == nil {
			t.Errorf("failing item without since: %+v", it)
		}
	}
	if bands[BandFailing] != 2 || bands[BandOwnershipGap] != 2 || len(bands) != 2 {
		t.Fatalf("bands = %v; want 2 failing, 2 gaps", bands)
	}
}

func TestFilterTypesAndCountTypes(t *testing.T) {
	t.Parallel()
	ranked := []Item{
		{Band: BandFailing, Type: "control", ID: "1"},
		{Band: BandApproval, Type: "exception", ID: "2"},
		{Band: BandOverdue, Type: "risk", ID: "3"},
		{Band: BandOverdue, Type: "control", ID: "4"},
	}
	if got := ids(FilterTypes(ranked, nil)); len(got) != 4 {
		t.Fatalf("no filter kept %v; want every item", got)
	}
	if got := ids(FilterTypes(ranked, []string{"control"})); !equal(got, []string{"failing/1", "overdue/4"}) {
		t.Fatalf("type=control kept %v", got)
	}
	if got := ids(FilterTypes(ranked, []string{"risk", "exception"})); !equal(got, []string{"approval/2", "overdue/3"}) {
		t.Fatalf("type=risk,exception kept %v", got)
	}
	c := CountTypes(ranked)
	if c["control"] != 2 || c["exception"] != 1 || c["risk"] != 1 || len(c) != 3 {
		t.Fatalf("type counts = %v", c)
	}
}

func TestParseTypes(t *testing.T) {
	t.Parallel()
	got, err := parseTypes([]string{"control,risk", " policy ", "control", ""})
	if err != nil || !equal(got, []string{"control", "risk", "policy"}) {
		t.Fatalf("parseTypes = %v, %v", got, err)
	}
	if got, err := parseTypes(nil); err != nil || len(got) != 0 {
		t.Fatalf("parseTypes(nil) = %v, %v", got, err)
	}
	if _, err := parseTypes([]string{"control,bogus"}); err == nil {
		t.Fatal("an unknown type must be refused")
	}
}
