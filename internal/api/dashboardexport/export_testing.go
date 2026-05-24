// export_testing.go — package-public shims that expose the
// per-format encoders to integration tests in the `_test` package.
//
// The encoders are package-private because production callers MUST
// go through `Handler.ExportDashboard` (which carries the role gate
// + meta-audit + content-type negotiation). The integration suite,
// however, needs to drive the encoders directly for the 50K-row
// streaming-memory assertion (AC-10) without standing up a 50K-row
// Postgres fixture.
//
// The naming convention `*ForTesting` is the slice 137 / 175
// precedent (see `internal/api/anchors/export_testing.go`): a
// package-private symbol exposed under an `*ForTesting`-suffixed
// public wrapper so a code reader sees at a glance "this is a test
// affordance, not a production API."
package dashboardexport

import "io"

// EncodeJSONForTesting is the integration-test affordance over the
// per-package `encodeJSON`. NOT for production callers.
func EncodeJSONForTesting(w io.Writer, s Snapshot) error {
	return encodeJSON(w, s)
}

// EncodeCSVZipForTesting is the integration-test affordance over
// `encodeCSVZip`. NOT for production callers.
func EncodeCSVZipForTesting(w io.Writer, s Snapshot) error {
	return encodeCSVZip(w, s)
}

// EncodeXLSXForTesting is the integration-test affordance over
// `encodeXLSX`. NOT for production callers.
func EncodeXLSXForTesting(w io.Writer, s Snapshot) error {
	return encodeXLSX(w, s)
}
