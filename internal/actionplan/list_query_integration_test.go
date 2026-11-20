//go:build integration

// #2618: GET /v1/action-plans?q= narrows the list to plans whose title
// contains q, ignoring case, so the bulk link picker can find a plan past
// the first page. `%` and `_` in q match literally.

package actionplan_test

import (
	"sort"
	"testing"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestList_QueryNarrowsByTitle(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	owner := seedUser(t, admin, tenant)
	store := actionplan.NewStore(app)
	ctx := ctxFor(t, tenant)

	for _, title := range []string{
		"Rotate the AWS root keys",
		"Close the IAC-37 freshness gap",
		"Enforce MFA on aws console",
		"Raise coverage to 100% on billing",
		"Raise coverage to 1000 on billing",
	} {
		in := validCreate(owner)
		in.Title = title
		if _, err := store.Create(ctx, in); err != nil {
			t.Fatalf("Create %q: %v", title, err)
		}
	}

	titles := func(q string) []string {
		t.Helper()
		rows, err := store.List(ctx, actionplan.ListFilter{Query: q})
		if err != nil {
			t.Fatalf("List q=%q: %v", q, err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Title
		}
		sort.Strings(out)
		return out
	}

	if got := titles("aws"); len(got) != 2 || got[0] != "Enforce MFA on aws console" || got[1] != "Rotate the AWS root keys" {
		t.Fatalf("q=aws: %v, want the two AWS plans", got)
	}
	if got := titles("100%"); len(got) != 1 || got[0] != "Raise coverage to 100% on billing" {
		t.Fatalf("q=100%%: %v, want only the literal 100%% plan", got)
	}
	if got := titles("no such plan"); len(got) != 0 {
		t.Fatalf("q=no such plan: %v, want none", got)
	}
	if got := titles(""); len(got) != 5 {
		t.Fatalf("q empty: %d plans, want all 5", len(got))
	}
}
