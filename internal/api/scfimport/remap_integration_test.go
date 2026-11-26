//go:build integration

package scfimport_test

// #1775: importing a new SCF release queues one remap proposal per control
// left on an older release's anchor, classified from the new release's
// legacy IDs. The fixtures are testdata/remap-release-{a,b}.json.
//
// The queue moves nothing. The one control that does move is TRM-01: its
// code keeps its title, so the #1971 D3 upgrade re-binds it in place (Matt's
// call, 2026-11-14), and its proposal records that as status "rebound".
// Every other control stays on its release-A anchor.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var remapCodes = []string{"TRM-01", "TRM-02", "TRM-03", "TRM-04", "TRM-05"}

func loadRemapRelease(t *testing.T, name string) *scfimport.Catalog {
	t.Helper()
	cat, err := scfimport.Load("testdata/remap-release-" + name + ".json")
	if err != nil {
		t.Fatalf("Load %s: %v", name, err)
	}
	return cat
}

func importRelease(t *testing.T, pool *pgxpool.Pool, cat *scfimport.Catalog) {
	t.Helper()
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("Import %s: %v", cat.ReleaseVersion, err)
	}
}

func anchorID(t *testing.T, pool *pgxpool.Pool, release, scfID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = $2`, release, scfID).Scan(&id); err != nil {
		t.Fatalf("anchor %s@%s: %v", scfID, release, err)
	}
	return id
}

// seedRemapControls gives a tenant one control on each release-A anchor and
// returns the control ids by anchor code.
func seedRemapControls(t *testing.T, pool *pgxpool.Pool, tenant, release string) map[string]uuid.UUID {
	t.Helper()
	out := map[string]uuid.UUID{}
	for _, code := range remapCodes {
		id := uuid.New()
		bundle := "remap_1775_" + code
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO controls
				(id, tenant_id, bundle_id, version, scf_id, scf_anchor_id,
				 title, description, control_family, implementation_type, owner_role,
				 lifecycle_state, applicability_expr, evidence_queries,
				 manual_evidence_schema, linked_policy_ids,
				 bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at,
				 bundle_uploaded_by)
			VALUES ($1, $2, $3, 1, $4, $5,
				'Control on ' || $4, '', 'TRM', 'automated', '',
				'active', '{"true": true}', '{}'::jsonb,
				'{}'::jsonb, '{}',
				'bundle_id: ' || $3 || E'\nscf_anchor_id: ' || $4 || E'\n', 'hash-' || $3, now(),
				'remap-1775-test')`,
			id, tenant, bundle, code, anchorID(t, pool, release, code)); err != nil {
			t.Fatalf("insert control on %s: %v", code, err)
		}
		out[code] = id
	}
	return out
}

type queued struct {
	class, status string
	successors    []string
}

func queueFor(t *testing.T, pool *pgxpool.Pool, tenant string) map[uuid.UUID]queued {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT control_id, class, status, successor_scf_ids
		FROM scf_remap_proposals WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]queued{}
	for rows.Next() {
		var id uuid.UUID
		var q queued
		if err := rows.Scan(&id, &q.class, &q.status, &q.successors); err != nil {
			t.Fatalf("scan queue: %v", err)
		}
		if _, dup := out[id]; dup {
			t.Fatalf("control %s has more than one proposal", id)
		}
		out[id] = q
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read queue: %v", err)
	}
	return out
}

func controlAnchor(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) uuid.UUID {
	t.Helper()
	var a uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT scf_anchor_id FROM controls WHERE id = $1`, id).Scan(&a); err != nil {
		t.Fatalf("read control %s anchor: %v", id, err)
	}
	return a
}

func TestRemapQueue_ImportingReleaseBQueuesOneClassifiedProposalPerControl(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	ctx := context.Background()

	a, b := loadRemapRelease(t, "a"), loadRemapRelease(t, "b")
	importRelease(t, pool, a)
	tenant := dbtest.SeedTenant(t, pool, "controls")
	controls := seedRemapControls(t, pool, tenant, a.ReleaseVersion)
	before := map[string]uuid.UUID{}
	for code, id := range controls {
		before[code] = controlAnchor(t, pool, id)
	}

	importRelease(t, pool, b)
	report, err := scfimport.RebindControls(ctx, pool)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}

	want := map[string]queued{
		"TRM-01": {"unchanged", "rebound", []string{"TRM-01"}},
		"TRM-02": {"renamed", "open", []string{"TRM-20"}},
		"TRM-03": {"split", "open", []string{"TRM-30", "TRM-31"}},
		"TRM-04": {"reused_id", "open", []string{"TRM-41"}},
		"TRM-05": {"no_successor", "open", []string{}},
	}
	got := queueFor(t, pool, tenant)
	if len(got) != len(controls) {
		t.Fatalf("queue has %d proposals; want one per control (%d): %+v", len(got), len(controls), got)
	}
	for code, w := range want {
		g, ok := got[controls[code]]
		if !ok {
			t.Errorf("%s: no proposal", code)
			continue
		}
		if g.class != w.class || g.status != w.status || !slices.Equal(g.successors, w.successors) {
			t.Errorf("%s: proposal = %+v; want %+v", code, g, w)
		}
	}

	// Nothing moves but what D3 re-binds: TRM-01 keeps its code and title.
	for code, id := range controls {
		after := controlAnchor(t, pool, id)
		wantAnchor := before[code]
		if code == "TRM-01" {
			wantAnchor = anchorID(t, pool, b.ReleaseVersion, "TRM-01")
		}
		if after != wantAnchor {
			t.Errorf("%s: anchor moved from %s to %s; want %s", code, before[code], after, wantAnchor)
		}
	}

	// The import summary counts match the queue.
	counts := map[scfimport.RemapClass]int{}
	for _, q := range got {
		counts[scfimport.RemapClass(q.class)]++
	}
	for _, class := range scfimport.RemapClasses {
		if report.Proposals[class] != counts[class] {
			t.Errorf("summary %s = %d; queue has %d", class, report.Proposals[class], counts[class])
		}
	}

	// A second run of the same import queues nothing new, and its summary
	// says so (#2325): the open proposals are not counted again.
	again, err := scfimport.RebindControls(ctx, pool)
	if err != nil {
		t.Fatalf("repeat RebindControls: %v", err)
	}
	if left := queueFor(t, pool, tenant); len(left) != len(got) {
		t.Fatalf("repeat run left %d proposals; want %d", len(left), len(got))
	}
	if len(again.NeedsReanchoring) == 0 {
		t.Fatal("repeat run flagged no control; the re-run count check proves nothing")
	}
	for _, class := range scfimport.RemapClasses {
		if n := again.Proposals[class]; n != 0 {
			t.Errorf("repeat run summary %s = %d; want 0", class, n)
		}
	}
}

func TestRemapQueue_ReleaseWithoutLegacyIDsQueuesEveryControlAsNoSuccessor(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)

	a := loadRemapRelease(t, "a")
	bare := loadRemapRelease(t, "b")
	bare.ReleaseVersion = "test-remap-b-bare"
	for i := range bare.Controls {
		bare.Controls[i].LegacySCFID = ""
	}
	importRelease(t, pool, a)
	tenant := dbtest.SeedTenant(t, pool, "controls")
	controls := seedRemapControls(t, pool, tenant, a.ReleaseVersion)
	importRelease(t, pool, bare)
	report, err := scfimport.RebindControls(context.Background(), pool)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}

	got := queueFor(t, pool, tenant)
	if len(got) != len(controls) {
		t.Fatalf("queue has %d proposals; want %d", len(got), len(controls))
	}
	for code, id := range controls {
		if q := got[id]; q.class != "no_successor" || len(q.successors) != 0 {
			t.Errorf("%s: proposal = %+v; want no_successor with no successors", code, q)
		}
	}
	if n := report.Proposals[scfimport.RemapNoSuccessor]; n != len(controls) {
		t.Errorf("summary no_successor = %d; want %d", n, len(controls))
	}
}

type remapListBody struct {
	Proposals []struct {
		ControlID       string   `json:"control_id"`
		LegacySCFID     string   `json:"legacy_scf_id"`
		Class           string   `json:"class"`
		Status          string   `json:"status"`
		SuccessorSCFIDs []string `json:"successor_scf_ids"`
	} `json:"proposals"`
	Count      int    `json:"count"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor"`
}

func listRemap(t *testing.T, app *pgxpool.Pool, tenant, query string, wantStatus int) remapListBody {
	t.Helper()
	h := apicontrols.NewRemapProposalsHandler(control.NewStore(app))
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/remap-proposals"+query, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "key_test_1775", TenantID: tenant})
	ctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rr := httptest.NewRecorder()
	h.List(rr, req.WithContext(ctx))
	if rr.Code != wantStatus {
		t.Fatalf("GET %s: status = %d; want %d; body=%s", query, rr.Code, wantStatus, rr.Body.String())
	}
	var out remapListBody
	if wantStatus == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return out
}

func TestRemapQueue_APIListsOnlyTheCallerTenantFilteredByClassAndStatus(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, pool)

	a, b := loadRemapRelease(t, "a"), loadRemapRelease(t, "b")
	importRelease(t, pool, a)
	tenant := dbtest.SeedTenant(t, pool, "controls")
	other := dbtest.SeedTenant(t, pool, "controls")
	mine := seedRemapControls(t, pool, tenant, a.ReleaseVersion)
	theirs := seedRemapControls(t, pool, other, a.ReleaseVersion)
	importRelease(t, pool, b)
	if _, err := scfimport.RebindControls(context.Background(), pool); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	own := map[string]bool{}
	for _, id := range mine {
		own[id.String()] = true
	}
	for _, id := range theirs {
		if own[id.String()] {
			t.Fatalf("seeded the same control id for both tenants")
		}
	}

	cases := []struct {
		query string
		codes []string
	}{
		{"", remapCodes},
		{"?class=split", []string{"TRM-03"}},
		{"?status=rebound", []string{"TRM-01"}},
		{"?status=open", []string{"TRM-02", "TRM-03", "TRM-04", "TRM-05"}},
		{"?class=reused_id&status=open", []string{"TRM-04"}},
		{"?class=unchanged&status=open", nil},
	}
	for _, c := range cases {
		got := listRemap(t, app, tenant, c.query, http.StatusOK)
		var codes []string
		for _, p := range got.Proposals {
			if !own[p.ControlID] {
				t.Errorf("%q: listed control %s of another tenant", c.query, p.ControlID)
			}
			codes = append(codes, p.LegacySCFID)
		}
		slices.Sort(codes)
		if !slices.Equal(codes, c.codes) || got.Count != len(c.codes) {
			t.Errorf("%q: listed %v (count %d); want %v", c.query, codes, got.Count, c.codes)
		}
	}
	if got := listRemap(t, app, other, "", http.StatusOK); got.Count != len(theirs) {
		t.Errorf("other tenant lists %d proposals; want its own %d", got.Count, len(theirs))
	}

	// RLS, not only the query's tenant filter: the app role with no tenant
	// set sees no rows.
	var visible int
	if err := app.QueryRow(context.Background(), `SELECT count(*) FROM scf_remap_proposals`).Scan(&visible); err != nil {
		t.Fatalf("count without tenant: %v", err)
	}
	if visible != 0 {
		t.Errorf("app role without tenant context sees %d proposals; want 0", visible)
	}
	// With the tenant set and no tenant filter in the query, RLS alone keeps
	// the other tenant's proposals out.
	tx, err := app.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM scf_remap_proposals`).Scan(&visible); err != nil {
		t.Fatalf("count with tenant: %v", err)
	}
	if visible != len(mine) {
		t.Errorf("app role as tenant sees %d proposals; want its own %d", visible, len(mine))
	}

	listRemap(t, app, tenant, "?class=moved", http.StatusBadRequest)
	listRemap(t, app, tenant, "?status=done", http.StatusBadRequest)
}

// supersedeControl gives a control a version 2, as an upload with a changed
// manifest does, and returns the new version's id.
func supersedeControl(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	next := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE controls SET superseded_by = $2 WHERE id = $1`, id, next); err != nil {
		t.Fatalf("supersede %s: %v", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO controls
			(id, tenant_id, bundle_id, version, scf_id, scf_anchor_id,
			 title, description, control_family, implementation_type, owner_role,
			 lifecycle_state, applicability_expr, evidence_queries,
			 manual_evidence_schema, linked_policy_ids,
			 bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at,
			 bundle_uploaded_by)
		SELECT $2, tenant_id, bundle_id, version + 1, scf_id, scf_anchor_id,
			 title, 'v2', control_family, implementation_type, owner_role,
			 lifecycle_state, applicability_expr, evidence_queries,
			 manual_evidence_schema, linked_policy_ids,
			 bundle_manifest_yaml, bundle_manifest_hash || '-v2', now(),
			 bundle_uploaded_by
		FROM controls WHERE id = $1`, id, next); err != nil {
		t.Fatalf("insert version 2 of %s: %v", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return next
}

func listedCodes(b remapListBody) []string {
	codes := []string{}
	for _, p := range b.Proposals {
		codes = append(codes, p.LegacySCFID)
	}
	return codes
}

// #2325: the list pages by cursor, filters out a class, and leaves out a
// pending proposal whose control has a newer version.
func TestRemapQueue_APIPagesFiltersClassNotAndHidesStaleProposals(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, pool)

	a, b := loadRemapRelease(t, "a"), loadRemapRelease(t, "b")
	importRelease(t, pool, a)
	tenant := dbtest.SeedTenant(t, pool, "controls")
	controls := seedRemapControls(t, pool, tenant, a.ReleaseVersion)
	importRelease(t, pool, b)
	if _, err := scfimport.RebindControls(context.Background(), pool); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}

	for _, c := range []struct {
		query string
		codes []string
	}{
		{"?class_not=unchanged", []string{"TRM-02", "TRM-03", "TRM-04", "TRM-05"}},
		{"?class_not=split&status=open", []string{"TRM-02", "TRM-04", "TRM-05"}},
		{"?status=rebound&class_not=unchanged", []string{}},
	} {
		got := listRemap(t, app, tenant, c.query, http.StatusOK)
		codes := listedCodes(got)
		slices.Sort(codes)
		if !slices.Equal(codes, c.codes) || got.Total != len(c.codes) {
			t.Errorf("%q: listed %v (total %d); want %v", c.query, codes, got.Total, c.codes)
		}
	}
	listRemap(t, app, tenant, "?class_not=moved", http.StatusBadRequest)

	// Walking two at a time reads every proposal once, in the unpaged
	// order, and every page reports the same total.
	all := listedCodes(listRemap(t, app, tenant, "", http.StatusOK))
	var walked []string
	pages, cursor := 0, ""
	for {
		q := "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		page := listRemap(t, app, tenant, q, http.StatusOK)
		pages++
		if page.Total != len(remapCodes) || page.Count != len(page.Proposals) || page.Count > 2 {
			t.Fatalf("page %d: count %d total %d; want at most 2 of %d", pages, page.Count, page.Total, len(remapCodes))
		}
		walked = append(walked, listedCodes(page)...)
		if page.NextCursor == "" {
			break
		}
		if pages > len(remapCodes) {
			t.Fatal("cursor walk does not end")
		}
		cursor = page.NextCursor
	}
	if pages != 3 || !slices.Equal(walked, all) {
		t.Errorf("walked %v in %d pages; want %v in 3", walked, pages, all)
	}
	listRemap(t, app, tenant, "?cursor=not-a-cursor", http.StatusBadRequest)

	// A newer version makes TRM-02's open proposal stale, so it leaves the
	// list. TRM-01's rebound proposal is a record, not a to-do, and stays.
	supersedeControl(t, pool, controls["TRM-01"])
	supersedeControl(t, pool, controls["TRM-02"])
	got := listRemap(t, app, tenant, "", http.StatusOK)
	codes := listedCodes(got)
	slices.Sort(codes)
	if want := []string{"TRM-01", "TRM-03", "TRM-04", "TRM-05"}; !slices.Equal(codes, want) || got.Total != len(want) {
		t.Errorf("after supersede: listed %v (total %d); want %v", codes, got.Total, want)
	}
	if got := listRemap(t, app, tenant, "?status=open", http.StatusOK); got.Total != 3 {
		t.Errorf("open after supersede: total %d; want 3", got.Total)
	}
}
