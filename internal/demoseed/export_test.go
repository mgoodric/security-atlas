package demoseed

import "context"

// WithBeforeRecordHook runs fn after ApplyInPlace's writers and before it
// records the rows it added, so a test can commit a row from another
// session mid-seed.
func (s *Seeder) WithBeforeRecordHook(fn func(ctx context.Context)) *Seeder {
	s.beforeRecord = fn
	return s
}
