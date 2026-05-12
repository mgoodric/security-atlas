// Package controls serves the slice-009 HTTP route for uploading control
// bundles. Authoring lives in the YAML manifest format described under
// docs/spec/control-bundle.md; this package only parses, validates, and
// persists.
package controls

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler exposes POST /v1/controls:upload-bundle.
type Handler struct {
	store    *control.Store
	registry control.SchemaRegistry
	// maxRequestBytes caps the HTTP body size as a belt to the parser's
	// own caps. 8 MB is generous given the 5 MB compressed-tarball cap
	// (multipart boundary overhead + JSON envelope tolerated).
	maxRequestBytes int64
}

// New constructs the handler. registry may be nil — when nil, the
// evidence_kind cross-check is skipped (slice 014 not wired in unit tests).
func New(store *control.Store, registry control.SchemaRegistry) *Handler {
	return &Handler{
		store:           store,
		registry:        registry,
		maxRequestBytes: 8 * 1024 * 1024,
	}
}

// inlineUploadReq is the JSON form of the upload endpoint. We accept the
// full manifest YAML as a string so authors can paste it from their editor
// without packaging a tarball.
type inlineUploadReq struct {
	ManifestYAML string `json:"manifest_yaml"`
}

// uploadResp is the HTTP response shape on success.
type uploadResp struct {
	ControlID    string `json:"control_id"`
	BundleID     string `json:"bundle_id"`
	Version      int32  `json:"version"`
	SupersededID string `json:"superseded_id,omitempty"`
	IsNewBundle  bool   `json:"is_new_bundle"`
}

// UploadBundle is the POST /v1/controls:upload-bundle handler.
//
// AC-3: upload posts a bundle and creates a controls row.
// AC-4: missing required metadata is rejected at parse with a field error.
// AC-5: bad applicability_expr is rejected.
// AC-6: re-upload supersedes prior version.
//
// Auth: admin credential only. The schema-registry upload (slice 014) uses
// the same gate; control authorship is similarly a privileged admin path.
func (h *Handler) UploadBundle(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "admin credential required")
		return
	}

	// Cap the body. http.MaxBytesReader emits a 413 implicitly when the
	// limit is exceeded — wrap r.Body up front before any read.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxRequestBytes)

	ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tenant context: "+err.Error())
		return
	}

	bundle, err := h.readBundle(r)
	if err != nil {
		// Map parser errors to 4xx; everything else is 500.
		writeBundleError(w, err)
		return
	}

	// Slice-017 applicability_expr validator (AC-5).
	if err := bundle.ValidateApplicabilityExpr(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Slice-014 schema-registry cross-check (AC-4 — implicitly, since an
	// unknown evidence_kind is a missing-dep parse failure too).
	if h.registry != nil {
		if err := bundle.ValidateEvidenceKinds(ctx, h.registry); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	result, err := h.store.Upload(ctx, bundle, cred.ID)
	if err != nil {
		switch {
		case errors.Is(err, control.ErrSCFAnchorUnknown):
			// Canvas invariant 7 — refuse to persist a control without an
			// anchor. 404 communicates "the thing you referenced isn't there".
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "persist: "+err.Error())
		}
		return
	}

	status := http.StatusOK
	if result.IsNewBundle {
		status = http.StatusCreated
	}
	resp := uploadResp{
		ControlID:   result.ControlID.String(),
		BundleID:    result.BundleID,
		Version:     result.Version,
		IsNewBundle: result.IsNewBundle,
	}
	if !result.IsNewBundle {
		resp.SupersededID = result.SupersededID.String()
	}
	writeJSON(w, status, resp)
}

// readBundle parses the request body into a *control.Bundle. Two shapes are
// accepted, dispatched on Content-Type:
//
//   - multipart/form-data: a single file part named `bundle.tar.gz` (or any
//     name with `.tar.gz` / `.tgz` suffix) containing the gzip-tar archive.
//   - application/json: { "manifest_yaml": "<full YAML>" } — for editors
//     and CI scripts that don't want to tar-up files.
//
// Anything else returns ErrBundleMalformed with the offending content type.
func (h *Handler) readBundle(r *http.Request) (*control.Bundle, error) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		return h.readMultipart(r)
	case strings.HasPrefix(ct, "application/json"):
		return h.readJSON(r)
	default:
		return nil, control.ErrBundleMalformed{Detail: fmt.Sprintf("unsupported Content-Type %q; expected multipart/form-data or application/json", ct)}
	}
}

func (h *Handler) readMultipart(r *http.Request) (*control.Bundle, error) {
	if err := r.ParseMultipartForm(h.maxRequestBytes); err != nil {
		return nil, control.ErrBundleMalformed{Detail: "parse multipart: " + err.Error()}
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	// Try the canonical name first; fall back to any tar-shaped part.
	if f, hdr, err := r.FormFile("bundle.tar.gz"); err == nil {
		defer func() { _ = f.Close() }()
		return control.ParseTarball(f)
	} else if !errors.Is(err, http.ErrMissingFile) {
		_ = hdr
		return nil, control.ErrBundleMalformed{Detail: "form file: " + err.Error()}
	}
	for name, files := range r.MultipartForm.File {
		if len(files) == 0 {
			continue
		}
		if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") {
			f, err := files[0].Open()
			if err != nil {
				return nil, control.ErrBundleMalformed{Detail: "open form file: " + err.Error()}
			}
			defer func() { _ = f.Close() }()
			return control.ParseTarball(f)
		}
	}
	return nil, control.ErrBundleMalformed{Detail: "multipart form has no file named bundle.tar.gz (or any *.tar.gz / *.tgz part)"}
}

func (h *Handler) readJSON(r *http.Request) (*control.Bundle, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, control.ErrBundleMalformed{Detail: "read body: " + err.Error()}
	}
	var req inlineUploadReq
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, control.ErrBundleMalformed{Detail: "decode JSON: " + err.Error()}
	}
	if strings.TrimSpace(req.ManifestYAML) == "" {
		return nil, control.ErrBundleMalformed{Detail: "manifest_yaml is required"}
	}
	// Build an in-memory directory by writing the YAML to a temp dir would
	// work, but we have a shortcut: feed the raw YAML through finalizeBundle
	// via a wrapper that exposes the same shape ParseDirectory yields.
	return parseInline([]byte(req.ManifestYAML))
}

// parseInline parses a YAML blob the same way ParseDirectory does. We can't
// reuse ParseDirectory directly (it expects a filesystem); duplicating the
// lightweight finalize path is simpler than introducing a temp dir.
func parseInline(rawYAML []byte) (*control.Bundle, error) {
	return control.FinalizeBundleForHTTP(rawYAML)
}

// writeBundleError maps a *control.ErrBundleMalformed (or wrapped) to a 400.
// Unknown errors fall through to a 500.
func writeBundleError(w http.ResponseWriter, err error) {
	var m control.ErrBundleMalformed
	if errors.As(err, &m) {
		writeError(w, http.StatusBadRequest, m.Error())
		return
	}
	var ue control.ErrUnknownEvidenceKind
	if errors.As(err, &ue) {
		writeError(w, http.StatusBadRequest, ue.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal: "+err.Error())
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
