package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalManifestYAML = `bundle_schema_version: "1"
bundle_id: minimal_control
title: "Minimal control"
scf_anchor_id: IAC-06
implementation_type: automated
`

func TestParseDirectory_Minimal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "control.yaml"), []byte(minimalManifestYAML))

	b, err := ParseDirectory(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if b.Manifest.BundleID != "minimal_control" {
		t.Fatalf("bundle_id mismatch: %s", b.Manifest.BundleID)
	}
	if b.ManifestHashHex == "" {
		t.Fatalf("hash should be set")
	}
}

func TestParseDirectory_MissingManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := ParseDirectory(dir)
	if err == nil {
		t.Fatalf("expected error for missing control.yaml")
	}
}

func TestParseDirectory_AppendsDescriptionMD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "control.yaml"), []byte(minimalManifestYAML+"description: \"inline desc\"\n"))
	mustWrite(t, filepath.Join(dir, "description.md"), []byte("Extra narrative."))

	b, err := ParseDirectory(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(b.Manifest.Description, "inline desc") || !strings.Contains(b.Manifest.Description, "Extra narrative") {
		t.Fatalf("description not merged: %q", b.Manifest.Description)
	}
}

func TestParseTarball_RoundTrip(t *testing.T) {
	t.Parallel()
	tarBytes := mustBuildTarball(t, map[string][]byte{
		"control.yaml": []byte(minimalManifestYAML),
	})
	b, err := ParseTarball(bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatalf("parse tarball: %v", err)
	}
	if b.Manifest.BundleID != "minimal_control" {
		t.Fatalf("expected minimal_control; got %s", b.Manifest.BundleID)
	}
}

func TestParseTarball_RejectsAbsolutePath(t *testing.T) {
	t.Parallel()
	tarBytes := mustBuildTarball(t, map[string][]byte{
		"/etc/passwd":  []byte("payload"),
		"control.yaml": []byte(minimalManifestYAML),
	})
	_, err := ParseTarball(bytes.NewReader(tarBytes))
	if err == nil {
		t.Fatalf("expected rejection for absolute-path entry")
	}
	if !strings.Contains(err.Error(), "path traversal") && !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error should mention path traversal/absolute; got %v", err)
	}
}

func TestParseTarball_RejectsParentSegment(t *testing.T) {
	t.Parallel()
	tarBytes := mustBuildTarball(t, map[string][]byte{
		"../../etc/passwd": []byte("payload"),
		"control.yaml":     []byte(minimalManifestYAML),
	})
	_, err := ParseTarball(bytes.NewReader(tarBytes))
	if err == nil {
		t.Fatalf("expected rejection for parent-segment entry")
	}
	if !errors.Is(err, errBundleParse) {
		t.Fatalf("expected ErrBundleMalformed (via errBundleParse), got %T: %v", err, err)
	}
}

func TestParseTarball_RejectsMissingManifestAtRoot(t *testing.T) {
	t.Parallel()
	tarBytes := mustBuildTarball(t, map[string][]byte{
		"subdir/control.yaml": []byte(minimalManifestYAML),
	})
	_, err := ParseTarball(bytes.NewReader(tarBytes))
	if err == nil || !strings.Contains(err.Error(), "missing required control.yaml") {
		t.Fatalf("expected missing-manifest error; got %v", err)
	}
}

func TestParseTarball_RejectsTooManyEntries(t *testing.T) {
	t.Parallel()
	entries := make(map[string][]byte, maxArchiveEntries+5)
	entries["control.yaml"] = []byte(minimalManifestYAML)
	for i := 0; i < maxArchiveEntries+5; i++ {
		entries[filepath.Join("tests", strings.Repeat("a", 1)+rngHexish(i)+".txt")] = []byte("x")
	}
	tarBytes := mustBuildTarball(t, entries)
	_, err := ParseTarball(bytes.NewReader(tarBytes))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected entries-cap error; got %v", err)
	}
}

func TestParseTarball_RejectsHugeUncompressed(t *testing.T) {
	t.Parallel()
	huge := bytes.Repeat([]byte("A"), maxUncompressedBytes+1024)
	tarBytes := mustBuildTarball(t, map[string][]byte{
		"control.yaml": []byte(minimalManifestYAML),
		"tests/big":    huge,
	})
	_, err := ParseTarball(bytes.NewReader(tarBytes))
	if err == nil {
		t.Fatalf("expected uncompressed-cap error")
	}
}

// rngHexish returns a deterministic small identifier — we just need entry
// names to differ so the tar reader doesn't dedupe.
func rngHexish(i int) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(intToStr(i), "0", "z"), "1", "y"), "2", "x"), "3", "w"), "4", "v"), "5", "u"), "6", "t"), "7", "s"), "8", "r"), "9", "q"), "", "")
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// mustBuildTarball produces a gzipped tar archive in memory. Entry order is
// non-deterministic (map iteration) but that's fine for parser tests.
func mustBuildTarball(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, contents := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(contents)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(contents); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
