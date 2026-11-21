// Package riskimports serves the risk register import API (#2839, spec
// #2812), following the controls import's routes (internal/api/
// controlimports).
//
// Routes (appended onto the platform root router):
//
//	POST /v1/risks/imports          multipart upload -> staged import batch
//	GET  /v1/risks/imports/{id}     the batch with its staged and rejected rows
//	POST /v1/risks/imports/{id}/approve
//	                                approve every pending row; each becomes a
//	                                risk through the risk store
//	POST /v1/risks/imports/{id}/rows/{row}/reject
//	                                reject one staged row with a reason
//
// The upload's multipart fields:
//
//	file        the CSV or XLSX risk register (required)
//	column_map  optional, repeatable "field=Header" overrides of header detection
//
// Upload is a write on risks at the authz layer, which admin and
// grc_engineer hold. Approve and reject also require a tenant approver who
// is a person: a machine credential is refused whatever its role. Tenant
// scoping is enforced by RLS via riskimport.Store.
package riskimports

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// multipartOverhead is the allowance for multipart framing and the small
// form fields on top of the file's own size cap.
const multipartOverhead = 64 * 1024

// maxDecisionBody bounds a reject body.
const maxDecisionBody = 16 * 1024

// Handler serves the risk import routes.
type Handler struct {
	store *riskimport.Store
}

func New(store *riskimport.Store) *Handler { return &Handler{store: store} }

// RegisterRoutes appends the routes onto the supplied router. They are
// literal /v1/risks/imports segments, so register them ahead of the
// generic /v1/risks/{id} routes.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/risks/imports", h.Create)
	r.Get("/v1/risks/imports/{id}", h.Get)
	r.Post("/v1/risks/imports/{id}/approve", h.Approve)
	r.Post("/v1/risks/imports/{id}/rows/{row}/reject", h.Reject)
}

// Create stages an uploaded risk register as an import batch.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, _ := authctx.CredentialFromContext(ctx)

	r.Body = http.MaxBytesReader(w, r.Body, riskimport.MaxUploadBytes+multipartOverhead)
	if err := r.ParseMultipartForm(riskimport.MaxUploadBytes + multipartOverhead); err != nil {
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

	raw, err := riskimport.ReadAllBounded(file)
	if err != nil {
		if errors.Is(err, riskimport.ErrUploadTooLarge) {
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
	batch, err := h.store.Stage(ctx, riskimport.StageInput{
		Filename:   header.Filename,
		Raw:        raw,
		ColumnMap:  columnMap,
		UploadedBy: credstore.PrincipalOf(cred),
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
	batch, err := h.store.Get(ctx, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, riskimport.ErrBatchNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
			return
		}
		httperr.WriteInternal(w, r, "riskimports", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, batch)
}

// Approve approves every pending row of the batch. It takes no body. The
// response reports the rows approved (with the risk each created), marked
// duplicate and skipped with their reasons.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	res, err := h.store.ApprovePending(r.Context(), riskimport.ApproveInput{
		BatchID:  chi.URLParam(r, "id"),
		Approver: approver,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, res)
}

type rejectBody struct {
	Reason string `json:"reason"`
}

// Reject rejects a staged row with a reason ({"reason": "..."}).
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	var body rejectBody
	dec := json.NewDecoder(io.LimitReader(r.Body, maxDecisionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return
	}
	d, err := h.store.RejectRow(r.Context(), riskimport.RejectRowInput{
		BatchID:  chi.URLParam(r, "id"),
		RowID:    chi.URLParam(r, "row"),
		Approver: approver,
		Reason:   body.Reason,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// decidingPerson resolves the caller to the users(id) who decides. Only a
// tenant approver who is a person decides: a machine token, an unbound API
// key or the bootstrap credential is refused, whatever its role.
func decidingPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return "", false
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return "", false
	}
	if !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required to decide an import row")
		return "", false
	}
	user, ok := credstore.CallerUserID(cred)
	if !ok {
		httpresp.WriteError(w, http.StatusForbidden,
			"a person must approve or reject: sign in as a user, or use an API key issued to one; a machine credential cannot decide an import row")
		return "", false
	}
	return user.String(), true
}

func writeStageError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, riskimport.ErrUploadTooLarge):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
	case errors.Is(err, riskimport.ErrWorkbookTooLarge):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("workbook expands past the %d MiB uncompressed cap; split it into smaller files", riskimport.MaxUnzipBytes>>20))
	case errors.Is(err, riskimport.ErrTooManyRows):
		httpresp.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("risk register exceeds the %d-row cap; split it into smaller files", riskimport.MaxRows))
	case errors.Is(err, riskimport.ErrTooManyColumns):
		httpresp.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("a row has a value past column %d; remove the stray cells or split the sheet", riskimport.MaxColumns))
	case errors.Is(err, riskimport.ErrUnsupportedFormat),
		errors.Is(err, riskimport.ErrEmptyFile),
		errors.Is(err, riskimport.ErrNoHeaderRow),
		errors.Is(err, riskimport.ErrInvalidColumnMap),
		errors.Is(err, riskimport.ErrMappedColumnAbsent):
		httpresp.WriteError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "riskimport: "))
	default:
		httperr.WriteInternal(w, r, "riskimports", err)
	}
}

func writeDecisionError(w http.ResponseWriter, r *http.Request, err error) {
	msg := strings.TrimPrefix(err.Error(), "riskimport: ")
	switch {
	case errors.Is(err, riskimport.ErrBatchNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
	case errors.Is(err, riskimport.ErrRowNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "staged row not found in this import batch")
	case errors.Is(err, riskimport.ErrRowDecided):
		httpresp.WriteError(w, http.StatusConflict, msg)
	case errors.Is(err, riskimport.ErrApproverRequired):
		httpresp.WriteError(w, http.StatusForbidden, msg)
	case errors.Is(err, riskimport.ErrReasonRequired), errors.Is(err, riskimport.ErrInvalidDecision):
		httpresp.WriteError(w, http.StatusBadRequest, msg)
	default:
		httperr.WriteInternal(w, r, "riskimports", err)
	}
}

func sizeCapMessage() string {
	return fmt.Sprintf("upload exceeds the %d MiB size cap; split it into smaller files", riskimport.MaxUploadBytes/(1024*1024))
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
