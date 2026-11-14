// Package controlimports serves the control import upload API (#1779).
//
// Routes (appended onto the platform root router):
//
//	POST /v1/controls/imports        multipart upload -> staged import batch
//	GET  /v1/controls/imports/{id}   the batch with its counts and rows
//
// The upload's multipart fields:
//
//	file        the CSV or XLSX control sheet (required)
//	framework   slug of the framework the tested-under codes belong to (required)
//	column_map  optional, repeatable "field=Header" overrides of header detection
//
// Every non-blank data row is returned either staged or rejected with a
// reason. Tenant scoping is enforced by RLS via controlimport.Store.
package controlimports

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// multipartOverhead is the allowance for multipart framing and the small
// form fields on top of the file's own size cap.
const multipartOverhead = 64 * 1024

// Handler serves the control import routes.
type Handler struct {
	stager *controlimport.Stager
}

func New(stager *controlimport.Stager) *Handler { return &Handler{stager: stager} }

// RegisterRoutes appends the routes onto the supplied router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/controls/imports", h.Create)
	r.Get("/v1/controls/imports/{id}", h.Get)
}

// Create stages an uploaded control sheet as an import batch.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, _ := authctx.CredentialFromContext(ctx)

	r.Body = http.MaxBytesReader(w, r.Body, controlimport.MaxUploadBytes+multipartOverhead)
	if err := r.ParseMultipartForm(controlimport.MaxUploadBytes + multipartOverhead); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "request must be multipart/form-data with a file field")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer func() { _ = file.Close() }()

	raw, err := controlimport.ReadAllBounded(file)
	if err != nil {
		if errors.Is(err, controlimport.ErrUploadTooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "could not read upload")
		return
	}
	columnMap, err := parseColumnMap(r.MultipartForm.Value["column_map"])
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	batch, err := h.stager.Stage(ctx, controlimport.StageInput{
		Filename:      header.Filename,
		Raw:           raw,
		FrameworkSlug: r.FormValue("framework"),
		ColumnMap:     columnMap,
		UploadedBy:    cred.ID,
	})
	if err != nil {
		writeStageError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, batch)
}

// Get returns one import batch with its staged and rejected rows.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	batch, err := h.stager.Get(ctx, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, controlimport.ErrBatchNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
			return
		}
		httperr.WriteInternal(w, r, "controlimports", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, batch)
}

func writeStageError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, controlimport.ErrUploadTooLarge):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
	case errors.Is(err, controlimport.ErrWorkbookTooLarge):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("workbook expands past the %d MiB uncompressed cap; split it into smaller files", controlimport.MaxUnzipBytes>>20))
	case errors.Is(err, controlimport.ErrTooManyRows):
		httpresp.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("control file exceeds the %d-row cap; split it into smaller files", controlimport.MaxRows))
	case errors.Is(err, controlimport.ErrTooManyColumns):
		httpresp.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("a row has a value past column %d; remove the stray cells or split the sheet", controlimport.MaxColumns))
	case errors.Is(err, controlimport.ErrFrameworkRequired):
		httpresp.WriteError(w, http.StatusBadRequest, "framework is required: the slug of the framework the tested-under codes belong to")
	case errors.Is(err, controlimport.ErrUnknownFramework):
		httpresp.WriteError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "controlimport: "))
	case errors.Is(err, controlimport.ErrUnsupportedFormat),
		errors.Is(err, controlimport.ErrEmptyFile),
		errors.Is(err, controlimport.ErrNoHeaderRow),
		errors.Is(err, controlimport.ErrInvalidColumnMap),
		errors.Is(err, controlimport.ErrMappedColumnAbsent):
		httpresp.WriteError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "controlimport: "))
	default:
		httperr.WriteInternal(w, r, "controlimports", err)
	}
}

func sizeCapMessage() string {
	return fmt.Sprintf("upload exceeds the %d MiB size cap; split it into smaller files", controlimport.MaxUploadBytes/(1024*1024))
}

// parseColumnMap turns repeated "field=Header" values into a column map.
func parseColumnMap(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		field, header, ok := strings.Cut(v, "=")
		field = strings.TrimSpace(field)
		header = strings.TrimSpace(header)
		if !ok || field == "" || header == "" {
			return nil, fmt.Errorf("column_map entry %q must be field=Header", v)
		}
		if _, dup := out[field]; dup {
			return nil, fmt.Errorf("column_map maps field %q more than once", field)
		}
		out[field] = header
	}
	return out, nil
}
