//go:build integration

package scfimport_test

// #2321: across an SCF upgrade where CHG-02 keeps its code and only its
// title changes (as in SCF 2026.3), the stock soc2_cc8_1_change_management
// control re-binds and keeps its CC8.1 credit under the default pin (#2308).
// A tenant-authored control on CHG-02 keeps the #1971 D3 rule: flagged, and
// listed by GET /v1/controls/needs-reanchoring (#2285). A pinned control is
// left alone. The fixtures are testdata/chg-release-{a,b}.json.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const stockCC81Bundle = "soc2_cc8_1_change_management"

// cc81Crosswalk maps one requirement, CC8.1, to CHG-02. scfRelease "" binds
// the edge in the current release, as the bundled crosswalks do at boot.
func cc81Crosswalk(slug, scfRelease string) *soc2import.Crosswalk {
	return &soc2import.Crosswalk{
		SchemaVersion:     soc2import.SchemaVersion,
		FrameworkSlug:     slug,
		FrameworkName:     "2321 stock re-bind fw",
		FrameworkIssuer:   "test",
		FrameworkVersion:  "2017",
		SourceAttribution: "community_draft",
		SCFRelease:        scfRelease,
		Requirements:      []soc2import.Requirement{{Code: "CC8.1", Title: "Change management"}},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "CC8.1", SCFAnchor: "CHG-02", RelationshipType: "equal", Strength: 1.0, Rationale: "test"},
		},
	}
}

// seedManifestControl inserts an active control on anchor with the given
// stored control.yaml, as an upload would store it.
func seedManifestControl(t *testing.T, pool *pgxpool.Pool, tenant, bundle string, anchor uuid.UUID, manifest string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO controls
			(id, tenant_id, bundle_id, version, scf_id, scf_anchor_id,
			 title, description, control_family, implementation_type, owner_role,
			 lifecycle_state, applicability_expr, evidence_queries,
			 manual_evidence_schema, linked_policy_ids,
			 bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at,
			 bundle_uploaded_by)
		VALUES ($1, $2, $3, 1, 'CHG-02', $4,
			'Control ' || $3, '', 'CHG', 'automated', '',
			'active', '{"true": true}', '{}'::jsonb,
			'{}'::jsonb, '{}',
			$5, encode(sha256(convert_to($5, 'UTF8')), 'hex'), now(),
			'stock-2321-test')`,
		id, tenant, bundle, anchor, manifest); err != nil {
		t.Fatalf("insert control %s: %v", bundle, err)
	}
	return id
}

type proposal struct {
	id     uuid.UUID
	status string
}

func proposalOf(t *testing.T, pool *pgxpool.Pool, control uuid.UUID) (proposal, bool) {
	t.Helper()
	var p proposal
	err := pool.QueryRow(context.Background(),
		`SELECT id, status FROM scf_remap_proposals WHERE control_id = $1`, control).Scan(&p.id, &p.status)
	if err != nil {
		return proposal{}, false
	}
	return p, true
}

// needsReanchoring reads GET /v1/controls/needs-reanchoring as the tenant
// and returns the listed control ids.
func needsReanchoring(t *testing.T, app *pgxpool.Pool, tenant string) map[string]bool {
	t.Helper()
	h := apicontrols.NewReanchoringHandler(control.NewStore(app))
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/needs-reanchoring", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "key_test_2321", TenantID: tenant})
	ctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rr := httptest.NewRecorder()
	h.List(rr, req.WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("needs-reanchoring: status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Controls []struct {
			ID string `json:"id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]bool{}
	for _, c := range body.Controls {
		out[c.ID] = true
	}
	return out
}

func TestRebind_StockControlRebindsOnTitleOnlyChangeAndKeepsCC81Coverage(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, pool)
	ctx := context.Background()

	stockYAML, err := os.ReadFile(filepath.Join("..", "..", "..", "controls", "soc2", stockCC81Bundle, "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	a, err := scfimport.Load("testdata/chg-release-a.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := scfimport.Load("testdata/chg-release-b.json")
	if err != nil {
		t.Fatal(err)
	}
	importRelease(t, pool, a)
	slug := "soc2-2321-" + uuid.NewString()[:8]
	if _, err := soc2import.Import(ctx, pool, cc81Crosswalk(slug, a.ReleaseVersion)); err != nil {
		t.Fatalf("import crosswalk in A: %v", err)
	}

	tenant := dbtest.SeedTenant(t, pool, "controls")
	legacyCHG02 := anchorID(t, pool, a.ReleaseVersion, "CHG-02")
	stock := seedManifestControl(t, pool, tenant, stockCC81Bundle, legacyCHG02, string(stockYAML))
	authored := seedManifestControl(t, pool, tenant, "tenant_change_2321", legacyCHG02,
		"bundle_id: tenant_change_2321\nscf_anchor_id: CHG-02\n")
	pinned := seedManifestControl(t, pool, tenant, "tenant_pinned_2321", legacyCHG02,
		"bundle_id: tenant_pinned_2321\nscf_anchor_id: CHG-02\nscf_release: "+a.ReleaseVersion+"\n")

	importRelease(t, pool, b)
	currentCHG02 := anchorID(t, pool, b.ReleaseVersion, "CHG-02")

	// An import run before #2321 flagged the stock control and queued an
	// open proposal; this run must turn it rebound, not leave it open.
	var target uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT framework_version_id FROM scf_anchors WHERE id = $1`, currentCHG02).Scan(&target); err != nil {
		t.Fatal(err)
	}
	var priorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO scf_remap_proposals (tenant_id, control_id, bundle_id, legacy_anchor_id,
			legacy_scf_id, legacy_release, target_framework_version_id, target_release,
			class, status, successor_scf_ids)
		VALUES ($1, $2, $3, $4, 'CHG-02', $5, $6, $7, 'no_successor', 'open', '{}')
		RETURNING id`,
		tenant, stock, stockCC81Bundle, legacyCHG02, a.ReleaseVersion, target, b.ReleaseVersion).Scan(&priorID); err != nil {
		t.Fatalf("seed the pre-#2321 open proposal: %v", err)
	}

	report, err := scfimport.RebindControls(ctx, pool)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	in := func(list []scfimport.ControlOnLegacyAnchor, id uuid.UUID) bool {
		for _, c := range list {
			if c.ControlID == id {
				return true
			}
		}
		return false
	}
	if !in(report.Rebound, stock) {
		t.Errorf("stock control not re-bound; report = %+v", report)
	}
	if !in(report.NeedsReanchoring, authored) {
		t.Errorf("tenant-authored control not flagged; report = %+v", report)
	}
	if !in(report.Pinned, pinned) {
		t.Errorf("pinned control not reported pinned; report = %+v", report)
	}

	for name, c := range map[string]struct {
		id   uuid.UUID
		want uuid.UUID
	}{
		"stock":           {stock, currentCHG02},
		"tenant-authored": {authored, legacyCHG02},
		"pinned":          {pinned, legacyCHG02},
	} {
		if got := controlAnchor(t, pool, c.id); got != c.want {
			t.Errorf("%s control: anchor = %s; want %s", name, got, c.want)
		}
	}

	// The open proposal turned rebound in place; the authored control's is
	// open; the pinned control gets none.
	if p, ok := proposalOf(t, pool, stock); !ok || p.id != priorID || p.status != scfimport.ProposalRebound {
		t.Errorf("stock proposal = %+v (found %v); want %s rebound", p, ok, priorID)
	}
	if p, ok := proposalOf(t, pool, authored); !ok || p.status != scfimport.ProposalOpen {
		t.Errorf("authored proposal = %+v (found %v); want open", p, ok)
	}
	if p, ok := proposalOf(t, pool, pinned); ok {
		t.Errorf("pinned control has a proposal: %+v", p)
	}

	// A re-run changes nothing: rebound stays rebound, open stays open.
	if _, err := scfimport.RebindControls(ctx, pool); err != nil {
		t.Fatalf("repeat RebindControls: %v", err)
	}
	if p, _ := proposalOf(t, pool, stock); p.status != scfimport.ProposalRebound {
		t.Errorf("repeat run: stock proposal status = %s; want rebound", p.status)
	}
	if p, _ := proposalOf(t, pool, authored); p.status != scfimport.ProposalOpen {
		t.Errorf("repeat run: authored proposal status = %s; want open", p.status)
	}

	flagged := needsReanchoring(t, app, tenant)
	if !flagged[authored.String()] || flagged[stock.String()] || flagged[pinned.String()] {
		t.Errorf("needs-reanchoring = %v; want only the tenant-authored control %s", flagged, authored)
	}

	// Boot re-imports the bundled crosswalk in the current release. Under
	// the default pin (#2308: the current release) CC8.1's anchors are
	// release B's, and the re-bound stock control sits on one of them.
	if _, err := soc2import.Import(ctx, pool, cc81Crosswalk(slug, "")); err != nil {
		t.Fatalf("re-import crosswalk: %v", err)
	}
	q := dbx.New(pool)
	rel, err := scfrelease.Lookup(ctx, q, "")
	if err != nil || rel.Version != b.ReleaseVersion {
		t.Fatalf("default release = %+v, %v; want %s", rel, err, b.ReleaseVersion)
	}
	var reqID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT r.id FROM framework_requirements r
		JOIN framework_versions fv ON fv.id = r.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND r.code = 'CC8.1'`, slug).Scan(&reqID); err != nil {
		t.Fatalf("requirement CC8.1: %v", err)
	}
	anchors, err := q.ListAnchorsForRequirementWithEdgesByFrameworkVersion(ctx, dbx.ListAnchorsForRequirementWithEdgesByFrameworkVersionParams{
		FrameworkRequirementID: pgtype.UUID{Bytes: reqID, Valid: true},
		FrameworkVersionID:     rel.ID,
	})
	if err != nil {
		t.Fatalf("CC8.1 anchors in the current release: %v", err)
	}
	covers := func(control uuid.UUID) bool {
		bound := controlAnchor(t, pool, control)
		for _, r := range anchors {
			if uuid.UUID(r.ScfAnchorID.Bytes) == bound {
				return true
			}
		}
		return false
	}
	if !covers(stock) {
		t.Errorf("stock control earns no CC8.1 credit under the default pin; anchors = %+v", anchors)
	}
	if covers(authored) {
		t.Errorf("tenant-authored control earns CC8.1 credit while flagged")
	}
}
