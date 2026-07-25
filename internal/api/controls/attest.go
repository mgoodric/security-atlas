// Slice 011 — manual control attestation endpoint.
//
// Two routes live here, alongside the slice-009 upload handler:
//
//	GET  /v1/controls/{id}/attest-form        return the control's
//	                                           manual_evidence_schema +
//	                                           metadata for client rendering.
//	POST /v1/controls/{id}/attestations       submit an attestation,
//	                                           optionally citing an
//	                                           artifact, and write an
//	                                           evidence record to the
//	                                           ledger via slice-013's
//	                                           ingest.Service.Process.
//
// Security posture:
//
//   - AuthN: the platform's bearer-token middleware attaches a
//     credstore.Credential to the request context. Missing → 401.
//   - AuthZ (AC-5): POST requires the caller to hold the control's
//     `owner_role` (declared in the slice-009 bundle manifest) via
//     credstore.Credential.HasOwnerRole. IsAdmin is a wildcard.
//   - Tenant boundary: GetControlByID is parameterized on the tenant
//     from the credential; cross-tenant control ids resolve to 404
//     (we map pgx.ErrNoRows → 404 without disclosing existence).
//   - Anti-AI-auto-attest (canvas §AI-assist boundary): actor_type is
//     hard-coded to "human" — the handler refuses to read it from the
//     request body — and actor_id is taken from credstore.Credential.UserID,
//     which is set only at credential issue time.
//   - Audit (AC-6): the ingest.Service.Process call writes one
//     evidence_audit_log row per attempt, keyed by credential id.
package controls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/control/attestation"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// AttestationKind is the platform evidence_kind used for manual
// attestations. Slice 014 ships the schema at this id at boot
// (`manual.attestation/1.1.0.json`).
const (
	AttestationKind    = "manual.attestation.v1"
	AttestationVersion = "1.1.0"
	// IngestionPath is the ledger-row tag identifying records that flow
	// through this handler. Canvas §4.7 enumerates the allowed values
	// (push/pull/subscribe/webhook/manual_upload); slice 002's
	// evidence_records_ingestion_path_chk constraint enforces them.
	// Manual attestation evidence rides under "manual_upload" — the
	// canvas's umbrella term for any human-driven evidence write — so
	// slice 011 stays a no-migration slice. Audit queries can still
	// disambiguate attestation vs file upload via evidence_kind
	// (manual.attestation.v1 vs manual.upload.v1).
	IngestionPath = "manual_upload"
)

// AttestHandler binds the GET/POST attestation routes. Constructor takes
// the dependencies as concrete types so unit tests can pass nils for
// branches that exit before the dependency is consulted (401/400 cases).
type AttestHandler struct {
	ingest  *ingest.Service
	uploads ArtifactUploader

	// reader is the slice-692 per-route read seam the AttestForm + Submit
	// paths read the control row through (contract-tier rollout, Option-A
	// per-route seam — slice 411/689 precedent). It carries JUST the single
	// control-by-id read those routes need. In production it is a pool-backed
	// adapter (poolControlReader) wired by NewAttestHandler; the contract
	// recorder (attest_contract_test.go) injects a fixed-row stub satisfying
	// this seam so the attest-form wire shape records on the plain
	// `go test ./...` unit surface with no Postgres pool (ADR-0007 /
	// P0-409-1). NewAttestHandler's signature is unchanged (P0-409-2).
	reader controlByIDReader

	// maxBodyBytes caps the JSON request body. Attestation payloads are
	// schema-validated and small; the cap is just a belt against a
	// runaway caller. Artifact bytes do NOT flow through this handler —
	// the client uploads to /v1/artifacts:upload first and posts the
	// returned artifact_id here.
	maxBodyBytes int64
}

// controlByIDReader is the slice-692 per-route read seam: the single
// control-by-id read the attestation routes depend on. Kept deliberately
// narrow (one method) — the recorder satisfies it with a fixed
// dbx.GetControlByIDRow fixture and no Postgres. The production
// poolControlReader runs the slice-009 GetControlByID query inside a
// tenant-GUC read tx so RLS hides cross-tenant rows.
type controlByIDReader interface {
	ControlByID(ctx context.Context, tenantID string, id uuid.UUID) (dbx.GetControlByIDRow, error)
}

// poolControlReader is the production controlByIDReader: it reads the control
// row from Postgres inside a tenant-GUC read tx. It carries the pgx pool the
// handler previously held directly; the read logic is unchanged from the
// former h.loadControl method.
type poolControlReader struct {
	pool *pgxpool.Pool
}

// ControlByID runs the GetControlByID query inside a tenant-GUC read tx so
// RLS hides cross-tenant rows. Returns pgx.ErrNoRows when the control isn't
// visible to the calling tenant.
func (p poolControlReader) ControlByID(ctx context.Context, tenantID string, id uuid.UUID) (dbx.GetControlByIDRow, error) {
	var out dbx.GetControlByIDRow
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return out, fmt.Errorf("parse tenant: %w", err)
	}
	err = pgx.BeginTxFunc(ctx, p.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if terr := tenancy.ApplyTenant(ctx, tx); terr != nil {
			return terr
		}
		q := dbx.New(tx)
		row, gerr := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgtype.UUID{Bytes: tenantUUID, Valid: true},
			ID:       pgtype.UUID{Bytes: id, Valid: true},
		})
		if gerr != nil {
			return gerr
		}
		out = row
		return nil
	})
	return out, err
}

// ArtifactUploader is the slice-036 surface this handler depends on. The
// interface lets unit tests stub the upload without spinning up MinIO.
// The integration test wires the real artifact.Store.
type ArtifactUploader interface {
	// PayloadURIFor returns the s3:// URI for artifactID under the
	// calling tenant. The artifact MUST exist for that tenant — RLS
	// hides cross-tenant rows; ErrNotFound surfaces as a 404 here.
	PayloadURIFor(ctx context.Context, artifactID uuid.UUID) (string, error)
}

// NewAttestHandler constructs the handler. pool may be nil in unit tests
// that exercise pre-DB branches (401/400). ingester may be nil for the
// same reason. uploader may be nil — the artifact_id field is optional.
func NewAttestHandler(pool *pgxpool.Pool, ingester *ingest.Service, uploader ArtifactUploader) *AttestHandler {
	// reader is the slice-692 per-route read seam. In production pool is
	// non-nil and we wire the pool-backed adapter. When pool is nil (the
	// unit-only servers that exercise the pre-read 401/400 branches) we leave
	// reader nil so the read-path gate returns a clean 503 rather than
	// dereferencing a nil pool inside pgx — preserving the prior
	// `h.pool == nil -> 503` contract verbatim.
	var reader controlByIDReader
	if pool != nil {
		reader = poolControlReader{pool: pool}
	}
	return &AttestHandler{
		ingest:       ingester,
		uploads:      uploader,
		reader:       reader,
		maxBodyBytes: 2 << 20, // 2 MiB
	}
}

// newAttestHandlerWithReader constructs an AttestHandler whose control-by-id
// read goes through an arbitrary seam. It exists ONLY for the slice-692
// contract recorder, which injects a fixed-row stub so the attest-form wire
// shape records with no Postgres pool. Unexported — not part of the public
// surface (P0-409-2). The handler keeps a nil pool so the per-route gates
// behave identically to a recorder server; the reader carries the read.
func newAttestHandlerWithReader(reader controlByIDReader) *AttestHandler {
	return &AttestHandler{
		reader:       reader,
		maxBodyBytes: 2 << 20,
	}
}

// ----- wire types -----

// attestFormResponse describes the form the frontend renders. The
// manual_evidence_schema is the control bundle's per-control declaration
// (slice 009); the frontend renders one input per top-level property
// inside it.
type attestFormResponse struct {
	ControlID              string         `json:"control_id"`
	BundleID               string         `json:"bundle_id"`
	Title                  string         `json:"title"`
	ImplementationType     string         `json:"implementation_type"`
	OwnerRole              string         `json:"owner_role"`
	FreshnessClass         *string        `json:"freshness_class,omitempty"`
	ManualEvidenceSchema   map[string]any `json:"manual_evidence_schema"`
	CallerCanAttest        bool           `json:"caller_can_attest"`
	PlatformSchemaKind     string         `json:"platform_schema_kind"`
	PlatformSchemaVersion  string         `json:"platform_schema_version"`
	PlatformSchemaRequires []string       `json:"platform_schema_requires"`
}

// attestSubmitRequest is the wire body for POST /v1/controls/{id}/attestations.
// We deliberately do NOT accept actor_type / actor_id from the wire —
// those are derived from the credential at the handler. AI-driven
// auto-attest would have to forge a credential.
type attestSubmitRequest struct {
	Statement       string         `json:"statement"`
	AttestationData map[string]any `json:"attestation_data,omitempty"`
	SupportingURI   string         `json:"supporting_uri,omitempty"`
	ArtifactID      string         `json:"artifact_id,omitempty"`
	IdempotencyKey  string         `json:"idempotency_key,omitempty"`
	ObservedAt      string         `json:"observed_at,omitempty"`
}

type attestSubmitResponse struct {
	RecordID     string `json:"record_id"`
	Hash         string `json:"hash"`
	IngestedAt   string `json:"ingested_at"`
	CredentialID string `json:"credential_id"`
	Deduplicated bool   `json:"deduplicated"`
	PayloadURI   string `json:"payload_uri,omitempty"`
}

type attestErrorBody struct {
	Error string `json:"error"`
}

// ----- handlers -----

// AttestForm serves GET /v1/controls/{id}/attest-form.
func (h *AttestHandler) AttestForm(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeAttestError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeAttestError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}
	if h.reader == nil {
		// Belt-and-braces: unit-only servers that don't wire a read path
		// can't serve this route; 503 is more honest than 500. In
		// production NewAttestHandler always wires a poolControlReader, so
		// a nil reader means the server was constructed without a control
		// store (the recorder injects a stub reader instead).
		writeAttestError(w, http.StatusServiceUnavailable, "control store not configured")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		writeAttestError(w, http.StatusInternalServerError, "tenant context: "+terr.Error())
		return
	}

	row, err := h.reader.ControlByID(ctx, cred.TenantID, id)
	if err != nil {
		h.writeControlLookupError(w, err)
		return
	}
	if !isManualImplementation(string(row.ImplementationType)) {
		writeAttestError(w, http.StatusBadRequest,
			fmt.Sprintf("control implementation_type %q is not manual; attestation rejected", row.ImplementationType))
		return
	}

	schemaMap, err := decodeJSONBObject(row.ManualEvidenceSchema)
	if err != nil {
		writeAttestError(w, http.StatusInternalServerError, "decode manual_evidence_schema: "+err.Error())
		return
	}
	form := attestFormResponse{
		ControlID:              uuid.UUID(row.ID.Bytes).String(),
		BundleID:               row.BundleID,
		Title:                  row.Title,
		ImplementationType:     string(row.ImplementationType),
		OwnerRole:              row.OwnerRole,
		FreshnessClass:         row.FreshnessClass,
		ManualEvidenceSchema:   schemaMap,
		CallerCanAttest:        cred.HasOwnerRole(row.OwnerRole),
		PlatformSchemaKind:     AttestationKind,
		PlatformSchemaVersion:  AttestationVersion,
		PlatformSchemaRequires: []string{"attestor", "statement"},
	}
	writeAttestJSON(w, http.StatusOK, form)
}

// Submit serves POST /v1/controls/{id}/attestations.
func (h *AttestHandler) Submit(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeAttestError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeAttestError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}
	// Bound body before reading.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAttestError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req attestSubmitRequest
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		writeAttestError(w, http.StatusBadRequest, "invalid JSON body: "+jerr.Error())
		return
	}
	if req.Statement == "" {
		writeAttestError(w, http.StatusBadRequest, "statement is required")
		return
	}

	// Pre-DB path: short-circuit for the unit-test branches that don't
	// configure a read path. The handler shouldn't 500 just because tests
	// pass nil; tests above this gate verify input validation.
	if h.reader == nil {
		writeAttestError(w, http.StatusServiceUnavailable, "control store not configured")
		return
	}

	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		writeAttestError(w, http.StatusInternalServerError, "tenant context: "+terr.Error())
		return
	}

	row, err := h.reader.ControlByID(ctx, cred.TenantID, id)
	if err != nil {
		h.writeControlLookupError(w, err)
		return
	}

	// AC-5: owner-role gate. Admins satisfy any role (wildcard).
	if !cred.HasOwnerRole(row.OwnerRole) {
		writeAttestError(w, http.StatusForbidden,
			fmt.Sprintf("caller credential does not hold owner_role %q for this control", row.OwnerRole))
		return
	}

	if !isManualImplementation(string(row.ImplementationType)) {
		writeAttestError(w, http.StatusBadRequest,
			fmt.Sprintf("control implementation_type %q is not manual; attestation rejected", row.ImplementationType))
		return
	}

	// Validate attestation_data against the control's manual_evidence_schema
	// when one is declared. The platform manual.attestation.v1 schema is
	// applied by ingest.Service.Process via the schema registry.
	if len(row.ManualEvidenceSchema) > 0 {
		controlSchema, derr := decodeJSONBObject(row.ManualEvidenceSchema)
		if derr != nil {
			writeAttestError(w, http.StatusInternalServerError, "decode manual_evidence_schema: "+derr.Error())
			return
		}
		if verr := attestation.ValidateAttestationData(controlSchema, req.AttestationData); verr != nil {
			writeAttestError(w, http.StatusBadRequest, "attestation_data: "+verr.Error())
			return
		}
	}

	// Optional artifact attach. Tenant boundary enforced by slice 036.
	var payloadURI string
	if req.ArtifactID != "" {
		artID, perr := uuid.Parse(req.ArtifactID)
		if perr != nil {
			writeAttestError(w, http.StatusBadRequest, "artifact_id must be a uuid")
			return
		}
		if h.uploads == nil {
			writeAttestError(w, http.StatusServiceUnavailable, "artifact store not configured")
			return
		}
		uri, uerr := h.uploads.PayloadURIFor(ctx, artID)
		if uerr != nil {
			writeAttestError(w, http.StatusNotFound, "artifact not found")
			return
		}
		payloadURI = uri
	}

	// Idempotency key default — a deterministic key over the inputs so
	// retries from the browser don't double-write while still allowing
	// the caller to opt in to their own key.
	idemKey := req.IdempotencyKey
	if idemKey == "" {
		idemKey = deriveIdempotencyKey(cred.UserID, id.String(), body)
	}

	observed := time.Now().UTC()
	if req.ObservedAt != "" {
		parsed, perr := time.Parse(time.RFC3339Nano, req.ObservedAt)
		if perr != nil {
			parsed, perr = time.Parse(time.RFC3339, req.ObservedAt)
			if perr != nil {
				writeAttestError(w, http.StatusBadRequest, "observed_at must be RFC3339: "+perr.Error())
				return
			}
		}
		observed = parsed
	}

	// Build the manual.attestation.v1 payload. actor_type=human and
	// actor_id=cred.UserID are server-set; clients can never escalate
	// to a non-human source_attribution from this endpoint.
	payload := map[string]any{
		"attestor":  cred.UserID,
		"statement": req.Statement,
	}
	if req.SupportingURI != "" {
		payload["supporting_uri"] = req.SupportingURI
	}
	if len(req.AttestationData) > 0 {
		payload["attestation_data"] = req.AttestationData
	}
	payloadStruct, err := structpb.NewStruct(payload)
	if err != nil {
		writeAttestError(w, http.StatusInternalServerError, "build payload struct: "+err.Error())
		return
	}

	rec := &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey,
		EvidenceKind:   AttestationKind,
		SchemaVersion:  AttestationVersion,
		ControlId:      id.String(),
		Scope: []*evidencev1.ScopeDimension{{
			Key:    "control_owner",
			Values: []string{row.OwnerRole},
		}},
		ObservedAt: timestamppb.New(observed),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payloadStruct,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "human",
			ActorId:   cred.UserID,
			SessionId: cred.ID,
		},
	}
	if payloadURI != "" {
		uri := payloadURI
		rec.PayloadUri = &uri
	}

	if h.ingest == nil {
		writeAttestError(w, http.StatusServiceUnavailable, "ingest service not configured")
		return
	}
	pathed := h.ingest.WithPath(IngestionPath)
	receipt, _, perr := pathed.Process(r.Context(), rec, cred)
	if perr != nil {
		writeAttestError(w, ingestErrorToStatus(perr), perr.Error())
		return
	}
	out := attestSubmitResponse{
		RecordID:     receipt.RecordID,
		Hash:         receipt.Hash,
		IngestedAt:   receipt.IngestedAt.Format(time.RFC3339Nano),
		CredentialID: receipt.CredentialID,
		Deduplicated: receipt.Deduplicated,
		PayloadURI:   payloadURI,
	}
	status := http.StatusCreated
	if receipt.Deduplicated {
		status = http.StatusOK
	}
	writeAttestJSON(w, status, out)
}

// ----- helpers -----

// isManualImplementation returns true for manual_attested / manual_periodic.
// Bundles declaring automated / semi_automated cannot be attested via this
// endpoint — their evidence flows through connectors / push.
func isManualImplementation(impl string) bool {
	return impl == "manual_attested" || impl == "manual_periodic"
}

func (h *AttestHandler) writeControlLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeAttestError(w, http.StatusNotFound, "control not found")
		return
	}
	writeAttestError(w, http.StatusInternalServerError, "load control: "+err.Error())
}

// decodeJSONBObject unmarshals a JSONB column. nil/empty input returns
// nil so callers can short-circuit "no schema declared".
func decodeJSONBObject(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// deriveIdempotencyKey produces a deterministic key for retries. The
// caller-supplied request body bytes are sha256'd along with the user
// and control id so two identical attestations within milliseconds
// deduplicate, but any field-level change produces a fresh key.
func deriveIdempotencyKey(userID, controlID string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{0})
	h.Write([]byte(controlID))
	h.Write([]byte{0})
	h.Write(body)
	return "attest-" + hex.EncodeToString(h.Sum(nil))[:32]
}

func ingestErrorToStatus(err error) int {
	switch {
	case errors.Is(err, ingest.ErrMissingField):
		return http.StatusBadRequest
	case errors.Is(err, ingest.ErrUnknownKind):
		return http.StatusPreconditionFailed
	case errors.Is(err, ingest.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, ingest.ErrIdempotencyMismatch):
		return http.StatusConflict
	case errors.Is(err, ingest.ErrScopeViolation):
		return http.StatusForbidden
	case errors.Is(err, ingest.ErrObservedAtSkew):
		return http.StatusBadRequest
	case errors.Is(err, ingest.ErrOversized):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

func writeAttestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAttestError(w http.ResponseWriter, status int, msg string) {
	writeAttestJSON(w, status, attestErrorBody{Error: msg})
}
