//go:build integration

// #2132 — GET /v1/risks filters, sorts and pages on the server.
//
// Seeds the front-door fixture (internal/dbtest/attention: two tenants, two
// users each, every needs-attention source) and adds risks to tenant A that
// spread across every filter and sort key: a high 5x5 residual with an org
// unit and themes, a FAIR risk, one with no residual yet, a closed risk and
// a roll-up parent with an open child.
//
// Run with:
//
//	go test -tags=integration -count=1 -run TestListRisksPage ./internal/api/risks/
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package risks_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
	"github.com/mgoodric/security-atlas/internal/risk"
)

type pageFixture struct {
	attention.Fixture
	env testEnv

	High, Fair, Pending, Closed, Parent, Child uuid.UUID
}

func seedPageFixture(t *testing.T) pageFixture {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := pageFixture{Fixture: attention.Seed(t, admin, now)}
	a := fx.A.ID
	ctx := context.Background()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	// Runs before attention's own cleanup (LIFO): the roll-up links and the
	// org unit are not in its table list.
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM risk_aggregations WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM org_units WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, a); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	unit := uuid.New()
	exec("org unit", `INSERT INTO org_units (id, tenant_id, name, level) VALUES ($1, $2, 'Platform', 'org')`, unit, a)

	later := now.Add(200 * 24 * time.Hour)
	insert := func(title, methodology, treatment, category, inherent, residual string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec(title, `
			INSERT INTO risks (
				id, tenant_id, title, category_id, methodology, treatment,
				inherent_score, residual_score, review_due_at
			)
			VALUES ($1, $2, $3, risk_category_id($2, $4), $5::risk_methodology, $6::risk_treatment,
			        $7::jsonb, $8::jsonb, $9)
		`, id, a, fx.A.RiskTitlePrefix+title, category, methodology, treatment, inherent, residual, later)
		return id
	}
	fx.High = insert("high residual", "nist_800_30", "avoid", "privacy",
		`{"likelihood":5,"impact":5}`, `{"score":20}`)
	exec("high extras", `UPDATE risks SET org_unit_id = $2, level = 'company', themes = ARRAY['identity','cloud'] WHERE id = $1`, fx.High, unit)
	fx.Fair = insert("fair dollars", "fair", "untreated", "privacy",
		`{"loss_event_frequency":2,"loss_magnitude":50000,"annualized_loss_exposure":100000}`, `{"score":100000}`)
	fx.Pending = insert("not yet scored", "nist_800_30", "untreated", "operational",
		`{"likelihood":2,"impact":2}`, `{}`)
	fx.Closed = insert("closed", "nist_800_30", "mitigate", "operational",
		`{"likelihood":3,"impact":3}`, `{"severity":9}`)
	exec("close", `
		UPDATE risks SET status = 'closed', closure_reason = 'not_applicable',
		       closed_at = now(), closed_by = 'fixture'
		WHERE id = $1`, fx.Closed)
	fx.Parent = insert("roll-up parent", "nist_800_30", "mitigate", "operational",
		`{"likelihood":4,"impact":4}`, `{"severity":16}`)
	fx.Child = insert("roll-up child", "nist_800_30", "mitigate", "operational",
		`{"likelihood":1,"impact":2}`, `{"likelihood":1,"impact":2}`)
	exec("roll-up link", `INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, tenant_id) VALUES ($1, $2, $3)`,
		fx.Parent, fx.Child, a)

	fx.env = testServer(t, app, a)
	return fx
}

// pageRow is the part of a register row the assertions read.
type pageRow struct {
	ID, Title, Category, Treatment, Status, Methodology, Level, Created string
	ReviewDue, Owner                                                    string
	Themes                                                              []string
	Residual                                                            map[string]any
}

func listPage(t *testing.T, env testEnv, query string) ([]pageRow, int) {
	t.Helper()
	resp, body := get(t, env, "/v1/risks?"+query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks?%s: status %d, body %v", query, resp.StatusCode, body)
	}
	raw, _ := body["risks"].([]any)
	out := make([]pageRow, len(raw))
	for i, r := range raw {
		m := r.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return s }
		row := pageRow{
			ID: str("id"), Title: str("title"), Category: str("category"),
			Treatment: str("treatment"), Status: str("status"),
			Methodology: str("methodology"), Level: str("level"),
			Created: str("created_at"), ReviewDue: str("review_due_at"),
			Owner: str("owner_user_id"),
		}
		for _, th := range m["themes"].([]any) {
			row.Themes = append(row.Themes, th.(string))
		}
		row.Residual, _ = m["residual_score"].(map[string]any)
		out[i] = row
	}
	total, _ := body["total"].(float64)
	return out, int(total)
}

func titles(fx pageFixture, rows []pageRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimPrefix(r.Title, fx.A.RiskTitlePrefix)
	}
	return out
}

// assertTenantA fails when a row is not tenant A's (#2132 tenant isolation).
func assertTenantA(t *testing.T, fx pageFixture, rows []pageRow) {
	t.Helper()
	for _, r := range rows {
		if !strings.HasPrefix(r.Title, fx.A.RiskTitlePrefix) {
			t.Fatalf("row %q is not tenant A's", r.Title)
		}
	}
}

func TestListRisksPage_DefaultIsOpenNonRollupByResidual(t *testing.T) {
	fx := seedPageFixture(t)
	rows, total := listPage(t, fx.env, "")
	assertTenantA(t, fx, rows)
	got := titles(fx, rows)
	// Residual descending: 5x5, then FAIR, then no residual (#2516).
	// The four fixture risks tie on 2x3 = 6 and break on id. The roll-up
	// child (1x2) is open, so it is listed; its parent is not.
	if len(got) != 8 || total != 8 {
		t.Fatalf("default page = %v (total %d); want 8 rows", got, total)
	}
	if got[0] != "high residual" {
		t.Errorf("first row = %q; want high residual", got[0])
	}
	tied := got[1:5]
	slices.Sort(tied)
	if want := []string{"overdue review", "review due soon", "review later", "unowned"}; !slices.Equal(tied, want) {
		t.Errorf("rows 2-5 = %v; want the four fixture risks", got[1:5])
	}
	if got[5] != "roll-up child" || got[6] != "fair dollars" || got[7] != "not yet scored" {
		t.Errorf("tail = %v; want roll-up child, fair dollars, not yet scored", got[5:])
	}
	for _, r := range rows {
		if r.Status != "open" {
			t.Errorf("default page carries a %s risk %q", r.Status, r.Title)
		}
	}
}

func TestListRisksPage_Filters(t *testing.T) {
	fx := seedPageFixture(t)
	day := func(d int) string { return fx.Now.AddDate(0, 0, d).Format(time.DateOnly) }
	cases := []struct {
		query string
		want  []string
	}{
		{"status=closed", []string{"closed"}},
		{"status=all&sort=title", []string{"closed", "fair dollars", "high residual", "not yet scored", "overdue review", "review due soon", "review later", "roll-up child", "unowned"}},
		{"include_closed=true&sort=title", []string{"closed", "fair dollars", "high residual", "not yet scored", "overdue review", "review due soon", "review later", "roll-up child", "unowned"}},
		{"include_rollups=true&sort=title", []string{"fair dollars", "high residual", "not yet scored", "overdue review", "review due soon", "review later", "roll-up child", "roll-up parent", "unowned"}},
		{"treatment=avoid", []string{"high residual"}},
		{"treatment=untreated&sort=title", []string{"fair dollars", "not yet scored"}},
		{"category=privacy&sort=title", []string{"fair dollars", "high residual"}},
		{"methodology=fair", []string{"fair dollars"}},
		{"level=company", []string{"high residual"}},
		{"theme=cloud", []string{"high residual"}},
		{"owner=" + fx.A.Owner.String() + "&sort=title", []string{"overdue review", "review due soon", "review later"}},
		{"owner=" + fx.A.Other.String(), nil},
		{"owner=" + fx.B.Owner.String(), nil},
		{"owner=none&sort=title", []string{"fair dollars", "high residual", "not yet scored", "roll-up child", "unowned"}},
		{"review_due=overdue", []string{"overdue review"}},
		{"review_due=due_soon", []string{"review due soon"}},
		{"review_due_from=" + day(20) + "&review_due_to=" + day(40), []string{"review later"}},
		{"review_due=due_soon&review_due_from=" + day(8), nil},
		{"treatment=mitigate&owner=none&sort=title", []string{"roll-up child", "unowned"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			rows, total := listPage(t, fx.env, tc.query)
			assertTenantA(t, fx, rows)
			got := titles(fx, rows)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v; want %v", got, tc.want)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d; want %d (the filtered count)", total, len(tc.want))
			}
		})
	}
	// The org unit filter, by id.
	var unit string
	if err := dbtest.NewMigratePool(t).QueryRow(context.Background(),
		`SELECT org_unit_id::text FROM risks WHERE id = $1`, fx.High).Scan(&unit); err != nil {
		t.Fatalf("read org unit: %v", err)
	}
	if got := titles(fx, mustList(t, fx, "org_unit="+unit)); !slices.Equal(got, []string{"high residual"}) {
		t.Errorf("org_unit filter = %v; want high residual", got)
	}
}

func mustList(t *testing.T, fx pageFixture, q string) []pageRow {
	t.Helper()
	rows, _ := listPage(t, fx.env, q)
	return rows
}

func TestListRisksPage_RefusesUnknownValues(t *testing.T) {
	fx := seedPageFixture(t)
	for _, q := range []string{
		"sort=bogus", "sort=-residual,age", "status=archived", "treatment=ignore",
		"methodology=octave", "level=division", "owner=me", "org_unit=x",
		"review_due=soon", "review_due_from=2026-13-01", "review_due_to=tomorrow",
		"review_due_from=2026-12-31&review_due_to=2026-01-01",
		"include_rollups=maybe", "limit=0", "limit=-1", "offset=-1",
		"category=no-such-category",
	} {
		resp, _ := get(t, fx.env, "/v1/risks?"+q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /v1/risks?%s: status %d; want 400", q, resp.StatusCode)
		}
	}
}

// residualOf reads a row's residual with #2185's risk.ScoreValue, and
// whether its methodology is risk.Monetary (FAIR dollars).
func residualOf(t *testing.T, r pageRow) risk.Rank {
	t.Helper()
	raw, err := json.Marshal(r.Residual)
	if err != nil {
		t.Fatalf("marshal residual: %v", err)
	}
	value, scored := risk.ScoreValue(raw)
	return risk.Rank{Unscored: !scored, Monetary: risk.Monetary(dbx.RiskMethodology(r.Methodology)), Value: value}
}

// sortValue is what a key orders on, read from the wire, and whether the
// row has one (a row without sorts last in both directions).
func sortValue(fx pageFixture, key string, r pageRow) (string, bool) {
	switch key {
	case "title":
		return strings.ToLower(r.Title), true
	case "category":
		return r.Category, true // the seeded category slugs order as their names do
	case "owner":
		return r.Owner, r.Owner != "" // tenant A's owned risks share one owner
	case "treatment":
		return r.Treatment, true
	case "review_due":
		if r.ReviewDue == "" {
			return "", false
		}
		return orderedTime(r.ReviewDue), true
	case "status":
		return r.Status, true
	case "methodology":
		return r.Methodology, true
	case "level":
		return map[string]string{"team": "1", "org": "2", "company": "3"}[r.Level], true
	case "org_unit":
		return "platform", r.ID == fx.High.String()
	case "themes":
		return strings.Join(r.Themes, ","), true
	case "created_at":
		return orderedTime(r.Created), true
	case "id":
		return r.ID, true
	}
	panic("unknown sort key " + key)
}

func TestListRisksPage_EverySortKeyAndPaging(t *testing.T) {
	fx := seedPageFixture(t)
	keys := []string{"title", "category", "owner", "treatment", "review_due", "status",
		"methodology", "level", "org_unit", "themes", "created_at", "id", "residual"}
	for _, key := range keys {
		for _, desc := range []bool{false, true} {
			sort := key
			if desc {
				sort = "-" + key
			}
			t.Run(sort, func(t *testing.T) {
				q := "status=all&include_rollups=true&sort=" + url.QueryEscape(sort)
				all, total := listPage(t, fx.env, q)
				assertTenantA(t, fx, all)
				if len(all) != 10 || total != 10 {
					t.Fatalf("got %d rows (total %d); want 10", len(all), total)
				}
				if key == "residual" {
					assertResidualOrder(t, all, desc)
				} else {
					assertOrdered(t, fx, key, desc, all)
				}
				// Two at a time, the pages concatenate to the whole list:
				// no duplicates, no gaps.
				var walked []string
				for off := 0; off < total; off += 2 {
					page, pageTotal := listPage(t, fx.env, fmt.Sprintf("%s&limit=2&offset=%d", q, off))
					if pageTotal != total {
						t.Fatalf("page at %d: total %d; want %d", off, pageTotal, total)
					}
					for _, r := range page {
						walked = append(walked, r.ID)
					}
				}
				want := make([]string, len(all))
				for i, r := range all {
					want[i] = r.ID
				}
				if !slices.Equal(walked, want) {
					t.Errorf("paged walk differs from the single page:\n got %v\nwant %v", walked, want)
				}
			})
		}
	}
}

func assertOrdered(t *testing.T, fx pageFixture, key string, desc bool, rows []pageRow) {
	t.Helper()
	seenMissing := false
	var prev string
	var havePrev bool
	for _, r := range rows {
		v, ok := sortValue(fx, key, r)
		if !ok {
			seenMissing = true
			continue
		}
		if seenMissing {
			t.Fatalf("%s: a row with a value (%q) follows one without", key, r.Title)
		}
		if havePrev && ((!desc && v < prev) || (desc && v > prev)) {
			t.Fatalf("%s desc=%v: %q (%q) out of order after %q", key, desc, r.Title, v, prev)
		}
		prev, havePrev = v, true
	}
}

// assertResidualOrder checks risk.RanksAbove's order (#2185, #2516): scored
// 5x5, then scored FAIR, then unscored, in both directions, then the value
// in the requested direction within a scored tier.
func assertResidualOrder(t *testing.T, rows []pageRow, desc bool) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		ar, br := residualOf(t, a), residualOf(t, b)
		switch {
		case ar.Tier() != br.Tier():
			// The tiers never flip with the direction.
			if ar.Tier() > br.Tier() {
				t.Fatalf("%q (tier %d) ranks above %q (tier %d)", a.Title, ar.Tier(), b.Title, br.Tier())
			}
		case ar.Unscored:
		case desc && risk.RanksAbove(br, ar):
			t.Fatalf("desc: %q (%v) before %q (%v), which RanksAbove it", a.Title, ar.Value, b.Title, br.Value)
		case !desc && ar.Value > br.Value:
			t.Fatalf("asc: %q (%v) before %q (%v)", a.Title, ar.Value, b.Title, br.Value)
		}
	}
}

// TestListRisksPage_ResidualOrderMatchesRanksAbove: the SQL default order
// is exactly the Go order #2185 and #2516 define. Sorting the same rows in
// Go with risk.RanksAbove (id on a tie) gives the order the API returned.
func TestListRisksPage_ResidualOrderMatchesRanksAbove(t *testing.T) {
	fx := seedPageFixture(t)
	insert := func(title, methodology, inherent, residual string) {
		t.Helper()
		if _, err := dbtest.NewMigratePool(t).Exec(context.Background(), `
			INSERT INTO risks (id, tenant_id, title, category_id, methodology, treatment,
			                   inherent_score, residual_score)
			VALUES ($1, $2, $3, risk_category_id($2, 'operational'), $4, 'untreated', $5, $6)
		`, uuid.New(), fx.A.ID, fx.A.RiskTitlePrefix+title, methodology, inherent, residual); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
	}
	// #2482: a residual whose score is a numeric string. Go (risk.ScoreValue)
	// and the SQL sort both read JSON numbers only, so both treat it as
	// unscored and it sorts with "not yet scored", below every scored risk.
	insert("string score", "nist_800_30", `{"likelihood":5,"impact":5}`, `{"score":"25"}`)
	// #2516: unscored FAIR and unscored 5x5 share one tier after every
	// scored FAIR risk, rather than each sorting last in its own group.
	insert("fair unscored", "fair", `{}`, `{}`)
	insert("5x5 unscored", "qualitative_5x5", `{}`, `{}`)
	rows, _ := listPage(t, fx.env, "status=all&include_rollups=true")
	got := titles(fx, rows)
	fair := slices.Index(got, "fair dollars")
	for _, u := range []string{"string score", "fair unscored", "5x5 unscored", "not yet scored"} {
		if i := slices.Index(got, u); i < 0 || i < fair {
			t.Fatalf("unscored %q ranks above scored FAIR: %v", u, got)
		}
	}
	want := slices.Clone(rows)
	slices.SortStableFunc(want, func(a, b pageRow) int {
		ar, br := residualOf(t, a), residualOf(t, b)
		switch {
		case risk.RanksAbove(ar, br):
			return -1
		case risk.RanksAbove(br, ar):
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	if exp := titles(fx, want); !slices.Equal(got, exp) {
		t.Fatalf("SQL residual order differs from risk.RanksAbove:\n got %v\nwant %v", got, exp)
	}
}

// The dashboard's residual,age keeps working: residual descending, oldest
// first on a tie, FAIR below 5x5.
func TestListRisksPage_ResidualAgeAlias(t *testing.T) {
	fx := seedPageFixture(t)
	rows, _ := listPage(t, fx.env, "sort=residual,age")
	assertResidualOrder(t, rows, true)
}
