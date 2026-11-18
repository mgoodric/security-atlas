//go:build integration

package ucfcoverage_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestRequirementCoverage_OverlayEdgesCarriedPerTenantAcrossReleaseImport
// extends #2315's A-to-B case (TestRequirementCoverage_DeclaredCrosswalk
// CarriedAcrossReleaseImport) to tenant overlay edges, #2554. The crosswalk
// declares release A and two tenants hold overlays on pairs it does not
// join:
//
//	tenant A: R-RENAMED -> IAC-06 (same title in B: carried)
//	          R-SAME    -> GOV-01 (retitled in B: stays a proposal)
//	tenant B: R-RENAMED -> IAC-06 (carried, in B's own context)
//
// After the import's steps, each tenant's exact match sits on B's IAC-06 in
// that tenant, recorded by the import in that tenant's history and audit
// trail, with the legacy edge still active. The retitled one is an open
// proposal. A re-run carries nothing, and no row of one tenant names the
// other's.
func TestRequirementCoverage_OverlayEdgesCarriedPerTenantAcrossReleaseImport(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	scfreleasetest.LoadAB(t, admin)
	slug := "rel2554-" + uuid.NewString()[:8]
	t.Cleanup(func() { removeReleaseFramework(t, admin, slug) })
	// Runs before removeReleaseFramework: the #2474 guard refuses to delete
	// a requirement or anchor an active overlay edge still names.
	t.Cleanup(func() { retireOverlaysOf(t, admin, slug) })

	if _, err := soc2import.Import(ctx, admin, releaseCrosswalk(slug, scfreleasetest.ReleaseA)); err != nil {
		t.Fatalf("import crosswalk declaring A: %v", err)
	}
	same := seedControl(t, tenantA, "IAC-06", "test-2554-same", "same-title control")
	if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2 WHERE id = $1`,
		same, anchorIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")); err != nil {
		t.Fatalf("bind control to A: %v", err)
	}
	store := mappingoverlay.NewStore(dbtest.NewAppPool(t))
	overlay := func(tenant, requirement, anchor string) mappingoverlay.Edge {
		t.Helper()
		tctx, err := tenancy.WithTenant(ctx, tenant)
		if err != nil {
			t.Fatalf("tenant context: %v", err)
		}
		e, err := store.Create(tctx, mappingoverlay.NewEdge{
			Framework: slug, FrameworkVersion: "2026", RequirementCode: requirement,
			SCFRelease: scfreleasetest.ReleaseA, AnchorCode: anchor, RelationshipType: "intersects_with",
			Strength: 0.7, Rationale: "auditor tested " + anchor, SourceRef: "FY25 " + tenant[:4],
		}, "seed-2554")
		if err != nil {
			t.Fatalf("overlay %s -> %s in %s: %v", requirement, anchor, tenant, err)
		}
		return e
	}
	aExact := overlay(tenantA, "R-RENAMED", "IAC-06")
	aRetitled := overlay(tenantA, "R-SAME", "GOV-01")
	bExact := overlay(tenantB, "R-RENAMED", "IAC-06")

	ts, bearer := setupHTTPServer(t, tenantA)
	linked := linkedReader(t, ts, bearer, slug, map[string]uuid.UUID{"R-RENAMED": same})
	if before, _ := linked("?scf_release=" + scfreleasetest.ReleaseA); !before["R-RENAMED"] {
		t.Fatalf("before the upgrade tenant A's overlay does not cover R-RENAMED: %v", before)
	}

	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	if _, err := scfimport.QueueEdgeRemapProposals(ctx, admin); err != nil {
		t.Fatalf("QueueEdgeRemapProposals: %v", err)
	}
	report, err := scfimport.QueueOverlayRemapProposals(ctx, admin)
	if err != nil {
		t.Fatalf("QueueOverlayRemapProposals: %v", err)
	}
	if report.Carried < 2 {
		t.Fatalf("carried %d overlay edges; want at least tenant A's and tenant B's", report.Carried)
	}

	// Tenant A's default read keeps R-RENAMED through its carried overlay;
	// its catalog edge on the retitled GOV-01 is not carried.
	def, rel := linked("")
	if rel != scfreleasetest.ReleaseB {
		t.Fatalf("default read echoed %q; want %q", rel, scfreleasetest.ReleaseB)
	}
	if !def["R-RENAMED"] {
		t.Errorf("default read lost R-RENAMED's overlay coverage across the release import: %v", def)
	}

	// Each tenant's exact match is carried in that tenant, by the import.
	onB := anchorIn(t, admin, scfreleasetest.ReleaseB, "IAC-06")
	for name, c := range map[string]struct {
		tenant string
		legacy mappingoverlay.Edge
	}{"tenant A": {tenantA, aExact}, "tenant B": {tenantB, bExact}} {
		got := carriedFrom(t, admin, c.legacy.ID)
		if got.tenant.String() != c.tenant || got.anchor != onB || got.requirement != c.legacy.FrameworkRequirementID ||
			got.createdBy != mappingoverlay.RemapCarryActor || got.status != "active" {
			t.Errorf("%s: carried edge = %+v; want an active copy on B's IAC-06 in %s by %s",
				name, got, c.tenant, mappingoverlay.RemapCarryActor)
		}
		if !slices.Equal(got.audit, []string{"create:" + mappingoverlay.RemapCarryActor + ":" + c.tenant}) {
			t.Errorf("%s: carried edge audit = %v; want one create by the import in %s", name, got.audit, c.tenant)
		}
		if got.decidedBy != mappingoverlay.RemapCarryActor || got.decisionTenant.String() != c.tenant {
			t.Errorf("%s: decision by %s in %s; want %s in %s", name, got.decidedBy, got.decisionTenant, mappingoverlay.RemapCarryActor, c.tenant)
		}
		if s := overlayStatus(t, admin, c.legacy.ID); s != "active" {
			t.Errorf("%s: legacy overlay is %s; want active", name, s)
		}
	}

	// The retitled anchor stays a pending proposal for tenant A's writers.
	var class, status string
	if err := admin.QueryRow(ctx, `
		SELECT class, status FROM scf_overlay_remap_proposals WHERE overlay_edge_id = $1`,
		aRetitled.ID).Scan(&class, &status); err != nil {
		t.Fatalf("retitled overlay's proposal: %v", err)
	}
	if class != string(scfimport.RemapRenamed) || status != "open" {
		t.Errorf("retitled overlay's proposal is %s/%s; want renamed/open", class, status)
	}

	// No cross-tenant write: each tenant holds exactly its own edges.
	counts := func() map[string]int {
		t.Helper()
		return overlayCountsOf(t, admin, slug)
	}
	want := map[string]int{tenantA: 3, tenantB: 2}
	if got := counts(); !maps.Equal(got, want) {
		t.Fatalf("overlay edges by tenant = %v; want %v", got, want)
	}

	// Idempotent: a second run carries and closes nothing and adds no edge.
	again, err := scfimport.QueueOverlayRemapProposals(ctx, admin)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again.Carried != 0 || again.Closed != 0 {
		t.Errorf("re-run carried %d and closed %d; want 0 and 0", again.Carried, again.Closed)
	}
	if got := counts(); !maps.Equal(got, want) {
		t.Errorf("after the re-run overlay edges by tenant = %v; want %v", got, want)
	}
}

type carried struct {
	tenant, requirement, anchor uuid.UUID
	createdBy, status           string
	audit                       []string
	decidedBy                   string
	decisionTenant              uuid.UUID
}

// carriedFrom reads, as the migrate role, the edge the import created from
// one legacy overlay edge, with its audit trail and its remap decision.
func carriedFrom(t *testing.T, admin *pgxpool.Pool, legacy uuid.UUID) carried {
	t.Helper()
	ctx := context.Background()
	var c carried
	var id uuid.UUID
	if err := admin.QueryRow(ctx, `
		SELECT o.id, o.tenant_id, o.framework_requirement_id, o.scf_anchor_id, o.created_by, o.status,
		       d.decided_by, d.tenant_id
		FROM scf_overlay_remap_proposals p
		JOIN fw_to_scf_overlay_edges o ON o.tenant_id = p.tenant_id AND o.id = p.new_overlay_edge_id
		JOIN scf_overlay_remap_decisions d ON d.proposal_id = p.id
		WHERE p.overlay_edge_id = $1 AND p.status = 'approved'`, legacy).Scan(
		&id, &c.tenant, &c.requirement, &c.anchor, &c.createdBy, &c.status, &c.decidedBy, &c.decisionTenant); err != nil {
		t.Fatalf("carried edge of %s: %v", legacy, err)
	}
	rows, err := admin.Query(ctx, `
		SELECT action || ':' || actor || ':' || tenant_id::text FROM fw_to_scf_overlay_edge_audit_log
		WHERE overlay_edge_id = $1 ORDER BY occurred_at, id`, id)
	if err != nil {
		t.Fatalf("audit of %s: %v", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		c.audit = append(c.audit, s)
	}
	return c
}

func overlayStatus(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := admin.QueryRow(context.Background(), `SELECT status FROM fw_to_scf_overlay_edges WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("overlay %s: %v", id, err)
	}
	return s
}

// overlayCountsOf counts the overlay edges on the framework's requirements,
// by tenant.
func overlayCountsOf(t *testing.T, admin *pgxpool.Pool, slug string) map[string]int {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT o.tenant_id::text, count(*) FROM fw_to_scf_overlay_edges o
		JOIN framework_requirements r ON r.id = o.framework_requirement_id
		JOIN framework_versions fv ON fv.id = r.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 GROUP BY o.tenant_id`, slug)
	if err != nil {
		t.Fatalf("count overlays: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var tenant string
		var n int
		if err := rows.Scan(&tenant, &n); err != nil {
			t.Fatalf("scan count: %v", err)
		}
		out[tenant] = n
	}
	return out
}

// retireOverlaysOf retires the framework's active overlay edges so the
// catalog rows under them can be removed.
func retireOverlaysOf(t *testing.T, admin *pgxpool.Pool, slug string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		UPDATE fw_to_scf_overlay_edges SET status = 'retired', retirement_reason = 'test cleanup',
		       retired_by = 'test', retired_at = now()
		WHERE status = 'active' AND framework_requirement_id IN (
		    SELECT r.id FROM framework_requirements r
		    JOIN framework_versions fv ON fv.id = r.framework_version_id
		    JOIN frameworks f ON f.id = fv.framework_id WHERE f.slug = $1)`, slug); err != nil {
		t.Errorf("retire overlays of %s: %v", slug, err)
	}
}
