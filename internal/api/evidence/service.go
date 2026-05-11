// Package evidence implements the EvidenceIngestService gRPC service.
//
// Slice 003 shipped this service against in-memory stores. Slice 013
// rewires the handler to call into internal/evidence/ingest.Service,
// which is the canonical ingestion-stage function — see canvas §4.3 and
// the package comment in internal/evidence/ingest. Both transports (this
// gRPC service and the REST handler in http.go) wrap the SAME ingest
// call; that is invariant 2 (ingestion and evaluation as separated
// stages, append-only ledger between).
//
// Slice 015 will swap ingest.Service.Process to publish to NATS
// JetStream instead of calling Postgres directly; the gRPC handler does
// not change.
package evidence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/idemstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// Service is the gRPC handler. It is parameterized over either the
// slice-013 DB-backed ingest service OR the slice-003 in-memory shim
// (used by unit tests that don't want a Postgres) so the gRPC wire shape
// stays stable across both. ingester != nil takes precedence; otherwise
// the legacy in-memory path runs.
type Service struct {
	evidencev1.UnimplementedEvidenceIngestServiceServer

	// Slice 013 path.
	ingester *ingest.Service

	// Slice 003 fallback (in-memory schema registry + idempotency store).
	registry schemaregistry.Registry
	idem     idemstore.Store
}

// New constructs the gRPC handler over the slice-013 DB-backed ingester.
// When ingester is nil, the handler falls back to the slice-003 in-memory
// path (registry + idem). Tests that don't need DB integration can pass
// the in-memory shim.
func New(ingester *ingest.Service, registry schemaregistry.Registry, idem idemstore.Store) *Service {
	return &Service{
		ingester: ingester,
		registry: registry,
		idem:     idem,
	}
}

// Push handles the gRPC streaming/unary RPC. Routes to the DB-backed
// ingest service when present, otherwise to the slice-003 in-memory
// surface.
func (s *Service) Push(ctx context.Context, req *evidencev1.PushRequest) (*evidencev1.PushResponse, error) {
	if req == nil || req.GetRecord() == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no credential in context")
	}

	if s.ingester != nil {
		receipt, decision, err := s.ingester.Process(ctx, req.GetRecord(), cred)
		if err != nil {
			return nil, decisionToGRPC(decision, err)
		}
		return &evidencev1.PushResponse{Receipt: ingest.ReceiptToProto(receipt)}, nil
	}

	// Slice 003 fallback path — kept for unit tests that don't bring a
	// pool, AND for compatibility with the existing pkg/sdk-go contract
	// tests. The fallback uses the in-memory registry + idem store; it
	// does NOT write to the ledger.
	r := req.GetRecord()
	if msg := missingField(r); msg != "" {
		return nil, status.Error(codes.InvalidArgument, msg)
	}
	if !s.registry.IsRegistered(r.GetEvidenceKind(), r.GetSchemaVersion()) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"evidence_kind %q version %q is not registered",
			r.GetEvidenceKind(), r.GetSchemaVersion())
	}
	hash, err := canonjson.HashRecord(r)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "hash: %v", err)
	}
	candidate := &evidencev1.EvidenceReceipt{
		RecordId:     legacyRecordID(),
		Hash:         hash,
		CredentialId: cred.ID,
	}
	existing, deduped, err := s.idem.LookupOrInsert(cred.TenantID, r.GetIdempotencyKey(), hash, candidate)
	if err != nil {
		var mismatch *idemstore.ErrHashMismatch
		if errors.As(err, &mismatch) {
			return nil, status.Errorf(codes.AlreadyExists,
				"idempotency_key %q reused with different content", mismatch.IdempotencyKey)
		}
		return nil, status.Errorf(codes.Internal, "idempotency check: %v", err)
	}
	if deduped {
		return &evidencev1.PushResponse{Receipt: existing}, nil
	}
	return &evidencev1.PushResponse{Receipt: candidate}, nil
}

// legacyRecordID returns a UUIDv4 for the slice-003 in-memory fallback
// path. The DB-backed slice-013 path mints UUIDv4 inside ingest.Service
// and returns it on the receipt; this helper exists only so the legacy
// path stays self-contained.
func legacyRecordID() string {
	return uuid.NewString()
}

// decisionToGRPC maps ingest decisions/errors to gRPC status codes.
// Mirrors the HTTP handler's status-code mapping so both transports
// stay aligned.
func decisionToGRPC(d ingest.Decision, err error) error {
	switch {
	case errors.Is(err, ingest.ErrMissingField):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ingest.ErrUnknownKind):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ingest.ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ingest.ErrIdempotencyMismatch):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ingest.ErrScopeViolation):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ingest.ErrObservedAtSkew):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ingest.ErrOversized):
		return status.Error(codes.ResourceExhausted, err.Error())
	}
	return status.Errorf(codes.Internal, "ingest: decision=%s err=%v", d, err)
}

// missingField returns the human-readable message for the first missing
// required field on r, or "" when every required field is set. The order
// follows the proto's field declaration order to give callers consistent
// errors.
func missingField(r *evidencev1.EvidenceRecord) string {
	switch {
	case r.GetIdempotencyKey() == "":
		return "idempotency_key is required"
	case r.GetEvidenceKind() == "":
		return "evidence_kind is required"
	case r.GetSchemaVersion() == "":
		return "schema_version is required"
	case r.GetControlId() == "":
		return "control_id is required"
	case len(r.GetScope()) == 0:
		return "scope is required (at least one dimension)"
	case r.GetObservedAt() == nil:
		return "observed_at is required"
	case r.GetResult() == evidencev1.Result_RESULT_UNSPECIFIED:
		return "result is required"
	case r.GetPayload() == nil:
		return "payload is required"
	case r.GetSourceAttribution() == nil:
		return "source_attribution is required"
	}
	for _, d := range r.GetScope() {
		if d.GetKey() == "" {
			return "scope dimension key is required"
		}
		if len(d.GetValues()) == 0 {
			return "scope dimension values is required"
		}
	}
	return ""
}
