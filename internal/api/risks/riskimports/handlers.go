// Package riskimports serves the risk register import API (#2839, spec
// #2812), following the controls import's routes (internal/api/
// controlimports).
//
// Routes (appended onto the platform root router):
//
//	GET  /v1/risks/imports          the newest batches with their pending
//	                                and waiting counts (#3002)
//	POST /v1/risks/imports          multipart upload -> staged import batch
//	POST /v1/risks/imports/preview  the same upload -> its header mapping
//	                                and first rows, staging nothing (#2848)
//	GET  /v1/risks/imports/{id}     the batch with its staged and rejected rows
//	POST /v1/risks/imports/{id}/approve
//	                                approve every pending row; each becomes a
//	                                risk through the risk store
//	POST /v1/risks/imports/{id}/rows/{row}/approve
//	                                approve one staged row (#2847)
//	POST /v1/risks/imports/{id}/rows/{row}/reject
//	                                reject one staged row with a reason
//	POST /v1/risks/imports/{id}/close
//	                                close a staged batch, rejecting its
//	                                pending rows with a reason (#3002)
//	PUT  /v1/risks/imports/{id}/values
//	                                map one distinct category or theme value
//	                                of the batch to an existing category or
//	                                theme, clear its mapping, or create the
//	                                category or theme for it (#2844)
//
// The upload's multipart fields:
//
//	file         the CSV or XLSX risk register (required)
//	methodology  optional, the batch's one scoring methodology: nist_800_30
//	             (the default), qualitative_5x5 or fair (#2841)
//	column_map   optional, repeatable "field=Header" overrides of header detection
//	ignore_column optional, repeatable header kept unmapped whatever its
//	             alias (#2848)
//
// Upload is a write on risks at the authz layer, which admin and
// grc_engineer hold. Approve, reject and close also require a person who is a
// tenant admin or holds the approver flag (authz.IsRiskImportDecider): a
// grc_engineer stages an import and hands it to an approver (#2881, spec
// #2812), and a machine credential is refused whatever its role. The
// person who uploaded a batch never decides its rows (#2881). Mapping a
// value is a write on risks, like upload; creating a category or theme for
// it also requires a tenant approver, as POST /v1/risks/categories and
// POST /v1/themes do, and is audited by the same calls. Tenant scoping is
// enforced by RLS via riskimport.Store.
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
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
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
	r.Get("/v1/risks/imports", h.List)
	r.Post("/v1/risks/imports", h.Create)
	r.Post("/v1/risks/imports/preview", h.Preview)
	r.Get("/v1/risks/imports/{id}", h.Get)
	r.Post("/v1/risks/imports/{id}/approve", h.Approve)
	r.Post("/v1/risks/imports/{id}/close", h.Close)
	r.Post("/v1/risks/imports/{id}/rows/{row}/approve", h.ApproveRow)
	r.Post("/v1/risks/imports/{id}/rows/{row}/reject", h.Reject)
	r.Put("/v1/risks/imports/{id}/values", h.MapValue)
}

// upload is a parsed multipart upload: the file and its mapping fields.
type upload struct {
	filename    string
	raw         []byte
	columnMap   map[string]string
	ignore      []string
	methodology string
}

// readUpload reads the multipart upload Create and Preview share, writing
// the error response itself when it returns false.
func readUpload(w http.ResponseWriter, r *http.Request) (upload, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return upload{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, riskimport.MaxUploadBytes+multipartOverhead)
	if err := r.ParseMultipartForm(riskimport.MaxUploadBytes + multipartOverhead); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
			return upload{}, false
		}
		httpresp.WriteError(w, http.StatusBadRequest, "request must be multipart/form-data with a file field")
		return upload{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "missing file field")
		return upload{}, false
	}
	defer func() { _ = file.Close() }()

	raw, err := riskimport.ReadAllBounded(file)
	if err != nil {
		if errors.Is(err, riskimport.ErrUploadTooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, sizeCapMessage())
			return upload{}, false
		}
		httpresp.WriteError(w, http.StatusBadRequest, "could not read upload")
		return upload{}, false
	}
	columnMap, err := parseColumnMap(r.MultipartForm.Value["column_map"])
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return upload{}, false
	}
	methodology := r.MultipartForm.Value["methodology"]
	if len(methodology) > 1 {
		httpresp.WriteError(w, http.StatusBadRequest, "methodology is given more than once; a batch has one methodology")
		return upload{}, false
	}
	return upload{
		filename:    header.Filename,
		raw:         raw,
		columnMap:   columnMap,
		ignore:      r.MultipartForm.Value["ignore_column"],
		methodology: strings.Join(methodology, ""),
	}, true
}

// Create stages an uploaded risk register as an import batch.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	up, ok := readUpload(w, r)
	if !ok {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	batch, err := h.store.Stage(r.Context(), riskimport.StageInput{
		Filename:       up.filename,
		Raw:            up.raw,
		ColumnMap:      up.columnMap,
		Ignore:         up.ignore,
		Methodology:    up.methodology,
		UploadedBy:     credstore.PrincipalOf(cred),
		UploaderUserID: uploaderUserID(cred),
	})
	if err != nil {
		writeStageError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, batch)
}

// Preview reads an upload with the same fields as Create and returns its
// header row, the field each header maps to, the fields the methodology
// reads and the first rows (#2848). Nothing is staged.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	up, ok := readUpload(w, r)
	if !ok {
		return
	}
	p, err := riskimport.PreviewFile(up.filename, up.raw, riskimport.ParseOptions{
		ColumnMap:   up.columnMap,
		Ignore:      up.ignore,
		Methodology: dbx.RiskMethodology(up.methodology),
	})
	if err != nil {
		writeStageError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, p)
}

// batchView is a batch as one caller reads it: CanDecide says whether that
// caller may approve or reject its rows, so the page hides controls the
// decide routes would refuse (#2881). The routes stay the authority.
type batchView struct {
	riskimport.Batch
	CanDecide bool `json:"can_decide"`
}

// canDecide reports whether cred may decide the rows of b: the decide
// routes' gate (decidingPerson) and not the batch's uploader.
func canDecide(cred credstore.Credential, b riskimport.Batch) bool {
	if !authz.IsRiskImportDecider(cred) {
		return false
	}
	user, ok := credstore.CallerUserID(cred)
	if !ok {
		return false
	}
	return b.UploaderUserID == "" || !strings.EqualFold(b.UploaderUserID, user.String())
}

// uploaderUserID is the users(id) of the uploading person, or "" when the
// caller is not a person.
func uploaderUserID(cred credstore.Credential) string {
	if u, ok := credstore.CallerUserID(cred); ok {
		return u.String()
	}
	return ""
}

// Get returns one import batch with its staged and rejected rows, and
// whether the caller may decide them.
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
	cred, _ := authctx.CredentialFromContext(ctx)
	httpresp.WriteJSON(w, http.StatusOK, batchView{Batch: batch, CanDecide: canDecide(cred, batch)})
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

// ApproveRow approves one staged row (#2847). It takes no body and is gated
// by decidingPerson, the same gate as Approve and Reject. A row the risk
// store refuses (a category archived since upload, a value it rejects)
// is a 422 and stays pending.
func (h *Handler) ApproveRow(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	d, err := h.store.ApproveRow(r.Context(), riskimport.ApproveRowInput{
		BatchID:  chi.URLParam(r, "id"),
		RowID:    chi.URLParam(r, "row"),
		Approver: approver,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
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

// Close closes a staged batch nobody will finish (#3002): every pending row
// is rejected with the body's reason ({"reason": "..."}), the rows of any
// import waiting on them are settled, and the batch is closed. It has the
// gate of Reject.
func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.store.CloseBatch(r.Context(), riskimport.CloseInput{
		BatchID:  chi.URLParam(r, "id"),
		Approver: approver,
		Reason:   body.Reason,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, res)
}

// listResponse is the import list.
type listResponse struct {
	Batches []riskimport.BatchSummary `json:"batches"`
}

// List returns the tenant's newest import batches, at most
// riskimport.MaxListedBatches, with their pending and waiting counts.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	batches, err := h.store.List(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "riskimports", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, listResponse{Batches: batches})
}

// createValueBody is the category or theme to create for a value: slug,
// name and description for a category; name and description for a theme.
type createValueBody struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// mapValueBody maps one value of the batch. Exactly one of target and
// create is given, or neither to clear the mapping.
type mapValueBody struct {
	Kind   string           `json:"kind"`
	Value  string           `json:"value"`
	Target string           `json:"target"`
	Create *createValueBody `json:"create"`
}

// MapValue maps one distinct category or theme value of a staged batch
// (#2844): to an existing category (slug or name) or theme (name) with
// target, to a category or theme created for it with create, or back to
// unmapped with neither. Rows take the mapping at approval.
func (h *Handler) MapValue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok || cred.ID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	var body mapValueBody
	dec := json.NewDecoder(io.LimitReader(r.Body, maxDecisionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Value) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "value is required")
		return
	}
	var (
		m   riskimport.ValueMapping
		err error
	)
	if body.Create != nil {
		if strings.TrimSpace(body.Target) != "" {
			httpresp.WriteError(w, http.StatusBadRequest, "give target or create, not both")
			return
		}
		if !authz.IsTenantApprover(cred) {
			httpresp.WriteError(w, http.StatusForbidden, "approver role required to create a category or theme")
			return
		}
		m, err = h.store.CreateValue(ctx, riskimport.CreateValueInput{
			BatchID: chi.URLParam(r, "id"), Kind: body.Kind, Value: body.Value,
			Slug: body.Create.Slug, Name: body.Create.Name, Description: body.Create.Description,
			// The actor the category and theme routes record (actorFrom).
			Actor:    cred.ID,
			MappedBy: credstore.PrincipalOf(cred),
		})
	} else {
		m, err = h.store.MapValue(ctx, riskimport.MapValueInput{
			BatchID: chi.URLParam(r, "id"), Kind: body.Kind, Value: body.Value, Target: body.Target,
			Actor: credstore.PrincipalOf(cred),
		})
	}
	if err != nil {
		writeValueError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, m)
}

func writeValueError(w http.ResponseWriter, r *http.Request, err error) {
	var unknown *riskimport.UnknownTargetError
	var badCategory *risk.InvalidCategoryError
	var badTheme *risk.InvalidThemeError
	switch {
	case errors.Is(err, riskimport.ErrBatchNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
	case errors.Is(err, riskimport.ErrValueNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "the import batch has no such value")
	case errors.Is(err, riskimport.ErrBatchCompleted),
		errors.Is(err, risk.ErrCategoryConflict),
		errors.Is(err, risk.ErrThemeConflict):
		httpresp.WriteError(w, http.StatusConflict, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "riskimport: "), "risk: "))
	case errors.Is(err, riskimport.ErrInvalidValueKind),
		errors.As(err, &badCategory),
		errors.As(err, &badTheme):
		httpresp.WriteError(w, http.StatusBadRequest, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "riskimport: "), "risk: "))
	case errors.As(err, &unknown):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "riskimport: "))
	default:
		httperr.WriteInternal(w, r, "riskimports", err)
	}
}

// decidingPerson resolves the caller to the users(id) who decides. Only a
// person who is a tenant admin or holds the approver flag decides
// (authz.IsRiskImportDecider): a grc_engineer is refused, by Matt's
// decision on #2881, and so are a machine token, an unbound API key and the
// bootstrap credential, whatever their role. It is the one gate of
// approve-all, per-row approve and reject. The store refuses the batch's
// uploader (riskimport.ErrSelfDecision), inside the decision's transaction.
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
	if !authz.IsRiskImportDecider(cred) {
		httpresp.WriteError(w, http.StatusForbidden,
			"an admin or approver must decide an import row; a GRC engineer stages an import and hands it to an approver")
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
		errors.Is(err, riskimport.ErrInvalidMethodology),
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
	case errors.Is(err, riskimport.ErrRowDecided), errors.Is(err, riskimport.ErrBatchNotStaged):
		httpresp.WriteError(w, http.StatusConflict, msg)
	case errors.Is(err, riskimport.ErrApproverRequired), errors.Is(err, riskimport.ErrSelfDecision):
		httpresp.WriteError(w, http.StatusForbidden, msg)
	case errors.Is(err, riskimport.ErrReasonRequired), errors.Is(err, riskimport.ErrInvalidDecision):
		httpresp.WriteError(w, http.StatusBadRequest, msg)
	case riskimport.IsRowRefusal(err):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(msg, "risk: "))
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
