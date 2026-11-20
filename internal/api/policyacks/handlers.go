// Package policyacks serves the slice-023 HTTP API for the policy
// acknowledgment workflow.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	GET  /v1/me/acknowledgments                     pending acks for current user (AC-1);
//	                                                ?include=current adds the fresh ones (#2668)
//	POST /v1/policies/{id}/acknowledge              record an ack + emit evidence (AC-2);
//	                                                body {"policy_version_id"} pins the version (#2668)
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	"github.com/mgoodric/security-atlas/internal/authz"
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

// currentItem is a policy the caller has acknowledged, for its current
// version, within the window (#2668).
type currentItem struct {
	PolicyID        string    `json:"policy_id"`
	PolicyVersionID string    `json:"policy_version_id"`
	Title           string    `json:"title"`
	Version         string    `json:"version"`
	AcknowledgedAt  time.Time `json:"acknowledged_at"`
}

type pendingResponse struct {
	Pending []pendingItem `json:"pending"`
	Count   int           `json:"count"`
	// Current is set only for ?include=current (#2668).
	Current *[]currentItem `json:"current,omitempty"`
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
	withCurrent := includesCurrent(r)
	if _, isUser := credstore.CallerUserID(cred); !isUser {
		out := pendingResponse{
			Pending:       []pendingItem{},
			Count:         0,
			WindowSeconds: int64(policy.AcknowledgmentFreshness / time.Second),
		}
		if withCurrent {
			out.Current = &[]currentItem{}
		}
		httpresp.WriteJSON(w, http.StatusOK, out)

		return
	}
	acks, err := h.store.ForUser(ctx, CallerFromCred(cred))
	if err != nil {
		if errors.Is(err, policy.ErrAckMissingUser) {
			httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
			return
		}
		httperr.WriteInternal(w, r, "list pending acks", err)
		return
	}
	out := pendingResponse{
		Pending:       make([]pendingItem, 0, len(acks.Pending)),
		WindowSeconds: int64(policy.AcknowledgmentFreshness / time.Second),
	}
	for _, p := range acks.Pending {
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
	if withCurrent {
		current := make([]currentItem, 0, len(acks.Current))
		for _, p := range acks.Current {
			item := currentItem{
				PolicyID:        p.PolicyID.String(),
				PolicyVersionID: p.PolicyVersionID.String(),
				Title:           p.Title,
				Version:         p.Version,
			}
			if p.LastAcknowledgedAt != nil {
				item.AcknowledgedAt = *p.LastAcknowledgedAt
			}
			current = append(current, item)
		}
		out.Current = &current
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// includesCurrent reports whether the request asked for ?include=current.
// Parsed like the policy list's ?include=ack_rate: CSV or repeated
// params, unknown tokens ignored.
func includesCurrent(r *http.Request) bool {
	for _, v := range r.URL.Query()["include"] {
		for _, tok := range strings.Split(v, ",") {
			if strings.TrimSpace(tok) == "current" {
				return true
			}
		}
	}
	return false
}

// ackRequest is the optional POST body. PolicyVersionID pins the version
// the caller was shown; absent, the ack is for the policy id (#2668).
type ackRequest struct {
	PolicyVersionID string `json:"policy_version_id"`
}

// decodeAckRequest reads the optional body. An empty body, or one without
// policy_version_id, pins nothing.
func decodeAckRequest(r *http.Request) (*uuid.UUID, error) {
	if r.Body == nil {
		return nil, nil
	}
	var req ackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, errors.New("body must be a JSON object")
	}
	if req.PolicyVersionID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(req.PolicyVersionID)
	if err != nil {
		return nil, errors.New("policy_version_id must be a uuid")
	}
	return &id, nil
}

// Acknowledge serves POST /v1/policies/{id}/acknowledge.
//
// Flow (anti-criterion P0 honored):
//  1. Auth gate: no credential => 401; a credential not bound to a
//     user (a machine actor) => 403 (#2586).
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
	// #2586: an ack is a person attesting. A machine credential (an unbound
	// key, the bootstrap key) names no users row, so it is refused here,
	// before Record, rather than failing there on its non-uuid id. An
	// unbound key with a live label otherwise matches an "everyone" policy.
	if _, isUser := credstore.CallerUserID(cred); !isUser {
		httpresp.WriteError(w, http.StatusForbidden, "only a person can acknowledge a policy; this credential is not bound to a user")
		return
	}
	policyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "policy id must be a uuid")
		return
	}
	versionID, berr := decodeAckRequest(r)
	if berr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, berr.Error())
		return
	}
	if h.ingester == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "evidence ingest service not configured")
		return
	}
	ack, rerr := h.store.Record(ctx, policy.RecordInput{
		PolicyID:        policyID,
		Caller:          CallerFromCred(cred),
		PolicyVersionID: versionID,
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
		// GLOSSARY.md "Policy acknowledgment (slice 023)".
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
	case errors.Is(err, policy.ErrAckVersionMismatch):
		httpresp.WriteError(w, http.StatusConflict, "policy_version_id is not the version this policy id names; reload the policy and acknowledge the version shown")
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

// CallerFromCred hands the ack store the users(id) behind c, or "" when c
// is not a user (the store then answers ErrAckMissingUser) (#2183).
func CallerFromCred(c credstore.Credential) policy.AckCaller {
	userID := ""
	if u, ok := credstore.CallerUserID(c); ok {
		userID = u.String()
	}
	return policy.AckCaller{
		UserID: userID,
		// #2505: live tenant roles plus a key's own label, never a JWT
		// session's role claim. #2524: a member, one holding a live role
		// (authz.TenantRoleLabels), also matches a policy aimed at everyone.
		OwnerRoles: policy.WithEveryone(authz.AckRoleLabels(c), len(authz.TenantRoleLabels(c)) > 0),
		IsAdmin:    c.IsAdmin,
	}
}
