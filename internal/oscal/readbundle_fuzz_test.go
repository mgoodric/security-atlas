package oscal

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzReadBundle drives the OSCAL bundle reader's manifest-parse path with
// arbitrary bytes. ReadBundle loads an auditor-/operator-supplied bundle
// directory: it parses manifest.json, validates member filenames against
// path traversal, and re-reads members. A malformed-manifest panic is a
// DoS on the `verify` / bundle-read path (slice 421, headline threat = DoS).
//
// ReadBundle takes a directory, so the fuzz target writes the candidate
// bytes as manifest.json into a temp dir (t.TempDir, auto-cleaned) and
// calls ReadBundle — exercising the ReadFile → json.Unmarshal → UUID-parse
// → member-iteration → traversal-guard seam.
//
// Contract asserted: ReadBundle returns either a clean error OR a non-nil
// *Bundle — it never panics, and it never escapes the bundle directory for
// a hostile member filename (threat T — parser confusion / path traversal,
// already guarded in bundle.go; the fuzz target proves the guard holds for
// arbitrary manifests). No recover() (P0-421-1).
func FuzzReadBundle(f *testing.F) {
	// Seed corpus: golden-shaped manifests (the round-trip + path-traversal
	// fixtures the unit tests pin) plus adversarial JSON shapes. Synthetic /
	// golden-derived only — no tenant data (P0-421-4).
	seeds := [][]byte{
		[]byte(`{
  "schema_version": "oscal-export-bundle/v1",
  "audit_period_id": "11111111-1111-1111-1111-111111111111",
  "frozen_at": "2026-05-01T00:00:00Z",
  "oscal_version": "1.1.2",
  "generated_at": "2026-05-14T12:00:00Z",
  "requested_by": "tester",
  "members": [{"filename": "ssp.json", "model_type": "system-security-plan", "sha256": "00", "size_bytes": 1}],
  "signature": {"algorithm": "ed25519", "digest": "00", "signature": "00", "public_key": "00"}
}`),
		// Hostile member filename (path traversal) — guard must reject cleanly.
		[]byte(`{"schema_version":"oscal-export-bundle/v1","audit_period_id":"11111111-1111-1111-1111-111111111111","frozen_at":"x","oscal_version":"x","generated_at":"x","requested_by":"x","members":[{"filename":"../escape.json","model_type":"x","sha256":"00","size_bytes":1}]}`),
		[]byte(`{"audit_period_id":"not-a-uuid","members":[]}`),
		[]byte(`{}`),
		[]byte(`null`),
		[]byte(``),
		[]byte(`{"members":[{"filename":""}]}`),
		[]byte(`{"audit_period_id":"11111111-1111-1111-1111-111111111111","members":[{"filename":"manifest.json"}]}`),
		[]byte(`{"audit_period_id":"11111111-1111-1111-1111-111111111111","members":[{"filename":"/abs/path.json"}]}`),
		[]byte("\xff\xfe not json"),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, manifest []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ManifestFilename), manifest, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		b, err := ReadBundle(dir)
		if err != nil {
			return // clean error is an acceptable outcome
		}
		// On success the bundle must be non-nil. A nil bundle with a nil
		// error would be a silent mis-parse (T).
		if b == nil {
			t.Fatalf("ReadBundle returned nil bundle and nil error for %d-byte manifest", len(manifest))
		}
		// Every member that survived the read must be a bare basename — the
		// traversal guard in ReadBundle must never let a path-separator name
		// through (threat T / path traversal).
		for _, m := range b.Members {
			if filepath.Base(m.Filename) != m.Filename {
				t.Fatalf("ReadBundle accepted a non-basename member filename %q", m.Filename)
			}
		}
	})
}
