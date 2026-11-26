package demoseed

import (
	"testing"
	"time"
)

// TestBuildFixtures_PopulationDrawsItsRowCount checks each audit period's
// population against the fixture ledger: its control has records the draw
// selects, and row_count counts exactly those (#3040). A window that has
// not closed yet may draw nothing (#3047).
func TestBuildFixtures_PopulationDrawsItsRowCount(t *testing.T) {
	t.Parallel()
	for _, scale := range []float64{MinScale, 0.2, DefaultScale, 2.0, MaxScale} {
		now := time.Now()
		s := &Seeder{scale: scale, clock: func() time.Time { return now }}
		fs := s.buildFixtures("demo-population")
		frozen := 0
		for _, ap := range fs.auditPeriods {
			var draw int
			for _, e := range fs.evidence {
				if e.ControlID == ap.ControlID && populationHolds(&ap, e.ObservedAt) {
					draw++
				}
			}
			if draw == 0 && ap.PeriodEnd.Before(now) {
				t.Errorf("scale %v: past period %q population draws no records", scale, ap.Name)
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

// TestBuildFixtures_SampleRecordsBelongToPopulation checks every record a
// period's sample links to is in that period's population: its control,
// inside its window and at or before its frozen_at (#3047).
func TestBuildFixtures_SampleRecordsBelongToPopulation(t *testing.T) {
	t.Parallel()
	for _, scale := range []float64{MinScale, DefaultScale, 2.0, MaxScale} {
		now := time.Now()
		s := &Seeder{scale: scale, clock: func() time.Time { return now }}
		fs := s.buildFixtures("demo-sample")
		byID := map[string]evidenceFixture{}
		for _, e := range fs.evidence {
			byID[e.ID.String()] = e
		}
		for _, ap := range fs.auditPeriods {
			if ap.PeriodEnd.Before(now) && len(ap.WindowEvidenceIDs) != samplesPerPeriod {
				t.Errorf("scale %v: past period %q sample links %d records; want %d",
					scale, ap.Name, len(ap.WindowEvidenceIDs), samplesPerPeriod)
			}
			for _, id := range ap.WindowEvidenceIDs {
				e, ok := byID[id.String()]
				if !ok {
					t.Fatalf("scale %v: period %q sample links unknown record %s", scale, ap.Name, id)
				}
				if e.ControlID != ap.ControlID || !populationHolds(&ap, e.ObservedAt) {
					t.Errorf("scale %v: period %q sample links record %s (control %s, observed %s) outside its population",
						scale, ap.Name, id, e.ControlID, e.ObservedAt.Format(time.RFC3339))
				}
			}
		}
	}
}

// TestBuildFixtures_NoFutureEvidence checks no seeded record is observed
// after now, at the largest scale, where later periods open in the future
// (#3047).
func TestBuildFixtures_NoFutureEvidence(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := &Seeder{scale: MaxScale, clock: func() time.Time { return now }}
	fs := s.buildFixtures("demo-future")
	future := 0
	for _, ap := range fs.auditPeriods {
		if ap.PeriodStart.After(now) {
			future++
		}
	}
	if future == 0 {
		t.Fatalf("scale %v seeds no period opening after now; the test does not exercise #3047", MaxScale)
	}
	for _, e := range fs.evidence {
		if e.ObservedAt.After(now.UTC()) {
			t.Errorf("record %s observed %s, after now %s", e.ID, e.ObservedAt.Format(time.RFC3339), now.Format(time.RFC3339))
		}
	}
}
