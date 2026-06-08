package control

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Bundle is a parsed control bundle with all relevant bytes captured so the
// caller can hash, persist, and round-trip the manifest verbatim.
type Bundle struct {
	Manifest        Manifest
	ManifestYAMLRaw []byte // verbatim YAML bytes — what we hash and store.
	ManifestHashHex string // sha256 of ManifestYAMLRaw, lowercase hex.
	DescriptionMD   string // contents of optional description.md (empty if absent).

	// TestFiles holds the verbatim bytes of every tests/*.yaml file found in
	// the archive, keyed by the file's basename (e.g. "mfa.yaml"). It is the
	// in-memory analogue of the on-disk tests/ directory the slice-496 runner
	// reads from a filesystem path, captured here so the slice-574 upload gate
	// can run a bundle's declared tests WITHOUT writing the archive to disk.
	//
	// Populated only by ParseTarball (the only ingest path that carries a
	// tests/ tree). ParseDirectory leaves it nil — a directory upload feeds the
	// runner the directory directly. The inline-JSON path (manifest-only)
	// likewise leaves it nil: an inline manifest cannot carry fixtures.
	TestFiles map[string][]byte
}

// testsDirPrefix is the in-archive directory, beside control.yaml, that holds a
// bundle's test-case files (slice 496 bundletest.TestsDirName). Captured by
// ParseTarball so the slice-574 upload gate can run them in memory.
const testsDirPrefix = "tests/"

// maxTestFilesPerBundle bounds how many tests/*.yaml files ParseTarball will
// retain. Generous relative to any realistic bundle; the maxArchiveEntries cap
// already bounds total entries, this just bounds the retained-in-memory subset.
const maxTestFilesPerBundle = 200

// maxTestFileBytes caps a single retained tests/*.yaml file. Mirrors the
// runner's own per-file cap (bundletest.maxTestFileBytes) so the gate rejects a
// pathological fixture file at capture time, before it reaches the evaluator.
const maxTestFileBytes = 4 * 1024 * 1024 // 4 MB

// Limits guarding decompression bombs and accidental large uploads. See
// docs/spec/control-bundle.md §1.
const (
	maxCompressedBytes   = 5 * 1024 * 1024  // 5 MB tarball cap
	maxUncompressedBytes = 20 * 1024 * 1024 // 20 MB after gunzip
	maxArchiveEntries    = 500              // generous; real bundles are <20 files
	maxManifestBytes     = 1 * 1024 * 1024  // control.yaml itself
)

// manifestFilename is the only mandatory file in a bundle.
const manifestFilename = "control.yaml"
const descriptionFilename = "description.md"

// errBundleParse is the sentinel returned for any malformed-bundle failure.
// Callers wrap it for context; the HTTP handler maps it to 400.
var errBundleParse = errors.New("control bundle: parse error")

// ErrBundleMalformed reports a parse / shape failure.
type ErrBundleMalformed struct{ Detail string }

func (e ErrBundleMalformed) Error() string { return "control bundle: " + e.Detail }
func (ErrBundleMalformed) Is(target error) bool {
	return target == errBundleParse
}

// ParseDirectory reads a bundle from a filesystem directory. It loads
// control.yaml (required), description.md (optional), and applies
// ValidateStructural before returning. Tarball / archive ingestion is
// ParseTarball.
func ParseDirectory(root string) (*Bundle, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("control bundle: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("path %s is not a directory; tarballs go through ParseTarball", root)}
	}

	manifestPath := filepath.Join(root, manifestFilename)
	rawYAML, err := readUpTo(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", manifestFilename, err)}
	}

	descPath := filepath.Join(root, descriptionFilename)
	descMD := ""
	if b, err := readUpToOptional(descPath, maxManifestBytes); err != nil {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", descriptionFilename, err)}
	} else if b != nil {
		descMD = string(b)
	}

	return finalizeBundle(rawYAML, descMD)
}

// ParseTarball reads a bundle from a gzip-compressed tar stream. Reader must
// emit at most maxCompressedBytes of gzipped data and decompress to at most
// maxUncompressedBytes; otherwise the call rejects with ErrBundleMalformed.
//
// Tar-slip protection: any entry with an absolute path, a `..` parent
// segment, or a non-regular header type is rejected. The archive root must
// contain control.yaml at the top level (no nested directory).
func ParseTarball(r io.Reader) (*Bundle, error) {
	// First, cap the compressed read so a 1 GB upload doesn't OOM us.
	limited := &countedReader{R: io.LimitReader(r, maxCompressedBytes+1)}
	gz, err := gzip.NewReader(limited)
	if err != nil {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("gzip header: %v", err)}
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(io.LimitReader(gz, maxUncompressedBytes+1))

	var (
		rawYAML   []byte
		descMD    string
		testFiles map[string][]byte
		entries   int
		totalUn   int64
	)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrBundleMalformed{Detail: fmt.Sprintf("tar header: %v", err)}
		}

		entries++
		if entries > maxArchiveEntries {
			return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive exceeds %d entries (decompression-bomb guard)", maxArchiveEntries)}
		}

		// Tar slip / path-traversal defence: reject absolute paths, parent
		// segments, and anything outside the archive root. Use filepath.Clean
		// + the same checks Go's stdlib `archive/tar` documentation
		// recommends. Forward slashes only per the spec.
		clean, ok := sanitizeTarPath(hdr.Name)
		if !ok {
			return nil, ErrBundleMalformed{Detail: fmt.Sprintf("rejected tar entry %q (path traversal or absolute path)", hdr.Name)}
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA is the legacy alias and we accept both.
			// fall through
		case tar.TypeDir:
			// Skip pure directory entries; we recreate the bundle's logical
			// shape from the file paths.
			continue
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// PAX global / extended headers are metadata-only and emit no
			// content of their own; tar.Reader applies them to subsequent
			// entries. Skip silently.
			continue
		default:
			return nil, ErrBundleMalformed{Detail: fmt.Sprintf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)}
		}

		// Cap individual entry size to maxManifestBytes for the two known
		// files we care about; everything else is read but discarded so we
		// still enforce maxUncompressedBytes across the archive.
		var buf []byte
		switch clean {
		case manifestFilename:
			buf, err = io.ReadAll(io.LimitReader(tr, maxManifestBytes+1))
			if err != nil {
				return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", manifestFilename, err)}
			}
			if int64(len(buf)) > maxManifestBytes {
				return nil, ErrBundleMalformed{Detail: fmt.Sprintf("%s exceeds %d bytes", manifestFilename, maxManifestBytes)}
			}
			rawYAML = buf
		case descriptionFilename:
			buf, err = io.ReadAll(io.LimitReader(tr, maxManifestBytes+1))
			if err != nil {
				return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", descriptionFilename, err)}
			}
			if int64(len(buf)) > maxManifestBytes {
				return nil, ErrBundleMalformed{Detail: fmt.Sprintf("%s exceeds %d bytes", descriptionFilename, maxManifestBytes)}
			}
			descMD = string(buf)
		default:
			// tests/*.yaml — capture so the slice-574 upload gate can run the
			// bundle's declared test cases in memory. Everything else is
			// streamed and discarded (we still count its bytes toward the
			// uncompressed-total guard).
			if base, ok := testFileBasename(clean); ok {
				fileBuf, err := io.ReadAll(io.LimitReader(tr, maxTestFileBytes+1))
				if err != nil {
					return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", clean, err)}
				}
				if int64(len(fileBuf)) > maxTestFileBytes {
					return nil, ErrBundleMalformed{Detail: fmt.Sprintf("%s exceeds %d bytes", clean, maxTestFileBytes)}
				}
				if testFiles == nil {
					testFiles = make(map[string][]byte)
				}
				if len(testFiles) >= maxTestFilesPerBundle {
					return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive declares more than %d tests/*.yaml files", maxTestFilesPerBundle)}
				}
				if _, dup := testFiles[base]; dup {
					return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive declares duplicate test file %q", base)}
				}
				testFiles[base] = fileBuf
				totalUn += int64(len(fileBuf))
				if totalUn > maxUncompressedBytes {
					return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive exceeds %d uncompressed bytes", maxUncompressedBytes)}
				}
				continue
			}
			// Stream and discard so the uncompressed-total guard counts every
			// byte; do not retain.
			n, err := io.Copy(io.Discard, tr)
			if err != nil {
				return nil, ErrBundleMalformed{Detail: fmt.Sprintf("read %s: %v", clean, err)}
			}
			totalUn += n
			continue
		}
		totalUn += int64(len(buf))
		if totalUn > maxUncompressedBytes {
			return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive exceeds %d uncompressed bytes", maxUncompressedBytes)}
		}
	}

	// io.LimitReader returns N+1 when over the cap; check after the loop.
	if limited.N > maxCompressedBytes {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive exceeds %d compressed bytes", maxCompressedBytes)}
	}

	if rawYAML == nil {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("archive missing required %s at root", manifestFilename)}
	}

	b, err := finalizeBundle(rawYAML, descMD)
	if err != nil {
		return nil, err
	}
	b.TestFiles = testFiles
	return b, nil
}

// testFileBasename reports whether a cleaned in-archive path is a tests/*.yaml
// (or *.yml) file at the top level of the tests/ directory, and returns its
// basename. A nested path (tests/foo/bar.yaml) or a non-YAML file is not a test
// file. The bundletest loader reads only top-level tests/*.yaml, so the gate
// captures exactly that set.
func testFileBasename(clean string) (string, bool) {
	if !strings.HasPrefix(clean, testsDirPrefix) {
		return "", false
	}
	rel := strings.TrimPrefix(clean, testsDirPrefix)
	if rel == "" || strings.Contains(rel, "/") {
		return "", false // a directory marker or a nested file
	}
	low := strings.ToLower(rel)
	if !strings.HasSuffix(low, ".yaml") && !strings.HasSuffix(low, ".yml") {
		return "", false
	}
	return rel, true
}

// FinalizeBundleForHTTP is the inline-YAML entry point used by the JSON
// upload handler. It parses, validates structurally, and assembles a
// *Bundle without touching the filesystem. Wraps the package-internal
// finalizeBundle so external packages do not need to reach into private
// state.
func FinalizeBundleForHTTP(rawYAML []byte) (*Bundle, error) {
	return finalizeBundle(rawYAML, "")
}

// finalizeBundle unmarshals the YAML, validates, and assembles the Bundle.
func finalizeBundle(rawYAML []byte, descMD string) (*Bundle, error) {
	if len(rawYAML) == 0 {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("%s is empty", manifestFilename)}
	}
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(rawYAML)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, ErrBundleMalformed{Detail: fmt.Sprintf("parse %s: %v", manifestFilename, err)}
	}
	if err := m.ValidateStructural(); err != nil {
		// ValidateStructural already returns "control bundle: ..."; strip
		// the prefix so ErrBundleMalformed doesn't double-prepend it.
		msg := err.Error()
		msg = strings.TrimPrefix(msg, "control bundle: ")
		return nil, ErrBundleMalformed{Detail: msg}
	}
	if descMD != "" && m.Description != "" {
		m.Description = m.Description + "\n\n" + descMD
	} else if descMD != "" {
		m.Description = descMD
	}

	sum := sha256.Sum256(rawYAML)
	return &Bundle{
		Manifest:        m,
		ManifestYAMLRaw: append([]byte(nil), rawYAML...),
		ManifestHashHex: hex.EncodeToString(sum[:]),
		DescriptionMD:   descMD,
	}, nil
}

// readUpTo opens a file and reads up to limit+1 bytes. If the file is larger
// than limit, the read returns an error; this protects against attempts to
// read a multi-GB malicious YAML.
func readUpTo(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return b, nil
}

// readUpToOptional is readUpTo but returns (nil, nil) when the file is absent.
func readUpToOptional(path string, limit int64) ([]byte, error) {
	b, err := readUpTo(path, limit)
	if err == nil {
		return b, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, err
}

// sanitizeTarPath returns the cleaned-relative form of hdr.Name and `ok=true`
// when the path is safe to use as an in-archive identifier. We reject:
//
//   - absolute paths ("/foo")
//   - paths containing ".." segments after Clean
//   - paths that escape the archive root after Clean
//   - empty paths
//
// The cleaned path is returned with forward slashes; callers compare against
// constants like "control.yaml" and "description.md".
func sanitizeTarPath(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	// Reject Windows-style absolute paths as well — `tar` spec allows them
	// but our format does not.
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":\\") {
		return "", false
	}
	// filepath.Clean uses the host separator; replace before/after so we
	// stay platform-stable.
	withFwd := strings.ReplaceAll(name, "\\", "/")
	clean := filepath.ToSlash(filepath.Clean(withFwd))
	if clean == "." || clean == ".." {
		return "", false
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", false
		}
	}
	// After Clean, anything starting with "/" is still absolute on some
	// platforms; reject as a second belt-and-braces guard.
	if strings.HasPrefix(clean, "/") {
		return "", false
	}
	return clean, true
}

// countedReader counts bytes consumed from R. We use it to detect when the
// compressed-cap LimitReader hit its limit (it returns EOF silently
// otherwise).
type countedReader struct {
	R io.Reader
	N int64
}

func (c *countedReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.N += int64(n)
	return n, err
}
