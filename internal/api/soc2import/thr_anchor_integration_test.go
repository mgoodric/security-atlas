//go:build integration

package soc2import_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 635 — the SIEM-rule kind's detection anchor RESOLVES against the
// seeded catalog.
//
// Slice 533 shipped datadog.siem_rule.v1 with
// x-default-scf-anchors=[MON-01, THR-01]. MON-01 resolved against the seeded
// sample catalog, but THR-01 (the SCF Threat-Management / threat-intelligence
// domain) did NOT exist in the sample catalog — it carried the MON domain
// (incl. MON-08 "Anomalous Behavior Detection") but no THR-domain anchor, so
// the connector's advisory detection anchor pointed at no catalog row (see
// slice 533 decisions-log D2). Slice 635 seeds THR-01 into
// migrations/fixtures/scf-sample.json with its STRM crosswalk edges to
// SOC 2 CC7.2/CC7.3 and ISO 27001 A.5.7/A.8.16, so the advisory anchor now
// resolves. This file is the load-bearing proof.

// TestTHRAnchor_ResolvesInSeededCatalog asserts THR-01 resolves through the
// exact production path the crosswalk importer uses (GetSCFAnchorBySCFID
// against slug='scf' AND status='current') — the same path that produced the
// `scf_anchor "GOV-01" not found` failure when an anchor was absent. Before
// slice 635 this returned pgx.ErrNoRows.
func TestTHRAnchor_ResolvesInSeededCatalog(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)

	anchor, err := dbx.New(pool).GetSCFAnchorBySCFID(context.Background(), "THR-01")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("THR-01 does not resolve in the seeded SCF catalog — slice 635 regression (the SIEM-rule advisory detection anchor points at no catalog row)")
		}
		t.Fatalf("GetSCFAnchorBySCFID(THR-01): %v", err)
	}
	if anchor.ScfID != "THR-01" {
		t.Fatalf("resolved scf_id = %q; want THR-01", anchor.ScfID)
	}
	if anchor.Family != "THR" {
		t.Fatalf("THR-01 family = %q; want THR (the dedicated threat-management domain)", anchor.Family)
	}
}

// anchorsForRequirement returns the set of SCF anchor scf_ids a framework
// requirement resolves to through fw_to_scf_edges — the same join the
// GET /v1/requirements/{id}/coverage handler runs.
func anchorsForRequirement(t *testing.T, pool *pgxpool.Pool, slug, version, code string) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND fv.version = $2 AND fr.code = $3`, slug, version, code)
	if err != nil {
		t.Fatalf("anchors-for-requirement(%s:%s:%s): %v", slug, version, code, err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var scfID string
		if err := rows.Scan(&scfID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[scfID] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return out
}

// TestTHRAnchor_DetectionCrosswalkEdgesExist proves the STRM crosswalk the
// slice records: SOC 2 CC7.2 + CC7.3 and ISO 27001 A.5.7 + A.8.16 each
// resolve to THR-01 after both crosswalks are imported. This is the catalog
// half of the SIEM-rule evidence question ("threat-detection rules are
// configured AND appropriately tiered" — slice 533 D1): the
// datadog.siem_rule.v1 family anchors at THR-01, and THR-01 now reaches the
// SOC 2 CC7.2/CC7.3 + ISO A.5.7/A.8.16 requirements through real STRM edges.
func TestTHRAnchor_DetectionCrosswalkEdgesExist(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	cases := []struct {
		slug, version, code string
	}{
		{"soc2", "2017", "CC7.2"},
		{"soc2", "2017", "CC7.3"},
		{"iso27001", "2022", "A.5.7"},
		{"iso27001", "2022", "A.8.16"},
	}
	for _, c := range cases {
		anchors := anchorsForRequirement(t, pool, c.slug, c.version, c.code)
		if !anchors["THR-01"] {
			t.Errorf("%s:%s:%s does not resolve to THR-01; resolved to %v", c.slug, c.version, c.code, keys(anchors))
		}
	}

	// A.5.7 (Threat intelligence) is the direct STRM equal of THR-01 — the
	// slice-635 upgrade from the slice-438 LOW-CONFIDENCE MON-08 placeholder.
	// Assert the relationship type is `equal`, not the old intersects_with.
	var rel string
	if err := pool.QueryRow(context.Background(), `
		SELECT e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'iso27001' AND fv.version = '2022' AND fr.code = 'A.5.7' AND a.scf_id = 'THR-01'`).Scan(&rel); err != nil {
		t.Fatalf("A.5.7 -> THR-01 edge query: %v", err)
	}
	if rel != "equal" {
		t.Fatalf("A.5.7 -> THR-01 relationship_type = %q; want equal (the dedicated threat-intel anchor is the direct STRM match)", rel)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
