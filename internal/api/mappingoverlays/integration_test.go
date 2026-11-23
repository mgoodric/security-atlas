//go:build integration

// #1797 — tenant overlay edges: create, list and retire (API). Runs against
// a real Postgres with RLS.
//
// Fixture (the #1735 shape): two tenants (A, B); a small framework whose
// one version is frozen (status current) with three criteria; the #1770
// two-release SCF fixture (A legacy, B current); catalog edges C1 -> IAC-06
// and C3 -> CRY-03; and tenant A controls anchored to IAC-06 whose
// tested-under references are C1 (reached through the catalog edge) and C2
// (not reached: the gap an overlay edge closes).

package mappingoverlays_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

type fixture struct {
	admin       *pgxpool.Pool
	framework   string // slug
	version     string // label of the frozen version
	catalogC1   uuid.UUID
	tenantA     env
	tenantB     env
	requirement map[string]uuid.UUID // code -> id
}

// env is one tenant's server, running the real authz engine, with the
// bearers of a seeded admin and a seeded viewer (live user_roles grants).
type env struct {
	url      string
	tenant   string
	admin    string
	viewer   string
	viewerID uuid.UUID
}

func newEnv(t *testing.T, app *pgxpool.Pool, tenant string) env {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	e := env{tenant: tenant, admin: liveroletest.Bearer(t, srv, tenant, "admin")}
	e.viewerID, e.viewer = liveroletest.Person(t, srv, tenant, "viewer")
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	e.url = ts.URL
	return e
}

func call(t *testing.T, e env, method, path, bearer string, payload any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(method, e.url+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)

	uniq := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	f := fixture{admin: admin, framework: "ovl-" + uniq, version: "2026", requirement: map[string]uuid.UUID{}}

	fwID, verID := uuid.New(), uuid.New()
	t.Cleanup(func() {
		// An active overlay edge refuses its requirement's delete (#2240), so
		// the overlay edges go first; requirements and catalog edges cascade
		// from the version.
		_, _ = admin.Exec(ctx, `DELETE FROM fw_to_scf_overlay_edges WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			WHERE fv.framework_id = $1)`, fwID)
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE framework_id = $1`, fwID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	if _, err := admin.Exec(ctx, `INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Overlay fixture', $2, 'test')`, fwID, f.framework); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, $3, 'current')`, verID, fwID, f.version); err != nil {
		t.Fatalf("seed framework version: %v", err)
	}
	for _, code := range []string{"C1", "C2", "C3"} {
		id := uuid.New()
		if _, err := admin.Exec(ctx, `INSERT INTO framework_requirements (id, framework_version_id, code, title)
			VALUES ($1, $2, $3, $3)`, id, verID, code); err != nil {
			t.Fatalf("seed requirement %s: %v", code, err)
		}
		f.requirement[code] = id
	}
	catalogEdge := func(code, anchorCode, tier string) uuid.UUID {
		id := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id,
				relationship_type, strength, source_attribution, mapping_tier)
			SELECT $1, $2, a.id, 'equal', 1.0, 'scf_official', $5::crosswalk_mapping_tier
			FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
			WHERE fv.version = $3 AND a.scf_id = $4`,
			id, f.requirement[code], scfreleasetest.ReleaseB, anchorCode, tier); err != nil {
			t.Fatalf("seed catalog edge %s -> %s: %v", code, anchorCode, err)
		}
		return id
	}
	f.catalogC1 = catalogEdge("C1", "IAC-06", "verified")
	// A rejected catalog edge is not active: an org may still assert it.
	catalogEdge("C3", "CRY-03", "rejected")

	tenantA := dbtest.SeedTenant(t, admin, "fw_to_scf_overlay_edges", "controls")
	tenantB := dbtest.SeedTenant(t, admin, "fw_to_scf_overlay_edges", "controls")
	refs := `[{"system":"` + f.framework + `","kind":"tested_under","id":"C1"},` +
		`{"system":"` + f.framework + `","kind":"tested_under","id":"C2","note":"FY25 report s4"}]`
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
			control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
			evidence_queries, manual_evidence_schema, linked_policy_ids, freshness_class,
			bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at, bundle_uploaded_by,
			external_refs
		)
		SELECT $1, $2, $3, 1, a.scf_id, a.id, 'Confidentiality agreements', '',
			'human_resources', 'automated', 'security', 'active', '',
			'[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'test', $5::jsonb
		FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $4 AND a.scf_id = 'IAC-06'`,
		uuid.New(), tenantA, "ovl-"+uniq, scfreleasetest.ReleaseB, refs); err != nil {
		t.Fatalf("seed tenant A control: %v", err)
	}
	f.tenantA = newEnv(t, app, tenantA)
	f.tenantB = newEnv(t, app, tenantB)
	return f
}

// body returns a valid create request for requirement code -> anchor code
// in release B.
func (f fixture) body(code, anchor string) map[string]any {
	return map[string]any{
		"framework":         f.framework,
		"framework_version": f.version,
		"requirement_code":  code,
		"scf_release":       scfreleasetest.ReleaseB,
		"anchor_code":       anchor,
		"relationship_type": "intersects_with",
		"strength":          0.8,
		"rationale":         "auditor tested the confidentiality-agreement control under " + code,
		"source_ref":        "FY25 SOC 2 report, section 4, " + code,
	}
}

func edgeOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	e, ok := body["overlay_edge"].(map[string]any)
	if !ok {
		t.Fatalf("response has no overlay_edge: %v", body)
	}
	return e
}

func list(t *testing.T, e env, query string) []map[string]any {
	t.Helper()
	status, body := call(t, e, http.MethodGet, "/v1/mapping-overlays"+query, e.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list: status %d (%v)", status, body)
	}
	var out []map[string]any
	for _, item := range body["overlay_edges"].([]any) {
		out = append(out, item.(map[string]any))
	}
	return out
}

// The fixture is what #1735's later steps build on: check it says what the
// header claims, so a broken fixture fails here rather than downstream.
func TestOverlayFixture_ControlHasOneReachedAndOneUnreachedCriterion(t *testing.T) {
	f := newFixture(t)
	var reached, unreached int
	if err := f.admin.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM fw_to_scf_edges e
				WHERE e.scf_anchor_id = c.scf_anchor_id AND e.framework_requirement_id = r.id)),
			count(*) FILTER (WHERE NOT EXISTS (
				SELECT 1 FROM fw_to_scf_edges e
				WHERE e.scf_anchor_id = c.scf_anchor_id AND e.framework_requirement_id = r.id))
		FROM controls c
		CROSS JOIN LATERAL jsonb_array_elements(c.external_refs) ref
		JOIN framework_requirements r ON r.code = ref->>'id' AND r.id = ANY($2)
		WHERE c.tenant_id = $1 AND ref->>'kind' = 'tested_under'`,
		f.tenantA.tenant, []uuid.UUID{f.requirement["C1"], f.requirement["C2"], f.requirement["C3"]},
	).Scan(&reached, &unreached); err != nil {
		t.Fatal(err)
	}
	if reached != 1 || unreached != 1 {
		t.Fatalf("tested-under criteria: reached=%d unreached=%d, want 1 and 1", reached, unreached)
	}
}

func TestOverlay_CreateListsForOwnerAndIsInvisibleToOtherTenant(t *testing.T) {
	f := newFixture(t)

	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	if status != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", status, body)
	}
	created := edgeOf(t, body)
	for k, want := range map[string]any{
		"framework": f.framework, "framework_version": f.version, "requirement_code": "C2",
		"scf_release": scfreleasetest.ReleaseB, "anchor_code": "IAC-06",
		"relationship_type": "intersects_with", "strength": 0.8, "status": "active",
		"framework_requirement_id": f.requirement["C2"].String(),
	} {
		if created[k] != want {
			t.Errorf("created[%s] = %v; want %v", k, created[k], want)
		}
	}
	if created["created_by"] == "" || created["rationale"] == "" || created["source_ref"] == "" {
		t.Errorf("created = %v", created)
	}

	got := list(t, f.tenantA, "")
	if len(got) != 1 || got[0]["id"] != created["id"] {
		t.Fatalf("tenant A list = %v", got)
	}
	for _, q := range []string{
		"?framework=" + f.framework, "?requirement=C2", "?anchor=IAC-06", "?status=active",
		"?framework=" + f.framework + "&requirement=C2&anchor=IAC-06&status=active",
	} {
		if got := list(t, f.tenantA, q); len(got) != 1 {
			t.Errorf("list%s = %d edges; want 1", q, len(got))
		}
	}
	for _, q := range []string{"?framework=soc2", "?requirement=C1", "?anchor=CRY-03", "?status=retired"} {
		if got := list(t, f.tenantA, q); len(got) != 0 {
			t.Errorf("list%s = %d edges; want 0", q, len(got))
		}
	}
	if status, _ := call(t, f.tenantA, http.MethodGet, "/v1/mapping-overlays?status=gone", f.tenantA.admin, nil); status != http.StatusBadRequest {
		t.Errorf("list bad status: %d; want 400", status)
	}

	// RLS: tenant B sees nothing and cannot retire A's edge.
	if got := list(t, f.tenantB, ""); len(got) != 0 {
		t.Fatalf("tenant B sees A's overlay edges: %v", got)
	}
	status, body = call(t, f.tenantB, http.MethodPost, "/v1/mapping-overlays/"+created["id"].(string)+"/retire",
		f.tenantB.admin, map[string]any{"reason": "not ours"})
	if status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("tenant B retire A's edge: status %d (%v); want 404 not_found", status, body)
	}
	if got := list(t, f.tenantA, "?status=active"); len(got) != 1 {
		t.Fatalf("A's edge after B's retire attempt: %v", got)
	}

	// The same pair is independent per tenant: B may add its own.
	if status, body := call(t, f.tenantB, http.MethodPost, "/v1/mapping-overlays", f.tenantB.admin, f.body("C2", "IAC-06")); status != http.StatusCreated {
		t.Fatalf("tenant B create same pair: status %d (%v)", status, body)
	}
	if got := list(t, f.tenantA, ""); len(got) != 1 {
		t.Fatalf("tenant A sees B's edge: %v", got)
	}

	// A second active edge on the same pair is a conflict.
	status, body = call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	if status != http.StatusConflict || body["code"] != "overlay_edge_exists" {
		t.Fatalf("duplicate overlay: status %d (%v); want 409 overlay_edge_exists", status, body)
	}
}

func TestOverlay_CreateRefusals(t *testing.T) {
	f := newFixture(t)
	post := func(b map[string]any) (int, map[string]any) {
		return call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, b)
	}
	with := func(k string, v any) map[string]any {
		b := f.body("C2", "IAC-06")
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
		return b
	}

	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
		msg    string
	}{
		{"unknown requirement", with("requirement_code", "C9"), 422, "unknown_requirement", `"C9"`},
		{"unknown framework version", with("framework_version", "1999"), 422, "unknown_framework_version", "1999"},
		{"unknown framework", with("framework", "no-such-framework"), 422, "unknown_framework_version", "no-such-framework"},
		// AST-09 is in release A only: unknown in the declared release B.
		{"anchor not in declared release", with("anchor_code", "AST-09"), 422, "unknown_anchor", scfreleasetest.ReleaseB},
		{"unknown SCF release", with("scf_release", "1999.1"), 422, "unknown_scf_release", "1999.1"},
		{"missing rationale", with("rationale", nil), 400, "invalid_overlay_edge", "rationale is required"},
		{"blank rationale", with("rationale", "   "), 400, "invalid_overlay_edge", "rationale is required"},
		{"missing source", with("source_ref", nil), 400, "invalid_overlay_edge", "source_ref is required"},
		{"bad relationship", with("relationship_type", "related"), 400, "invalid_overlay_edge", "relationship_type"},
		{"strength out of range", with("strength", 1.5), 400, "invalid_overlay_edge", "strength"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := post(tc.body)
			msg, _ := body["error"].(string)
			if status != tc.status || body["code"] != tc.code || !strings.Contains(msg, tc.msg) {
				t.Fatalf("status %d code %v error %q; want %d %s containing %q", status, body["code"], msg, tc.status, tc.code, tc.msg)
			}
		})
	}

	// The anchor is resolved in the declared release: AST-09, refused in
	// release B above, is accepted in release A and binds A's anchor.
	b := with("anchor_code", "AST-09")
	b["scf_release"] = scfreleasetest.ReleaseA
	status, body := post(b)
	if status != http.StatusCreated || edgeOf(t, body)["scf_release"] != scfreleasetest.ReleaseA {
		t.Fatalf("AST-09 in release A: status %d (%v)", status, body)
	}

	if status, body := post(with("strength", nil)); status != http.StatusBadRequest {
		t.Errorf("missing strength: status %d (%v)", status, body)
	}
	if status, body := post(with("tenant_id", f.tenantB.tenant)); status != http.StatusBadRequest {
		t.Errorf("body naming a tenant: status %d (%v); want 400 (unknown field)", status, body)
	}

	// A pair an active catalog edge already joins is refused, naming it.
	status, body = post(f.body("C1", "IAC-06"))
	if status != http.StatusConflict || body["code"] != "catalog_edge_exists" {
		t.Fatalf("catalog duplicate: status %d (%v); want 409 catalog_edge_exists", status, body)
	}
	if ce, _ := body["catalog_edge"].(map[string]any); ce["id"] != f.catalogC1.String() || ce["mapping_tier"] != "verified" {
		t.Fatalf("catalog duplicate names %v; want edge %s", body["catalog_edge"], f.catalogC1)
	}
	// A rejected catalog edge is not active: the org may assert the pair.
	if status, body := post(f.body("C3", "CRY-03")); status != http.StatusCreated {
		t.Fatalf("pair with a rejected catalog edge: status %d (%v)", status, body)
	}

	// A viewer cannot write.
	status, body = call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.viewer, f.body("C2", "AST-04"))
	if status != http.StatusForbidden {
		t.Fatalf("viewer create: status %d (%v); want 403", status, body)
	}
	if got := list(t, f.tenantA, "?anchor=AST-04"); len(got) != 0 {
		t.Fatalf("refused create wrote a row: %v", got)
	}
}

func TestOverlay_FrozenVersionAcceptsEdgeWithoutChangingRequirements(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	snapshot := func() string {
		var s string
		if err := f.admin.QueryRow(ctx, `
			SELECT coalesce(string_agg(r::text, '|' ORDER BY r.code), '')
			FROM framework_requirements r
			JOIN framework_versions fv ON fv.id = r.framework_version_id
			JOIN frameworks fw ON fw.id = fv.framework_id
			WHERE fw.slug = $1`, f.framework).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	var status string
	if err := f.admin.QueryRow(ctx, `
		SELECT fv.status::text FROM framework_versions fv JOIN frameworks fw ON fw.id = fv.framework_id
		WHERE fw.slug = $1 AND fv.version = $2`, f.framework, f.version).Scan(&status); err != nil || status == "draft" {
		t.Fatalf("fixture version status = %q (%v); want a frozen (non-draft) version", status, err)
	}

	before := snapshot()
	// Named by version label and, separately, with the version omitted
	// (the framework's current version).
	if code, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06")); code != http.StatusCreated {
		t.Fatalf("create on frozen version: status %d (%v)", code, body)
	}
	b := f.body("C3", "IAC-06")
	delete(b, "framework_version")
	delete(b, "scf_release")
	code, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, b)
	if code != http.StatusCreated || edgeOf(t, body)["framework_version"] != f.version ||
		edgeOf(t, body)["scf_release"] != scfreleasetest.ReleaseB {
		t.Fatalf("create with current version and release: status %d (%v)", code, body)
	}
	if after := snapshot(); after != before {
		t.Fatalf("framework requirements changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestOverlay_RetireRecordsReasonAndActor(t *testing.T) {
	f := newFixture(t)
	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	id := edgeOf(t, body)["id"].(string)
	creator := edgeOf(t, body)["created_by"]
	path := "/v1/mapping-overlays/" + id + "/retire"

	if status, _ := call(t, f.tenantA, http.MethodPost, path, f.tenantA.viewer, map[string]any{"reason": "x"}); status != http.StatusForbidden {
		t.Fatalf("viewer retire: status %d; want 403", status)
	}
	if status, body := call(t, f.tenantA, http.MethodPost, path, f.tenantA.admin, map[string]any{"reason": " "}); status != http.StatusBadRequest {
		t.Fatalf("retire without reason: status %d (%v); want 400", status, body)
	}
	if status, _ := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/not-a-uuid/retire", f.tenantA.admin, map[string]any{"reason": "x"}); status != http.StatusBadRequest {
		t.Fatalf("retire bad id: status %d; want 400", status)
	}
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+uuid.NewString()+"/retire", f.tenantA.admin, map[string]any{"reason": "x"}); status != http.StatusNotFound {
		t.Fatalf("retire unknown id: status %d (%v); want 404", status, body)
	}

	status, body := call(t, f.tenantA, http.MethodPost, path, f.tenantA.admin, map[string]any{"reason": "auditor withdrew the finding"})
	if status != http.StatusOK {
		t.Fatalf("retire: status %d (%v)", status, body)
	}
	retired := edgeOf(t, body)
	if retired["status"] != "retired" || retired["retirement_reason"] != "auditor withdrew the finding" ||
		retired["retired_by"] != creator || retired["retired_by"] == "" || retired["retired_at"] == nil {
		t.Fatalf("retired = %v", retired)
	}

	if got := list(t, f.tenantA, "?status=retired"); len(got) != 1 || got[0]["id"] != id || got[0]["retirement_reason"] != "auditor withdrew the finding" {
		t.Fatalf("retired list = %v", got)
	}
	if got := list(t, f.tenantA, "?status=active"); len(got) != 0 {
		t.Fatalf("active list after retire = %v", got)
	}
	status, body = call(t, f.tenantA, http.MethodPost, path, f.tenantA.admin, map[string]any{"reason": "again"})
	if status != http.StatusConflict || body["code"] != "already_retired" {
		t.Fatalf("retire twice: status %d (%v); want 409 already_retired", status, body)
	}
	// Retiring frees the pair for a new active edge; the retired one stays.
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06")); status != http.StatusCreated {
		t.Fatalf("re-add after retire: status %d (%v)", status, body)
	}
	if got := list(t, f.tenantA, ""); len(got) != 2 {
		t.Fatalf("list after re-add = %d edges; want 2", len(got))
	}
}

// The engine, not a handler gate, refuses a viewer (#3016): the body is the
// authz middleware's own "forbidden", nothing is written, and each refused
// write leaves its deny row in decision_audit_log. viewer.rego admits no
// mapping-overlays read either; the read case pins that until #3021
// decides whether a viewer should read overlays.
func TestOverlay_ViewerRefusedByAuthz(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	id := edgeOf(t, body)["id"].(string)

	for _, rt := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/mapping-overlays", f.body("C2", "AST-04")},
		{"/v1/mapping-overlays/" + id + "/retire", map[string]any{"reason": "viewer"}},
	} {
		status, body := call(t, f.tenantA, http.MethodPost, rt.path, f.tenantA.viewer, rt.body)
		if status != http.StatusForbidden || body["error"] != "forbidden" {
			t.Errorf("viewer POST %s = %d %v; want 403 forbidden", rt.path, status, body)
		}
	}
	for _, path := range []string{"/v1/mapping-overlays", "/v1/mapping-overlays/audited-under"} {
		status, body := call(t, f.tenantA, http.MethodGet, path, f.tenantA.viewer, nil)
		if status != http.StatusForbidden || body["error"] != "forbidden" {
			t.Errorf("viewer GET %s = %d %v; want 403 forbidden (#3021)", path, status, body)
		}
	}
	if got := list(t, f.tenantA, "?anchor=AST-04"); len(got) != 0 {
		t.Fatalf("refused create wrote a row: %v", got)
	}
	if got := list(t, f.tenantA, "?status=active"); len(got) != 1 {
		t.Fatalf("active edges after refused retire = %d; want 1", len(got))
	}

	var denies int
	if err := f.admin.QueryRow(ctx, `
		SELECT count(*) FROM decision_audit_log
		WHERE tenant_id = $1 AND user_id = $2 AND result = 'deny' AND request_method = 'POST'
		  AND request_path LIKE '/v1/mapping-overlays%'`,
		f.tenantA.tenant, "user:"+f.tenantA.viewerID.String()).Scan(&denies); err != nil {
		t.Fatal(err)
	}
	if denies != 2 {
		t.Fatalf("decision_audit_log deny rows for the viewer = %d; want 2", denies)
	}
}

func TestOverlay_AuditLogRecordsCreateAndRetire(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	edge := edgeOf(t, body)
	id := edge["id"].(string)
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+id+"/retire", f.tenantA.admin,
		map[string]any{"reason": "superseded"}); status != http.StatusOK {
		t.Fatalf("retire: status %d (%v)", status, body)
	}
	// A refused write leaves no trail.
	call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C9", "IAC-06"))

	rows, err := f.admin.Query(ctx, `
		SELECT action, actor, before IS NULL, after->>'status', after->>'retirement_reason', after->>'requirement_code'
		FROM fw_to_scf_overlay_edge_audit_log
		WHERE tenant_id = $1 ORDER BY occurred_at, action`, f.tenantA.tenant)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		action, actor           string
		noBefore                bool
		status, reason, reqCode *string
	}
	var got []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.action, &e.actor, &e.noBefore, &e.status, &e.reason, &e.reqCode); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	rows.Close()
	if len(got) != 2 {
		t.Fatalf("audit rows = %d; want 2 (create, retire)", len(got))
	}
	if got[0].action != "create" || !got[0].noBefore || *got[0].status != "active" || *got[0].reqCode != "C2" || got[0].actor != edge["created_by"] {
		t.Errorf("create row = %+v", got[0])
	}
	if got[1].action != "retire" || got[1].noBefore || *got[1].status != "retired" || *got[1].reason != "superseded" || got[1].actor != edge["created_by"] {
		t.Errorf("retire row = %+v", got[1])
	}

	// The rows surface in the unified admin audit log as kind mapping_overlay,
	// and only for their own tenant.
	now := time.Now().UTC()
	q := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":   {now.Add(time.Hour).Format(time.RFC3339)},
		"kind": {"mapping_overlay"},
	}
	status, unified := call(t, f.tenantA, http.MethodGet, "/v1/admin/audit-log/unified?"+q.Encode(), f.tenantA.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("unified audit log: status %d (%v)", status, unified)
	}
	entries, _ := unified["entries"].([]any)
	actions := map[string]bool{}
	for _, item := range entries {
		e := item.(map[string]any)
		if e["kind"] != "mapping_overlay" || e["target_id"] != id || e["target_type"] != "overlay_edge" {
			t.Errorf("unified entry = %v", e)
		}
		actions[e["action"].(string)] = true
	}
	if !actions["create"] || !actions["retire"] || len(entries) != 2 {
		t.Fatalf("unified entries = %v", entries)
	}
	if _, unifiedB := call(t, f.tenantB, http.MethodGet, "/v1/admin/audit-log/unified?"+q.Encode(), f.tenantB.admin, nil); len(unifiedB["entries"].([]any)) != 0 {
		t.Fatalf("tenant B sees A's overlay audit rows: %v", unifiedB["entries"])
	}

	// The trail is append-only even for the migrate role.
	if _, err := f.admin.Exec(ctx, `DELETE FROM fw_to_scf_overlay_edge_audit_log WHERE tenant_id = $1`, f.tenantA.tenant); err == nil {
		t.Fatal("deleting an audit row succeeded")
	}
}

// The catalog edges table stays read-only for the app role, and an overlay
// edge's assertion cannot be rewritten in place.
func TestOverlay_CatalogEdgesStayReadOnlyAndEdgesAreImmutable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	app := dbtest.NewAppPool(t)
	var canInsert, canDelete, canUpdateRationale bool
	if err := app.QueryRow(ctx, `SELECT
		has_table_privilege('fw_to_scf_edges', 'INSERT'),
		has_table_privilege('fw_to_scf_edges', 'DELETE'),
		has_column_privilege('fw_to_scf_edges', 'rationale', 'UPDATE')`).Scan(&canInsert, &canDelete, &canUpdateRationale); err != nil {
		t.Fatal(err)
	}
	if canInsert || canDelete || canUpdateRationale {
		t.Fatalf("app role on fw_to_scf_edges: insert=%v delete=%v update(rationale)=%v; want all false",
			canInsert, canDelete, canUpdateRationale)
	}

	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	id := edgeOf(t, body)["id"].(string)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, f.tenantA.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE fw_to_scf_overlay_edges SET rationale = 'rewritten' WHERE id = $1`, id); err == nil {
		t.Fatal("rewriting an overlay edge's rationale succeeded")
	}
}
