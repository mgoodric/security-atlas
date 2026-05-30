// Slice 367 — errleak-lint analyzer tests.
//
// These exercise the analyzer against curated fixtures under testdata/.
// Each fixture file is a tiny Go program with `// want` annotations that
// pin the expected diagnostic position. The analysistest framework
// re-types the fixture and asserts the analyzer reports exactly those
// diagnostics in those positions.
package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestErrLeakAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, analyzer, "a")
}
