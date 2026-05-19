// Package emptyset hosts the slice-150 cross-cutting empty-set robustness
// integration test sweep. The test in this package is intentionally
// orthogonal to the per-handler integration tests in
// internal/api/<handler>/empty_set_integration_test.go: those pin the
// per-package wire-shape contract (200, array-not-null, count==0). This
// sweep is the AUDIT — every GET list/aggregate endpoint the platform
// exposes is hit against a fresh-install zero-row tenant, and any 5xx
// fails the suite.
//
// The convention this enforces is documented in CONTRIBUTING.md
// ("Empty-set robustness"): list endpoints MUST return 200 with an empty
// envelope on 0 rows, never a 500.
package emptyset
