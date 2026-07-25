// Package walkthroughs serves the slice-027 HTTP API. Routes (appended to
// the platform root router by internal/api/httpserver.go):
//
//	POST   /v1/walkthroughs                          create (status=draft)
//	GET    /v1/walkthroughs                          list current tenant
//	GET    /v1/walkthroughs/{id}                     get + tamper-check (AC-6)
//	POST   /v1/walkthroughs/{id}/attachments         multipart upload (AC-2)
//	POST   /v1/walkthroughs/{id}:finalize            terminal hash commit
//	GET    /v1/walkthroughs/{id}/export              ?format=pdf|json (AC-5)
//
// Authorization:
//   - Write paths (POST) require IsAdmin OR OwnerRoles contains
//     "grc_engineer". The slice-035 OPA middleware also enforces
//     resource_type="walkthroughs" + the period-assignment ABAC predicate
//     for auditors (per policies/authz/auditor.rego, added by this slice).
//   - Read paths additionally allow the "auditor" role (period-scoped via
//     the OPA layer).
//
// All handlers run with the tenant set by upstream auth middleware
// (internal/api/authctx + internal/api/tenancymw). The walkthrough.Store
// opens its own transaction per call and applies the tenant GUC; the
// slice-036 artifact.Store does the same for its writes.
package walkthroughs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// MaxAttachmentBytes caps a single walkthrough attachment. The artifact
// store's MaxUploadBytes is the global ceiling; we use that here so the
// limits stay in lockstep.
var MaxAttachmentBytes = artifact.MaxUploadBytes

// MaxMultipartMemory bounds the in-memory portion of the multipart parse.
const MaxMultipartMemory = int64(8 * 1024 * 1024) // 8 MiB

// ArtifactUploader is the narrow surface this handler needs from the
// slice-036 artifact store. Tests inject an in-memory fake; production
// passes the real *artifact.Store via the adapter below.
type ArtifactUploader interface {
	Put(ctx context.Context, in artifact.PutInput) (artifact.Artifact, error)
}

// walkthroughReader is the per-route read seam the two GET read paths —
// GET /v1/walkthroughs/{id} (Get) and GET /v1/walkthroughs (List) — read
// through (slice 689 added Get; slice 690's contract-tier rollout adds the
// List read). It carries JUST the read methods those routes need —
// deliberately narrow (slice 409 D1 / slice 411 D2 / slice 412 D2 sizing rule:
// a two-method seam over the wider walkthrough.Store, NOT a mirror of its
// create/attach/finalize surface). The contract-tier recorder
// (contractrecord_test.go) injects a fixed-row stub satisfying this seam so the
// wire shapes (with attachments + canonical_hash + tamper flag) record on the
// plain `go test ./...` unit surface with no Postgres pool (ADR-0007 /
// P0-409-1). The production *walkthrough.Store satisfies it verbatim; the seam
// is unexported and New(*walkthrough.Store, ArtifactUploader) is unchanged
// (P0-409-2). The write/export handlers keep using the concrete h.store
// directly (Export also calls Get, but it streams a download envelope, not the
// JSON wire shape the BFF consumes — it is left on the concrete store).
type walkthroughReader interface {
	Get(ctx context.Context, id uuid.UUID) (walkthrough.Walkthrough, error)
	List(ctx context.Context) ([]walkthrough.Walkthrough, error)
}

// Handler bundles slice-027 routes over a walkthrough.Store + an optional
// artifact uploader. When the uploader is nil, the attachments endpoint
// 503s -- consistent with the slice-011 attest pattern.
//
// reader is the slice-689 per-route read seam the Get path reads through; New
// points it at store, so production behavior is identical.
type Handler struct {
	store    *walkthrough.Store
	uploader ArtifactUploader
	reader   walkthroughReader
}

// New constructs a Handler. The slice-689 per-route read seam (reader) is
// wired to the same store — the public signature is unchanged (P0-409-2).
func New(store *walkthrough.Store, uploader ArtifactUploader) *Handler {
	return &Handler{store: store, uploader: uploader, reader: store}
}

// newHandlerWithReader constructs a Handler whose Get path reads through an
// arbitrary read seam. It exists ONLY for the slice-689 contract recorder,
// which injects a fixed-row stub so the single-walkthrough wire shape records
// with no Postgres pool. Unexported — not part of the public surface.
func newHandlerWithReader(reader walkthroughReader) *Handler {
	return &Handler{reader: reader}
}

// ----- wire shapes -----

type createReq struct {
	ControlID     string `json:"control_id"`
	AuditPeriodID string `json:"audit_period_id,omitempty"`
	Narrative     string `json:"narrative"`
	Transcript    string `json:"transcript,omitempty"`
}

type walkthroughWire struct {
	ID             string           `json:"id"`
	AuditPeriodID  string           `json:"audit_period_id,omitempty"`
	ControlID      string           `json:"control_id"`
	Narrative      string           `json:"narrative"`
	Transcript     string           `json:"transcript,omitempty"`
	Status         string           `json:"status"`
	CanonicalHash  string           `json:"canonical_hash"`
	CreatedBy      string           `json:"created_by"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	Attachments    []attachmentWire `json:"attachments,omitempty"`
	TamperDetected bool             `json:"tamper_detected"`
}

type attachmentWire struct {
	ID          string          `json:"id"`
	StorageKey  string          `json:"storage_key"`
	ContentType string          `json:"content_type"`
	SizeBytes   int64           `json:"size_bytes"`
	SHA256      string          `json:"sha256"`
	Annotations json.RawMessage `json:"annotations"`
	UploadedBy  string          `json:"uploaded_by"`
	UploadedAt  time.Time       `json:"uploaded_at"`
}

// ----- handlers -----

// Create handles POST /v1/walkthroughs (AC-1).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}

	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Narrative == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "narrative is required")
		return
	}
	ctrlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	in := walkthrough.CreateInput{
		ControlID:  ctrlID,
		Narrative:  req.Narrative,
		Transcript: req.Transcript,
		CreatedBy:  cred.ID,
	}
	if req.AuditPeriodID != "" {
		pid, err := uuid.Parse(req.AuditPeriodID)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
			return
		}
		in.AuditPeriodID = &pid
	}

	wt, err := h.store.Create(ctx, in)
	if err != nil {
		h.writeStoreErr(w, r, "create walkthrough", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"walkthrough": toWire(wt)})
}

// Get handles GET /v1/walkthroughs/{id}. AC-4 + AC-6.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	wt, err := h.reader.Get(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "get walkthrough", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"walkthrough": toWire(wt)})
}

// List handles GET /v1/walkthroughs.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	ws, err := h.reader.List(ctx)
	if err != nil {
		h.writeStoreErr(w, r, "list walkthroughs", err)
		return
	}
	out := make([]walkthroughWire, len(ws))
	for i, wt := range ws {
		out[i] = toWire(wt)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"walkthroughs": out, "count": len(out)})
}

// AddAttachment handles POST /v1/walkthroughs/{id}/attachments (AC-2).
//
// Multipart form:
//
//	file          required: binary payload
//	annotations   optional: free-form JSON metadata (image regions, notes)
//
// On success, the blob is persisted via the slice-036 artifact store
// (per-tenant prefix; AC-2) and a walkthrough_attachments row is written
// with the sha256 + storage_key. The walkthrough's canonical_hash is
// recomputed inside walkthrough.Store.AddAttachment.
func (h *Handler) AddAttachment(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}
	if h.uploader == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "artifact store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxAttachmentBytes+1024)
	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", MaxAttachmentBytes))
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "missing `file` form part")
		return
	}
	defer func() { _ = file.Close() }()

	body, err := io.ReadAll(file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", MaxAttachmentBytes))
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if int64(len(body)) > MaxAttachmentBytes {
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", MaxAttachmentBytes))
		return
	}
	if len(body) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "empty file")
		return
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	// Annotations JSON (optional). Validate that it parses; the schema
	// is intentionally free-form (v1 ships with no canonical shape).
	annotationsRaw := strings.TrimSpace(r.FormValue("annotations"))
	if annotationsRaw == "" {
		annotationsRaw = "{}"
	}
	var probe any
	if err := json.Unmarshal([]byte(annotationsRaw), &probe); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "annotations must be valid JSON")
		return
	}

	// Persist the blob to the slice-036 artifact store FIRST. If the
	// store-level write fails, we never wrote the walkthrough_attachments
	// row, so the walkthrough's canonical_hash stays consistent with its
	// current attachment set.
	art, err := h.uploader.Put(ctx, artifact.PutInput{
		ContentType: contentType,
		SizeBytes:   int64(len(body)),
		ContentHash: hash,
		UploadedBy:  cred.ID,
		Body:        body,
	})
	if err != nil {
		writeArtifactErr(w, r, "store attachment blob", err)
		return
	}

	// Wire the metadata + hash into the walkthroughs side. AddAttachment
	// recomputes the canonical hash over the new attachment set.
	wt, err := h.store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  id,
		StorageKey:     art.StorageKey,
		ContentType:    art.ContentType,
		SizeBytes:      art.SizeBytes,
		SHA256Hex:      hash,
		AnnotationsRaw: []byte(annotationsRaw),
		UploadedBy:     cred.ID,
	})
	if err != nil {
		h.writeStoreErr(w, r, "add attachment", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"walkthrough": toWire(wt)})
}

// Finalize handles POST /v1/walkthroughs/{id}:finalize.
func (h *Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	wt, err := h.store.Finalize(ctx, id, cred.ID)
	if err != nil {
		h.writeStoreErr(w, r, "finalize walkthrough", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"walkthrough": toWire(wt)})
}

// Export handles GET /v1/walkthroughs/{id}/export?format=pdf|json (AC-5).
//
// JSON shape mirrors walkthrough.ExportJSON exactly; PDF is rendered via
// chromedp through walkthrough.RenderPDF. PDF unavailability (missing
// Chrome binary) is surfaced as 503 so operators can run the platform
// without Chrome and still get the JSON export.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	wt, err := h.store.Get(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "get walkthrough (export)", err)
		return
	}
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="walkthrough-%s.json"`, id.String()))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(walkthrough.ToExportJSON(wt))
	case "pdf":
		// The render budget + concurrency cap live on the shared pdfrender
		// limiter (slice 475, fanned out to walkthroughs by slice 477);
		// walkthrough.RenderPDF routes through it. We do NOT wrap in a second
		// WithTimeout here — the limiter owns the bounded deadline. All three
		// degradation modes (chrome absent / render deadline exceeded / render
		// queue saturated) map to a deterministic 503 instead of a 500/hang.
		buf, err := walkthrough.RenderPDF(r.Context(), wt)
		if err != nil {
			if status, msg, ok := pdfDegradation(err); ok {
				logPDFDegradation(r, err)
				httpresp.WriteError(w, status, msg)
				return
			}
			httperr.WriteInternal(w, r, "render PDF", err)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="walkthrough-%s.pdf"`, id.String()))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	default:
		httpresp.WriteError(w, http.StatusBadRequest, "format must be 'json' or 'pdf'")
	}
}

// ----- helpers -----

func (h *Handler) authnContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// canWrite returns true when the credential is an admin or carries the
// grc_engineer owner role. Mirrors the slice-028 (audit_periods)
// defense-in-depth check. The slice-035 OPA middleware is the primary
// gate; this handler-local check returns 403 even when the OPA
// middleware is not wired (unit-test servers).
func canWrite(cred credstore.Credential) bool {
	if cred.IsAdmin {
		return true
	}
	for _, role := range cred.OwnerRoles {
		if role == "grc_engineer" {
			return true
		}
	}
	return false
}

func toWire(wt walkthrough.Walkthrough) walkthroughWire {
	out := walkthroughWire{
		ID:             wt.ID.String(),
		ControlID:      wt.ControlID.String(),
		Narrative:      wt.Narrative,
		Transcript:     wt.Transcript,
		Status:         string(wt.Status),
		CanonicalHash:  hex.EncodeToString(wt.CanonicalHash),
		CreatedBy:      wt.CreatedBy,
		CreatedAt:      wt.CreatedAt,
		UpdatedAt:      wt.UpdatedAt,
		TamperDetected: wt.TamperDetected,
	}
	if wt.AuditPeriodID != nil {
		out.AuditPeriodID = wt.AuditPeriodID.String()
	}
	if len(wt.Attachments) > 0 {
		out.Attachments = make([]attachmentWire, len(wt.Attachments))
		for i, a := range wt.Attachments {
			annotations := a.AnnotationsRaw
			if len(annotations) == 0 {
				annotations = []byte(`{}`)
			}
			out.Attachments[i] = attachmentWire{
				ID:          a.ID.String(),
				StorageKey:  a.StorageKey,
				ContentType: a.ContentType,
				SizeBytes:   a.SizeBytes,
				SHA256:      a.SHA256Hex,
				Annotations: json.RawMessage(annotations),
				UploadedBy:  a.UploadedBy,
				UploadedAt:  a.UploadedAt,
			}
		}
	}
	return out
}

func (h *Handler) writeStoreErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, walkthrough.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "walkthrough not found")
	case errors.Is(err, walkthrough.ErrFinalized):
		httpresp.WriteError(w, http.StatusConflict, "walkthrough is finalized")
	case errors.Is(err, walkthrough.ErrPeriodFrozen):
		httpresp.WriteError(w, http.StatusConflict, "audit period is frozen")
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}

func writeArtifactErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, artifact.ErrOversized):
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, artifact.ErrInvalidInput):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, artifact.ErrHashMismatch):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}
