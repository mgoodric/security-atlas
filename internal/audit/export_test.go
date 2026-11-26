package audit

// SetDrawSampleAfterRevision installs the DrawSample seam that runs between
// reading the population's freeze revision and reading its evidence (#1742),
// and returns a func that removes it.
func SetDrawSampleAfterRevision(fn func()) func() {
	drawSampleAfterRevision = fn
	return func() { drawSampleAfterRevision = nil }
}
