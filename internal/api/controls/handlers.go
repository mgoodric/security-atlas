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

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/control/bundletest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// isMachineActor reports whether the credential is a non-human caller.
// Mirrors `internal/authz.BuildInput`'s is_machine_actor predicate so
// the handler-level + OPA-level checks stay symmetric:
//
//   - empty UserID         — slice-034 api_keys without a bound user
//   - "key_..."            — slice-014/034 api_keys (legacy bearer)
//   - "oauth_client:..."   — slice 188 OAuth client_credentials JWTs
//
// Slice 196 introduces the OAuth-client prefix to support the
// atlas-bootstrap container's migration off the slice-037 fixed-token
// admin credential.
func isMachineActor(cred credstore.Credential) bool {
	return cred.UserID == "" ||
		strings.HasPrefix(cred.UserID, "key_") ||
		strings.HasPrefix(cred.UserID, "oauth_client:")
}

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
	// GateWarning carries a non-fatal note from the slice-574 upload test-gate
	// — e.g. "the bundle declares no tests". Empty (dropped) when the gate had
	// nothing to say.
	GateWarning string `json:"gate_warning,omitempty"`
	// GateTestReport carries the per-case report when a RED bundle was accepted
	// under the slice-608 advisory gate policy (the upload was NOT blocked, but
	// the uploader still sees which cases are red). Nil (dropped) otherwise.
	GateTestReport *bundletest.Report `json:"gate_test_report,omitempty"`
}

// gateRejectionResp is the 400 body returned when the slice-574 upload test-
// gate blocks a bundle: a human message plus the full per-case report so the
// uploader (and the CLI) sees exactly which fixture failed (AC-2 / AC-5).
type gateRejectionResp struct {
	Error  string             `json:"error"`
	Reason string             `json:"reason"`
	Report *bundletest.Report `json:"test_report,omitempty"`
}

// UploadBundle is the POST /v1/controls:upload-bundle handler.
//
// AC-3: upload posts a bundle and creates a controls row.
// AC-4: missing required metadata is rejected at parse with a field error.
// AC-5: bad applicability_expr is rejected.
// AC-6: re-upload supersedes prior version.
//
// Auth: admin credential OR machine-actor credential (the slice-196
// bootstrap container drives this endpoint via OAuth client_credentials).
// The schema-registry upload (slice 014) uses the same gate; control
// authorship is similarly a privileged path.
//
// Slice 196: the slice-035 OPA system.rego carve-out admits
// `action=upload-bundle, resource=controls, is_machine_actor=true`
// at the policy layer; this handler-level check is the symmetric
// peer — without it, the OAuth-driven bootstrap upload short-circuits
// at the handler before OPA even fires. The two checks together pin
// the invariant: only admins OR bootstrap-style machine actors can
// upload bundles.
func (h *Handler) UploadBundle(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !cred.IsAdmin && !isMachineActor(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return
	}

	// Cap the body. http.MaxBytesReader emits a 413 implicitly when the
	// limit is exceeded — wrap r.Body up front before any read.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxRequestBytes)

	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httperr.WriteInternal(w, r, "tenant context", err)
		return
	}

	bundle, err := h.readBundle(r)
	if err != nil {
		// Map parser errors to 4xx; everything else is 500.
		writeBundleError(w, r, err)
		return
	}

	// Slice-017 applicability_expr validator (AC-5).
	if err := bundle.ValidateApplicabilityExpr(); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Slice-014 schema-registry cross-check (AC-4 — implicitly, since an
	// unknown evidence_kind is a missing-dep parse failure too).
	if h.registry != nil {
		if err := bundle.ValidateEvidenceKinds(ctx, h.registry); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Slice-574 upload test-gate: run the bundle's declared tests/ cases through
	// the slice-496 runner and BLOCK the upload (strict policy) if any case
	// fails or errors. A bundle with no tests is allowed with a warning. The
	// gate is read-only (invariant #2): it evaluates, it never writes.
	//
	// h.store is the tenant-tx runner SQL fixtures need; when nil (unit servers
	// with no pool) pass an explicit nil so the gate degrades gracefully (SQL
	// fixtures then surface as a per-case error rather than panicking on a
	// typed-nil pointer).
	var gateRunner txRunner
	if h.store != nil {
		gateRunner = h.store
	}
	// Slice 608: resolve the per-tenant gate policy. A nil store (unit servers)
	// or any tenant without an explicit value resolves to the strict default,
	// preserving slice 574's global behaviour. An unrecognised stored value
	// (only reachable if the DB CHECK were bypassed) also falls back to strict
	// — never a looser posture than the default.
	mode := GateModeStrict
	if h.store != nil {
		raw, merr := h.store.BundleGateMode(ctx)
		if merr != nil {
			httperr.WriteInternal(w, r, "resolve gate policy", merr)
			return
		}
		if parsed, ok := ParseGateMode(raw); ok {
			mode = parsed
		}
	}
	verdict, err := runGate(ctx, gateRunner, bundle, mode)
	if err != nil {
		httperr.WriteInternal(w, r, "bundle test gate", err)
		return
	}
	if verdict.blocked {
		httpresp.WriteJSON(w, http.StatusBadRequest, gateRejectionResp{
			Error:  "control bundle rejected: its declared tests do not pass",
			Reason: "the upload test-gate runs a bundle's tests/ cases before persisting; this bundle has a failing or errored case",
			Report: verdict.report,
		})
		return
	}

	result, err := h.store.Upload(ctx, bundle, cred.ID)
	if err != nil {
		switch {
		case errors.Is(err, control.ErrSCFAnchorUnknown):
			// Canvas invariant 7 — refuse to persist a control without an
			// anchor. 404 communicates "the thing you referenced isn't there".
			httpresp.WriteError(w, http.StatusNotFound, err.Error())
		default:
			httperr.WriteInternal(w, r, "persist", err)
		}
		return
	}

	status := http.StatusOK
	if result.IsNewBundle {
		status = http.StatusCreated
	}
	resp := uploadResp{
		ControlID:      result.ControlID.String(),
		BundleID:       result.BundleID,
		Version:        result.Version,
		IsNewBundle:    result.IsNewBundle,
		GateWarning:    verdict.warning,
		GateTestReport: verdict.advisoryReport,
	}
	// SupersededID is set only when this upload actually superseded a
	// predecessor. It is the zero UUID for an initial upload AND for a
	// byte-identical-content no-op re-upload (nothing changed) — in both
	// cases leave the field empty so `omitempty` drops it.
	if result.SupersededID != (uuid.UUID{}) {
		resp.SupersededID = result.SupersededID.String()
	}
	httpresp.WriteJSON(w, status, resp)
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
func writeBundleError(w http.ResponseWriter, r *http.Request, err error) {
	var m control.ErrBundleMalformed
	if errors.As(err, &m) {
		httpresp.WriteError(w, http.StatusBadRequest, m.Error())
		return
	}
	var ue control.ErrUnknownEvidenceKind
	if errors.As(err, &ue) {
		httpresp.WriteError(w, http.StatusBadRequest, ue.Error())
		return
	}
	httperr.WriteInternal(w, r, "internal", err)
}
