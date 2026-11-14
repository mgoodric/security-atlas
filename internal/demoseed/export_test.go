package demoseed

import "context"

// WithBeforeRecordHook runs fn after ApplyInPlace's writers and before it
// records the rows it added, so a test can commit a row from another
// session mid-seed.
func (s *Seeder) WithBeforeRecordHook(fn func(ctx context.Context)) *Seeder {
	s.beforeRecord = fn
	return s
}

// WithSCFRelease overrides the SCF release the posture spine adopts anchors
// from, so a test can name a fixture release.
func (s *Seeder) WithSCFRelease(label string) *Seeder {
	s.scfRelease = label
	return s
}

// IsTenantAuditTable exposes isTenantAuditTable to the integration tests,
// so "exactly the pre-seed rows" excludes the same tables the seed does.
var IsTenantAuditTable = isTenantAuditTable
