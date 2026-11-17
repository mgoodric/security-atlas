//go:build integration

// #2131 — `GET /v1/controls?include=state` filters, sorts and pages on the
// server, so the /controls page no longer loads every control and filters
// in the browser.
//
// What this pins, on the front-door fixture (internal/dbtest/attention:
// two tenants, two users each, every needs-attention source) plus a few
// controls in tenant A with real evidence timestamps, a family of their
// own and a framework edge:
//   - each filter (result, freshness, owner, owner=none, family,
//     framework) narrows the page AND the total, and never returns
//     tenant B's controls;
//   - each sort key, ascending and descending, orders the rows, and
//     paging through any order two rows at a time returns every control
//     once, with no duplicates or gaps;
//   - the default order is failing controls first, then the stalest
//     evidence (never observed before the oldest observation);
//   - an unknown sort, result or freshness value is a 400.

package controls_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

type filterRow struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	ControlFamily string   `json:"control_family"`
	SCFID         string   `json:"scf_id"`
	Frameworks    []string `json:"frameworks"`
	OwnerUserID   *string  `json:"owner_user_id"`
	State         *struct {
		Result          string  `json:"result"`
		FreshnessStatus string  `json:"freshness_status"`
		LastObservedAt  *string `json:"last_observed_at"`
	} `json:"state"`
}

type filterResp struct {
	Controls []filterRow `json:"controls"`
	Count    int         `json:"count"`
	Total    int64       `json:"total"`
	Families []string    `json:"families"`
}

// listFiltered GETs one page; a non-200 fails the test.
func listFiltered(t *testing.T, ts string, query string) filterResp {
	t.Helper()
	status, raw := getBody(t, ts+"/v1/controls?include=state&"+query)
	if status != http.StatusOK {
		t.Fatalf("list %s: status %d body=%s", query, status, raw)
	}
	var out filterResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode list %s: %v", query, err)
	}
	return out
}

// walk pages through one order two rows at a time and fails on a
// duplicate, or on a walk that does not add up to the total.
func walk(t *testing.T, ts string, query string) []filterRow {
	t.Helper()
	const pageSize = 2
	var all []filterRow
	seen := map[string]bool{}
	var total int64 = -1
	for offset := 0; ; offset += pageSize {
		page := listFiltered(t, ts, fmt.Sprintf("%s&limit=%d&offset=%d", query, pageSize, offset))
		if total >= 0 && page.Total != total {
			t.Fatalf("%s: total moved from %d to %d mid-walk", query, total, page.Total)
		}
		total = page.Total
		for _, row := range page.Controls {
			if seen[row.ID] {
				t.Fatalf("%s: control %s on two pages", query, row.ID)
			}
			seen[row.ID] = true
			all = append(all, row)
		}
		if len(page.Controls) < pageSize {
			break
		}
	}
	if int64(len(all)) != total {
		t.Fatalf("%s: walked %d rows, total %d", query, len(all), total)
	}
	return all
}

func ids(rows []filterRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func sortedIDs(in ...uuid.UUID) []string {
	out := make([]string, len(in))
	for i, id := range in {
		out[i] = id.String()
	}
	sort.Strings(out)
	return out
}

// filterFixture is the attention fixture plus tenant A's extra controls.
type filterFixture struct {
	attention.Fixture
	Framework string // the catalog framework slug ControlFramework satisfies
	Family    string // the family only ControlFramework carries

	FailOlder        uuid.UUID // fail, observed 10 days ago, on the framework
	PassStale        uuid.UUID // pass, stale, observed 30 days ago
	Inconclusive     uuid.UUID // inconclusive, observed 5 days ago
	OwnedByOther     uuid.UUID // pass, fresh, observed 1 day ago, owned by Other
	ControlFramework uuid.UUID // = FailOlder
}

func (f filterFixture) tenantAControls() []uuid.UUID {
	a := f.A
	return []uuid.UUID{
		a.FailingControl, a.PeriodicControl, a.UnownedControl, a.WaiverControl,
		f.FailOlder, f.PassStale, f.Inconclusive, f.OwnedByOther,
	}
}

func seedFilterFixture(t *testing.T, admin *pgxpool.Pool, now time.Time) filterFixture {
	t.Helper()
	ctx := context.Background()
	fx := filterFixture{Fixture: attention.Seed(t, admin, now)}
	tenant := fx.A.ID
	tag := uuid.NewString()[:8]
	fx.Framework = "t2131-" + tag
	fx.Family = "Zeta " + tag

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	// A catalog framework of this test's own, with one requirement mapped
	// to one anchor, so the framework filter has exactly one control.
	anchor := insertAnchor(t, admin, "T2131-"+tag, "Test")
	plain := insertAnchor(t, admin, "T2131-"+tag+"-P", "Test") // on no framework
	fwID, verID, reqID := uuid.New(), uuid.New(), uuid.New()
	exec("framework", `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'Issue 2131 test framework', 'test', '')
	`, fwID, fx.Framework)
	exec("framework version", `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '1', 'current')
	`, verID, fwID)
	exec("requirement", `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'R-1', 'issue 2131 requirement', '')
	`, reqID, verID)
	exec("edge", `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES ($1, $2, $3, 'equal', 1.0, 'scf_official', 'issue 2131 test edge')
	`, uuid.New(), reqID, anchor)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = admin.Exec(ctx, `DELETE FROM fw_to_scf_edges WHERE framework_requirement_id = $1`, reqID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_requirements WHERE id = $1`, reqID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, verID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})

	evaluate := func(ctrl uuid.UUID, result, freshness string, observed time.Time) {
		exec("evaluation", `
			INSERT INTO control_evaluations (
				id, tenant_id, control_id, eval_run_id, evaluated_at,
				result, freshness_status, last_observed_at,
				evidence_count_in_window, trigger
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 'manual')
		`, uuid.New(), tenant, ctrl, uuid.New(), now.Add(-time.Hour), result, freshness, observed)
	}
	day := 24 * time.Hour

	fx.FailOlder = insertControl(t, admin, tenant, anchor, "Alpha failing older", fx.Family, "t2131-fail-"+tag)
	fx.ControlFramework = fx.FailOlder
	evaluate(fx.FailOlder, "fail", "fresh", now.Add(-10*day))

	fx.PassStale = insertControl(t, admin, tenant, plain, "Beta passing stale", "AAA", "t2131-stale-"+tag)
	evaluate(fx.PassStale, "pass", "stale", now.Add(-30*day))

	fx.Inconclusive = insertControl(t, admin, tenant, plain, "Gamma inconclusive", "AAA", "t2131-inc-"+tag)
	evaluate(fx.Inconclusive, "inconclusive", "fresh", now.Add(-5*day))

	fx.OwnedByOther = insertControl(t, admin, tenant, plain, "Delta owned by other", "AAA", "t2131-other-"+tag)
	evaluate(fx.OwnedByOther, "pass", "fresh", now.Add(-1*day))
	exec("owner", `
		INSERT INTO control_owner_assignments (tenant_id, control_id, owner_user_id, assigned_by)
		VALUES ($1, $2, $3, $3)
	`, tenant, fx.OwnedByOther, fx.A.Other)
	return fx
}

// listServer serves the list as tenant A's Owner.
func listServer(t *testing.T, app *pgxpool.Pool, at attention.Tenant) string {
	t.Helper()
	return ownerReadServer(t, app, at.ID, at.Owner.String()).URL
}

func TestListWithState_Filters(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedFilterFixture(t, admin, time.Now().UTC())
	ts := listServer(t, app, fx.A)
	a := fx.A

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"no filter", "", sortedIDs(fx.tenantAControls()...)},
		{"result=fail", "result=fail", sortedIDs(a.FailingControl, fx.FailOlder)},
		{"result=inconclusive", "result=inconclusive", sortedIDs(fx.Inconclusive)},
		{"freshness=stale", "freshness=stale", sortedIDs(fx.PassStale)},
		{"owner=Owner", "owner=" + a.Owner.String(), sortedIDs(a.FailingControl, a.PeriodicControl, a.WaiverControl)},
		{"owner=Other", "owner=" + a.Other.String(), sortedIDs(fx.OwnedByOther)},
		{"owner=none", "owner=none", sortedIDs(a.UnownedControl, fx.FailOlder, fx.PassStale, fx.Inconclusive)},
		{"family", "family=" + url.QueryEscape(strings.ToUpper(fx.Family)), sortedIDs(fx.FailOlder)},
		{"framework", "framework=" + fx.Framework, sortedIDs(fx.ControlFramework)},
		{"framework unknown", "framework=no-such-framework", []string{}},
		{"result+owner", "result=fail&owner=" + a.Owner.String(), sortedIDs(a.FailingControl)},
		// Tenant B's owner id matches nothing in tenant A.
		{"owner from tenant B", "owner=" + fx.B.Owner.String(), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := walk(t, ts, tc.query)
			got := ids(rows)
			sort.Strings(got)
			if got == nil {
				got = []string{}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("%s: got %v, want %v", tc.query, got, tc.want)
			}
		})
	}

	// The family list is the tenant's, unfiltered by the request.
	page := listFiltered(t, ts, "result=fail")
	if strings.Join(page.Families, ",") != strings.Join([]string{"AAA", fx.Family}, ",") {
		t.Fatalf("families = %v, want [AAA %s]", page.Families, fx.Family)
	}

	// Tenant B sees only its own four controls, under every filter.
	tsB := listServer(t, app, fx.B)
	b := fx.B
	wantB := sortedIDs(b.FailingControl, b.PeriodicControl, b.UnownedControl, b.WaiverControl)
	gotB := ids(walk(t, tsB, ""))
	sort.Strings(gotB)
	if strings.Join(gotB, ",") != strings.Join(wantB, ",") {
		t.Fatalf("tenant B: got %v, want %v", gotB, wantB)
	}
	if got := ids(walk(t, tsB, "framework="+fx.Framework)); len(got) != 0 {
		t.Fatalf("tenant B framework filter: got %v, want none", got)
	}
	if got := ids(walk(t, tsB, "result=fail")); len(got) != 1 || got[0] != b.FailingControl.String() {
		t.Fatalf("tenant B result=fail: got %v, want [%s]", got, b.FailingControl)
	}
}

func TestListWithState_DefaultOrderFailingThenStalest(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedFilterFixture(t, admin, time.Now().UTC())
	ts := listServer(t, app, fx.A)
	a := fx.A

	got := ids(walk(t, ts, ""))
	// fail: never observed, then 10 days ago; inconclusive; pass: never
	// observed (periodic), then 30 days ago, then 1 day ago; then the two
	// controls with no evaluation.
	wantHead := []string{
		a.FailingControl.String(), fx.FailOlder.String(),
		fx.Inconclusive.String(),
		a.PeriodicControl.String(), fx.PassStale.String(), fx.OwnedByOther.String(),
	}
	if len(got) != 8 {
		t.Fatalf("got %d rows, want 8: %v", len(got), got)
	}
	if strings.Join(got[:6], ",") != strings.Join(wantHead, ",") {
		t.Fatalf("default order head:\n got  %v\n want %v", got[:6], wantHead)
	}
	tail := got[6:]
	sort.Strings(tail)
	if strings.Join(tail, ",") != strings.Join(sortedIDs(a.UnownedControl, a.WaiverControl), ",") {
		t.Fatalf("default order tail: got %v", got[6:])
	}
	// An explicit empty sort is the same order.
	if again := ids(walk(t, ts, "sort=")); strings.Join(again, ",") != strings.Join(got, ",") {
		t.Fatalf("sort= differs from the default: %v vs %v", again, got)
	}
}

var resultRank = map[string]int{"fail": 5, "inconclusive": 4, "excepted": 3, "pass": 2, "na": 1}
var freshnessRank = map[string]int{"expired": 4, "stale": 3, "no_evidence": 2, "fresh": 1}

func TestListWithState_EverySortKeyOrdersAndPages(t *testing.T) {
	t.Parallel()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := seedFilterFixture(t, admin, time.Now().UTC())
	ts := listServer(t, app, fx.A)

	ownerName := map[string]string{
		fx.A.Owner.String(): "owner a",
		fx.A.Other.String(): "other a",
	}
	// key returns the row's sort value, or "" with ok=false for a null.
	keys := map[string]func(r filterRow) (string, bool){
		"scf_id": func(r filterRow) (string, bool) { return r.SCFID, r.SCFID != "" },
		"name":   func(r filterRow) (string, bool) { return strings.ToLower(r.Title), true },
		"family": func(r filterRow) (string, bool) { return strings.ToLower(r.ControlFamily), true },
		"state": func(r filterRow) (string, bool) {
			if r.State == nil {
				return "0", true
			}
			return fmt.Sprint(resultRank[r.State.Result]), true
		},
		"freshness": func(r filterRow) (string, bool) {
			if r.State == nil {
				return "0", true
			}
			return fmt.Sprint(freshnessRank[r.State.FreshnessStatus]), true
		},
		"last_observed_at": func(r filterRow) (string, bool) {
			if r.State == nil || r.State.LastObservedAt == nil {
				return "", false
			}
			ts, err := time.Parse(time.RFC3339Nano, *r.State.LastObservedAt)
			if err != nil {
				return "", false
			}
			return ts.UTC().Format("2006-01-02T15:04:05.000000000"), true
		},
		"owner": func(r filterRow) (string, bool) {
			if r.OwnerUserID == nil {
				return "", false
			}
			return ownerName[*r.OwnerUserID], true
		},
		"frameworks": func(r filterRow) (string, bool) { return fmt.Sprint(len(r.Frameworks)), true },
	}
	// nullsFirst names the orders that put nulls first: the never-observed
	// control is the stalest, so it leads an ascending last-observed sort.
	nullsFirst := map[string]bool{"last_observed_at": true}

	all := sortedIDs(fx.tenantAControls()...)
	for key, value := range keys {
		for _, desc := range []bool{false, true} {
			param := key
			if desc {
				param = "-" + key
			}
			t.Run(param, func(t *testing.T) {
				rows := walk(t, ts, "sort="+param)
				got := ids(rows)
				sorted := append([]string(nil), got...)
				sort.Strings(sorted)
				if strings.Join(sorted, ",") != strings.Join(all, ",") {
					t.Fatalf("sort=%s returned %v, want every tenant A control %v", param, got, all)
				}
				leadingNulls := nullsFirst[key] && !desc
				seenNull, seenValue := false, false
				prev := ""
				for i, r := range rows {
					v, ok := value(r)
					if !ok {
						if leadingNulls && seenValue {
							t.Fatalf("sort=%s: null at row %d after a value", param, i)
						}
						seenNull = true
						continue
					}
					if seenNull && !leadingNulls {
						t.Fatalf("sort=%s: value %q at row %d after a null", param, v, i)
					}
					if seenValue {
						if (!desc && v < prev) || (desc && v > prev) {
							t.Fatalf("sort=%s: row %d value %q out of order after %q", param, i, v, prev)
						}
					}
					prev, seenValue = v, true
				}
			})
		}
	}
}

func TestListWithState_RefusesUnknownSortAndFilterValues(t *testing.T) {
	t.Parallel()
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "controls")
	ts := ownerReadServer(t, app, tenant, uuid.NewString()).URL
	for _, q := range []string{
		"sort=title%3Bdrop",
		"sort=--name",
		"sort=evaluated_at",
		"result=insufficient_evidence",
		"freshness=old",
		"owner=nobody",
	} {
		status, raw := getBody(t, ts+"/v1/controls?include=state&"+q)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status %d body=%s, want 400", q, status, raw)
		}
	}
}
