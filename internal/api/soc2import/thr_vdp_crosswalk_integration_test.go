//go:build integration

package soc2import_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 651 — THR-07 (Vulnerability Disclosure Program) gains its framework
// crosswalk edge.
//
// Slice 646 left THR-05 / THR-06 / THR-07 unmapped on the finding that no
// bundled framework carries a dedicated insider-threat or coordinated-
// disclosure requirement. That finding holds for THR-05 / THR-06 but was wrong
// for THR-07: NIST CSF 2.0 ID.RA-08 ("Processes for receiving, analyzing, and
// responding to vulnerability disclosures are established") is exactly a
// coordinated-disclosure requirement. It entered the bundled crosswalk in slice
// 514's full-Subcategory pass, anchored only to VPM-04, and slice 646's sweep
// looked at ID.RA-01 (internal vulnerability identification) rather than
// ID.RA-08. Slice 651 authors the ID.RA-08 -> THR-07 edge; the per-framework
// search evidence for the still-unmapped THR-05 / THR-06 is recorded in
// docs/audit-log/651-thr-insider-vdp-crosswalk-decisions.md.

// TestTHRVDPCrosswalk_EdgeResolves proves the ID.RA-08 -> THR-07 edge resolves
// to its target anchor through a real fw_to_scf_edges row (the same join GET
// /v1/requirements/{id}/coverage runs), and that the slice-514 VPM-04 edge
// survives alongside it (invariant #1 — N anchors per requirement; the edge is
// additive, not a replacement).
func TestTHRVDPCrosswalk_EdgeResolves(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	anchors := anchorsForRequirement(t, pool, "nist_csf", "2.0", "ID.RA-08")
	for _, want := range []string{"THR-07", "VPM-04"} {
		if !anchors[want] {
			t.Errorf("nist_csf:2.0:ID.RA-08 does not resolve to %s; resolved to %v", want, keys(anchors))
		}
	}
}

// TestTHRVDPCrosswalk_RelationshipIsHonest pins the STRM relationship + strength
// that is the load-bearing JUDGMENT of slice 651. `equal` (not intersects_with)
// because ID.RA-08 and THR-07 name the same control concept — an established
// intake process for externally-reported vulnerabilities. `0.9` (not 1.0)
// because ID.RA-08 also spans the downstream analyze/respond half that the
// VPM-04 edge carries, and THR-07 scopes the VDP to products and services.
// A future re-strengthening or downgrade must be a deliberate edit here.
func TestTHRVDPCrosswalk_RelationshipIsHonest(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	var rel string
	var strength float64
	if err := pool.QueryRow(context.Background(), `
		SELECT e.relationship_type::text, e.strength
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'nist_csf' AND fv.version = '2.0'
		  AND fr.code = 'ID.RA-08' AND a.scf_id = 'THR-07'`).Scan(&rel, &strength); err != nil {
		t.Fatalf("ID.RA-08 -> THR-07 edge query: %v", err)
	}
	if rel != "equal" {
		t.Errorf("ID.RA-08 -> THR-07 relationship_type = %q; want %q", rel, "equal")
	}
	if strength != 0.9 {
		t.Errorf("ID.RA-08 -> THR-07 strength = %v; want 0.9", strength)
	}
}

// TestTHRInsiderAnchors_RemainUnmapped is the honest-gap guard. THR-05 (Insider
// Threat Program) and THR-06 (Insider Threat Awareness) carry NO framework edge
// because no bundled crosswalk has a dedicated insider-threat requirement —
// the nearest candidates are general security-awareness controls (SOC 2 CC1.x,
// ISO A.6.3, CSF PR.AT-01, PCI 12.6.1, HIPAA 164.308(a)(5)) and personnel-
// activity monitoring (CSF DE.CM-03), neither of which is insider-threat-
// specific. This test fails if someone later adds an edge to either anchor
// WITHOUT updating the decisions log that justifies it — the gap is a recorded
// finding, not an oversight, and closing it should be deliberate.
func TestTHRInsiderAnchors_RemainUnmapped(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	for _, cw := range []*soc2import.Crosswalk{
		loadCrosswalk(t), loadISOCrosswalk(t), loadCSFCrosswalk(t),
	} {
		if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
			t.Fatalf("Import: %v", err)
		}
	}

	for _, anchor := range []string{"THR-05", "THR-06"} {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM fw_to_scf_edges e
			JOIN scf_anchors a ON a.id = e.scf_anchor_id
			WHERE a.scf_id = $1`, anchor).Scan(&n); err != nil {
			t.Fatalf("%s edge count: %v", anchor, err)
		}
		if n != 0 {
			t.Errorf("%s has %d framework edge(s); slice 651 recorded that no bundled framework "+
				"carries a dedicated insider-threat requirement. If a genuine match was found, "+
				"record its strength rationale in docs/audit-log/ and update this guard.", anchor, n)
		}
	}
}
