// Package evidence implements the EvidenceIngestService gRPC service. The
// validation + hashing + idempotency dedup runs against in-memory stores;
// DB-backed ledger writes land in slice 013.
package evidence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/idemstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/canonjson"
)

type Service struct {
	evidencev1.UnimplementedEvidenceIngestServiceServer
	registry schemaregistry.Registry
	idem     idemstore.Store
	clock    func() *timestamppb.Timestamp
}

func New(registry schemaregistry.Registry, idem idemstore.Store, clock func() *timestamppb.Timestamp) *Service {
	if clock == nil {
		clock = timestamppb.Now
	}
	return &Service{registry: registry, idem: idem, clock: clock}
}

func (s *Service) Push(ctx context.Context, req *evidencev1.PushRequest) (*evidencev1.PushResponse, error) {
	if req == nil || req.GetRecord() == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}
	r := req.GetRecord()

	if msg := missingField(r); msg != "" {
		return nil, status.Error(codes.InvalidArgument, msg)
	}
	if !s.registry.IsRegistered(r.GetEvidenceKind(), r.GetSchemaVersion()) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"evidence_kind %q version %q is not registered",
			r.GetEvidenceKind(), r.GetSchemaVersion())
	}

	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no credential in context")
	}

	hash, err := canonjson.HashRecord(r)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "hash: %v", err)
	}

	candidate := &evidencev1.EvidenceReceipt{
		RecordId:     uuid.NewString(),
		Hash:         hash,
		IngestedAt:   s.clock(),
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
