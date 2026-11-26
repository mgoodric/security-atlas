package controlimports

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// BundleExporter reads a batch's approved controls as bundles (#1784).
type BundleExporter interface {
	ExportBundles(ctx context.Context, batchID string) ([]controlimport.ExportedBundle, error)
}

// WithExport serves GET /v1/controls/imports/{id}/export.
func (h *Handler) WithExport(e BundleExporter) *Handler {
	h.exporter = e
	return h
}

// Export downloads the batch's approved controls as a gzip-tar of
// <bundle_id>/control.yaml bundles. A batch with nothing approved yields an
// empty archive. The X-Bundle-Count header carries the number of bundles.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	bundles, err := h.exporter.ExportBundles(ctx, id)
	var buf bytes.Buffer
	if err == nil {
		err = controlimport.WriteBundleArchive(&buf, bundles)
	}
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="control-import-%s.tar.gz"`, id))
		w.Header().Set("X-Bundle-Count", strconv.Itoa(len(bundles)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	case errors.Is(err, controlimport.ErrBatchNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
	case errors.Is(err, controlimport.ErrExportTooLarge):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the approved controls exceed the %d MiB export cap", controlimport.MaxExportBytes>>20))
	default:
		httperr.WriteInternal(w, r, "controlimports", err)
	}
}
