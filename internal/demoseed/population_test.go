package demoseed

import (
	"testing"
	"time"
)

// TestBuildFixtures_PopulationDrawsItsRowCount checks each audit period's
// population against the fixture ledger: its control has records the draw
// selects, and row_count counts exactly those (#3040).
func TestBuildFixtures_PopulationDrawsItsRowCount(t *testing.T) {
	t.Parallel()
	for _, scale := range []float64{MinScale, 0.2, DefaultScale, 2.0} {
		s := &Seeder{scale: scale, clock: time.Now}
		fs := s.buildFixtures("demo-population")
		frozen := 0
		for _, ap := range fs.auditPeriods {
			var draw int
			for _, e := range fs.evidence {
				if e.ControlID != ap.ControlID || e.ObservedAt.Before(ap.PeriodStart) || e.ObservedAt.After(ap.PeriodEnd) {
					continue
				}
				if ap.FrozenAt != nil && e.ObservedAt.After(*ap.FrozenAt) {
					continue
				}
				draw++
			}
			if draw == 0 {
				t.Errorf("scale %v: period %q population draws no records", scale, ap.Name)
			}
			if draw != ap.RowCount {
				t.Errorf("scale %v: period %q population draws %d records; row_count %d", scale, ap.Name, draw, ap.RowCount)
			}
			if ap.Frozen {
				frozen++
			}
		}
		if frozen == 0 {
			t.Errorf("scale %v: no frozen period", scale)
		}
	}
}
