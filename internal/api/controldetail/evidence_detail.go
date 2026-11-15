// evidence_detail.go — GET /v1/evidence/{id} (#2137): one evidence-ledger
// record with its payload, for the evidence detail view.
//
// Access is the evidence list's: the OPA middleware evaluates the same
// resource type ("evidence", action "read"), and the handler repeats the
// requireControlRead role guard. Unlike the list, a denial answers 404, the
// same as an unknown id, so a caller without access cannot tell which
// evidence ids exist.
//
// Redaction runs at ingestion (slice 015), so the stored payload is already
// redacted under the rules in force when the record arrived. The handler
// re-applies the kind's CURRENT x-redaction-rules before serialising, so a
// rule added after ingest still hides the field on read. The stored row is
// never rewritten (invariant #2).
//
// The payload can carry personal data. Nothing here logs it.
package controldetail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/demoprov"
	"github.com/mgoodric/security-atlas/internal/evidence/redact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// errEvidenceNotFound is the store's answer when no row matches the id in
// the caller's tenant (absent, or another tenant's under RLS).
var errEvidenceNotFound = errors.New("controldetail: evidence not found")

// evidenceDetailReader is the read seam the detail handler uses. The
// production *Store satisfies it; unit tests inject a fixed-row stub.
type evidenceDetailReader interface {
	EvidenceByID(ctx context.Context, id uuid.UUID) (dbx.EvidenceRecord, error)
	DemoEvidence(ctx context.Context, ids []uuid.UUID) (demoprov.Set, error)
}

// RedactionLookup returns the x-redaction-rules for an evidence kind and
// schema version. schemaregistry.Service satisfies it (the same method
// ingest.RedactionLookup names).
type RedactionLookup interface {
	RedactionRulesFor(ctx context.Context, tenantID, kind, version string) ([]string, error)
}

// WithRedaction makes the detail handler re-apply the current redaction
// rules on read. Without it the stored (ingest-redacted) payload is served.
func (h *Handler) WithRedaction(l RedactionLookup) *Handler {
	h.redaction = l
	return h
}

// EvidenceByID reads one evidence record by id in the caller's tenant.
func (s *Store) EvidenceByID(ctx context.Context, id uuid.UUID) (dbx.EvidenceRecord, error) {
	var rec dbx.EvidenceRecord
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rec, qerr = q.GetEvidenceRecordByID(ctx, dbx.GetEvidenceRecordByIDParams{
			ID:       pgUUID(id),
			TenantID: pgUUID(tenantID),
		})
		if errors.Is(qerr, pgx.ErrNoRows) {
			return errEvidenceNotFound
		}
		return qerr
	})
	return rec, err
}

// evidenceDetailWire is the GET /v1/evidence/{id} record shape.
type evidenceDetailWire struct {
	EvidenceID        string          `json:"evidence_id"`
	EvidenceKind      *string         `json:"evidence_kind"`
	SchemaVersion     *string         `json:"schema_version"`
	ObservedAt        string          `json:"observed_at"`
	IngestedAt        string          `json:"ingested_at"`
	Result            string          `json:"result"`
	Payload           json.RawMessage `json:"payload"`
	PayloadURI        *string         `json:"payload_uri"`
	Source            json.RawMessage `json:"source"`
	SourceAttribution json.RawMessage `json:"source_attribution"`
	CredentialID      *string         `json:"credential_id"`
	IngestionPath     string          `json:"ingestion_path"`
	ControlID         *string         `json:"control_id"`
	ControlRef        string          `json:"control_ref"`
	ScopeCell         *string         `json:"scope_cell"`
	ContentHash       string          `json:"content_hash"`
	Demo              bool            `json:"demo,omitempty"`
}

// EvidenceDetail handles GET /v1/evidence/{id}.
func (h *Handler) EvidenceDetail(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasControlRead(cred) {
		writeEvidenceNotFound(w)
		return
	}
	tenantID, err := tenancy.TenantFromContext(r.Context())
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeEvidenceNotFound(w)
		return
	}

	rec, err := h.detail.EvidenceByID(r.Context(), id)
	if errors.Is(err, errEvidenceNotFound) {
		writeEvidenceNotFound(w)
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	payload, err := h.redactPayload(r.Context(), tenantID, rec)
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	demo, err := h.detail.DemoEvidence(r.Context(), []uuid.UUID{id})
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail", err)
		return
	}

	out := evidenceDetailWire{
		EvidenceID:        uuidString(rec.ID),
		EvidenceKind:      rec.EvidenceKind,
		SchemaVersion:     rec.SchemaVersion,
		ObservedAt:        tsString(rec.ObservedAt),
		IngestedAt:        tsString(rec.IngestedAt),
		Result:            string(rec.Result),
		Payload:           payload,
		PayloadURI:        rec.PayloadUri,
		Source:            jsonOrNull(rec.Provenance),
		SourceAttribution: jsonOrNull(rec.SourceAttribution),
		CredentialID:      rec.CredentialID,
		IngestionPath:     rec.IngestionPath,
		ControlID:         uuidPtr(rec.ControlID),
		ControlRef:        rec.ControlRef,
		ScopeCell:         uuidPtr(rec.ScopeID),
		ContentHash:       rec.Hash,
		Demo:              demo.Has(id),
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"evidence": out})
}

// redactPayload re-applies the kind's current redaction rules to the stored
// payload. A record with no kind, no lookup, no rules, or a non-object
// payload is served as stored.
func (h *Handler) redactPayload(ctx context.Context, tenantID string, rec dbx.EvidenceRecord) (json.RawMessage, error) {
	stored := jsonOrNull(rec.Payload)
	if h.redaction == nil || rec.EvidenceKind == nil || rec.SchemaVersion == nil {
		return stored, nil
	}
	rules, err := h.redaction.RedactionRulesFor(ctx, tenantID, *rec.EvidenceKind, *rec.SchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("redaction lookup: %w", err)
	}
	if len(rules) == 0 {
		return stored, nil
	}
	var obj structpb.Struct
	if protojson.Unmarshal(stored, &obj) != nil {
		// Not a JSON object, so no rule's field path can match.
		return stored, nil
	}
	redacted, err := redact.Apply(&obj, rules)
	if err != nil {
		return nil, fmt.Errorf("redaction apply: %w", err)
	}
	b, err := protojson.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("redaction marshal: %w", err)
	}
	return b, nil
}

func writeEvidenceNotFound(w http.ResponseWriter) {
	httpresp.WriteError(w, http.StatusNotFound, "evidence not found")
}
