//go:build integration

package scfimport_test

// #1804: importing a new SCF release queues one remap proposal per active
// tenant overlay edge left on an older release's anchor, in that edge's
// tenant, and a writer of the tenant decides it. The SCF fixtures and the
// release-A crosswalk are #1777's (edgeremap_integration_test.go): TRM-01
// unchanged, TRM-02 renamed (-> TRM-20), TRM-03 split (-> TRM-30, TRM-31),
// TRM-04 reused_id (-> TRM-41) and TRM-05 withdrawn. The overlay edges sit
// on pairs the catalog crosswalk does not join, so each is a real overlay.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

// overlayPair is one overlay edge a tenant holds in release A.
type overlayPair struct {
	requirement, anchor, relationship string
	strength                          float64
}

// tenantAOverlays gives tenant A one active overlay edge on each fixture
// anchor, keyed by that anchor, each with its own content.
var tenantAOverlays = map[string]overlayPair{
	"TRM-01": {"R-02", "TRM-01", "equal", 0.9},
	"TRM-02": {"R-01", "TRM-02", "intersects_with", 0.6},
	"TRM-03": {"R-01", "TRM-03", "subset_of", 0.5},
	"TRM-04": {"R-01", "TRM-04", "superset_of", 0.4},
	"TRM-05": {"R-01", "TRM-05", "intersects_with", 0.3},
}

func newOverlay(p overlayPair) mappingoverlay.NewEdge {
	return mappingoverlay.NewEdge{
		Framework: edgeFwSlug, FrameworkVersion: "2026", RequirementCode: p.requirement,
		SCFRelease: edgeReleaseA, AnchorCode: p.anchor, RelationshipType: p.relationship,
		Strength: p.strength, Rationale: "auditor tested " + p.anchor + " under " + p.requirement,
		SourceRef: "FY25 report " + p.anchor,
	}
}

// overlayFixture is the tenants and overlay edges setupOverlayRemap made.
type overlayFixture struct {
	admin, app *pgxpool.Pool
	a, b       uuid.UUID
	// aEdges are tenant A's active release-A edges by anchor code.
	aEdges  map[string]mappingoverlay.Edge
	aRetire mappingoverlay.Edge // retired before the import
	bEdge   mappingoverlay.Edge // tenant B's edge on TRM-02
}

// setupOverlayRemap gives tenant A an overlay edge on each fixture anchor
// plus a retired one, tenant B one edge on TRM-02, then imports release B
// and queues every remap.
func setupOverlayRemap(t *testing.T) overlayFixture {
	t.Helper()
	var f overlayFixture
	f.admin, f.app = setupEdgeRemap(t, func(admin *pgxpool.Pool) {
		cleanup := []string{"scf_overlay_remap_decisions", "scf_overlay_remap_proposals", "fw_to_scf_overlay_edges"}
		f.a = uuid.MustParse(dbtest.SeedTenant(t, admin, cleanup...))
		f.b = uuid.MustParse(dbtest.SeedTenant(t, admin, cleanup...))
		store := mappingoverlay.NewStore(dbtest.NewAppPool(t))
		f.aEdges = map[string]mappingoverlay.Edge{}
		for code, p := range tenantAOverlays {
			e, err := store.Create(withTenant(t, f.a.String()), newOverlay(p), "seed-a")
			if err != nil {
				t.Fatalf("tenant A overlay on %s: %v", code, err)
			}
			f.aEdges[code] = e
		}
		retired, err := store.Create(withTenant(t, f.a.String()), newOverlay(overlayPair{"R-03", "TRM-01", "equal", 1}), "seed-a")
		if err != nil {
			t.Fatalf("tenant A overlay to retire: %v", err)
		}
		if f.aRetire, err = store.Retire(withTenant(t, f.a.String()), retired.ID, "no longer asserted", "seed-a"); err != nil {
			t.Fatalf("retire: %v", err)
		}
		if f.bEdge, err = store.Create(withTenant(t, f.b.String()), newOverlay(overlayPair{"R-01", "TRM-02", "equal", 1}), "seed-b"); err != nil {
			t.Fatalf("tenant B overlay: %v", err)
		}
	})
	if _, err := scfimport.QueueOverlayRemapProposals(context.Background(), f.admin); err != nil {
		t.Fatalf("QueueOverlayRemapProposals: %v", err)
	}
	return f
}

type overlayProposal struct {
	id, edgeID, class, status string
	successors                []string
}

// overlayProposals reads a tenant's queue as the migrate role, keyed by
// legacy anchor code.
func overlayProposals(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID) map[string]overlayProposal {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT p.legacy_scf_id, p.id::text, p.overlay_edge_id::text, p.class, p.status, p.successor_scf_ids
		FROM scf_overlay_remap_proposals p
		JOIN fw_to_scf_overlay_edges o ON o.id = p.overlay_edge_id
		WHERE p.tenant_id = $1 AND o.tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read overlay queue: %v", err)
	}
	defer rows.Close()
	out := map[string]overlayProposal{}
	for rows.Next() {
		var code string
		var p overlayProposal
		if err := rows.Scan(&code, &p.id, &p.edgeID, &p.class, &p.status, &p.successors); err != nil {
			t.Fatalf("scan overlay proposal: %v", err)
		}
		if _, dup := out[code]; dup {
			t.Fatalf("tenant %s has two proposals on %s", tenant, code)
		}
		out[code] = p
	}
	return out
}

// overlayRow is one fw_to_scf_overlay_edges row.
type overlayRow struct {
	ID, Tenant, Requirement, Anchor uuid.UUID
	Relationship                    string
	Strength                        float64
	Rationale, SourceRef, Status    string
	RetirementReason, RetiredBy     *string
}

func readOverlay(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) overlayRow {
	t.Helper()
	var o overlayRow
	if err := pool.QueryRow(context.Background(), `
		SELECT id, tenant_id, framework_requirement_id, scf_anchor_id, relationship_type::text,
		       strength, rationale, source_ref, status, retirement_reason, retired_by
		FROM fw_to_scf_overlay_edges WHERE id = $1`, id).Scan(
		&o.ID, &o.Tenant, &o.Requirement, &o.Anchor, &o.Relationship, &o.Strength,
		&o.Rationale, &o.SourceRef, &o.Status, &o.RetirementReason, &o.RetiredBy); err != nil {
		t.Fatalf("read overlay %s: %v", id, err)
	}
	return o
}

func overlayCount(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fw_to_scf_overlay_edges WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count overlays: %v", err)
	}
	return n
}

func TestOverlayRemapQueue_ImportingReleaseBQueuesEachTenantsActiveEdgesWithTheirClass(t *testing.T) {
	f := setupOverlayRemap(t)

	got := overlayProposals(t, f.admin, f.a)
	want := map[string]overlayProposal{
		"TRM-01": {class: "unchanged", successors: []string{"TRM-01"}},
		"TRM-02": {class: "renamed", successors: []string{"TRM-20"}},
		"TRM-03": {class: "split", successors: []string{"TRM-30", "TRM-31"}},
		"TRM-04": {class: "reused_id", successors: []string{"TRM-41"}},
		"TRM-05": {class: "no_successor", successors: []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("tenant A has %d proposals; want %d (the retired edge is not queued): %+v", len(got), len(want), got)
	}
	for code, w := range want {
		g := got[code]
		if g.class != w.class || g.status != "open" || !slices.Equal(g.successors, w.successors) {
			t.Errorf("%s: class=%s status=%s successors=%v; want %s open %v", code, g.class, g.status, g.successors, w.class, w.successors)
		}
		if g.edgeID != f.aEdges[code].ID.String() {
			t.Errorf("%s: proposal names edge %s; want tenant A's %s", code, g.edgeID, f.aEdges[code].ID)
		}
	}
	// Tenant B's own edge is queued in tenant B, not A.
	if b := overlayProposals(t, f.admin, f.b); len(b) != 1 || b["TRM-02"].edgeID != f.bEdge.ID.String() || b["TRM-02"].class != "renamed" {
		t.Fatalf("tenant B queue = %+v; want its one renamed TRM-02 edge", b)
	}

	// Queueing moves nothing.
	for code, e := range f.aEdges {
		if o := readOverlay(t, f.admin, e.ID); o.Status != "active" || o.Anchor != e.SCFAnchorID {
			t.Errorf("%s: overlay changed by queueing: %+v", code, o)
		}
	}
	// A re-run of the import queues nothing new.
	counts, err := scfimport.QueueOverlayRemapProposals(context.Background(), f.admin)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	for class, n := range counts {
		if n != 0 {
			t.Errorf("re-run queued %d %s proposals; want 0", n, class)
		}
	}
	if n := len(overlayProposals(t, f.admin, f.a)); n != 5 {
		t.Fatalf("after the re-run tenant A has %d proposals; want 5", n)
	}
}

func TestOverlayRemapDecide_ApproveCreatesTheEdgeOnTheSuccessorAndRetiresTheLegacyOne(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := withTenant(t, f.a.String())
	store := mappingoverlay.NewStore(f.app)
	prop := overlayProposals(t, f.admin, f.a)["TRM-02"]
	legacy := readOverlay(t, f.admin, f.aEdges["TRM-02"].ID)
	bBefore := readOverlay(t, f.admin, f.bEdge.ID)
	bCount := overlayCount(t, f.admin, f.b)
	decider := uuid.NewString()

	d, err := store.DecideRemap(ctx, mappingoverlay.RemapDecideInput{
		ProposalID: prop.id, Decision: mappingoverlay.RemapApproved, Reason: "cosmetic rename",
		Actor: "jwt:writer", DecidedBy: decider,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	newAnchor := anchorID(t, f.admin, edgeReleaseB, "TRM-20")
	created := readOverlay(t, f.admin, uuid.MustParse(d.NewOverlayEdgeID))
	if created.Tenant != f.a || created.Requirement != legacy.Requirement || created.Anchor != newAnchor || created.Status != "active" {
		t.Fatalf("new overlay = %+v; want tenant A's %s -> TRM-20@B, active", created, legacy.Requirement)
	}
	if created.Relationship != legacy.Relationship || created.Strength != legacy.Strength ||
		created.Rationale != legacy.Rationale || created.SourceRef != legacy.SourceRef {
		t.Fatalf("new overlay content %+v differs from legacy %+v", created, legacy)
	}
	retired := readOverlay(t, f.admin, legacy.ID)
	if retired.Status != "retired" || retired.RetirementReason == nil || *retired.RetirementReason != "remapped" ||
		retired.RetiredBy == nil || *retired.RetiredBy != "jwt:writer" || retired.Anchor != legacy.Anchor {
		t.Fatalf("legacy overlay = %+v; want retired as remapped by jwt:writer on its old anchor", retired)
	}

	// Tenant B's edge on the same legacy anchor is untouched, and B gained
	// nothing.
	if after := readOverlay(t, f.admin, f.bEdge.ID); after.Status != bBefore.Status || after.Anchor != bBefore.Anchor {
		t.Fatalf("tenant B's overlay changed: %+v -> %+v", bBefore, after)
	}
	if n := overlayCount(t, f.admin, f.b); n != bCount {
		t.Fatalf("tenant B has %d overlays; want %d", n, bCount)
	}
	if b := overlayProposals(t, f.admin, f.b)["TRM-02"]; b.status != "open" {
		t.Fatalf("tenant B's proposal is %s; want open", b.status)
	}

	// The proposal, the history and the audit trail record the outcome.
	var status string
	var target, newEdge uuid.UUID
	if err := f.admin.QueryRow(context.Background(), `
		SELECT status, target_anchor_id, new_overlay_edge_id FROM scf_overlay_remap_proposals WHERE id = $1`,
		prop.id).Scan(&status, &target, &newEdge); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if status != "approved" || target != newAnchor || newEdge != created.ID {
		t.Fatalf("proposal = %s %s %s; want approved %s %s", status, target, newEdge, newAnchor, created.ID)
	}
	if d.DecidedBy != decider || d.NewSCFID != "TRM-20" || d.NewRelease != edgeReleaseB || d.LegacySCFID != "TRM-02" ||
		d.LegacyRelease != edgeReleaseA || d.LegacyOverlayEdgeID != legacy.ID.String() || d.Reason != "cosmetic rename" || d.DecidedAt == "" {
		t.Fatalf("decision = %+v", d)
	}
	var history int
	if err := f.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM scf_overlay_remap_decisions
		WHERE tenant_id = $1 AND proposal_id = $2 AND decision = 'approved' AND new_overlay_edge_id = $3 AND decided_by = $4`,
		f.a, prop.id, created.ID, decider).Scan(&history); err != nil || history != 1 {
		t.Fatalf("decision history rows = %d (err %v); want 1", history, err)
	}
	var audit []string
	rows, err := f.admin.Query(context.Background(), `
		SELECT action || ':' || overlay_edge_id::text || ':' || actor FROM fw_to_scf_overlay_edge_audit_log
		WHERE tenant_id = $1 AND overlay_edge_id IN ($2, $3) AND actor = 'jwt:writer' ORDER BY action`, f.a, legacy.ID, created.ID)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		audit = append(audit, s)
	}
	rows.Close()
	if want := []string{"create:" + created.ID.String() + ":jwt:writer", "retire:" + legacy.ID.String() + ":jwt:writer"}; !slices.Equal(audit, want) {
		t.Fatalf("audit = %v; want %v", audit, want)
	}

	// Approved is final.
	if _, err := store.DecideRemap(ctx, mappingoverlay.RemapDecideInput{ProposalID: prop.id, Decision: mappingoverlay.RemapKeptLegacy, Actor: "a", DecidedBy: decider}); !errors.Is(err, mappingoverlay.ErrRemapDecided) {
		t.Fatalf("keep legacy after approve: err=%v; want ErrRemapDecided", err)
	}
}

func TestOverlayRemapDecide_SplitNeedsAnAnchorAndStaleOrDuplicateTargetsAreRefused(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := withTenant(t, f.a.String())
	store := mappingoverlay.NewStore(f.app)
	props := overlayProposals(t, f.admin, f.a)
	approve := func(code, scf string) (mappingoverlay.RemapDecision, error) {
		return store.DecideRemap(ctx, mappingoverlay.RemapDecideInput{
			ProposalID: props[code].id, Decision: mappingoverlay.RemapApproved, SCFID: scf, Actor: "jwt:w", DecidedBy: "u",
		})
	}
	if _, err := approve("TRM-03", ""); !errors.Is(err, mappingoverlay.ErrRemapAnchorRequired) {
		t.Fatalf("split with no scf_id: err=%v; want ErrRemapAnchorRequired", err)
	}
	if _, err := approve("TRM-05", ""); !errors.Is(err, mappingoverlay.ErrRemapAnchorRequired) {
		t.Fatalf("no_successor with no scf_id: err=%v; want ErrRemapAnchorRequired", err)
	}
	// TRM-02 exists only in A.
	if _, err := approve("TRM-03", "TRM-02"); !errors.Is(err, mappingoverlay.ErrRemapAnchorNotInRelease) {
		t.Fatalf("code outside the target release: err=%v; want ErrRemapAnchorNotInRelease", err)
	}
	d, err := approve("TRM-03", "TRM-31")
	if err != nil {
		t.Fatalf("split with TRM-31: %v", err)
	}
	if d.NewAnchorID != anchorID(t, f.admin, edgeReleaseB, "TRM-31").String() {
		t.Fatalf("split approved onto %s; want TRM-31", d.NewAnchorID)
	}
	// R-01 now holds an active overlay on TRM-31: approving the withdrawn
	// TRM-05 edge (also R-01) onto it would duplicate it.
	if _, err := approve("TRM-05", "TRM-31"); !errors.Is(err, mappingoverlay.ErrDuplicateOverlay) {
		t.Fatalf("approve onto the tenant's own active pair: err=%v; want ErrDuplicateOverlay", err)
	}
	// A pair an active catalog edge joins gets no overlay, as for Create.
	if _, err := f.admin.Exec(context.Background(), `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES (gen_random_uuid(), $1, $2, 'equal', 1, 'community_draft', 'catalog already carries it')`,
		requirementID(t, f.admin, "R-01"), anchorID(t, f.admin, edgeReleaseB, "TRM-41")); err != nil {
		t.Fatalf("seed catalog edge: %v", err)
	}
	var catalog *mappingoverlay.CatalogEdgeExistsError
	if _, err := approve("TRM-04", ""); !errors.As(err, &catalog) {
		t.Fatalf("approve onto a catalog pair: err=%v; want CatalogEdgeExistsError", err)
	}
	// A legacy edge retired since it was queued makes its proposal stale.
	if _, err := mappingoverlay.NewStore(f.app).Retire(ctx, f.aEdges["TRM-01"].ID, "withdrawn by hand", "jwt:w"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := approve("TRM-01", ""); !errors.Is(err, mappingoverlay.ErrRemapEdgeChanged) {
		t.Fatalf("approve a retired edge's proposal: err=%v; want ErrRemapEdgeChanged", err)
	}
	// Every refusal left its proposal open.
	for _, code := range []string{"TRM-01", "TRM-04", "TRM-05"} {
		if p := overlayProposals(t, f.admin, f.a)[code]; p.status != "open" {
			t.Errorf("%s: status %s after a refused approval; want open", code, p.status)
		}
	}
}

func TestOverlayRemapDecide_RejectIsRevisitableKeepLegacyIsFinal(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := withTenant(t, f.a.String())
	store := mappingoverlay.NewStore(f.app)
	id := overlayProposals(t, f.admin, f.a)["TRM-02"].id
	decide := func(decision string) error {
		_, err := store.DecideRemap(ctx, mappingoverlay.RemapDecideInput{ProposalID: id, Decision: decision, Reason: "not yet", Actor: "jwt:w", DecidedBy: "u"})
		return err
	}
	if err := decide(mappingoverlay.RemapRejected); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := decide(mappingoverlay.RemapRejected); !errors.Is(err, mappingoverlay.ErrRemapDecided) {
		t.Fatalf("second reject: err=%v; want ErrRemapDecided", err)
	}
	if err := decide(mappingoverlay.RemapKeptLegacy); err != nil {
		t.Fatalf("keep legacy after reject: %v", err)
	}
	if err := decide(mappingoverlay.RemapApproved); !errors.Is(err, mappingoverlay.ErrRemapDecided) {
		t.Fatalf("approve after keep legacy: err=%v; want ErrRemapDecided", err)
	}
	if o := readOverlay(t, f.admin, f.aEdges["TRM-02"].ID); o.Status != "active" {
		t.Fatalf("legacy overlay is %s; want active", o.Status)
	}
	var decisions []string
	rows, err := f.admin.Query(context.Background(), `SELECT decision FROM scf_overlay_remap_decisions WHERE proposal_id = $1 ORDER BY decided_at, id`, id)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		decisions = append(decisions, s)
	}
	rows.Close()
	if !slices.Equal(decisions, []string{"rejected", "kept_legacy"}) {
		t.Fatalf("history = %v; want [rejected kept_legacy]", decisions)
	}
}

// RLS, not the handler, keeps a tenant to its own proposals: under tenant
// B's context A's rows are invisible and unwritable, and the app role cannot
// queue or delete at all.
func TestOverlayRemap_RLSKeepsEachTenantToItsOwnProposals(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := context.Background()
	aID := overlayProposals(t, f.admin, f.a)["TRM-02"].id

	tx, err := f.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, f.b.String()); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var seen int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM scf_overlay_remap_proposals WHERE tenant_id = $1`, f.a).Scan(&seen); err != nil || seen != 0 {
		t.Fatalf("tenant B sees %d of A's proposals (err %v); want 0", seen, err)
	}
	tag, err := tx.Exec(ctx, `UPDATE scf_overlay_remap_proposals SET status = 'kept_legacy' WHERE id = $1`, aID)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("tenant B's update of A's proposal: %v rows, err %v; want 0 rows", tag.RowsAffected(), err)
	}
	// Moving its own proposal into tenant A fails the WITH CHECK.
	bID := overlayProposals(t, f.admin, f.b)["TRM-02"].id
	if _, err := tx.Exec(ctx, `UPDATE scf_overlay_remap_proposals SET tenant_id = $1 WHERE id = $2`, f.a, bID); err == nil {
		t.Fatalf("tenant B moved its proposal into tenant A")
	}
	_ = tx.Rollback(ctx)

	for name, stmt := range map[string]string{
		"queue": `INSERT INTO scf_overlay_remap_proposals (tenant_id, overlay_edge_id, legacy_anchor_id, legacy_scf_id, legacy_release, target_framework_version_id, target_release, class)
			SELECT tenant_id, overlay_edge_id, legacy_anchor_id, legacy_scf_id, legacy_release, target_framework_version_id, target_release, class FROM scf_overlay_remap_proposals LIMIT 1`,
		"delete": `DELETE FROM scf_overlay_remap_proposals`,
	} {
		if _, err := f.app.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s as atlas_app: err=%v; want permission denied", name, err)
		}
	}
	if p := overlayProposals(t, f.admin, f.a)["TRM-02"]; p.status != "open" {
		t.Fatalf("A's proposal is %s; want open", p.status)
	}
}

// --- through the API server ---

// writerRoles stands in for user_roles: the live tenant roles of each
// writer subject, which authzmw stamps on the credential the overlay gate
// reads (#2447). A JWT's role claim alone no longer passes that gate.
type writerRoles struct {
	mu    sync.Mutex
	roles map[[2]string][]authz.Role
}

func (w *writerRoles) RolesFor(_ context.Context, tenant, user string) ([]authz.Role, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.roles[[2]string{tenant, user}], nil
}

// overlayServer is edgeServer with the authz middleware attached, resolving
// live roles from the returned writerRoles.
func overlayServer(t *testing.T, app *pgxpool.Pool) (*api.Server, *writerRoles) {
	t.Helper()
	roles := &writerRoles{roles: map[[2]string][]authz.Role{}}
	engine, err := authz.NewEngine(context.Background(), roles)
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := edgeServer(t, app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	return srv, roles
}

// writerBearer is a tenant admin who is a person (a users(id) subject) and
// not a super-admin: tenant writers decide overlay remaps. The admin role
// is granted live in roles as well as carried in the claim.
func writerBearer(t *testing.T, srv *api.Server, roles *writerRoles, tenant uuid.UUID) string {
	t.Helper()
	c := testjwt.AdminFor(tenant)
	c.SuperAdmin = false
	c.Subject = uuid.NewString()
	roles.mu.Lock()
	roles.roles[[2]string{tenant.String(), c.Subject}] = []authz.Role{authz.RoleAdmin}
	roles.mu.Unlock()
	return srv.IssueTestJWT(t, c)
}

type overlayProposalList struct {
	Proposals []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"proposals"`
}

func TestOverlayRemapAPI_TenantBCannotSeeOrDecideTenantAsProposals(t *testing.T) {
	f := setupOverlayRemap(t)
	srv, roles := overlayServer(t, f.app)
	aBearer := writerBearer(t, srv, roles, f.a)
	bBearer := writerBearer(t, srv, roles, f.b)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	aProps := overlayProposals(t, f.admin, f.a)
	base := "/v1/mapping-overlays/remap-proposals"
	list := func(bearer string) overlayProposalList {
		t.Helper()
		code, body := do(t, ts, http.MethodGet, base, bearer, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", base, code, body)
		}
		var l overlayProposalList
		if err := json.Unmarshal(body, &l); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return l
	}

	// B lists only its own proposal.
	bList := list(bBearer)
	if len(bList.Proposals) != 1 || bList.Proposals[0].ID != overlayProposals(t, f.admin, f.b)["TRM-02"].id {
		t.Fatalf("tenant B lists %+v; want only its own TRM-02 proposal", bList.Proposals)
	}
	// B cannot decide any of A's: each is a 404, and nothing changes.
	for code, p := range aProps {
		for _, verb := range []string{"approve", "reject", "keep-legacy"} {
			body := `{}`
			if verb == "approve" && (code == "TRM-03" || code == "TRM-05") {
				body = `{"scf_id":"TRM-30"}`
			}
			if status, resp := do(t, ts, http.MethodPost, base+"/"+p.id+"/"+verb, bBearer, body); status != http.StatusNotFound {
				t.Errorf("tenant B %s A's %s: %d %s; want 404", verb, code, status, resp)
			}
		}
	}
	for code, p := range overlayProposals(t, f.admin, f.a) {
		if p.status != "open" {
			t.Fatalf("after tenant B's calls A's %s is %s; want open", code, p.status)
		}
	}
	if n := overlayCount(t, f.admin, f.b); n != 1 {
		t.Fatalf("tenant B has %d overlays after its calls; want 1", n)
	}

	// A, a tenant writer and not a super-admin, lists and decides its own.
	if l := list(aBearer); len(l.Proposals) != 5 {
		t.Fatalf("tenant A lists %d proposals; want 5", len(l.Proposals))
	}
	if status, resp := do(t, ts, http.MethodPost, base+"/"+aProps["TRM-02"].id+"/approve", aBearer, `{"reason":"rename"}`); status != http.StatusOK {
		t.Fatalf("tenant A approve: %d %s", status, resp)
	}
	if status, resp := do(t, ts, http.MethodPost, base+"/"+aProps["TRM-03"].id+"/approve", aBearer, `{}`); status != http.StatusBadRequest {
		t.Fatalf("tenant A approve a split with no scf_id: %d %s; want 400", status, resp)
	}
	if status, resp := do(t, ts, http.MethodPost, base+"/"+aProps["TRM-02"].id+"/reject", aBearer, `{}`); status != http.StatusConflict {
		t.Fatalf("tenant A reject an approved proposal: %d %s; want 409", status, resp)
	}
	if status, resp := do(t, ts, http.MethodGet, base+"?status=bogus", aBearer, ""); status != http.StatusBadRequest {
		t.Fatalf("bad status filter: %d %s; want 400", status, resp)
	}
	approved := list(aBearer)
	n := 0
	for _, p := range approved.Proposals {
		if p.Status == "approved" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("tenant A has %d approved proposals; want 1", n)
	}
	// B's own proposal on the same legacy code is still open.
	if p := overlayProposals(t, f.admin, f.b)["TRM-02"]; p.status != "open" {
		t.Fatalf("tenant B's proposal is %s; want open", p.status)
	}
}

// For a same-meaning successor (an unchanged anchor), requirement coverage
// is the same before the release bump and after the overlay remap is
// approved. Between the two, the default read (pinned to the current
// release) has lost the path: the approval is what restores it.
func TestOverlayRemapCoverage_SameBeforeAndAfterForAnUnchangedSuccessor(t *testing.T) {
	var tenant uuid.UUID
	var ts *httptest.Server
	var bearer string
	var overlay mappingoverlay.Edge
	read := func() []string {
		t.Helper()
		code, body := do(t, ts, http.MethodGet, "/v1/requirements/"+edgeFwSlug+":2026:R-05/coverage", bearer, "")
		if code != http.StatusOK {
			t.Fatalf("coverage: %d %s", code, body)
		}
		var c coverage
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("decode coverage: %v", err)
		}
		var out []string
		for _, x := range c.Controls {
			out = append(out, x.BundleID)
		}
		slices.Sort(out)
		return out
	}

	var before []string
	admin, _ := setupEdgeRemap(t, func(admin *pgxpool.Pool) {
		// With release A current: an overlay R-05 -> TRM-01 and a control
		// on TRM-01, so R-05 is covered only through the overlay.
		tenant = uuid.MustParse(dbtest.SeedTenant(t, admin,
			"scf_overlay_remap_decisions", "scf_overlay_remap_proposals", "fw_to_scf_overlay_edges", "scf_remap_proposals"))
		app := dbtest.NewAppPool(t)
		b, err := control.FinalizeBundleForHTTP([]byte(`bundle_schema_version: "1"
bundle_id: overlay_1804_inventory
title: "Asset inventory"
scf_anchor_id: TRM-01
implementation_type: manual_attested
`))
		if err != nil {
			t.Fatalf("bundle: %v", err)
		}
		if _, err := control.NewStore(app).Upload(withTenant(t, tenant.String()), b, "seed-1804"); err != nil {
			t.Fatalf("upload: %v", err)
		}
		if overlay, err = mappingoverlay.NewStore(app).Create(withTenant(t, tenant.String()),
			newOverlay(overlayPair{"R-05", "TRM-01", "equal", 1}), "seed-1804"); err != nil {
			t.Fatalf("overlay: %v", err)
		}
		srv, roles := overlayServer(t, app)
		bearer = writerBearer(t, srv, roles, tenant)
		ts = httptest.NewServer(srv.HTTPHandlerForTests())
		t.Cleanup(ts.Close)
		before = read()
	})
	if !slices.Equal(before, []string{"overlay_1804_inventory"}) {
		t.Fatalf("before the upgrade R-05 is covered by %v; want overlay_1804_inventory", before)
	}
	if _, err := scfimport.QueueOverlayRemapProposals(context.Background(), admin); err != nil {
		t.Fatalf("queue: %v", err)
	}
	// The import rebinds the unchanged control onto TRM-01 in B; the overlay
	// is still on A's TRM-01, so the path is gone until the remap.
	if mid := read(); len(mid) != 0 {
		t.Fatalf("before the overlay remap R-05 is covered by %v; want nothing", mid)
	}
	prop := overlayProposals(t, admin, tenant)["TRM-01"]
	if prop.class != "unchanged" {
		t.Fatalf("overlay proposal class %s; want unchanged", prop.class)
	}
	if code, body := do(t, ts, http.MethodPost, "/v1/mapping-overlays/remap-proposals/"+prop.id+"/approve", bearer, `{}`); code != http.StatusOK {
		t.Fatalf("approve overlay remap: %d %s", code, body)
	}
	if after := read(); !slices.Equal(after, before) {
		t.Fatalf("coverage changed across the remap: before %v, after %v", before, after)
	}
	if o := readOverlay(t, admin, overlay.ID); o.Status != "retired" || *o.RetirementReason != "remapped" {
		t.Fatalf("legacy overlay = %+v; want retired as remapped", o)
	}
}
