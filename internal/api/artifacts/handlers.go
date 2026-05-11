// Package artifacts serves the slice-036 HTTP API for the S3 artifact
// store. Routes (all auth-gated by the platform's bearer middleware,
// tenant resolved from the credential):
//
//	POST /v1/artifacts:upload    multipart upload; returns metadata + payload_uri
//	GET  /v1/artifacts/{id}      returns metadata + short-TTL presigned URL
//
// Security posture:
//
//   - Tenant boundary: enforced at the database layer via RLS. Handlers
//     translate pgx.ErrNoRows (and artifact.ErrNotFound) into HTTP 404,
//     so cross-tenant lookups cannot disclose existence.
//   - Body size: capped via http.MaxBytesReader BEFORE the multipart
//     parse, so an oversized payload doesn't burn memory or temp-file
//     storage. Returns 413 with a clear error.
//   - Content hash: re-computed server-side inside artifact.Store.Put.
//     The handler never trusts a client-supplied hash.
//   - Presign TTL: clamped inside artifact.ClampTTL. Client requests
//     above 1h fold to 1h; default 15m.
//   - Audit log: every successful upload AND every successful presign
//     appends one row to artifact_access_log (AC-6 / ISC-A3).
//
// Opaque storage: this endpoint is content-agnostic. The body is bytes;
// the metadata is owner + size + content-type + hash. Callers that need
// semantic linkage (e.g., slice 018 storing approval evidence URLs) hold
// the returned payload_uri in their own row.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// MaxMultipartMemory bounds the in-memory portion of the multipart parse
// before spilling to temp files. Anything beyond this lands on disk —
// http.MaxBytesReader is the real cap on total bytes.
const MaxMultipartMemory = int64(8 * 1024 * 1024) // 8 MiB

// Handler bundles slice-036 routes.
type Handler struct {
	store *artifact.Store
}

// New constructs a Handler over an artifact.Store.
func New(store *artifact.Store) *Handler { return &Handler{store: store} }

// ----- wire types -----

type artifactWire struct {
	ID          string    `json:"id"`
	StorageKey  string    `json:"storage_key"`
	ContentHash string    `json:"content_hash"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	UploadedBy  string    `json:"uploaded_by"`
	UploadedAt  time.Time `json:"uploaded_at"`
	PayloadURI  string    `json:"payload_uri"`
}

type uploadResponse struct {
	Artifact artifactWire `json:"artifact"`
}

type downloadResponse struct {
	Artifact   artifactWire `json:"artifact"`
	URL        string       `json:"url"`
	ExpiresAt  time.Time    `json:"expires_at"`
	TTLSeconds int64        `json:"ttl_seconds"`
}

// ----- handlers -----

// Upload handles POST /v1/artifacts:upload.
//
// Form fields:
//
//	file     required: the binary payload
//	(others) ignored — content type comes from the multipart part header
//
// Returns 201 with the artifact metadata + payload_uri. 413 on oversize,
// 400 on missing file part, 401 on missing credential.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	// Body cap BEFORE the multipart parse — never read more than this
	// many bytes off the wire, period. +1 KiB headroom for the
	// multipart envelope itself.
	r.Body = http.MaxBytesReader(w, r.Body, artifact.MaxUploadBytes+1024)

	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", artifact.MaxUploadBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing `file` form part")
		return
	}
	defer func() { _ = file.Close() }()

	// Read the whole body. We hash twice — once here for the
	// PutInput.ContentHash field (so the store can defence-in-depth
	// the re-compute), once inside Put. The body is bounded by the
	// MaxBytesReader above, so memory use is capped.
	body, err := io.ReadAll(file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", artifact.MaxUploadBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if int64(len(body)) > artifact.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", artifact.MaxUploadBytes))
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty file")
		return
	}

	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	art, err := h.store.Put(ctx, artifact.PutInput{
		ContentType: contentType,
		SizeBytes:   int64(len(body)),
		ContentHash: hash,
		UploadedBy:  cred.ID,
		Body:        body,
	})
	if err != nil {
		h.writeStoreErr(w, "put artifact", err)
		return
	}

	// Audit log on success. Failure here is logged but doesn't roll
	// back the upload — the audit row is best-effort downstream of the
	// authoritative S3 + DB write.
	if logErr := h.store.LogAccess(ctx, art.ID, artifact.ActionUpload, cred.ID); logErr != nil {
		w.Header().Set("X-Atlas-Audit-Warning", "log-upload-failed")
	}

	writeJSON(w, http.StatusCreated, uploadResponse{Artifact: h.toWire(art)})
}

// Get handles GET /v1/artifacts/{id}.
//
// Query params:
//
//	?ttl=300   optional download TTL in seconds; clamped to ≤ 3600.
//
// Returns 200 with the metadata + presigned URL + expiration. 404 when
// the artifact does not exist FOR THE CALLING TENANT (RLS hides it).
// 401 on missing credential.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}

	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	var ttl time.Duration
	if v := strings.TrimSpace(r.URL.Query().Get("ttl")); v != "" {
		secs, perr := strconv.Atoi(v)
		if perr != nil || secs < 0 {
			writeError(w, http.StatusBadRequest, "ttl must be a non-negative integer (seconds)")
			return
		}
		ttl = time.Duration(secs) * time.Second
	}

	pre, err := h.store.Presign(ctx, id, ttl)
	if err != nil {
		h.writeStoreErr(w, "presign artifact", err)
		return
	}

	// Audit log on success. Same best-effort posture as upload.
	if logErr := h.store.LogAccess(ctx, pre.Artifact.ID, artifact.ActionDownload, cred.ID); logErr != nil {
		w.Header().Set("X-Atlas-Audit-Warning", "log-download-failed")
	}

	writeJSON(w, http.StatusOK, downloadResponse{
		Artifact:   h.toWire(pre.Artifact),
		URL:        pre.URL,
		ExpiresAt:  pre.ExpiresAt,
		TTLSeconds: pre.TTLSeconds,
	})
}

// ----- helpers -----

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, false
	}
	ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
	if err != nil {
		return nil, false
	}
	return ctx, true
}

func (h *Handler) toWire(a artifact.Artifact) artifactWire {
	return artifactWire{
		ID:          a.ID.String(),
		StorageKey:  a.StorageKey,
		ContentHash: a.ContentHash,
		SizeBytes:   a.SizeBytes,
		ContentType: a.ContentType,
		UploadedBy:  a.UploadedBy,
		UploadedAt:  a.UploadedAt,
		PayloadURI:  a.PayloadURI(h.store.Bucket()),
	}
}

func (h *Handler) writeStoreErr(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, artifact.ErrNotFound):
		// Important: 404, NOT 403. Avoids existence-disclosure to
		// adjacent tenants (anti-criterion ISC-A1).
		writeError(w, http.StatusNotFound, "artifact not found")
	case errors.Is(err, artifact.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, artifact.ErrOversized):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, artifact.ErrHashMismatch):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": op + ": " + err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
