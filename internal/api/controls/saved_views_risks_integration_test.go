//go:build integration

// #2133 — saved views for the risk register. A risks view is saved, listed
// and deleted under ?surface=risks; a controls view saved beside it is
// untouched and stays on the default surface.

package controls_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func listViewsOn(t *testing.T, base, surface string) []savedViewWire {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/v1/saved-views?surface="+surface, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list %s views: %v", surface, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s views: expected 200; got %d body=%s", surface, resp.StatusCode, raw)
	}
	var out struct {
		Views []savedViewWire `json:"views"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode views: %v", err)
	}
	return out.Views
}

func deleteView(t *testing.T, base, id string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, base+"/v1/saved-views/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete view: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestSavedViews_RisksSurface_SaveListDelete(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	alice := seedUser(t, migrate, tenant)
	bob := seedUser(t, migrate, tenant)
	ts := ownerAssignServer(t, app, tenant, alice.String())

	// A controls view first, on the default surface.
	status, raw := postJSON(t, ts.URL+"/v1/saved-views", `{"name":"Triage","filters":{"family":"IAC","sort":"title"}}`)
	if status != http.StatusCreated {
		t.Fatalf("controls create: expected 201; got %d body=%s", status, raw)
	}
	var controlsView savedViewWire
	_ = json.Unmarshal(raw, &controlsView)

	// A risks view with the same name: names are per surface.
	status, raw = postJSON(t, ts.URL+"/v1/saved-views", `{"name":"Triage","surface":"risks","filters":{
		"status":"all","owner":"none","treatment":"mitigate","category":"vendor",
		"review_due":"overdue","sort":"-review_due","include_rollups":"true",
		"cols":"methodology,themes"}}`)
	if status != http.StatusCreated {
		t.Fatalf("risks create: expected 201; got %d body=%s", status, raw)
	}
	var risksView savedViewWire
	if err := json.Unmarshal(raw, &risksView); err != nil {
		t.Fatalf("decode risks view: %v", err)
	}
	want := map[string]string{
		"status": "all", "owner": "none", "treatment": "mitigate", "category": "vendor",
		"review_due": "overdue", "sort": "-review_due", "include_rollups": "true",
		"cols": "methodology,themes",
	}

	got := listViewsOn(t, ts.URL, "risks")
	if len(got) != 1 || got[0].ID != risksView.ID {
		t.Fatalf("risks list: want only the risks view; got %#v", got)
	}
	for k, v := range want {
		if got[0].Filters[k] != v {
			t.Fatalf("risks view %s = %q; want %q (all %#v)", k, got[0].Filters[k], v, got[0].Filters)
		}
	}
	if len(got[0].Filters) != len(want) {
		t.Fatalf("risks view filters = %#v; want %#v", got[0].Filters, want)
	}

	// The controls list (default surface and named) holds only the controls view.
	for _, list := range [][]savedViewWire{listViews(t, ts.URL), listViewsOn(t, ts.URL, "controls")} {
		if len(list) != 1 || list[0].ID != controlsView.ID || list[0].Filters["family"] != "IAC" || list[0].Filters["sort"] != "title" {
			t.Fatalf("controls list: want only the unchanged controls view; got %#v", list)
		}
	}

	// Another user in the tenant sees none of alice's risks views and cannot delete one.
	tsBob := ownerAssignServer(t, app, tenant, bob.String())
	if views := listViewsOn(t, tsBob.URL, "risks"); len(views) != 0 {
		t.Fatalf("ISOLATION BREACH: bob read alice's risks view; got %#v", views)
	}
	if code := deleteView(t, tsBob.URL, risksView.ID); code != http.StatusNotFound {
		t.Fatalf("bob delete alice's risks view: got %d; want 404", code)
	}

	// Alice deletes her risks view; the controls view stays.
	if code := deleteView(t, ts.URL, risksView.ID); code != http.StatusNoContent {
		t.Fatalf("delete risks view: got %d; want 204", code)
	}
	if views := listViewsOn(t, ts.URL, "risks"); len(views) != 0 {
		t.Fatalf("risks list after delete: want empty; got %#v", views)
	}
	if views := listViews(t, ts.URL); len(views) != 1 || views[0].ID != controlsView.ID {
		t.Fatalf("controls view affected by the risks delete; got %#v", views)
	}
}

func TestSavedViews_RisksSurface_RejectsUnknownEntries(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	alice := seedUser(t, migrate, tenant)
	ts := ownerAssignServer(t, app, tenant, alice.String())

	for name, body := range map[string]string{
		"unknown key":    `{"name":"v","surface":"risks","filters":{"family":"IAC"}}`,
		"bad value":      `{"name":"v","surface":"risks","filters":{"treatment":"ignore"}}`,
		"bad sort":       `{"name":"v","surface":"risks","filters":{"sort":"color"}}`,
		"unknown column": `{"name":"v","surface":"risks","filters":{"cols":"methodology,colour"}}`,
		"paging":         `{"name":"v","surface":"risks","filters":{"offset":"50"}}`,
		"bad surface":    `{"name":"v","surface":"evidence","filters":{}}`,
	} {
		if status, raw := postJSON(t, ts.URL+"/v1/saved-views", body); status != http.StatusBadRequest {
			t.Errorf("%s: expected 400; got %d body=%s", name, status, raw)
		}
	}
	if views := listViewsOn(t, ts.URL, "risks"); len(views) != 0 {
		t.Fatalf("a rejected view was stored: %#v", views)
	}

	// A value retired after the view was saved drops out on read.
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO saved_views (tenant_id, user_id, surface, name, filters)
		 VALUES ($1, $2, 'risks', 'Old', '{"treatment":"retired","owner":"none","cols":"gone"}')`,
		tenant, alice); err != nil {
		t.Fatalf("seed stale view: %v", err)
	}
	views := listViewsOn(t, ts.URL, "risks")
	if len(views) != 1 || len(views[0].Filters) != 1 || views[0].Filters["owner"] != "none" {
		t.Fatalf("stale view: want only owner=none kept; got %#v", views)
	}
}
