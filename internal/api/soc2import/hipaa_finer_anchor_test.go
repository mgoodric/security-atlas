package soc2import_test

import (
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 567 — pure-Go coverage for the 21 palette-bound HIPAA rows slice 516
// recorded as its residual table (slice-353 Q-2 convention: the fast tier
// carries the data assertion, the integration tier carries the resolution
// proof).
//
// Slice 516 mapped these 21 rows to the closest COVERING anchor because the
// bundled sample palette had no finer one. Slice 567 grew the palette with the
// 15 finer anchors they needed (the operator's interim option-(A) decision;
// slice 754 still owns the real-SCF-catalog question) and re-pointed 16 of the
// 21. Five are settled in place — two permanently, three after re-checking them
// against the grown palette.
//
// The table below IS the work list, and it is shared with the integration suite
// (hipaa_finer_anchor_integration_test.go) so the two tiers can never disagree
// about what was decided. Each row's reasoning lives in
// docs/audit-log/567-hipaa-finer-anchor-remap-decisions.md D6.

// hipaaResidualRow is one row of slice 516's residual table with its post-567
// disposition. Verdict is recorded alongside so a reader sees at a glance which
// rows moved; the assertions are on (anchor, relationship_type, strength).
type hipaaResidualRow struct {
	code     string  // HIPAA requirement code (45 CFR Part 164 Subpart C)
	anchor   string  // SCF anchor the row points at after slice 567
	rel      string  // STRM relationship_type
	strength float64 // recorded strength
	verdict  string  // "re-point" | "left as-is" | "hold"
}

var hipaaResidualRows = []hipaaResidualRow{
	// --- Re-pointed onto anchors slice 567 added to the palette (16 rows) ---
	{"164.308(a)(1)(ii)(C)", "HRS-07", "subset_of", 0.8, "re-point"},        // Sanction Policy
	{"164.308(a)(4)(ii)(A)", "NET-06", "intersects_with", 0.65, "re-point"}, // Isolate Clearinghouse Functions
	{"164.308(a)(5)(ii)(D)", "IAC-10", "equal", 0.85, "re-point"},           // Password Management
	{"164.308(a)(7)(ii)(E)", "RSK-08", "equal", 0.85, "re-point"},           // Applications & Data Criticality Analysis
	{"164.310(a)(2)(iv)", "MNT-02", "intersects_with", 0.7, "re-point"},     // Maintenance Records
	{"164.310(b)", "HRS-12", "intersects_with", 0.75, "re-point"},           // Workstation Use
	{"164.310(d)(2)(iii)", "DCH-07", "subset_of", 0.75, "re-point"},         // Accountability (media custody)
	{"164.312(a)(2)(ii)", "IAC-24", "equal", 0.8, "re-point"},               // Emergency Access Procedure
	{"164.312(a)(2)(iii)", "IAC-25", "equal", 0.85, "re-point"},             // Automatic Logoff
	{"164.312(c)(1)", "DCH-05", "subset_of", 0.8, "re-point"},               // Integrity
	{"164.312(c)(2)", "CRY-11", "subset_of", 0.8, "re-point"},               // Mechanism to Authenticate ePHI
	{"164.312(e)(2)(i)", "CRY-12", "subset_of", 0.85, "re-point"},           // Integrity Controls (Transmission)
	{"164.314(b)(2)(i)", "TPM-05", "intersects_with", 0.65, "re-point"},     // Plan Safeguards
	{"164.314(b)(2)(iii)", "TPM-05", "subset_of", 0.8, "re-point"},          // Agents Safeguard
	{"164.316(b)(2)(ii)", "GOV-02", "subset_of", 0.8, "re-point"},           // Availability (dissemination)
	{"164.316(b)(2)(iii)", "GOV-03", "subset_of", 0.8, "re-point"},          // Updates

	// --- Checked against the grown palette, deliberately NOT re-pointed (3) ---
	{"164.310(a)(2)(i)", "BCD-02", "intersects_with", 0.6, "left as-is"},   // Contingency Operations
	{"164.314(b)(2)(ii)", "TPM-01", "intersects_with", 0.55, "left as-is"}, // Adequate Separation
	{"164.316(b)(2)(i)", "DCH-03", "intersects_with", 0.6, "left as-is"},   // Time Limit

	// --- Held permanently: HIPAA regulatory arrangements, no control analog (2) ---
	{"164.314(a)(2)(ii)", "TPM-01", "intersects_with", 0.65, "hold"}, // Other Arrangements
	{"164.314(b)(1)", "TPM-01", "intersects_with", 0.55, "hold"},     // Group Health Plans
}

// hipaaFinerAnchorsAddedBy567 are the 15 anchors slice 567 added to
// migrations/fixtures/scf-sample.json so the re-pointed rows resolve. Shared
// with the integration suite, which proves they are actually seeded.
var hipaaFinerAnchorsAddedBy567 = []string{
	"HRS-07", "HRS-12", // personnel sanctions, rules of behavior
	"NET-06",                     // network segmentation
	"IAC-10", "IAC-24", "IAC-25", // authenticator mgmt, break-glass, session termination
	"RSK-08",           // business impact analysis
	"MNT-02",           // controlled maintenance
	"DCH-05", "DCH-07", // data integrity, media custody
	"CRY-11", "CRY-12", // cryptographic + transmission integrity
	"TPM-05",           // third-party contract requirements
	"GOV-02", "GOV-03", // publishing + periodic review of documentation
}

// crosswalkPath resolves a bundled crosswalk by file name, from this package's
// directory (mirrors hipaaPath / csfPath).
func crosswalkPath(name string) string {
	return filepath.Join("..", "..", "..", "data", "crosswalks", name)
}

// bundledSampleAnchorIDs loads migrations/fixtures/scf-sample.json through the
// canonical scfimport parser — the same shape scfseed.EnsureSCFCatalog seeds —
// and returns the set of anchor codes it carries. This is the palette every
// bundled crosswalk resolves against in the integration suite.
func bundledSampleAnchorIDs(t *testing.T) map[string]bool {
	t.Helper()
	cat, err := scfimport.Load(filepath.Join("..", "..", "..", "migrations", "fixtures", "scf-sample.json"))
	if err != nil {
		t.Fatalf("load bundled SCF sample fixture: %v", err)
	}
	present := make(map[string]bool, len(cat.Controls))
	for _, c := range cat.Controls {
		present[c.SCFID] = true
	}
	if len(present) == 0 {
		t.Fatal("bundled SCF sample fixture parsed zero anchors")
	}
	return present
}

// TestLoad_HIPAAResidualRowsCarryTheirRecordedDisposition asserts every one of
// slice 516's 21 residual rows carries, in the shipped YAML, exactly the anchor
// / STRM type / strength slice 567 recorded for it. A silent re-point or a
// strength drifting away from its rationale fails here without a database —
// mapping accuracy is an audit-facing claim, so it is asserted, not assumed.
func TestLoad_HIPAAResidualRowsCarryTheirRecordedDisposition(t *testing.T) {
	t.Parallel()
	if n := len(hipaaResidualRows); n != 21 {
		t.Fatalf("residual table has %d rows; slice 516 recorded 21", n)
	}

	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}
	byCode := map[string]soc2import.Mapping{}
	for _, m := range cw.Mappings {
		byCode[m.RequirementCode] = m
	}

	for _, want := range hipaaResidualRows {
		want := want
		t.Run(want.code, func(t *testing.T) {
			t.Parallel()
			got, ok := byCode[want.code]
			if !ok {
				t.Fatalf("residual row %s has no mapping in the shipped crosswalk", want.code)
			}
			if got.SCFAnchor != want.anchor {
				t.Errorf("%s (%s) anchor = %q; want %q", want.code, want.verdict, got.SCFAnchor, want.anchor)
			}
			if got.RelationshipType != want.rel {
				t.Errorf("%s -> %s relationship_type = %q; want %q", want.code, want.anchor, got.RelationshipType, want.rel)
			}
			if got.Strength != want.strength {
				t.Errorf("%s -> %s strength = %v; want %v", want.code, want.anchor, got.Strength, want.strength)
			}
			// Boundary: no row may be re-pointed without a recorded rationale.
			if got.Rationale == "" {
				t.Errorf("%s -> %s carries no rationale; mapping accuracy is an audit-facing claim", want.code, want.anchor)
			}
		})
	}
}

// TestLoad_HIPAAResidualRowsResolveAgainstBundledPalette closes the loop the
// slice-516 residual table was blocked on: every anchor the 21 rows point at
// must exist in the bundled sample fixture. Before slice 567 grew the palette,
// a finer-anchor re-point would have rolled the entire soc2import transaction
// back for all five frameworks; this asserts the palette now carries what the
// crosswalk claims, without needing Postgres to find out.
func TestLoad_HIPAAResidualRowsResolveAgainstBundledPalette(t *testing.T) {
	t.Parallel()
	present := bundledSampleAnchorIDs(t)

	for _, want := range hipaaResidualRows {
		if !present[want.anchor] {
			t.Errorf("residual row %s points at %s, which the bundled sample fixture lacks — the import would roll back",
				want.code, want.anchor)
		}
	}
	for _, id := range hipaaFinerAnchorsAddedBy567 {
		if !present[id] {
			t.Errorf("anchor %s is listed as added by slice 567 but is absent from migrations/fixtures/scf-sample.json", id)
		}
	}
}

// TestLoad_SamplePaletteStillCarriesPre567Anchors is the slice-567 boundary
// assertion ("do NOT weaken or delete the sample fixture; add alongside it").
// Growing the palette must be strictly additive, so EVERY anchor referenced by
// EVERY bundled crosswalk — not just HIPAA's — must still resolve against the
// fixture.
func TestLoad_SamplePaletteStillCarriesPre567Anchors(t *testing.T) {
	t.Parallel()
	present := bundledSampleAnchorIDs(t)

	for _, f := range []string{
		"soc2-tsc-2017.yaml",
		"iso27001-2022.yaml",
		"pci-dss-4.0.yaml",
		"nist-csf-2.0.yaml",
		"hipaa-security-rule.yaml",
	} {
		f := f
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			cw, err := soc2import.Load(crosswalkPath(f))
			if err != nil {
				t.Fatalf("Load %s: %v", f, err)
			}
			for _, m := range cw.Mappings {
				if !present[m.SCFAnchor] {
					t.Errorf("%s: %s -> %s is dangling against the bundled sample fixture",
						f, m.RequirementCode, m.SCFAnchor)
				}
			}
		})
	}
}
