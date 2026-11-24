package ingest

// SetTestHooks installs Process's transaction hooks (#3038) on s.
func SetTestHooks(s *Service, afterBegin, beforeCommit func(attempt int)) {
	s.afterBegin = afterBegin
	s.beforeCommit = beforeCommit
}
