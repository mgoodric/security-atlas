//go:build integration

package soc2import_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 567 — the 21 palette-bound HIPAA rows from slice 516's residual table,
// asserted against the seeded catalog.
//
// Slice 516 mapped 21 rows to the closest COVERING anchor because the bundled
// sample palette carried no finer one, and recorded them as a residual table.
// Slice 567 grew the palette with the 15 finer anchors those rows needed (the
// operator's interim option-(A) decision — slice 754 still owns the real-SCF
// catalog question) and re-pointed 16 of the 21. The remaining five are settled
// in place, two permanently (HIPAA regulatory arrangements with no control
// analog) and three after re-checking them against the grown palette.
//
// This file is the load-bearing proof for all 21: each row resolves through a
// real fw_to_scf_edges row to the anchor, STRM type, and strength the decisions
// log records (docs/audit-log/567-hipaa-finer-anchor-remap-decisions.md D6). It
// mirrors slice 646's TestTHRFinerCrosswalk_EdgesResolve. A silent re-point, a
// dropped edge, or a strength drifting away from its recorded rationale fails
// here — mapping accuracy is an audit-facing claim, so it is asserted, not
// assumed.
//
// It exercises the SAME seeding path every other crosswalk suite uses
// (scfseed.EnsureSCFCatalog via ensureSCFLoaded → migrations/fixtures/
// scf-sample.json). That is the point of the option-(A) decision: the finer
// anchors live in the shared seeded catalog, so these assertions run in CI
// rather than behind an opt-in full-catalog path that would never execute.

// The 21-row work list (hipaaResidualRows) and the 15 added anchors
// (hipaaFinerAnchorsAddedBy567) are declared in the untagged
// hipaa_finer_anchor_test.go, which asserts them at the YAML/fixture level.
// This file asserts the same table through Postgres, so the two tiers can never
// disagree about what was decided.

// TestHIPAAFinerAnchor_ResidualRowsResolveAsRecorded imports the HIPAA
// crosswalk against the seeded catalog and asserts every one of slice 516's 21
// residual rows resolves to the anchor / STRM type / strength slice 567
// recorded for it. The 16 re-pointed rows would have rolled the whole import
// back before the palette grew (the standing
// TestHIPAAImport_RejectsEdgeToNonexistentAnchor behaviour), so a successful
// import here is itself the proof that the finer anchors are seeded.
func TestHIPAAFinerAnchor_ResidualRowsResolveAsRecorded(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadHIPAACrosswalk(t)); err != nil {
		t.Fatalf("HIPAA Import: %v", err)
	}

	if n := len(hipaaResidualRows); n != 21 {
		t.Fatalf("residual table has %d rows; slice 516 recorded 21", n)
	}

	for _, c := range hipaaResidualRows {
		c := c
		t.Run(c.code+"->"+c.anchor, func(t *testing.T) {
			var rel string
			var strength float64
			if err := pool.QueryRow(context.Background(), `
				SELECT e.relationship_type::text, e.strength
				FROM framework_requirements fr
				JOIN framework_versions fv ON fv.id = fr.framework_version_id
				JOIN frameworks f ON f.id = fv.framework_id
				JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
				JOIN scf_anchors a ON a.id = e.scf_anchor_id
				WHERE f.slug = 'hipaa_security_rule' AND fr.code = $1 AND a.scf_id = $2`,
				c.code, c.anchor).Scan(&rel, &strength); err != nil {
				t.Fatalf("%s (%s) does not resolve to %s: %v; resolved to %v",
					c.code, c.verdict, c.anchor, err,
					keys(anchorsForRequirement(t, pool, "hipaa_security_rule", "2013-01-25", c.code)))
			}
			if rel != c.rel {
				t.Errorf("%s -> %s relationship_type = %q; want %q", c.code, c.anchor, rel, c.rel)
			}
			if strength != c.strength {
				t.Errorf("%s -> %s strength = %v; want %v (the value its recorded rationale justifies)",
					c.code, c.anchor, strength, c.strength)
			}
		})
	}
}

// TestHIPAAFinerAnchor_AddedAnchorsAreSeeded asserts the 15 anchors slice 567
// added to the sample fixture are present in the CURRENT SCF framework version
// — the resolution path the crosswalk importer uses. This separates "the
// palette grew" from "the crosswalk points at it": if the fixture were reverted
// or the seed path changed, this fails with a clear cause rather than as an
// opaque rollback in the row-resolution test above.
func TestHIPAAFinerAnchor_AddedAnchorsAreSeeded(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)

	if n := len(hipaaFinerAnchorsAddedBy567); n != 15 {
		t.Fatalf("added-anchor list has %d entries; slice 567 added 15", n)
	}
	for _, id := range hipaaFinerAnchorsAddedBy567 {
		id := id
		t.Run(id, func(t *testing.T) {
			var family, title string
			if err := pool.QueryRow(context.Background(), `
				SELECT a.family, a.title
				FROM scf_anchors a
				JOIN framework_versions fv ON fv.id = a.framework_version_id
				JOIN frameworks f ON f.id = fv.framework_id
				WHERE f.slug = 'scf' AND fv.status = 'current' AND a.scf_id = $1`,
				id).Scan(&family, &title); err != nil {
				t.Fatalf("anchor %s is not seeded in the current SCF catalog: %v", id, err)
			}
			if family == "" || title == "" {
				t.Errorf("anchor %s seeded with empty family/title (%q/%q)", id, family, title)
			}
		})
	}
}

// TestHIPAAFinerAnchor_SamplePaletteNotWeakened is the slice-567 boundary
// assertion ("do NOT weaken or delete the 53-anchor sample fixture; add the
// full-catalog path alongside it"). Growing the palette must be strictly
// additive: every anchor the pre-567 crosswalks already resolved against must
// still resolve. The five bundled crosswalks are imported together and every
// edge must land — the same all-or-nothing property the shared suite relies on.
func TestHIPAAFinerAnchor_SamplePaletteNotWeakened(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetHIPAA(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	for _, cw := range []struct {
		name string
		load func(*testing.T) *soc2import.Crosswalk
	}{
		{"soc2", loadCrosswalk},
		{"iso27001", loadISOCrosswalk},
		{"pcidss", loadPCICrosswalk},
		{"nist_csf", loadCSFCrosswalk},
		{"hipaa", loadHIPAACrosswalk},
	} {
		if _, err := soc2import.Import(context.Background(), pool, cw.load(t)); err != nil {
			t.Fatalf("%s Import against the grown palette: %v", cw.name, err)
		}
	}

	// The pre-567 palette's sentinel anchors still resolve for the frameworks
	// that were already pointing at them.
	for _, c := range []struct{ slug, version, code, anchor string }{
		{"soc2", "2017", "CC6.1", "IAC-01"},
		{"iso27001", "2022", "A.5.15", "IAC-01"},
		{"pcidss", "4.0", "8.2.1", "IAC-01"},
		{"nist_csf", "2.0", "PR.AA-01", "IAC-01"},
		{"hipaa_security_rule", "2013-01-25", "164.312(a)(1)", "IAC-01"},
	} {
		c := c
		t.Run(c.slug+"/"+c.code, func(t *testing.T) {
			anchors := anchorsForRequirement(t, pool, c.slug, c.version, c.code)
			if !anchors[c.anchor] {
				t.Errorf("%s:%s no longer resolves to %s after the palette grew; resolved to %v",
					c.slug, c.code, c.anchor, keys(anchors))
			}
		})
	}
}
