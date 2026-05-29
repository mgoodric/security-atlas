// Slice 369 — duphelper-lint analyzer tests.
//
// Exercises the analyzer against a curated fixture under testdata/. Each
// `// want` annotation pins the expected diagnostic position; analysistest
// re-types the fixture and asserts the analyzer reports exactly those
// diagnostics.
package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestDupHelperAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, analyzer, "a")
}
