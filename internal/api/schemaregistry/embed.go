package schemaregistry

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// PlatformSchema is one bundled (kind, semver) pair plus its JSON Schema
// body. Slice 014 ships ten of these; the LoadPlatformSchemas helper
// walks an fs.FS rooted at schemas/ to find them. Keeping the loader
// fs.FS-shaped (rather than os.DirFS) means tests can swap in an
// in-memory FS and the production code does not need to know where
// the files live on disk.
type PlatformSchema struct {
	Kind              string
	Semver            string
	SchemaJSON        []byte
	Owner             string
	DefaultSCFAnchors []string
}

// LoadPlatformSchemas walks `root` looking for `<kind>/<semver>.json`
// files. Each file must be a JSON Schema (draft 2020-12) carrying
// `x-evidence-kind`, `x-semver`, `x-owner`, and optionally
// `x-default-scf-anchors` extensions. Returns the parsed list in
// kind-then-semver order so the loader output is stable across runs.
//
// Files whose directory name contains `..` or whose `x-evidence-kind`
// disagrees with the path are rejected.
func LoadPlatformSchemas(root fs.FS) ([]PlatformSchema, error) {
	var out []PlatformSchema
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		// Skip the README and any top-level non-schema doc.
		if !strings.Contains(p, "/") {
			return nil
		}
		dir := path.Dir(p)
		base := strings.TrimSuffix(path.Base(p), ".json")

		body, err := fs.ReadFile(root, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}

		var meta struct {
			XEvidenceKind     string   `json:"x-evidence-kind"`
			XSemver           string   `json:"x-semver"`
			XOwner            string   `json:"x-owner"`
			XDefaultSCFAnchor []string `json:"x-default-scf-anchors"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			return fmt.Errorf("decode %s: %w", p, err)
		}
		if meta.XEvidenceKind == "" || meta.XSemver == "" || meta.XOwner == "" {
			return fmt.Errorf("%s missing required extension keys (x-evidence-kind / x-semver / x-owner)", p)
		}

		// Path consistency: directory holds the kind family (e.g. sast.scan_result),
		// and x-evidence-kind ends with .v<major>. We just check the kind family
		// matches the directory; a mismatch indicates someone moved a file without
		// updating it.
		expectedDir := strings.TrimSuffix(meta.XEvidenceKind, ".v1")
		expectedDir = strings.TrimSuffix(expectedDir, ".v2")
		expectedDir = strings.TrimSuffix(expectedDir, ".v3")
		if expectedDir == meta.XEvidenceKind {
			// kind without .v<n> suffix — accept but require directory matches it.
			expectedDir = meta.XEvidenceKind
		}
		if dir != expectedDir {
			return fmt.Errorf("%s: directory %q does not match x-evidence-kind %q (expected dir %q)",
				p, dir, meta.XEvidenceKind, expectedDir)
		}
		if base != meta.XSemver {
			return fmt.Errorf("%s: filename semver %q does not match x-semver %q", p, base, meta.XSemver)
		}

		out = append(out, PlatformSchema{
			Kind:              meta.XEvidenceKind,
			Semver:            meta.XSemver,
			SchemaJSON:        body,
			Owner:             meta.XOwner,
			DefaultSCFAnchors: meta.XDefaultSCFAnchor,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Semver < out[j].Semver
	})
	return out, nil
}
