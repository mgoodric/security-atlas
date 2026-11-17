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
//
// #2111: an attestation carries a result, pass or fail, and a fail needs a
// fail_reason. The record's result is the submitted one, not a hard-coded
// pass. After the record lands the handler evaluates the control at once,
// because this path writes the ledger directly and never crosses the
// message bus the ingest subscriber listens on.
package controls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// AttestationKind is the platform evidence_kind used for manual
// attestations. Slice 014 ships the schema at this id at boot; #2111's
// `manual.attestation/1.2.0.json` adds result + fail_reason.
const (
	AttestationKind    = attestation.Kind
	AttestationVersion = "1.2.0"
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

	// evaluator, when set, evaluates the control right after an
	// attestation lands (#2111). Nil leaves evaluation to the scheduler.
	evaluator controlEvaluator

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

// idempotencyReader looks up an attestation already on the ledger by its
// idempotency key (#2509). poolControlReader implements it; the contract
// test's stub reader does not need to.
type idempotencyReader interface {
	AttestationByIdempotencyKey(ctx context.Context, tenantID string, controlID uuid.UUID, idemKey string) (priorAttestation, bool, error)
}

// priorAttestation is the record already under an idempotency key.
// superseded reports that the control has a later attestation than it,
// under the engine's latest-attestation order (#2520).
type priorAttestation struct {
	observedAt time.Time
	superseded bool
}

// AttestationByIdempotencyKey returns the tenant's record under idemKey,
// and whether a later attestation for controlID exists, inside one
// tenant-GUC read tx. found is false when there is none.
func (p poolControlReader) AttestationByIdempotencyKey(ctx context.Context, tenantID string, controlID uuid.UUID, idemKey string) (priorAttestation, bool, error) {
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return priorAttestation{}, false, fmt.Errorf("parse tenant: %w", err)
	}
	var (
		out   priorAttestation
		found bool
	)
	err = pgx.BeginTxFunc(ctx, p.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if terr := tenancy.ApplyTenant(ctx, tx); terr != nil {
			return terr
		}
		q := dbx.New(tx)
		rec, gerr := q.GetEvidenceRecordByIdempotency(ctx, dbx.GetEvidenceRecordByIdempotencyParams{
			TenantID:       pgtype.UUID{Bytes: tenantUUID, Valid: true},
			IdempotencyKey: &idemKey,
		})
		if errors.Is(gerr, pgx.ErrNoRows) {
			return nil
		}
		if gerr != nil {
			return gerr
		}
		// ingested_at is NOT NULL DEFAULT now() on evidence_records, so a
		// ledger row always carries it; the engine's zero fallback in
		// attestedAt is for fixtures only (#2520).
		if !rec.IngestedAt.Valid {
			return errors.New("evidence record has no ingested_at")
		}
		observed, ingested := rec.ObservedAt.Time.UTC(), rec.IngestedAt.Time.UTC()
		attested := observed
		if ingested.Before(observed) {
			attested = ingested
		}
		newer, eerr := q.ExistsAttestationAfter(ctx, dbx.ExistsAttestationAfterParams{
			TenantID:     pgtype.UUID{Bytes: tenantUUID, Valid: true},
			ControlID:    pgtype.UUID{Bytes: controlID, Valid: true},
			ControlRef:   controlID.String(),
			EvidenceKind: AttestationKind,
			ID:           rec.ID,
			AttestedAt:   pgtype.Timestamptz{Time: attested, Valid: true},
			IngestedAt:   pgtype.Timestamptz{Time: ingested, Valid: true},
			IDText:       uuid.UUID(rec.ID.Bytes).String(),
		})
		if eerr != nil {
			return eerr
		}
		out, found = priorAttestation{observedAt: observed, superseded: newer}, true
		return nil
	})
	return out, found, err
}

// attestRetryWindow is how long a server-keyed attestation dedupes: a
// retry of the same body inside one window returns the first receipt, and
// the same body after it is a new attestation (#2509).
const attestRetryWindow = 5 * time.Minute

// attestMaxFutureSkew is how far ahead of server time a client-supplied
// observed_at may be (#2509): enough for ordinary clock drift, far short
// of ingest's MaxObservedAtSkew.
const attestMaxFutureSkew = 5 * time.Minute

// controlEvaluator is the slice-012 engine call the handler runs after an
// attestation: the same EvaluateControl the ingest subscriber, scheduler
// and Evaluate now run.
type controlEvaluator interface {
	EvaluateControl(ctx context.Context, controlID uuid.UUID, trigger string, asOf time.Time) (int, error)
}

// attestEvaluateTimeout bounds the post-attestation evaluation. It matches
// Evaluate now's bound (#2110); a slower evaluation leaves the record in
// place and the next scheduled run picks the control up.
const attestEvaluateTimeout = 30 * time.Second

// WithEvaluator makes the handler evaluate the control after each
// attestation it records (#2111).
func (h *AttestHandler) WithEvaluator(e controlEvaluator) *AttestHandler {
	h.evaluator = e
	return h
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
	// Result is "pass" or "fail" (#2111). Absent means pass, which is what
	// every attestation before 1.2.0 was.
	Result string `json:"result,omitempty"`
	// FailReason is required when Result is "fail" and ignored otherwise.
	FailReason string `json:"fail_reason,omitempty"`
}

type attestSubmitResponse struct {
	RecordID     string `json:"record_id"`
	Hash         string `json:"hash"`
	IngestedAt   string `json:"ingested_at"`
	CredentialID string `json:"credential_id"`
	Deduplicated bool   `json:"deduplicated"`
	PayloadURI   string `json:"payload_uri,omitempty"`
	// Result is the outcome recorded, pass or fail (#2111).
	Result string `json:"result"`
	// Evaluated reports whether the control was evaluated after the record
	// landed. False means the evaluation failed or is not wired; the
	// scheduled run will pick the control up.
	Evaluated bool `json:"evaluated"`
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
	result, failReason, rerr := attestResult(req.Result, req.FailReason)
	if rerr != nil {
		writeAttestError(w, http.StatusBadRequest, rerr.Error())
		return
	}

	// Microsecond precision: the ledger stores observed_at as timestamptz,
	// and a retry that reuses a stored observed_at must hash the same.
	now := time.Now().UTC().Truncate(time.Microsecond)
	observed := now
	if req.ObservedAt != "" {
		parsed, perr := time.Parse(time.RFC3339Nano, req.ObservedAt)
		if perr != nil {
			parsed, perr = time.Parse(time.RFC3339, req.ObservedAt)
			if perr != nil {
				writeAttestError(w, http.StatusBadRequest, "observed_at must be RFC3339: "+perr.Error())
				return
			}
		}
		// #2509: a future-dated attestation would outvote every honest
		// later one until its date passed. Backdating only makes it lose,
		// so it stays allowed (within ingest's MaxObservedAtSkew).
		if parsed.After(now.Add(attestMaxFutureSkew)) {
			writeAttestError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("observed_at %s is in the future; at most %s ahead of server time is allowed",
					parsed.UTC().Format(time.RFC3339), attestMaxFutureSkew))
			return
		}
		observed = parsed
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

	// Idempotency key default (#2509): the inputs plus the retry window
	// the request falls in. A retry of the same body inside the window gets
	// the same key and deduplicates; the same text attested again later
	// gets a new key and is recorded. A caller may send its own key.
	idemKey, prior, found, kerr := h.resolveIdempotencyKey(ctx, cred.TenantID, cred.UserID, id, body, req.IdempotencyKey, now)
	if errors.Is(kerr, errAttestKeyChainExhausted) {
		writeAttestError(w, http.StatusConflict, kerr.Error())
		return
	}
	if kerr != nil {
		writeAttestError(w, http.StatusInternalServerError, "idempotency lookup: "+kerr.Error())
		return
	}
	// The server stamps observed_at when the body has none, so a retry
	// would carry a new time and hash differently. Reuse the first
	// request's observed_at so the retry deduplicates instead of 409ing.
	if req.ObservedAt == "" && found {
		observed = prior
	}

	// Build the manual.attestation.v1 payload. actor_type=human and
	// actor_id=cred.UserID are server-set; clients can never escalate
	// to a non-human source_attribution from this endpoint.
	payload := map[string]any{
		"attestor":  cred.UserID,
		"statement": req.Statement,
		"result":    result,
	}
	if failReason != "" {
		payload["fail_reason"] = failReason
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
		Result:     attestResultProto(result),
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
		Result:       result,
	}
	status := http.StatusCreated
	if receipt.Deduplicated {
		// A replay wrote nothing; the first request already evaluated.
		status = http.StatusOK
	} else {
		out.Evaluated = h.evaluateAfterAttest(r.Context(), id)
	}
	writeAttestJSON(w, status, out)
}

// ----- helpers -----

// attestResult normalises the submitted result (#2111). An empty result is
// a pass. A fail needs a non-blank fail_reason; a pass drops any reason
// sent with it. The 1.2.0 schema enforces the same rule on the ledger
// side; checking here gives the caller a plain 400 before anything is read.
func attestResult(result, failReason string) (string, string, error) {
	switch result {
	case "", eval.ResultPass:
		return eval.ResultPass, "", nil
	case eval.ResultFail:
		reason := strings.TrimSpace(failReason)
		if reason == "" {
			return "", "", errors.New("fail_reason is required when result is fail")
		}
		return eval.ResultFail, reason, nil
	}
	return "", "", fmt.Errorf("result must be %q or %q", eval.ResultPass, eval.ResultFail)
}

func attestResultProto(result string) evidencev1.Result {
	if result == eval.ResultFail {
		return evidencev1.Result_RESULT_FAIL
	}
	return evidencev1.Result_RESULT_PASS
}

// evaluateAfterAttest evaluates the control once the attestation is on the
// ledger, so the control page shows the new state without waiting for the
// scheduled run. The attestation is already recorded, so a failed
// evaluation is logged and reported as evaluated=false, never as an error.
// It runs detached from the request's cancellation, bounded by
// attestEvaluateTimeout.
func (h *AttestHandler) evaluateAfterAttest(ctx context.Context, controlID uuid.UUID) bool {
	if h.evaluator == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attestEvaluateTimeout)
	defer cancel()
	if _, err := h.evaluator.EvaluateControl(ctx, controlID, eval.TriggerIngest, eval.FarFuture); err != nil {
		slog.WarnContext(ctx, "evaluate control after attestation", "control_id", controlID.String(), "error", err)
		return false
	}
	return true
}

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
// caller-supplied request body bytes are sha256'd along with the user,
// the control id and the start of the retry window, so identical requests
// inside one window deduplicate, while any field-level change, or the same
// body in a later window, produces a fresh key (#2509).
func deriveIdempotencyKey(userID, controlID string, body []byte, window time.Time) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{0})
	h.Write([]byte(controlID))
	h.Write([]byte{0})
	h.Write(body)
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(window.Unix(), 10)))
	return "attest-" + hex.EncodeToString(h.Sum(nil))[:32]
}

// attestMaxKeyGenerations bounds how far one server-derived key steps
// inside a retry window (#2520). Each step needs a newer attestation
// between two identical bodies, so no person submitting by hand reaches it.
const attestMaxKeyGenerations = 32

// errAttestKeyChainExhausted is a keyless caller repeating one body past
// attestMaxKeyGenerations in a window. The caller can reach it, so it is a
// 409 telling them to send their own key, not a 500.
var errAttestKeyChainExhausted = fmt.Errorf("the same attestation was repeated more than %d times in one %s window without an idempotency_key; send an idempotency_key with each attestation", attestMaxKeyGenerations, attestRetryWindow)

// resolveIdempotencyKey picks the key the attestation is recorded under,
// and returns the observed_at of the record already under it, if any.
//
// A client-supplied key is used as-is and dedupes strictly. A
// server-derived key dedupes a retry, but not a repeat that a later
// attestation has overtaken (#2520): keyless fail, pass, fail inside one
// window would otherwise map the second fail onto the first, skip
// evaluation and leave the control passing. While the record under the
// key is superseded, the key steps to its next generation (key.1, key.2,
// ...); the first generation with no record, or whose record is still the
// latest, is used. A retry of a stepped request walks the same chain and
// stops on the same generation, so it still dedupes.
//
// Without a reader that can look keys up (unit tests) it reports no prior
// record, and a retry falls back to ingest's 409.
func (h *AttestHandler) resolveIdempotencyKey(ctx context.Context, tenantID, userID string, controlID uuid.UUID, body []byte, clientKey string, now time.Time) (string, time.Time, bool, error) {
	lr, canLookup := h.reader.(idempotencyReader)
	if clientKey != "" {
		if !canLookup {
			return clientKey, time.Time{}, false, nil
		}
		prior, found, err := lr.AttestationByIdempotencyKey(ctx, tenantID, controlID, clientKey)
		return clientKey, prior.observedAt, found, err
	}
	base := deriveIdempotencyKey(userID, controlID.String(), body, now.Truncate(attestRetryWindow))
	if !canLookup {
		return base, time.Time{}, false, nil
	}
	for gen := 0; gen < attestMaxKeyGenerations; gen++ {
		key := base
		if gen > 0 {
			key = base + "." + strconv.Itoa(gen)
		}
		prior, found, err := lr.AttestationByIdempotencyKey(ctx, tenantID, controlID, key)
		if err != nil {
			return "", time.Time{}, false, err
		}
		if !found {
			return key, time.Time{}, false, nil
		}
		if !prior.superseded {
			return key, prior.observedAt, true, nil
		}
	}
	return "", time.Time{}, false, errAttestKeyChainExhausted
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
