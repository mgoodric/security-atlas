//go:build integration

// #2210: the monthly brief reports coverage from the same per-framework
// posture as the board pack.

package board_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// The brief and the pack report the same coverage figure, and the same
// per-framework rows, for one tenant and one as-of time.
func TestBriefAndPack_SameCoverage(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// A: 3 of 4 covered (75%); B: 1 of 5 covered (20%); C unscoped.
	// The mean, 47.5, is not a whole number, so both have to carry the
	// same decimal.
	seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework A (#2210)", reqs: 4, coveredNow: 3, coveredThen: 1, activate: true})
	seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework B (#2210)", reqs: 5, coveredNow: 1, coveredThen: 0, activate: true})
	seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework C (#2210)", reqs: 2, coveredNow: 2, coveredThen: 2, activate: false})

	now := time.Now().UTC()
	periodEnd := now.Format("2006-01-02")
	pack, err := board.NewPackGenerator(board.NewPackStore(app), nil).
		WithClock(func() time.Time { return now }).
		Generate(ctx, periodEnd)
	if err != nil {
		t.Fatalf("pack Generate: %v", err)
	}
	brief, err := board.NewGenerator(board.NewStore(app), fixedFreshness{}, fixedDrift{}).
		WithClock(func() time.Time { return now }).
		Generate(ctx, periodEnd)
	if err != nil {
		t.Fatalf("brief Generate: %v", err)
	}

	trendSec := pack.Content.Sections[board.SectionCoverageTrend]
	cov := brief.Content.Coverage
	if cov == nil {
		t.Fatal("brief has no coverage figure")
	}
	if cov.CoveragePct != trendSec.Data.CoveragePct || cov.ScopedFrameworkCount != trendSec.Data.ScopedFrameworks() {
		t.Fatalf("brief coverage (%v over %d) != pack coverage_trend (%v over %d)",
			cov.CoveragePct, cov.ScopedFrameworkCount, trendSec.Data.CoveragePct, trendSec.Data.ScopedFrameworks())
	}
	if cov.CoveragePct != 47.5 || cov.ScopedFrameworkCount != 2 {
		t.Errorf("coverage = %v over %d, want 47.5 over 2", cov.CoveragePct, cov.ScopedFrameworkCount)
	}
	if !strings.Contains(brief.NarrativeMd, "Requirement coverage averages 47.5% across the 2 frameworks with an activated scope.") ||
		!strings.Contains(trendSec.TemplatedText, "averages 47.5% across the 2 frameworks") {
		t.Errorf("brief and pack narratives do not state the same figure\nbrief:\n%s\npack: %s",
			brief.NarrativeMd, trendSec.TemplatedText)
	}

	packRows := map[string]board.PackFrameworkPosture{}
	for _, p := range pack.Content.Sections[board.SectionPosture].Data.Frameworks {
		packRows[p.FrameworkVersionID] = p
	}
	if len(brief.Content.Frameworks) != len(packRows) {
		t.Fatalf("brief has %d framework rows, pack has %d", len(brief.Content.Frameworks), len(packRows))
	}
	for _, b := range brief.Content.Frameworks {
		if p := packRows[b.FrameworkVersionID]; p != b {
			t.Errorf("framework %s: brief row %+v != pack row %+v", b.Name, b, p)
		}
	}
}

// A tenant with no activated scope gets no coverage figure in the brief,
// never 0%.
func TestBrief_NoActivatedScopeHasNoCoverage(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	seedPostureFramework(t, admin, tenant, postureFramework{name: "Framework U (#2210)", reqs: 2, coveredNow: 1, activate: false})

	now := time.Now().UTC()
	brief, err := board.NewGenerator(board.NewStore(app), fixedFreshness{}, fixedDrift{}).
		WithClock(func() time.Time { return now }).
		Generate(ctx, now.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("brief Generate: %v", err)
	}
	if cov := brief.Content.Coverage; cov == nil || cov.ScopedFrameworkCount != 0 {
		t.Fatalf("coverage = %+v, want zero scoped frameworks", cov)
	}
	if !strings.Contains(brief.NarrativeMd, "No framework has an activated scope, so no coverage figure is reported.") {
		t.Errorf("narrative does not say no framework is scoped:\n%s", brief.NarrativeMd)
	}
	if strings.Contains(brief.NarrativeMd, "averages") {
		t.Errorf("narrative states a coverage average with no scoped framework:\n%s", brief.NarrativeMd)
	}
}
