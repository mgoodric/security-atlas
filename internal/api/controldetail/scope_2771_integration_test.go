//go:build integration

// #2771 — GET /v1/evidence?scope_cell_id= matches a record to a scope cell
// by the scope it was pushed with (`scope_canonical`), not by the
// `scope_id` FK the push path leaves NULL. Real Postgres under RLS.

package controldetail_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func scopeTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"evidence_records",
		"scope_cells",
		"controls",
	)
}

func seedScopeCell(t *testing.T, admin *pgxpool.Pool, tenant, dims string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		VALUES ($1, $2, 'cell 2771', $3::jsonb, $4)
	`, id, tenant, dims, "hash-2771-"+id.String()); err != nil {
		t.Fatalf("seed scope cell: %v", err)
	}
	return id
}

// seedScopedEvidence inserts one record carrying scope_canonical (nil
// leaves it NULL, as on a pre-slice-474 row).
func seedScopedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, scope *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind, source_attribution,
			scope_canonical
		)
		VALUES ($1, $2, $3, $4, now() - interval '1 hour', now(),
		        '{"ingestion_path":"push"}'::jsonb, 'pass', '{}'::jsonb, $5,
		        'k2771', '{"actor_type":"connector","actor_id":"c2771"}'::jsonb,
		        $6::jsonb)
	`, id, tenant, ctrlID, ctrlID.String(), "hash-2771-"+id.String()[:8], scope); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func strp(s string) *string { return &s }

func evidenceIDs(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()
	rows, _ := body["evidence"].([]any)
	out := map[string]bool{}
	for _, r := range rows {
		out[r.(map[string]any)["evidence_id"].(string)] = true
	}
	return out
}

func TestEvidence_ScopeCellMatchesPushedScope(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := scopeTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrl := seedControl(t, admin, tenant)
	cell := seedScopeCell(t, admin, tenant, `{"environment":"prod","geography":"us-east-1"}`)

	// Covers both dimensions; geography is multi-valued.
	inCell := seedScopedEvidence(t, admin, tenant, ctrl, strp(
		`[{"key":"environment","values":["prod"]},{"key":"geography","values":["us-east-1","us-west-2"]},{"key":"cloud","values":["aws"]}]`))
	// Missing the geography dimension: not in the cell.
	seedScopedEvidence(t, admin, tenant, ctrl, strp(`[{"key":"environment","values":["prod"]}]`))
	// Another environment.
	seedScopedEvidence(t, admin, tenant, ctrl, strp(
		`[{"key":"environment","values":["dev"]},{"key":"geography","values":["us-east-1"]}]`))
	// No captured scope (pre-slice-474) and JSON null.
	seedScopedEvidence(t, admin, tenant, ctrl, nil)
	seedScopedEvidence(t, admin, tenant, ctrl, strp(`null`))

	resp, body := get(t, env, "/v1/evidence?scope_cell_id="+cell.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body %v", resp.StatusCode, body)
	}
	got := evidenceIDs(t, body)
	if len(got) != 1 || !got[inCell.String()] {
		t.Fatalf("scope_cell_id matched %v, want only %s", got, inCell)
	}

	// The unfiltered ledger still holds all five.
	_, body = get(t, env, "/v1/evidence")
	if n := len(evidenceIDs(t, body)); n != 5 {
		t.Fatalf("unfiltered ledger: %d rows, want 5", n)
	}
}

// A cell with no dimensions covers every record that has a captured scope.
func TestEvidence_ScopeCellWithNoDimensions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := scopeTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrl := seedControl(t, admin, tenant)
	cell := seedScopeCell(t, admin, tenant, `{}`)
	scoped := seedScopedEvidence(t, admin, tenant, ctrl, strp(`[{"key":"environment","values":["prod"]}]`))
	seedScopedEvidence(t, admin, tenant, ctrl, nil)

	_, body := get(t, env, "/v1/evidence?scope_cell_id="+cell.String())
	got := evidenceIDs(t, body)
	if len(got) != 1 || !got[scoped.String()] {
		t.Fatalf("empty-dimension cell matched %v, want only %s", got, scoped)
	}
}

// Another tenant's cell, or a cell id that does not exist, matches
// nothing: it must not fall through to the whole ledger.
func TestEvidence_ScopeCellOutOfTenantMatchesNothing(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := scopeTenant(t, admin)
	tenantB := scopeTenant(t, admin)
	env := testServer(t, app, tenantA)

	ctrl := seedControl(t, admin, tenantA)
	seedScopedEvidence(t, admin, tenantA, ctrl, strp(`[{"key":"environment","values":["prod"]}]`))
	foreign := seedScopeCell(t, admin, tenantB, `{}`)

	for _, id := range []string{foreign.String(), uuid.NewString()} {
		resp, body := get(t, env, "/v1/evidence?scope_cell_id="+id)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200; body %v", resp.StatusCode, body)
		}
		if n := len(evidenceIDs(t, body)); n != 0 {
			t.Fatalf("scope_cell_id=%s matched %d rows, want 0", id, n)
		}
	}
}

// The list wire carries source_attribution, the column the
// source_actor_* filters match.
func TestEvidence_WireCarriesSourceAttribution(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := scopeTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrl := seedControl(t, admin, tenant)
	seedScopedEvidence(t, admin, tenant, ctrl, nil)

	for _, path := range []string{"/v1/evidence", "/v1/evidence?control_id=" + ctrl.String()} {
		_, body := get(t, env, path)
		rows, _ := body["evidence"].([]any)
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows, want 1", path, len(rows))
		}
		sa, _ := rows[0].(map[string]any)["source_attribution"].(map[string]any)
		if sa["actor_type"] != "connector" || sa["actor_id"] != "c2771" {
			t.Fatalf("%s: source_attribution = %v", path, rows[0].(map[string]any)["source_attribution"])
		}
	}
}
