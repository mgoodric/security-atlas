package scfimport

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad drives the SCF catalog importer's file-load + JSON-parse +
// validate path with arbitrary bytes. Load ingests an operator-supplied
// catalog JSON file (untrusted input); a malformed-input panic is a DoS
// on the catalog-import path (slice 421, headline threat = DoS).
//
// Load takes a path, so the fuzz target writes the candidate bytes to a
// temp file (t.TempDir, auto-cleaned) and calls Load — exercising the
// exact ReadFile → json.Unmarshal → validate seam an operator hits.
//
// Contract asserted: Load returns either a clean error OR a non-nil
// *Catalog — it never panics. No recover() (P0-421-1). This is the
// PARSE path only; Import (DB transaction) is integration-tested
// separately and out of fuzz scope (invariant #2: ingestion stage).
func FuzzLoad(f *testing.F) {
	// Seed from the real golden catalog fixture (synthetic test data,
	// no tenant rows — P0-421-4) plus adversarial JSON shapes.
	if golden, err := os.ReadFile("../../../migrations/fixtures/scf-sample.json"); err == nil {
		f.Add(golden)
	}
	for _, s := range [][]byte{
		[]byte(`{"schema_version":"1.0","release_version":"test-2026.1","controls":[{"scf_id":"IAC-01","family":"IAC","title":"t"}]}`),
		[]byte(`{}`),
		[]byte(``),
		[]byte(`null`),
		[]byte(`{"schema_version":"1.0","release_version":"v","controls":[]}`),
		[]byte(`{"schema_version":"1.0","release_version":"v","controls":[{}]}`),
		[]byte(`{"controls":` + `[` + `]}`),
		[]byte(`[1,2,3]`),
		[]byte("\xff\xfe not json"),
		[]byte(`{"schema_version":"1.0","release_version":"v","controls":[{"scf_id":"x","family":"f","title":"t","subtopics":[{"id":"a","text":"b"}]}]}`),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "catalog.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write temp catalog: %v", err)
		}
		cat, err := Load(path)
		if err != nil {
			return // clean error is an acceptable outcome
		}
		// On success Load must return a non-nil, validated catalog. A nil
		// catalog with a nil error would be a silent mis-parse (T).
		if cat == nil {
			t.Fatalf("Load returned nil catalog and nil error for %d-byte input", len(data))
		}
	})
}
