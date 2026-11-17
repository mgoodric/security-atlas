//go:build integration

// #2240 — an overlay edge holds its catalog requirement and anchor while
// active, a cascade of a retired one is audited, an overlay edge may only
// name a requirement its tenant can see, and a no_relationship catalog edge
// does not block an overlay. Runs against a real Postgres with RLS, on the
// newFixture (#1797) shape.

package mappingoverlays_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func (f fixture) anchorID(t *testing.T, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.admin.QueryRow(context.Background(), `
		SELECT a.id FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = $2`, scfreleasetest.ReleaseB, code).Scan(&id); err != nil {
		t.Fatalf("anchor %s: %v", code, err)
	}
	return id
}

// Deleting a requirement, an anchor or a framework version that an active
// overlay edge references is refused (23001), and the edge survives.
func TestOverlay_CatalogDeleteRefusedWhileOverlayActive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	if status != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", status, body)
	}
	edgeID := edgeOf(t, body)["id"].(string)

	for name, stmt := range map[string]struct {
		sql string
		arg any
	}{
		"requirement": {`DELETE FROM framework_requirements WHERE id = $1`, f.requirement["C2"]},
		"anchor":      {`DELETE FROM scf_anchors WHERE id = $1`, f.anchorID(t, "IAC-06")},
		"framework version": {`DELETE FROM framework_versions WHERE id = (
			SELECT framework_version_id FROM framework_requirements WHERE id = $1)`, f.requirement["C2"]},
	} {
		// Each attempt runs in a transaction that is rolled back, so a
		// regression cannot wipe the shared fixture catalog.
		err := pgx.BeginFunc(ctx, f.admin, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt.sql, stmt.arg)
			if err == nil {
				return errors.New("rollback")
			}
			return err
		})
		if sqlState(err) != "23001" || !strings.Contains(err.Error(), "active overlay edge") {
			t.Errorf("%s delete with an active overlay edge: err = %v; want 23001 naming the overlay edge", name, err)
		}
	}

	var status2 string
	if err := f.admin.QueryRow(ctx, `SELECT status FROM fw_to_scf_overlay_edges WHERE id = $1`, edgeID).Scan(&status2); err != nil || status2 != "active" {
		t.Fatalf("overlay edge after refused deletes: status %q, err %v; want active", status2, err)
	}
}

// A retired overlay edge does not hold its requirement: the delete
// cascades it away and writes one cascade_removed audit row first.
func TestOverlay_CatalogDeleteAuditsRetiredCascade(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	edgeID := edgeOf(t, body)["id"].(string)
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+edgeID+"/retire", f.tenantA.admin,
		map[string]any{"reason": "superseded"}); status != http.StatusOK {
		t.Fatalf("retire: status %d (%v)", status, body)
	}

	if _, err := f.admin.Exec(ctx, `DELETE FROM framework_requirements WHERE id = $1`, f.requirement["C2"]); err != nil {
		t.Fatalf("delete requirement with only a retired overlay edge: %v", err)
	}
	var left int
	if err := f.admin.QueryRow(ctx, `SELECT count(*) FROM fw_to_scf_overlay_edges WHERE id = $1`, edgeID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("retired overlay edge after requirement delete: %d rows, err %v; want cascaded away", left, err)
	}

	var actor, reqCode, beforeStatus, removedWith, removedRow string
	if err := f.admin.QueryRow(ctx, `
		SELECT actor, before->>'requirement_code', before->>'status', after->>'removed_with', after->>'removed_row_id'
		FROM fw_to_scf_overlay_edge_audit_log
		WHERE tenant_id = $1 AND overlay_edge_id = $2 AND action = 'cascade_removed'`,
		f.tenantA.tenant, edgeID).Scan(&actor, &reqCode, &beforeStatus, &removedWith, &removedRow); err != nil {
		t.Fatalf("cascade_removed audit row: %v", err)
	}
	if actor != "system:catalog_delete" || reqCode != "C2" || beforeStatus != "retired" ||
		removedWith != "framework_requirements" || removedRow != f.requirement["C2"].String() {
		t.Errorf("cascade_removed row: actor %q, code %q, before status %q, removed_with %q, removed_row_id %q",
			actor, reqCode, beforeStatus, removedWith, removedRow)
	}

	// The tenant sees the removal in its own audit trail, and nobody else does.
	var countA, countB int
	for tenant, n := range map[string]*int{f.tenantA.tenant: &countA, f.tenantB.tenant: &countB} {
		if err := pgx.BeginFunc(ctx, dbtest.NewAppPool(t), func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT count(*) FROM fw_to_scf_overlay_edge_audit_log
				WHERE overlay_edge_id = $1 AND action = 'cascade_removed'`, edgeID).Scan(n)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if countA != 1 || countB != 0 {
		t.Errorf("cascade_removed rows visible: tenant A %d, tenant B %d; want 1, 0", countA, countB)
	}
}

// An overlay edge's requirement must belong to a catalog framework version
// or to the overlay's own tenant. Checked on INSERT and UPDATE, as the app
// role and as a role that bypasses RLS.
func TestOverlay_RequirementMustBeCatalogOrOwnTenant(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	app := dbtest.NewAppPool(t)
	anchor := f.anchorID(t, "IAC-06")

	// Tenant B's private framework version with one requirement.
	fwID, verID, privReq := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = f.admin.Exec(ctx, `DELETE FROM fw_to_scf_overlay_edges WHERE framework_requirement_id = $1`, privReq)
		_, _ = f.admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, verID)
		_, _ = f.admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	slug := "ovl-priv-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, $2, 'Private', $3, 'test')`,
			[]any{fwID, f.tenantB.tenant, slug}},
		{`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status) VALUES ($1, $2, $3, '1', 'current')`,
			[]any{verID, f.tenantB.tenant, fwID}},
		{`INSERT INTO framework_requirements (id, framework_version_id, code, title) VALUES ($1, $2, 'P1', 'P1')`,
			[]any{privReq, verID}},
	} {
		if _, err := f.admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed tenant B private framework: %v", err)
		}
	}

	insert := func(tenant string, req uuid.UUID) error {
		return pgx.BeginFunc(ctx, app, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO fw_to_scf_overlay_edges (
					tenant_id, framework_requirement_id, scf_anchor_id,
					relationship_type, strength, rationale, source_ref, created_by)
				VALUES ($1, $2, $3, 'equal', 1.0, 'direct insert', 'test', 'test')`, tenant, req, anchor)
			return err
		})
	}

	if err := insert(f.tenantA.tenant, privReq); sqlState(err) != "23514" || !strings.Contains(err.Error(), "another tenant") {
		t.Errorf("tenant A inserting on tenant B's private requirement: err = %v; want 23514 naming another tenant", err)
	}
	if err := insert(f.tenantB.tenant, privReq); err != nil {
		t.Errorf("tenant B inserting on its own private requirement: %v", err)
	}
	if err := insert(f.tenantA.tenant, f.requirement["C2"]); err != nil {
		t.Errorf("tenant A inserting on a catalog requirement: %v", err)
	}

	// A role that bypasses RLS is held to the same rule, on INSERT and on
	// UPDATE (the update guard would refuse too; this trigger fires first).
	if _, err := f.admin.Exec(ctx, `INSERT INTO fw_to_scf_overlay_edges (
			tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by)
		VALUES ($1, $2, $3, 'equal', 1.0, 'direct insert', 'test', 'test')`, f.tenantA.tenant, privReq, anchor); sqlState(err) != "23514" {
		t.Errorf("migrate role inserting tenant A on tenant B's private requirement: err = %v; want 23514", err)
	}
	if _, err := f.admin.Exec(ctx, `UPDATE fw_to_scf_overlay_edges SET framework_requirement_id = $1
		WHERE tenant_id = $2 AND framework_requirement_id = $3`, privReq, f.tenantA.tenant, f.requirement["C2"]); sqlState(err) != "23514" ||
		!strings.Contains(err.Error(), "another tenant") {
		t.Errorf("moving tenant A's edge onto tenant B's private requirement: err = %v; want 23514 naming another tenant", err)
	}
}

// A no_relationship catalog edge credits nothing, so it does not block an
// overlay edge on the same pair, and the overlay is the live path in
// coverage. Create and the coverage view agree.
func TestOverlay_NoRelationshipCatalogEdgeDoesNotBlockOverlay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, source_attribution, mapping_tier)
		VALUES ($1, $2, $3, 'no_relationship', 0.0, 'scf_official', 'verified')`,
		uuid.New(), f.requirement["C2"], f.anchorID(t, "CRY-03")); err != nil {
		t.Fatalf("seed no_relationship catalog edge: %v", err)
	}

	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "CRY-03"))
	if status != http.StatusCreated {
		t.Fatalf("overlay on a no_relationship catalog pair: status %d (%v); want 201", status, body)
	}
	edgeID := edgeOf(t, body)["id"].(string)

	var origins []string
	if err := pgx.BeginFunc(ctx, dbtest.NewAppPool(t), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, f.tenantA.tenant); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT origin || ':' || id::text FROM effective_fw_to_scf_edges
			WHERE framework_requirement_id = $1 AND scf_anchor_id = $2 ORDER BY origin`,
			f.requirement["C2"], f.anchorID(t, "CRY-03"))
		if err != nil {
			return err
		}
		origins, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(origins) != 2 || !strings.HasPrefix(origins[0], "catalog:") || origins[1] != "overlay:"+edgeID {
		t.Fatalf("effective edges for C2 -> CRY-03 = %v; want the no_relationship catalog row and the overlay row", origins)
	}
}
