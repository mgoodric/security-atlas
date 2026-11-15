package controlimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// Pure-Go tier (slice-353 Q-2): the export archive's writer and reader, no
// Postgres. The integration suite covers ExportBundles and the re-upload.

func exportedFor(t *testing.T, orgID, title string) ExportedBundle {
	t.Helper()
	b, err := bundleForRow(stagedForDecision{
		ExternalID: orgID, Title: title, Description: "Sheet text: with: colons\n- and a dash",
		TestedUnder: []Requirement{{Code: "CC6.1"}}, FrameworkSlug: "soc2", SCFRelease: "2025.2",
	}, "IAC-06", ApproveRowInput{})
	if err != nil {
		t.Fatalf("bundleForRow: %v", err)
	}
	return ExportedBundle{BundleID: b.Manifest.BundleID, ManifestYAML: b.ManifestYAMLRaw}
}

func TestBundleArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	in := []ExportedBundle{exportedFor(t, "ORG-AC-01", "MFA"), exportedFor(t, "ORG-AC-02", `Quoted "title": yes`)}
	var a, b bytes.Buffer
	if err := WriteBundleArchive(&a, in); err != nil {
		t.Fatalf("WriteBundleArchive: %v", err)
	}
	if err := WriteBundleArchive(&b, in); err != nil {
		t.Fatalf("WriteBundleArchive: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two writes of the same bundles differ; the archive must be deterministic")
	}

	dir := filepath.Join(t.TempDir(), "controls")
	ids, err := ExtractBundleArchive(bytes.NewReader(a.Bytes()), dir)
	if err != nil {
		t.Fatalf("ExtractBundleArchive: %v", err)
	}
	if strings.Join(ids, ",") != "org-ac-01,org-ac-02" {
		t.Errorf("ids = %v", ids)
	}
	for _, want := range in {
		bundle, err := control.ParseDirectory(filepath.Join(dir, want.BundleID))
		if err != nil {
			t.Fatalf("ParseDirectory %s: %v", want.BundleID, err)
		}
		if !bytes.Equal(bundle.ManifestYAMLRaw, want.ManifestYAML) {
			t.Errorf("%s: extracted manifest differs from the exported bytes", want.BundleID)
		}
		m := bundle.Manifest
		if m.SCFAnchorID != "IAC-06" || m.SCFRelease != "2025.2" || len(m.ExternalRefs) != 2 {
			t.Errorf("%s: manifest = %+v, want anchor, release and both refs", want.BundleID, m)
		}
	}

	// Re-extracting into the same directory replaces the manifests.
	if _, err := ExtractBundleArchive(bytes.NewReader(a.Bytes()), dir); err != nil {
		t.Errorf("re-extract into the same directory: %v", err)
	}
}

func TestBundleArchiveEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := WriteBundleArchive(&buf, nil); err != nil {
		t.Fatalf("WriteBundleArchive: %v", err)
	}
	ids, err := ExtractBundleArchive(&buf, t.TempDir())
	if err != nil || len(ids) != 0 {
		t.Errorf("empty archive = %v, %v", ids, err)
	}
}

func TestWriteBundleArchiveRefusesUnsafeNames(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"../etc", "/abs/path", "a/b/c", "ab", ".hidden", "Upper-Case"} {
		err := WriteBundleArchive(&bytes.Buffer{}, []ExportedBundle{{BundleID: id, ManifestYAML: []byte("x")}})
		if err == nil {
			t.Errorf("WriteBundleArchive(%q) succeeded, want a refusal", id)
		}
	}
	big := ExportedBundle{BundleID: "big-one", ManifestYAML: make([]byte, MaxExportBytes+1)}
	if err := WriteBundleArchive(&bytes.Buffer{}, []ExportedBundle{big}); !errors.Is(err, ErrExportTooLarge) {
		t.Errorf("oversized export = %v, want ErrExportTooLarge", err)
	}
}

type entry struct {
	name     string
	typeflag byte
	body     string
	link     string
	size     int64
}

func archiveOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		size := int64(len(e.body))
		if e.size > 0 {
			size = e.size
		}
		if e.typeflag != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.typeflag, Linkname: e.link, Mode: 0o644, Size: size}); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if e.typeflag == tar.TypeReg {
			body := []byte(e.body)
			if e.size > 0 {
				body = make([]byte, e.size)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBundleArchiveRefusesUnsafeEntries(t *testing.T) {
	t.Parallel()
	cases := map[string][]entry{
		"parent traversal":  {{name: "../escape/control.yaml", typeflag: tar.TypeReg, body: "x"}},
		"nested traversal":  {{name: "abc/../../control.yaml", typeflag: tar.TypeReg, body: "x"}},
		"absolute path":     {{name: "/tmp/abc/control.yaml", typeflag: tar.TypeReg, body: "x"}},
		"other file":        {{name: "abc/run.sh", typeflag: tar.TypeReg, body: "x"}},
		"top-level file":    {{name: "control.yaml", typeflag: tar.TypeReg, body: "x"}},
		"deeper file":       {{name: "abc/tests/control.yaml", typeflag: tar.TypeReg, body: "x"}},
		"symlink":           {{name: "abc/control.yaml", typeflag: tar.TypeSymlink, link: "/etc/passwd"}},
		"hardlink":          {{name: "abc/control.yaml", typeflag: tar.TypeLink, link: "/etc/passwd"}},
		"duplicate bundle":  {{name: "abc/control.yaml", typeflag: tar.TypeReg, body: "x"}, {name: "abc/control.yaml", typeflag: tar.TypeReg, body: "y"}},
		"oversize manifest": {{name: "abc/control.yaml", typeflag: tar.TypeReg, size: maxExportManifestBytes + 1}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			dir := filepath.Join(parent, "out")
			_, err := ExtractBundleArchive(bytes.NewReader(archiveOf(t, entries...)), dir)
			if !errors.Is(err, ErrBadExportArchive) {
				t.Fatalf("err = %v, want ErrBadExportArchive", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "escape")); err == nil {
				t.Error("an entry was written outside the target directory")
			}
		})
	}
	if _, err := ExtractBundleArchive(strings.NewReader("not gzip"), t.TempDir()); !errors.Is(err, ErrBadExportArchive) {
		t.Errorf("non-gzip = %v, want ErrBadExportArchive", err)
	}
}

// A symlinked bundle directory already in the target that points outside
// it is not followed: the write fails rather than landing outside.
func TestExtractBundleArchiveDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	dir := filepath.Join(parent, "out")
	for _, d := range []string{outside, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "abc")); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractBundleArchive(bytes.NewReader(archiveOf(t, entry{name: "abc/control.yaml", typeflag: tar.TypeReg, body: "x"})), dir)
	if err == nil {
		t.Fatal("extract through an escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "control.yaml")); err == nil {
		t.Error("control.yaml was written outside the target directory")
	}
}
