package controlimport

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// #1784: a batch's approved controls download as control.yaml bundles, one
// directory per bundle in a gzip-tar, so a team can keep them in version
// control. Each control.yaml is the manifest the control store holds for
// the control's active version, byte for byte: it already declares the
// anchor, its SCF release and the external references, and re-uploading it
// matches the stored manifest hash, so the upload is a no-op.

// MaxExportBytes caps the manifest bytes one export carries, on the server
// writing the archive and on the CLI reading it.
const MaxExportBytes = 32 << 20

// maxExportManifestBytes is the control bundle parser's control.yaml cap.
const maxExportManifestBytes = 1 << 20

// ExportManifestName is the file each bundle directory holds.
const ExportManifestName = "control.yaml"

var (
	// ErrExportTooLarge is an export whose manifests pass MaxExportBytes.
	ErrExportTooLarge = errors.New("controlimport: export exceeds the size cap")
	// ErrBadExportArchive is an archive entry that is not
	// <bundle_id>/control.yaml, or an archive past the caps.
	ErrBadExportArchive = errors.New("controlimport: not a control import export archive")
)

// exportBundleID is the control bundle_id pattern. An entry name built from
// it can hold neither a separator nor "..", and never starts with ".".
var exportBundleID = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)

// ExportedBundle is one approved control's bundle.
type ExportedBundle struct {
	BundleID     string
	ControlID    string
	ManifestYAML []byte
}

// ExportBundles returns the bundles of the batch's approved rows, in row
// order: for each, the manifest of the active version of the control with
// the row's bundle ID. A row whose control has no active version is left
// out.
func (s *Store) ExportBundles(ctx context.Context, batchID string) ([]ExportedBundle, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return nil, ErrBatchNotFound
	}
	var out []ExportedBundle
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID string) error {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM control_import_batches WHERE id = $1::uuid)`,
			batchID).Scan(&found); err != nil {
			return fmt.Errorf("controlimport: read batch: %w", err)
		}
		if !found {
			return ErrBatchNotFound
		}
		rows, err := tx.Query(ctx, `
			SELECT c.bundle_id, c.id::text, c.bundle_manifest_yaml
			FROM control_import_rows r
			JOIN controls c ON c.tenant_id = r.tenant_id AND c.bundle_id = r.bundle_id AND c.superseded_by IS NULL
			WHERE r.batch_id = $1::uuid AND r.tenant_id = $2::uuid AND r.status = 'approved'
			ORDER BY r.row_number
		`, batchID, tenantID)
		if err != nil {
			return fmt.Errorf("controlimport: read approved controls: %w", err)
		}
		defer rows.Close()
		total := 0
		for rows.Next() {
			var b ExportedBundle
			var manifest string
			if err := rows.Scan(&b.BundleID, &b.ControlID, &manifest); err != nil {
				return fmt.Errorf("controlimport: scan approved control: %w", err)
			}
			total += len(manifest)
			if total > MaxExportBytes {
				return ErrExportTooLarge
			}
			b.ManifestYAML = []byte(manifest)
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WriteBundleArchive writes the bundles as a gzip-tar with one
// <bundle_id>/control.yaml entry each. The archive is deterministic: fixed
// modes and timestamps, so the same bundles give the same bytes.
func WriteBundleArchive(w io.Writer, bundles []ExportedBundle) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	total := 0
	for _, b := range bundles {
		if !exportBundleID.MatchString(b.BundleID) {
			return fmt.Errorf("controlimport: bundle_id %q cannot name an archive entry", b.BundleID)
		}
		total += len(b.ManifestYAML)
		if total > MaxExportBytes {
			return ErrExportTooLarge
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     b.BundleID + "/",
			Mode:     0o755,
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeDir,
		}); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     b.BundleID + "/" + ExportManifestName,
			Mode:     0o644,
			Size:     int64(len(b.ManifestYAML)),
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		if _, err := tw.Write(b.ManifestYAML); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ExtractBundleArchive writes an export archive's bundles under dir, which
// is created if absent, and returns the bundle IDs written in archive
// order. Only <bundle_id>/control.yaml files and their directories are
// accepted; any other entry, a manifest past the parser's cap or an archive
// past MaxExportBytes is refused. Writes go through an os.Root on dir, so
// nothing lands outside it, symlinks included. An existing control.yaml of
// the same bundle is replaced: re-exporting into a checkout updates it.
func ExtractBundleArchive(r io.Reader, dir string) ([]string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadExportArchive, err)
	}
	defer func() { _ = gz.Close() }()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("controlimport: create %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("controlimport: open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(gz)
	var written []string
	seen := map[string]bool{}
	total := int64(0)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return written, fmt.Errorf("%w: %v", ErrBadExportArchive, err)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		bundleID, file := path.Split(name)
		bundleID = strings.TrimSuffix(bundleID, "/")
		switch {
		case hdr.Typeflag == tar.TypeDir && bundleID == "" && exportBundleID.MatchString(file):
			continue
		case hdr.Typeflag != tar.TypeReg || file != ExportManifestName || !exportBundleID.MatchString(bundleID):
			return written, fmt.Errorf("%w: unexpected entry %q", ErrBadExportArchive, hdr.Name)
		case seen[bundleID]:
			return written, fmt.Errorf("%w: bundle %s appears twice", ErrBadExportArchive, bundleID)
		case hdr.Size > maxExportManifestBytes:
			return written, fmt.Errorf("%w: %s exceeds the %d-byte manifest cap", ErrBadExportArchive, hdr.Name, maxExportManifestBytes)
		}
		total += hdr.Size
		if total > MaxExportBytes {
			return written, fmt.Errorf("%w: archive exceeds the %d MiB cap", ErrBadExportArchive, MaxExportBytes>>20)
		}
		seen[bundleID] = true
		if err := writeRootFile(root, bundleID, io.LimitReader(tr, hdr.Size)); err != nil {
			return written, err
		}
		written = append(written, bundleID)
	}
	return written, nil
}

func writeRootFile(root *os.Root, bundleID string, content io.Reader) error {
	if err := root.Mkdir(bundleID, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("controlimport: create bundle directory %s: %w", bundleID, err)
	}
	f, err := root.OpenFile(bundleID+"/"+ExportManifestName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("controlimport: write %s/%s: %w", bundleID, ExportManifestName, err)
	}
	if _, err := io.Copy(f, content); err != nil {
		_ = f.Close()
		return fmt.Errorf("controlimport: write %s/%s: %w", bundleID, ExportManifestName, err)
	}
	return f.Close()
}
