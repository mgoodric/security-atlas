package scheduler

// export_test.go bridges in-package-only hooks to the external
// scheduler_test package (integration_test.go). Go compiles _test.go files
// into the package under test, so this exported shim is visible to the
// external test package without widening the production API surface.

// SetInlineSweepHookForTest registers a one-shot callback fired after Run's
// immediate pre-tick sweep completes. Test-only: it lets the integration
// suite assert on the inline sweep's RETURNED report instead of racing a
// wall-clock deadline against DB latency (the chronic-flake fix).
func (s *Scheduler) SetInlineSweepHookForTest(fn func(SweepReport, error)) {
	s.setInlineSweepHook(fn)
}
