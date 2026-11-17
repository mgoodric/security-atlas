package controls

// #2110: GET /v1/controls/{id}/bundle — Download bundle on control detail.
//
// The archive is the shape POST /v1/controls:upload-bundle reads
// (control.ParseTarball): a gzip-tar with control.yaml at its root, the
// manifest the store holds for the control byte for byte. So a download
// re-uploads as a no-op, and an edited download uploads as a new version.
// The manifest is what the tenant uploaded: control text, applicability and
// evidence queries, no credentials (a bundle declares none; connectors hold
// source-side credentials). The read runs in the caller's tenant under RLS.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// bundleManifestReader is the download seam; *control.Store satisfies it.
type bundleManifestReader interface {
	BundleManifest(ctx context.Context, id uuid.UUID) (string, []byte, error)
}

// downloadBundleID keeps the bundle_id safe to put in a filename header.
var downloadBundleID = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)

// BundleDownloadHandler serves GET /v1/controls/{id}/bundle.
type BundleDownloadHandler struct {
	store bundleManifestReader
}

// NewBundleDownloadHandler wires the handler over the control store.
func NewBundleDownloadHandler(store *control.Store) *BundleDownloadHandler {
	return &BundleDownloadHandler{store: store}
}

// Download writes the control's bundle as <bundle_id>.tar.gz.
func (h *BundleDownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}
	bundleID, manifest, err := h.store.BundleManifest(r.Context(), id)
	if errors.Is(err, control.ErrControlNotFound) {
		httpresp.WriteError(w, http.StatusNotFound, "control not found")
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "controls bundle download", err)
		return
	}
	// A control seeded straight into the table, not through the bundle
	// upload, has no stored manifest; an empty control.yaml would not
	// upload back, so there is no bundle to download.
	if len(bytes.TrimSpace(manifest)) == 0 {
		httpresp.WriteError(w, http.StatusNotFound, "control has no stored bundle")
		return
	}
	var buf bytes.Buffer
	if err := writeControlBundle(&buf, manifest); err != nil {
		httperr.WriteInternal(w, r, "controls bundle archive", err)
		return
	}
	name := "control-bundle"
	if downloadBundleID.MatchString(bundleID) {
		name = bundleID
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// writeControlBundle writes a gzip-tar holding control.yaml at the root,
// with a fixed mode and timestamp so the same manifest gives the same bytes.
func writeControlBundle(w *bytes.Buffer, manifest []byte) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "control.yaml",
		Mode:     0o644,
		Size:     int64(len(manifest)),
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	if _, err := tw.Write(manifest); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
