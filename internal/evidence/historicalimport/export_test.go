package historicalimport

// SetTestHooks installs the per-attempt seams the integration tests use to
// run a freeze at a chosen point inside an import's transaction (#1742).
func SetTestHooks(im *Importer, afterBegin, afterLock func(attempt int)) {
	im.afterBegin = afterBegin
	im.afterLock = afterLock
}
