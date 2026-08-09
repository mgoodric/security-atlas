// Package accessreviews serves the OE-670 HTTP API over the OE-628
// access-review campaign store (internal/accessreview). Routes
// (registered onto the platform root router by
// internal/api/register_accessreview.go):
//
//	POST /v1/access-reviews                                create (JSON = SCIM-sourced, multipart = manual CSV)
//	GET  /v1/access-reviews                                list campaigns (?status= filter)
//	GET  /v1/access-reviews/{id}                           one campaign + rollup + items
//	POST /v1/access-reviews/{id}/items/{itemID}/attest     keep/revoke attestation by the assigned reviewer
//	GET  /v1/access-reviews/{id}/revoke-list.csv           revoke-decision export (operator enforcement handoff)
//	POST /v1/access-reviews/{id}/complete                  complete — emits the CC6.3 completion evidence
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx + internal/api/tenancymw). The OE-628 store
// opens its own transaction per call and applies the tenant GUC; the
// package-local ReadStore (store.go) does the same for the reads the
// store lacks (the OE-663 precedent for API-owned reads over another
// slice's tables).
//
// Identity posture (the slice-384 actionplans stance): the campaign
// creator and the attesting reviewer are ALWAYS the verified
// credential's user id (via jwtmw.SubjectUserID), never a request-body
// string — attestations must be repudiation-proof. An attestation
// against an item assigned to a different reviewer is a 403; an item
// (or campaign) in another tenant is a 404, indistinguishable from a
// missing one (RLS + the tenant predicate never surface it).
//
// Boundary (OE-628 D5 carried forward): the revoke-list endpoint
// EXPORTS decisions; no route revokes access anywhere. Enforcement
// stays operator-side.
package accessreviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/accessreview"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// maxCreateBodyBytes caps a multipart create request (the manual CSV
	// plus form fields). A 100k-row entitlement snapshot is ~10 MiB; the
	// cap is applied via http.MaxBytesReader BEFORE the multipart parse
	// so no more than this ever comes off the wire (the artifacts-upload
	// posture).
	maxCreateBodyBytes = 16 << 20
	// maxMultipartMemory bounds the in-memory portion of the multipart
	// parse; the remainder spills to temp files.
	maxMultipartMemory = 1 << 20
)

// Handler bundles the OE-670 routes over the OE-628 store plus the
// package-local ReadStore.
type Handler struct {
	store  *accessreview.Store
	reader *ReadStore
}

// New constructs a Handler.
func New(store *accessreview.Store, reader *ReadStore) *Handler {
	return &Handler{store: store, reader: reader}
}

// ----- wire shapes -----

type scopeWire struct {
	Systems      []string `json:"systems"`
	Entitlements []string `json:"entitlements"`
	UserIDs      []string `json:"user_ids"`
}

type createReq struct {
	Name      string     `json:"name"`
	Source    string     `json:"source"`
	DueAt     *time.Time `json:"due_at"`
	Reviewers []string   `json:"reviewers"`
	Scope     scopeWire  `json:"scope"`
}

type attestReq struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type campaignWire struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Source           string     `json:"source"`
	Scope            scopeWire  `json:"scope"`
	Status           string     `json:"status"`
	DueAt            time.Time  `json:"due_at"`
	CreatedBy        string     `json:"created_by"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	EvidenceRecordID *string    `json:"evidence_record_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type itemWire struct {
	ID              string     `json:"id"`
	CampaignID      string     `json:"campaign_id"`
	System          string     `json:"system"`
	Entitlement     string     `json:"entitlement"`
	PrincipalUserID string     `json:"principal_user_id"`
	PrincipalEmail  string     `json:"principal_email"`
	ReviewerID      string     `json:"reviewer_id"`
	Status          string     `json:"status"`
	Decision        *string    `json:"decision,omitempty"`
	Reason          string     `json:"reason"`
	AttestedBy      *string    `json:"attested_by,omitempty"`
	AttestedAt      *time.Time `json:"attested_at,omitempty"`
	Source          string     `json:"source"`
	SourceRef       string     `json:"source_ref"`
}

// ----- handlers -----

// CreateCampaign handles POST /v1/access-reviews.
//
// Two request shapes:
//
//   - application/json — a SCIM-sourced campaign snapshotting the
//     tenant's live users ⨝ scim groups (source omitted or "scim").
//   - multipart/form-data — a manual-CSV campaign: fields name, due_at
//     (RFC3339), reviewers / scope_systems / scope_entitlements /
//     scope_user_ids (repeatable and/or comma-separated), plus the CSV
//     in the `file` part (columns system,entitlement,user_id[,email,
//     source_ref] — the OE-628 parseManualCSV contract).
//
// created_by is always the verified credential's user id.
func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, cred, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	creator := jwtmw.SubjectUserID(cred.UserID)
	if creator == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
		return
	}

	var in accessreview.CreateInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		// Body cap BEFORE the multipart parse — never read more than
		// this many bytes off the wire. +1 KiB headroom for the
		// multipart envelope itself (the artifacts-upload posture).
		r.Body = http.MaxBytesReader(w, r.Body, maxCreateBodyBytes+1024)
		if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httpresp.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d-byte cap", maxCreateBodyBytes))
				return
			}
			httpresp.WriteError(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "missing `file` form part (the entitlement CSV)")
			return
		}
		defer func() { _ = file.Close() }()
		dueAt, derr := parseDueAt(r.FormValue("due_at"))
		if derr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "due_at must be an RFC3339 timestamp")
			return
		}
		in = accessreview.CreateInput{
			Name:      r.FormValue("name"),
			Source:    accessreview.SourceManualCSV,
			DueAt:     dueAt,
			Reviewers: splitList(r.MultipartForm.Value["reviewers"]),
			Scope: accessreview.Scope{
				Systems:      splitList(r.MultipartForm.Value["scope_systems"]),
				Entitlements: splitList(r.MultipartForm.Value["scope_entitlements"]),
				UserIDs:      splitList(r.MultipartForm.Value["scope_user_ids"]),
			},
			ManualCSV: file,
		}
	} else {
		var req createReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		switch req.Source {
		case "", accessreview.SourceSCIM:
			// SCIM snapshot — the store default.
		case accessreview.SourceManualCSV:
			httpresp.WriteError(w, http.StatusBadRequest, "manual_csv campaigns are created via a multipart/form-data upload with a `file` part")
			return
		default:
			httpresp.WriteError(w, http.StatusBadRequest, "source must be scim or manual_csv")
			return
		}
		in = accessreview.CreateInput{
			Name:      req.Name,
			Source:    req.Source,
			Reviewers: req.Reviewers,
			Scope: accessreview.Scope{
				Systems:      req.Scope.Systems,
				Entitlements: req.Scope.Entitlements,
				UserIDs:      req.Scope.UserIDs,
			},
		}
		if req.DueAt != nil {
			in.DueAt = req.DueAt.UTC()
		}
	}
	in.CreatedBy = creator

	campaign, items, err := h.store.CreateCampaign(ctx, in)
	if err != nil {
		h.writeCreateErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{
		"campaign": campaignWireFrom(campaign),
		"items":    itemsWireFrom(items),
		"count":    len(items),
	})
}

// ListCampaigns handles GET /v1/access-reviews (?status= filter). A
// present-but-unknown status is a 400, not a silently-ignored filter
// (the slice-067 posture).
func (h *Handler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if !validListStatus(status) {
		httpresp.WriteError(w, http.StatusBadRequest, "status must be draft, active, completed, or cancelled")
		return
	}
	campaigns, err := h.reader.List(ctx, status)
	if err != nil {
		httperr.WriteInternal(w, r, "list access review campaigns", err)
		return
	}
	out := make([]campaignWire, len(campaigns))
	for i, c := range campaigns {
		out[i] = campaignWireFrom(c)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"campaigns": out, "count": len(out)})
}

// GetCampaign handles GET /v1/access-reviews/{id} — the campaign, its
// completion rollup, and its review items in one round-trip.
func (h *Handler) GetCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	campaign, err := h.reader.Get(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "get access review campaign", err)
		return
	}
	rollup, err := h.store.Rollup(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "roll up access review campaign", err)
		return
	}
	items, err := h.reader.Items(ctx, id)
	if err != nil {
		httperr.WriteInternal(w, r, "list access review items", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"campaign": campaignWireFrom(campaign),
		"rollup":   rollup,
		"items":    itemsWireFrom(items),
	})
}

// AttestItem handles POST /v1/access-reviews/{id}/items/{itemID}/attest.
//
// The attesting reviewer is the verified credential's user id — never a
// body field. Mapping (per the OE-670 acceptance criteria): an item that
// does not exist under this tenant + campaign is a 404; an item assigned
// to a DIFFERENT reviewer is a 403 (the assignment pre-check below is
// what distinguishes the two — the OE-628 store folds both into
// ErrNotFound); a bad decision or missing reason is a 422.
func (h *Handler) AttestItem(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, cred, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	reviewer := jwtmw.SubjectUserID(cred.UserID)
	if reviewer == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential carries no user id")
		return
	}
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "itemID must be a UUID")
		return
	}
	var req attestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	assigned, err := h.reader.ItemReviewer(ctx, campaignID, itemID)
	if err != nil {
		if errors.Is(err, accessreview.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "review item not found")
			return
		}
		httperr.WriteInternal(w, r, "resolve review item assignment", err)
		return
	}
	if assigned != reviewer {
		httpresp.WriteError(w, http.StatusForbidden, "review item is assigned to a different reviewer")
		return
	}
	item, err := h.store.Attest(ctx, accessreview.AttestInput{
		ItemID:     itemID,
		ReviewerID: reviewer,
		Decision:   req.Decision,
		Reason:     req.Reason,
	})
	if err != nil {
		h.writeStoreErr(w, r, "attest review item", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"item": itemWireFrom(item)})
}

// RevokeListCSV handles GET /v1/access-reviews/{id}/revoke-list.csv —
// the operator's enforcement handoff. It EXPORTS revoke decisions; no
// code path revokes access (OE-628 boundary).
func (h *Handler) RevokeListCSV(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if _, err := h.reader.Get(ctx, id); err != nil {
		h.writeStoreErr(w, r, "get access review campaign", err)
		return
	}
	decisions, err := h.store.RevokeList(ctx, id)
	if err != nil {
		httperr.WriteInternal(w, r, "load revoke list", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "access-review-"+id.String()+"-revoke-list.csv"))
	w.WriteHeader(http.StatusOK)
	// Headers are sent; a mid-write failure cannot change the status.
	_ = accessreview.WriteRevokeCSV(w, decisions)
}

// CompleteCampaign handles POST /v1/access-reviews/{id}/complete. The
// OE-628 store checks for pending items, writes the
// access_review.completion.v1 evidence record, and flips the campaign
// to completed in ONE transaction (decisions log OE-670 D1); completing
// an already-completed campaign is an idempotent 200 with the prior
// evidence id.
func (h *Handler) CompleteCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	rollup, err := h.store.Complete(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "complete access review campaign", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"rollup": rollup})
}

// ----- error mapping -----

// writeStoreErr maps OE-628 store errors to HTTP status codes:
//
//	ErrNotFound                          -> 404
//	ErrIncomplete                        -> 409
//	ErrReasonRequired/ErrInvalidDecision -> 422
//	anything else                        -> 500
func (h *Handler) writeStoreErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, accessreview.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "access review campaign not found")
	case errors.Is(err, accessreview.ErrIncomplete):
		httpresp.WriteError(w, http.StatusConflict, "campaign has pending review items")
	case errors.Is(err, accessreview.ErrReasonRequired),
		errors.Is(err, accessreview.ErrInvalidDecision):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}

// writeCreateErr maps CreateCampaign errors. Validation sentinels are
// 400s; an empty entitlement set is a 422 (the request was well-formed,
// the scope/CSV just matched nothing); CSV parse failures — which the
// OE-628 store returns as fmt-wrapped errors, not sentinels — are 422s
// recognized by their "csv" message fragment (every parseManualCSV
// error carries it, and no other error reachable from CreateCampaign
// does).
func (h *Handler) writeCreateErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accessreview.ErrNameRequired),
		errors.Is(err, accessreview.ErrDueRequired),
		errors.Is(err, accessreview.ErrCreatedByRequired),
		errors.Is(err, accessreview.ErrReviewerRequired):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, accessreview.ErrItemsRequired):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "no entitlement items matched the campaign scope")
	case strings.Contains(err.Error(), "csv"):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		httperr.WriteInternal(w, r, "create access review campaign", err)
	}
}

// ----- helpers -----

// tenantCtx returns the request context (tenant GUC already applied
// upstream) plus the resolved credential.
func (h *Handler) tenantCtx(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// validListStatus reports whether the ?status= filter value is empty or
// one of the OE-628 campaign statuses.
func validListStatus(status string) bool {
	switch status {
	case "", accessreview.StatusDraft, accessreview.StatusActive,
		accessreview.StatusCompleted, accessreview.StatusCancelled:
		return true
	}
	return false
}

// parseDueAt parses an optional RFC3339 form value. Empty is a zero
// time — the store rejects it with ErrDueRequired.
func parseDueAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// splitList flattens repeated form values, splitting each on commas
// and trimming whitespace — so both `reviewers=a&reviewers=b` and
// `reviewers=a,b` work. Deduplication stays in the store (cleanUnique).
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func campaignWireFrom(c accessreview.Campaign) campaignWire {
	out := campaignWire{
		ID:     c.ID.String(),
		Name:   c.Name,
		Source: c.Source,
		Scope: scopeWire{
			Systems:      append([]string{}, c.Scope.Systems...),
			Entitlements: append([]string{}, c.Scope.Entitlements...),
			UserIDs:      append([]string{}, c.Scope.UserIDs...),
		},
		Status:    c.Status,
		DueAt:     c.DueAt.UTC(),
		CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
	if c.CompletedAt != nil {
		t := c.CompletedAt.UTC()
		out.CompletedAt = &t
	}
	if c.EvidenceRecordID != nil {
		s := c.EvidenceRecordID.String()
		out.EvidenceRecordID = &s
	}
	return out
}

func itemWireFrom(item accessreview.Item) itemWire {
	out := itemWire{
		ID:              item.ID.String(),
		CampaignID:      item.CampaignID.String(),
		System:          item.System,
		Entitlement:     item.Entitlement,
		PrincipalUserID: item.PrincipalUserID,
		PrincipalEmail:  item.PrincipalEmail,
		ReviewerID:      item.ReviewerID,
		Status:          item.Status,
		Reason:          item.Reason,
		Source:          item.Source,
		SourceRef:       item.SourceRef,
	}
	if item.Decision != nil {
		d := *item.Decision
		out.Decision = &d
	}
	if item.AttestedBy != nil {
		a := *item.AttestedBy
		out.AttestedBy = &a
	}
	if item.AttestedAt != nil {
		t := item.AttestedAt.UTC()
		out.AttestedAt = &t
	}
	return out
}

func itemsWireFrom(items []accessreview.Item) []itemWire {
	out := make([]itemWire, len(items))
	for i, item := range items {
		out[i] = itemWireFrom(item)
	}
	return out
}
