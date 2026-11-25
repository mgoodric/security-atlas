//go:build integration

package seed_test

// Issue #3064: when a stock policy's declared SCF release is loaded but not
// current, a code with no control bound in it falls back to the current
// release's anchor with the same code and title. Releases A and B
// (internal/scfrelease/testdata) give both cases: IAC-06 has the same title
// in both, GOV-01's title differs in B.

import (
	"context"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy/seed"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// notesOf returns the ReleaseFallbacks lines for the policy titled title.
func notesOf(report seed.Report, title string) []string {
	var out []string
	for _, f := range report.ReleaseFallbacks {
		if strings.HasPrefix(f, title+": ") {
			out = append(out, f)
		}
	}
	return out
}

// The policies declare legacy release A, and each control is bound to B's
// anchor, as on a bring-your-own install after the bootstrap carried the
// stock controls over. IAC-06 links B's control with a note naming B.
// GOV-01 stays unlinked with a note naming it.
func TestSeed_DeclaredLegacyFallsBackByCodeAndTitle(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	ctrlB := seedControlOnAnchor(t, admin, tenant, scfreleasetest.ReleaseB, "IAC-06")
	_ = seedControlOnAnchor(t, admin, tenant, scfreleasetest.ReleaseB, "GOV-01")

	report, err := seed.Seed(context.Background(), app, tenant,
		validStockPoliciesIn(t, scfreleasetest.ReleaseA, []string{"IAC-06", "GOV-01"}), seed.NewSQLAnchorResolver(admin))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if linked := linkedOf(t, admin, tenant, "a"); len(linked) != 1 || linked[0] != ctrlB {
		t.Fatalf("linked_control_ids = %v; want [%s] (B's IAC-06 control)", linked, ctrlB)
	}
	notes := notesOf(report, "a")
	if len(notes) != 2 {
		t.Fatalf("ReleaseFallbacks for a = %v; want one note for IAC-06 and one for GOV-01", notes)
	}
	if n := notes[0]; !strings.Contains(n, "IAC-06 linked") || !strings.Contains(n, scfreleasetest.ReleaseB) {
		t.Errorf("IAC-06 note %q does not say it linked in %s", n, scfreleasetest.ReleaseB)
	}
	if n := notes[1]; !strings.Contains(n, "GOV-01 left unlinked") || !strings.Contains(n, "different title") {
		t.Errorf("GOV-01 note %q does not say it was left unlinked for a different title", n)
	}
	gov := 0
	for _, m := range report.MissingAnchors {
		if m == "GOV-01" {
			gov++
		}
	}
	if gov != seed.StockPolicyCount || len(report.MissingAnchors) != seed.StockPolicyCount {
		t.Errorf("MissingAnchors = %v; want GOV-01 once per policy and nothing else", report.MissingAnchors)
	}
}

// A control bound in the declared legacy release wins over the fallback:
// the fallback only fills codes the declared release leaves unlinked.
func TestSeed_DeclaredLegacyKeepsItsOwnBinding(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	ctrlA := seedControlOnAnchor(t, admin, tenant, scfreleasetest.ReleaseA, "IAC-06")
	_ = seedControlOnAnchor(t, admin, tenant, scfreleasetest.ReleaseB, "IAC-06")

	report, err := seed.Seed(context.Background(), app, tenant,
		validStockPoliciesIn(t, scfreleasetest.ReleaseA, []string{"IAC-06"}), seed.NewSQLAnchorResolver(admin))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if linked := linkedOf(t, admin, tenant, "a"); len(linked) != 1 || linked[0] != ctrlA {
		t.Fatalf("linked_control_ids = %v; want [%s] (A's control)", linked, ctrlA)
	}
	if len(report.ReleaseFallbacks) != 0 {
		t.Errorf("ReleaseFallbacks = %v; want none", report.ReleaseFallbacks)
	}
}

// Declaring the current release is unchanged: a control bound only in the
// legacy release is not linked, and there is no note.
func TestSeed_DeclaredCurrentHasNoFallback(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	_ = seedControlOnAnchor(t, admin, tenant, scfreleasetest.ReleaseA, "IAC-06")

	report, err := seed.Seed(context.Background(), app, tenant,
		validStockPoliciesIn(t, scfreleasetest.ReleaseB, []string{"IAC-06"}), seed.NewSQLAnchorResolver(admin))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if linked := linkedOf(t, admin, tenant, "a"); len(linked) != 0 {
		t.Fatalf("linked_control_ids = %v; want none (B is current, so no fallback)", linked)
	}
	if len(report.ReleaseFallbacks) != 0 {
		t.Errorf("ReleaseFallbacks = %v; want none", report.ReleaseFallbacks)
	}
}
