// Package policyacks serves the slice-023 HTTP API for the policy
// acknowledgment workflow.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	GET  /v1/me/acknowledgments                     pending acks for current user (AC-1)
//	POST /v1/policies/{id}/acknowledge              record an ack + emit evidence (AC-2)
//	GET  /v1/policies/{id}/acknowledgment-rate      numerator/denominator/percent (AC-6)
//
// All handlers run with the tenant set by upstream auth middleware
// (slice 033 tenancy.Middleware). The store opens its own transaction
// per call and applies the tenant GUC.
//
// Anti-criteria honored (P0):
//   - Anonymous ack rejected: authctx.CredentialFromContext absent => 401.
//   - Stale acks not counted: AckStore.Rate uses a freshness cutoff
//     parameter; counts only rows >= now - AcknowledgmentFreshness.
//   - Superseded ack not counted: AckStore.Record returns
//     ErrAckPolicyNotPublished for non-published rows; handler maps to 409.
//
// Evidence emission (AC-2): the ack handler calls AckStore.Record to
// reserve the row, then calls ingest.Service.Process to emit one
// policy.acknowledgment.v1 record, then backfills evidence_record_id.
// The ack row is authoritative even if the evidence emission fails
// downstream -- the slice-013 audit log captures the rejection and the
// row is replayable.
package policyacks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Evidence kind + version this slice ships. The schema file lives at
// internal/api/schemaregistry/schemas/policy.acknowledgment/1.0.0.json
// and is also listed in registry.DefaultSeed.
const (
	EvidenceKind    = "policy.acknowledgment.v1"
	EvidenceVersion = "1.0.0"
	// IngestionPath tags every record this handler emits. Canvas §4.7
	// enumerates the allowed values; "manual_upload" is the umbrella for
	// human-driven evidence writes (same choice as slice 011's manual
	// attestation handler).
	IngestionPath = "manual_upload"
)

// Handler wires the three routes over a single AckStore + ingest.Service.
// ingester may be nil when the platform is started without an ingest
// service (unit-only); the POST route then returns 503.
type Handler struct {
	store    *policy.AckStore
	ingester *ingest.Service
}

// New constructs a Handler. ingester may be nil; the POST handler
// short-circuits to 503 when so.
func New(store *policy.AckStore, ingester *ingest.Service) *Handler {
	return &Handler{store: store, ingester: ingester}
}

// ----- wire shapes -----

type pendingItem struct {
	PolicyID           string     `json:"policy_id"`
	PolicyVersionID    string     `json:"policy_version_id"`
	Title              string     `json:"title"`
	Version            string     `json:"version"`
	EffectiveDate      *string    `json:"effective_date,omitempty"`
	RequiredRoles      []string   `json:"required_roles"`
	LastAcknowledgedAt *time.Time `json:"last_acknowledged_at,omitempty"`
}

type pendingResponse struct {
	Pending []pendingItem `json:"pending"`
	Count   int           `json:"count"`
	// WindowSeconds tells the frontend how to compute "fresh until"
	// without hardcoding 365 days.
	WindowSeconds int64 `json:"window_seconds"`
}

type ackResponse struct {
	ID                  string    `json:"id"`
	PolicyID            string    `json:"policy_id"`
	PolicyVersionID     string    `json:"policy_version_id"`
	UserID              string    `json:"user_id"`
	AcknowledgedAt      time.Time `json:"acknowledged_at"`
	AckToken            string    `json:"ack_token"`
	EvidenceRecordID    *string   `json:"evidence_record_id,omitempty"`
	Deduplicated        bool      `json:"deduplicated"`
	EvidenceEmissionOK  bool      `json:"evidence_emission_ok"`
	EvidenceEmissionErr *string   `json:"evidence_emission_error,omitempty"`
}

type rateResponse struct {
	Numerator     int64    `json:"numerator"`
	Denominator   int64    `json:"denominator"`
	Percent       *float64 `json:"percent"`
	WindowSeconds int64    `json:"window_seconds"`
}

// ----- handlers -----

// MyAcknowledgments serves GET /v1/me/acknowledgments.
//
// Slice 150 — empty-set robustness: when the calling credential is a
// service-account-shaped bootstrap key (UserID is non-empty but not a
// UUID), the caller has no human "pending acks" — return a 200 with an
// empty pending list, not a 500. The dashboard panel on a fresh install
// reads this endpoint with the bootstrap-owner credential; it MUST
// render an empty list, not the upstream parse error. Real human
// credentials (post-slice-034 OIDC) carry a UUID UserID and follow the
// normal path. See docs/issues/150-empty-set-robustness-audit-across-
// list-endpoints.md AC-5.
func (h *Handler) MyAcknowledgments(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !isUUIDUser(cred.UserID) {
		httpresp.WriteJSON(w, http.StatusOK, pendingResponse{
			Pending:       []pendingItem{},
			Count:         0,
			WindowSeconds: int64(policy.AcknowledgmentFreshness / time.Second),
		})

		return
	}
	pending, err := h.store.PendingForUser(ctx, ackCallerFromCred(cred))
	if err != nil {
		if errors.Is(err, policy.ErrAckMissingUser) {
			httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
			return
		}
		httperr.WriteInternal(w, r, "list pending acks", err)
		return
	}
	out := pendingResponse{
		Pending:       make([]pendingItem, 0, len(pending)),
		WindowSeconds: int64(policy.AcknowledgmentFreshness / time.Second),
	}
	for _, p := range pending {
		item := pendingItem{
			PolicyID:        p.PolicyID.String(),
			PolicyVersionID: p.PolicyVersionID.String(),
			Title:           p.Title,
			Version:         p.Version,
			RequiredRoles:   append([]string{}, p.RequiredRoles...),
		}
		if p.EffectiveDate != nil {
			s := p.EffectiveDate.Format("2006-01-02")
			item.EffectiveDate = &s
		}
		if p.LastAcknowledgedAt != nil {
			t := *p.LastAcknowledgedAt
			item.LastAcknowledgedAt = &t
		}
		out.Pending = append(out.Pending, item)
	}
	out.Count = len(out.Pending)
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// Acknowledge serves POST /v1/policies/{id}/acknowledge.
//
// Flow (anti-criterion P0 honored):
//  1. Auth gate: cred.UserID == "" => 401.
//  2. Parse id; reject malformed.
//  3. Reserve the ack row via AckStore.Record. Returns:
//     ErrAckPolicyNotPublished => 409 (P0-3).
//     ErrAckNotRequired        => 403 (AC-3).
//     ErrNotFound              => 404.
//     dedup                    => 200 with prior receipt.
//  4. Emit one policy.acknowledgment.v1 evidence record via ingest.
//  5. Backfill evidence_record_id (best-effort).
//
// On evidence emission failure the ack row STAYS — the slice-013 audit
// log captures the rejection and operators can replay. The response
// reports evidence_emission_ok=false so the UI can warn.
func (h *Handler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if cred.UserID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
		return
	}
	policyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "policy id must be a uuid")
		return
	}
	if h.ingester == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "evidence ingest service not configured")
		return
	}
	ack, rerr := h.store.Record(ctx, policy.RecordInput{
		PolicyID: policyID,
		Caller:   ackCallerFromCred(cred),
	})
	if rerr != nil {
		h.writeRecordError(w, r, rerr)
		return
	}
	// Build the evidence payload.
	payload := map[string]any{
		"policy_id":         ack.PolicyID.String(),
		"policy_version_id": ack.PolicyVersionID.String(),
		"user_id":           ack.UserID.String(),
		"acknowledged_at":   ack.AcknowledgedAt.UTC().Format(time.RFC3339Nano),
	}
	payloadStruct, perr := structpb.NewStruct(payload)
	if perr != nil {
		// Domain row exists; surface the emission failure but keep
		// the row.
		h.writeAckResp(w, ack, false, perr)
		return
	}
	rec := &evidencev1.EvidenceRecord{
		IdempotencyKey: ack.AckToken,
		EvidenceKind:   EvidenceKind,
		SchemaVersion:  EvidenceVersion,
		// control_id is a non-UUID reference; ingest stores it in
		// control_ref only (ingest.go line 386-392). This is legit per
		// CONTEXT.md "Policy acknowledgment (slice 023)".
		ControlId: fmt.Sprintf("policy:%s:v%s", ack.PolicyID.String(), ack.PolicyVersionID.String()),
		Scope: []*evidencev1.ScopeDimension{{
			Key:    "policy_id",
			Values: []string{ack.PolicyID.String()},
		}},
		ObservedAt: timestamppb.New(ack.AcknowledgedAt),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payloadStruct,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "human",
			ActorId:   cred.UserID,
			SessionId: cred.ID,
		},
	}
	pathed := h.ingester.WithPath(IngestionPath)
	receipt, _, ierr := pathed.Process(ctx, rec, cred)
	if ierr != nil {
		h.writeAckResp(w, ack, false, ierr)
		return
	}
	// Backfill the evidence_record_id on the ack row. Best-effort: a
	// failure here doesn't unwind the evidence (the receipt is already
	// authoritative).
	if recordUUID, perr := uuid.Parse(receipt.RecordID); perr == nil {
		if uerr := h.store.SetEvidenceRecordID(ctx, ack.ID, recordUUID); uerr == nil {
			ack.EvidenceRecordID = &recordUUID
		}
	}
	h.writeAckResp(w, ack, true, nil)
}

// AcknowledgmentRate serves GET /v1/policies/{id}/acknowledgment-rate.
func (h *Handler) AcknowledgmentRate(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	policyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "policy id must be a uuid")
		return
	}
	rate, rerr := h.store.Rate(ctx, policyID)
	if rerr != nil {
		switch {
		case errors.Is(rerr, policy.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "policy not found")
		case errors.Is(rerr, policy.ErrAckPolicyNotPublished):
			httpresp.WriteError(w, http.StatusConflict, "policy is not in published state")
		default:
			httperr.WriteInternal(w, r, "compute rate", rerr)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, rateResponse{
		Numerator:     rate.Numerator,
		Denominator:   rate.Denominator,
		Percent:       rate.Percent,
		WindowSeconds: rate.WindowSeconds,
	})

}

// ----- helpers -----

func (h *Handler) writeAckResp(w http.ResponseWriter, ack policy.Ack, emissionOK bool, emissionErr error) {
	resp := ackResponse{
		ID:                 ack.ID.String(),
		PolicyID:           ack.PolicyID.String(),
		PolicyVersionID:    ack.PolicyVersionID.String(),
		UserID:             ack.UserID.String(),
		AcknowledgedAt:     ack.AcknowledgedAt,
		AckToken:           ack.AckToken,
		Deduplicated:       ack.Deduplicated,
		EvidenceEmissionOK: emissionOK,
	}
	if ack.EvidenceRecordID != nil {
		s := ack.EvidenceRecordID.String()
		resp.EvidenceRecordID = &s
	}
	if emissionErr != nil {
		s := emissionErr.Error()
		resp.EvidenceEmissionErr = &s
	}
	status := http.StatusCreated
	if ack.Deduplicated {
		status = http.StatusOK
	}
	httpresp.WriteJSON(w, status, resp)
}

func (h *Handler) writeRecordError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "policy not found")
	case errors.Is(err, policy.ErrAckPolicyNotPublished):
		httpresp.WriteError(w, http.StatusConflict, "policy is not in published state")
	case errors.Is(err, policy.ErrAckNotRequired):
		httpresp.WriteError(w, http.StatusForbidden, "caller's roles do not intersect the policy's acknowledgment_required_roles")
	case errors.Is(err, policy.ErrAckMissingUser):
		httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
	case errors.Is(err, policy.ErrAckMissingPolicyID):
		httpresp.WriteError(w, http.StatusBadRequest, "policy id is required")
	default:
		httperr.WriteInternal(w, r, "record acknowledgment", err)
	}
}

func (h *Handler) tenantCredContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// isUUIDUser reports whether the credential's UserID is a parseable
// UUID. Slice 150 — a non-UUID UserID is the platform's marker for a
// service-account / bootstrap credential; the user-pending-acks read
// path returns an empty envelope rather than 500-ing the dashboard
// panel. See MyAcknowledgments for the contract.
func isUUIDUser(id string) bool {
	if id == "" {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func ackCallerFromCred(c credstore.Credential) policy.AckCaller {
	return policy.AckCaller{
		UserID:     c.UserID,
		OwnerRoles: append([]string(nil), c.OwnerRoles...),
		IsAdmin:    c.IsAdmin,
	}
}
