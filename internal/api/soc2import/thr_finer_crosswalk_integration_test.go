//go:build integration

package soc2import_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 646 — the FINER SCF Threat-Management controls (THR-02..THR-10) carry
// framework crosswalk edges.
//
// Slice 641 imported THR-02..THR-10 into the sample catalog, but only THR-01
// (from slice 635) carried framework crosswalk edges — the finer controls
// resolved in the catalog yet no framework requirement pointed at them. This
// slice authors the requirement -> SCF-anchor STRM edges (invariant #7) that
// map SOC 2 / ISO 27001 / NIST CSF requirements onto the finer THR controls.
//
// This file is the load-bearing proof that each authored edge RESOLVES through
// a real fw_to_scf_edges row (the same join GET
// /v1/requirements/{id}/coverage runs), mirroring slice 635's
// TestTHRAnchor_DetectionCrosswalkEdgesExist.
//
// Coverage note: THR-05 (Insider Threat Program), THR-06 (Insider Threat
// Awareness), and THR-07 (Vulnerability Disclosure Program) intentionally
// carry NO edges — none of the five bundled framework crosswalks has a
// dedicated insider-threat or coordinated-disclosure requirement, and mapping
// general security-awareness or internal vuln-management requirements onto
// them would over-state the relationship. See slice 646 decisions-log D5 and
// the spillover slice for a finer pass when a framework with those concepts
// lands.

// TestTHRFinerCrosswalk_EdgesResolve proves every finer-THR edge slice 646
// authors resolves to its target anchor through a real fw_to_scf_edges row,
// after the SOC 2 + ISO + CSF crosswalks are imported against the seeded
// catalog. A dropped or mistyped edge fails here.
func TestTHRFinerCrosswalk_EdgesResolve(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	cases := []struct {
		slug, version, code, anchor string
	}{
		// SOC 2
		{"soc2", "2017", "CC7.2", "THR-04"}, // Threat Hunting
		// ISO 27001:2022
		{"iso27001", "2022", "A.5.7", "THR-03"},  // Threat Intelligence Feeds
		{"iso27001", "2022", "A.8.16", "THR-04"}, // Threat Hunting
		{"iso27001", "2022", "A.8.8", "THR-02"},  // Indicators of Exposure
		// NIST CSF 2.0
		{"nist_csf", "2.0", "ID.RA-02", "THR-03"}, // Threat Intelligence Feeds
		{"nist_csf", "2.0", "ID.RA-03", "THR-09"}, // Threat Catalog
		{"nist_csf", "2.0", "ID.RA-03", "THR-10"}, // Threat Analysis
		{"nist_csf", "2.0", "DE.AE-07", "THR-01"}, // Threat Intelligence Program
	}
	for _, c := range cases {
		c := c
		t.Run(c.slug+"/"+c.code+"->"+c.anchor, func(t *testing.T) {
			anchors := anchorsForRequirement(t, pool, c.slug, c.version, c.code)
			if !anchors[c.anchor] {
				t.Errorf("%s:%s:%s does not resolve to %s; resolved to %v",
					c.slug, c.version, c.code, c.anchor, keys(anchors))
			}
		})
	}
}

// TestTHRFinerCrosswalk_RelationshipsAreHonest spot-checks the STRM
// relationship_type on the two edges whose strength selection is the
// load-bearing JUDGMENT of this slice: ID.RA-02 -> THR-03 is the dedicated
// `equal` upgrade of the slice-480 LOW-confidence MON-08 placeholder, and
// CC7.2 -> THR-04 is the honest `intersects_with` (hunting is one technique
// within CC7.2's broader anomaly monitoring, not an equivalence).
func TestTHRFinerCrosswalk_RelationshipsAreHonest(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	cases := []struct {
		slug, version, code, anchor, wantRel string
	}{
		{"nist_csf", "2.0", "ID.RA-02", "THR-03", "equal"},
		{"soc2", "2017", "CC7.2", "THR-04", "intersects_with"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.slug+"/"+c.code+"->"+c.anchor, func(t *testing.T) {
			var rel string
			if err := pool.QueryRow(context.Background(), `
				SELECT e.relationship_type::text
				FROM framework_requirements fr
				JOIN framework_versions fv ON fv.id = fr.framework_version_id
				JOIN frameworks f ON f.id = fv.framework_id
				JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
				JOIN scf_anchors a ON a.id = e.scf_anchor_id
				WHERE f.slug = $1 AND fv.version = $2 AND fr.code = $3 AND a.scf_id = $4`,
				c.slug, c.version, c.code, c.anchor).Scan(&rel); err != nil {
				t.Fatalf("%s:%s:%s -> %s edge query: %v", c.slug, c.version, c.code, c.anchor, err)
			}
			if rel != c.wantRel {
				t.Fatalf("%s:%s:%s -> %s relationship_type = %q; want %q",
					c.slug, c.version, c.code, c.anchor, rel, c.wantRel)
			}
		})
	}
}
