package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// buildTarballForUpload prepares the upload body. For a tarball path we read
// the file verbatim (server hashes the inner manifest, not the tar bytes).
// For a directory we create a deterministic gzip-tar with control.yaml at
// the root, plus description.md if it exists.
//
// Determinism matters: the server stores `bundle_manifest_hash` over the
// inner manifest YAML, not over the tar bytes, so two different tar layouts
// that contain the same control.yaml produce the same hash. We only need
// the tar to be valid and small.
func buildTarballForUpload(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		low := strings.ToLower(path)
		if !strings.HasSuffix(low, ".tar.gz") && !strings.HasSuffix(low, ".tgz") {
			return nil, fmt.Errorf("expected directory or *.tar.gz path: %s", path)
		}
		return os.ReadFile(path)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// Walk the directory; include control.yaml + description.md + anything
	// under tests/. Anything else is ignored.
	if err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		if rel == "." || d.IsDir() {
			return nil
		}
		if !shouldIncludeBundlePath(rel) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:     filepath.ToSlash(rel),
			Mode:     int64(fi.Mode().Perm()),
			Size:     fi.Size(),
			ModTime:  fi.ModTime(),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	}); err != nil {
		return nil, fmt.Errorf("walk %s: %w", path, err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func shouldIncludeBundlePath(rel string) bool {
	switch rel {
	case "control.yaml", "description.md":
		return true
	}
	return strings.HasPrefix(rel, "tests/") || strings.HasPrefix(rel, "tests\\")
}
