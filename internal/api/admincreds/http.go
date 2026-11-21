// Package admincreds is the HTTP surface for /v1/admin/credentials.
//
// It wraps the DB-backed apikeystore (slice 034) — issue, list, rotate,
// revoke — with JSON request/response shapes. The bearer plaintext is
// returned exactly once in the issue/rotate response and never persisted.
//
// Auth: every handler requires an admin of the caller's tenant
// (authz.IsTenantAdmin, #2503). A tenant admin by role, with no admin
// credential, works only with keys it could hold itself: it cannot mint,
// rotate or revoke an is_admin or is_approver key, and cannot label a key
// with a canonical role it does not hold live (mintWithinCaller). A tenant
// admin that is itself a key mints no key wider than itself: no later
// expiry, no wider scope, no other kinds (boundByCallerKey).
package admincreds

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the HTTP routes.
type Handler struct {
	store *apikeystore.Store
}

// New constructs a Handler.
func New(store *apikeystore.Store) *Handler { return &Handler{store: store} }

// IssueRequest is the JSON body for POST /v1/admin/credentials.
//
// Slice 051: the TenantID field is retained for back-compat error
// messaging only. Tenant is derived strictly from the calling credential
// (slice 033 D1 — "tenancy.Middleware sets app.current_tenant strictly
// from cred.TenantID; no handler-level overrides"). Any non-empty value
// supplied in this field causes Issue to return HTTP 400 with a
// descriptive error so legacy clients discover the contract change
// instead of seeing a JSON-decode failure or, worse, silent acceptance.
type IssueRequest struct {
	TenantID       string   `json:"tenant_id,omitempty"`
	ScopePredicate string   `json:"scope_predicate"`
	AllowedKinds   []string `json:"allowed_kinds"`
	TTLSeconds     int64    `json:"ttl_seconds"` // 0 = no expiry; at most maxTTLSeconds
	IsAdmin        bool     `json:"is_admin"`
	// IsApprover grants the approver role. A key minted here is a machine
	// key bound to no user, and only people approve policies (#1807), so
	// it cannot approve a policy even with this flag set; it still gates
	// the approver-only routes that do not require a person.
	IsApprover bool     `json:"is_approver"`
	OwnerRoles []string `json:"owner_roles"`
	// Label is an optional name for the key (#2384), shown in place of its
	// id: <Person> reads "API key '<label>' (owner: <user>)". Trimmed;
	// empty or all-space means no label.
	Label string `json:"label,omitempty"`
	// Purpose is empty for an ordinary key, or "policy-sync" (#2849) for
	// a key that may only sync policies: every other route refuses it. A
	// policy-sync key carries no other grant (checkPurpose). Any other
	// value is refused; reset-bootstrap alone mints bootstrap-recovery.
	Purpose string `json:"purpose,omitempty"`
}

// checkPurpose reports why a key with this purpose and these grants is
// refused, or "" when it is accepted. A policy-sync key holds nothing
// else: an admin, approver, role or kind grant would be dead weight on a
// key every route but policy sync refuses, and would read in the key
// list as access it does not have.
func checkPurpose(req IssueRequest) string {
	switch req.Purpose {
	case "":
		return ""
	case credstore.PurposePolicySync:
		if req.IsAdmin || req.IsApprover || len(req.OwnerRoles) > 0 || len(req.AllowedKinds) > 0 {
			return "a policy-sync key cannot also carry is_admin, is_approver, owner_roles or allowed_kinds"
		}
		return ""
	default:
		return "purpose must be empty or policy-sync"
	}
}

// maxLabelLen is the longest label, in characters (api_keys_label_chk).
const maxLabelLen = 100

// normalizeLabel trims a requested label and reports why it is refused,
// or "" when it is accepted. An empty result is no label.
func normalizeLabel(raw string) (string, string) {
	label := strings.TrimSpace(raw)
	if utf8.RuneCountInString(label) > maxLabelLen {
		return "", "label must be at most 100 characters"
	}
	for _, r := range label {
		// Cf covers the bidi overrides and isolates (U+202E, U+2066-2069)
		// and zero-width characters, which would make a label render as
		// something else.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", "label must not contain control or formatting characters"
		}
		// A quote would let a label close its own quotes in "API key
		// '<label>' (owner: <user>)" and fake a different owner.
		if r == '\'' {
			return "", "label must not contain '"
		}
	}
	return label, ""
}

// maxTTLSeconds is the largest ttl_seconds whose Duration does not
// overflow int64 nanoseconds (about 292 years). Anything larger would
// wrap, so Issue refuses it with 400 (#1914).
const maxTTLSeconds = math.MaxInt64 / int64(time.Second)

// IssueResponse carries the bearer plaintext (returned exactly once).
type IssueResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	BearerToken string    `json:"bearer_token"`
	Last4       string    `json:"last4"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   string    `json:"expires_at,omitempty"`
	Label       string    `json:"label,omitempty"`
	// Purpose is the key's purpose (#2849); absent on an ordinary key.
	Purpose string `json:"purpose,omitempty"`
}

// Issue handles POST /v1/admin/credentials.
//
// Slice 051: tenant is derived strictly from the calling credential.
// slice-033's tenancymw.Middleware already set app.current_tenant from
// cred.TenantID before this handler runs; we trust that GUC and pass
// cred.TenantID through to the store. The IssueRequest.TenantID field
// is rejected with a 400 if supplied, surfacing the contract change to
// legacy callers.
func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.TenantID != "" {
		httpresp.WriteError(w, http.StatusBadRequest, "tenant_id is not accepted; tenant is derived from the calling credential")
		return
	}
	// Range-check the seconds before converting: a huge value wraps
	// time.Duration to an arbitrary or negative TTL (#1914).
	if req.TTLSeconds < 0 || req.TTLSeconds > maxTTLSeconds {
		httpresp.WriteError(w, http.StatusBadRequest, "ttl_seconds must be between 0 (no expiry) and 9223372036")
		return
	}
	// The predicate is stored as JSONB and enforced at push (#2435); a
	// malformed one is refused here, not at the insert.
	predicate, err := scope.NormalizeCredentialPredicate(req.ScopePredicate)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	label, msg := normalizeLabel(req.Label)
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := checkPurpose(req); msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	// requireAdmin returning true guarantees the credential is in context.
	cred, _ := authctx.CredentialFromContext(r.Context())
	if msg := mintWithinCaller(cred, req.IsAdmin, req.IsApprover, req.OwnerRoles); msg != "" {
		httpresp.WriteError(w, http.StatusForbidden, msg)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if msg := boundByCallerKey(cred, ttl, predicate, req.AllowedKinds, time.Now()); msg != "" {
		httpresp.WriteError(w, http.StatusForbidden, msg)
		return
	}
	in := apikeystore.IssueInput{
		ScopePredicate: predicate,
		AllowedKinds:   req.AllowedKinds,
		TTL:            ttl,
		IsAdmin:        req.IsAdmin,
		IsApprover:     req.IsApprover,
		OwnerRoles:     req.OwnerRoles,
		Label:          label,
		Purpose:        req.Purpose,
	}
	// The minting admin is attribution only (#1947): the key is a machine
	// key, not the admin's, and keeps working after the admin is disabled.
	if m, merr := uuid.Parse(credstore.MinterUserID(cred)); merr == nil {
		in.MintedBy = &m
	}
	issued, plain, err := h.store.Issue(r.Context(), cred.TenantID, in, auditActor(cred))
	if err != nil {
		httperr.WriteInternal(w, r, "issue failed", err)
		return
	}
	resp := IssueResponse{
		ID:          issued.ID,
		TenantID:    issued.TenantID,
		BearerToken: plain,
		Last4:       issued.Last4,
		IssuedAt:    issued.IssuedAt,
		Label:       issued.Label,
		Purpose:     issued.Purpose,
	}
	if !issued.IssuedAt.IsZero() && issued.TTL > 0 {
		resp.ExpiresAt = issued.IssuedAt.Add(issued.TTL).Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ListItem is one row in the credentials list — never includes bearer_token.
type ListItem struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	ScopePredicate string     `json:"scope_predicate"`
	AllowedKinds   []string   `json:"allowed_kinds"`
	Last4          string     `json:"last4"`
	IssuedAt       time.Time  `json:"issued_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	IsAdmin        bool       `json:"is_admin"`
	IsApprover     bool       `json:"is_approver"`
	OwnerRoles     []string   `json:"owner_roles"`
	RotatedFrom    string     `json:"rotated_from,omitempty"`
	// ExpiresAt is IssuedAt + TTL; absent for a key that never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// MintedBy is the users(id) of the admin who minted the key, for
	// attribution only (#1947). Absent when no user minted it.
	MintedBy string `json:"minted_by,omitempty"`
	// Label is the admin-chosen name for the key (#2384). Absent when the
	// key has none.
	Label string `json:"label,omitempty"`
	// Purpose is why the key exists when it is not an ordinary key
	// (#2849): "policy-sync" or "bootstrap-recovery". Absent otherwise.
	Purpose string `json:"purpose,omitempty"`
}

// ListResponse is the GET /v1/admin/credentials shape.
type ListResponse struct {
	Items []ListItem `json:"items"`
}

// List handles GET /v1/admin/credentials.
//
// Slice 051: tenant is derived strictly from the calling credential.
// The previously-accepted ?tenant_id query parameter is rejected with
// 400 — see the Issue docstring for the rationale.
//
// ?minted_by=<user uuid> (#1953) narrows the list to the live keys that
// user minted: not revoked, not past its rotation grace, not past expiry. The admin UI reads it to
// warn, on the user revoke screen, about machine keys that outlive their
// minter. It only lists; it never revokes.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.URL.Query().Get("tenant_id") != "" {
		httpresp.WriteError(w, http.StatusBadRequest, "tenant_id query parameter is not accepted; tenant is derived from the calling credential")
		return
	}
	mintedBy := r.URL.Query().Get("minted_by")
	if mintedBy != "" {
		u, perr := uuid.Parse(mintedBy)
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "minted_by must be a user uuid")
			return
		}
		mintedBy = u.String()
	}
	// requireAdmin returning true guarantees the credential is in context.
	cred, _ := authctx.CredentialFromContext(r.Context())
	creds, err := h.store.List(r.Context(), cred.TenantID)
	if err != nil {
		httperr.WriteInternal(w, r, "list failed", err)
		return
	}
	if mintedBy != "" {
		creds = liveMintedBy(creds, mintedBy, time.Now())
	}
	items := make([]ListItem, 0, len(creds))
	for _, c := range creds {
		item := ListItem{
			ID:             c.ID,
			TenantID:       c.TenantID,
			ScopePredicate: c.ScopePredicate,
			AllowedKinds:   c.Kinds,
			Last4:          c.Last4,
			IssuedAt:       c.IssuedAt,
			IsAdmin:        c.IsAdmin,
			IsApprover:     c.IsApprover,
			OwnerRoles:     c.OwnerRoles,
			RotatedFrom:    c.RotatedFrom,
			MintedBy:       c.MintedBy,
			Label:          c.Label,
			Purpose:        c.Purpose,
		}
		if !c.LastUsedAt.IsZero() {
			lu := c.LastUsedAt
			item.LastUsedAt = &lu
		}
		if exp := c.ExpiresAt(); !exp.IsZero() {
			item.ExpiresAt = &exp
		}
		items = append(items, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ListResponse{Items: items})
}

// RotateResponse carries the successor's bearer plaintext + the
// predecessor's retirement deadline.
type RotateResponse struct {
	ID                   string    `json:"id"`
	BearerToken          string    `json:"bearer_token"`
	Last4                string    `json:"last4"`
	PredecessorExpiresAt time.Time `json:"predecessor_expires_at"`
}

// Rotate handles POST /v1/admin/credentials/:id/rotate.
func (h *Handler) Rotate(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	id := chi.URLParam(r, "id")
	uid, err := parseCredID(id)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid tenant in credential")
		return
	}
	if !h.callerMayHandle(w, r, cred, uid, true) {
		return
	}
	successor, plain, predExp, err := h.store.Rotate(ctx, cred.TenantID, uid, auditActor(cred))
	if err != nil {
		if errors.Is(err, apikeystore.ErrUnknownKey) {
			httpresp.WriteError(w, http.StatusNotFound, "unknown credential id")
			return
		}
		httperr.WriteInternal(w, r, "rotate failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RotateResponse{
		ID:                   successor.ID,
		BearerToken:          plain,
		Last4:                successor.Last4,
		PredecessorExpiresAt: predExp,
	})
}

// Revoke handles POST /v1/admin/credentials/:id/revoke.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	id := chi.URLParam(r, "id")
	uid, err := parseCredID(id)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	// Slice 033: tenancy.Middleware already set app.current_tenant from
	// cred.TenantID. Confirm; bail if absent (would mean misconfig).
	ctx := r.Context()
	if _, terr := tenancy.TenantFromContext(ctx); terr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid tenant in credential")
		return
	}
	if !h.callerMayHandle(w, r, cred, uid, false) {
		return
	}
	if err := h.store.Revoke(ctx, cred.TenantID, uid, auditActor(cred)); err != nil {
		if errors.Is(err, apikeystore.ErrUnknownKey) {
			httpresp.WriteError(w, http.StatusNotFound, "unknown credential id")
			return
		}
		httperr.WriteInternal(w, r, "revoke failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// liveMintedBy keeps the credentials minted by userID that are still live
// at now, matching apikeystore's keyLive. The store's list already drops
// revoked rows; this also drops a rotated-out key past its grace window
// (retires_at) and a key past its expiry.
func liveMintedBy(creds []credstore.Credential, userID string, now time.Time) []credstore.Credential {
	out := make([]credstore.Credential, 0, len(creds))
	for _, c := range creds {
		if c.MintedBy != userID {
			continue
		}
		if !c.RetiresAt.IsZero() && now.After(c.RetiresAt) {
			continue
		}
		if exp := c.ExpiresAt(); !exp.IsZero() && now.After(exp) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return false
	}
	// #2503: a tenant admin by its live roles manages its own tenant's API
	// keys, within mintWithinCaller.
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return false
	}
	return true
}

// auditActor is who the audit row names (#2609): the users(id) behind the
// caller (uuid.Nil for a caller that is not a user) and the acting
// credential's principal (#2329).
func auditActor(cred credstore.Credential) apikeystore.Actor {
	return apikeystore.ActorOf(cred)
}

// mintWithinCaller reports why cred may not hold a key with these flags and
// role labels, or "" when it may (#2503 D6). An admin credential
// (cred.IsAdmin: the super_admin claim, or an admin-flagged key) may mint
// anything, as before. A tenant admin by role may not mint an is_admin key,
// which would be an admin credential and reach the platform /v1/admin
// segments, nor an is_approver key. Each canonical role label must be one
// the caller holds live, and a live admin holds every tenant role; a
// free-text label grants nothing (#2595) and passes.
func mintWithinCaller(cred credstore.Credential, isAdmin, isApprover bool, ownerRoles []string) string {
	if cred.IsAdmin {
		return ""
	}
	if isAdmin {
		return "a tenant admin cannot mint an is_admin key"
	}
	if isApprover {
		return "a tenant admin cannot mint an is_approver key"
	}
	held := authz.TenantRoleLabels(cred)
	if containsRole(held, string(authz.RoleAdmin)) {
		return ""
	}
	for _, role := range ownerRoles {
		if !authz.IsCanonical(authz.Role(role)) {
			continue
		}
		if !containsRole(held, role) {
			return "owner_roles names " + role + ", which the caller does not hold"
		}
	}
	return ""
}

// boundByCallerKey reports why a key-as-tenant-admin caller may not mint a
// key with this TTL, scope predicate and kinds, or "" when it may (#2656).
// A minted key is no wider than the key minting it: it expires no later than
// the caller does, its scope is within the caller's (scope.PredicateWithin)
// and its kinds are a subset of the caller's (no kinds is every kind). An
// admin credential, a JWT session and any other non-key caller are not
// bound here.
func boundByCallerKey(cred credstore.Credential, ttl time.Duration, predicate string, kinds []string, now time.Time) string {
	if cred.IsAdmin || !strings.HasPrefix(cred.ID, "key_") {
		return ""
	}
	if exp := cred.ExpiresAt(); !exp.IsZero() {
		if ttl <= 0 || now.Add(ttl).After(exp) {
			return "ttl_seconds must end no later than the calling key expires (" + exp.UTC().Format(time.RFC3339) + ")"
		}
	}
	within, err := scope.PredicateWithin(predicate, cred.ScopePredicate)
	if err != nil || !within {
		return "scope_predicate must be within the calling key's scope"
	}
	if len(cred.Kinds) > 0 {
		if len(kinds) == 0 {
			return "allowed_kinds must be within the calling key's kinds"
		}
		for _, k := range kinds {
			if !containsRole(cred.Kinds, k) {
				return "allowed_kinds names " + k + ", which the calling key may not push"
			}
		}
	}
	return ""
}

// callerMayHandle refuses a rotate or revoke of a key the caller could not
// have minted (mintWithinCaller), writing the 403. A rotate hands back a new
// plaintext with the same flags, so a tenant admin rotating an is_admin key
// would get an admin credential. For the same reason a rotate is refused when
// the successor, which keeps the key's TTL, scope and kinds, would be wider
// than a calling key (boundByCallerKey, #2656). A rotate of another user's personal
// (user-bound) key is refused too: the plaintext would let the caller act as
// that user, and the user's own token would retire. Revoking one stays
// allowed. An unknown id passes here and the store answers 404.
func (h *Handler) callerMayHandle(w http.ResponseWriter, r *http.Request, cred credstore.Credential, id uuid.UUID, rotate bool) bool {
	if cred.IsAdmin {
		return true
	}
	creds, err := h.store.List(r.Context(), cred.TenantID)
	if err != nil {
		httperr.WriteInternal(w, r, "load credential", err)
		return false
	}
	for _, c := range creds {
		if parsed, perr := parseCredID(c.ID); perr != nil || parsed != id {
			continue
		}
		if msg := mintWithinCaller(cred, c.IsAdmin, c.IsApprover, c.OwnerRoles); msg != "" {
			httpresp.WriteError(w, http.StatusForbidden, "this key is outside what a tenant admin manages: "+msg)
			return false
		}
		if rotate {
			if msg := boundByCallerKey(cred, c.TTL, c.ScopePredicate, c.Kinds, time.Now()); msg != "" {
				httpresp.WriteError(w, http.StatusForbidden, "rotating this key would mint one wider than the calling key: "+msg)
				return false
			}
		}
		if rotate && credstore.OwnedByUser(c) {
			owner, _ := credstore.CallerUserID(c)
			self, isUser := credstore.CallerUserID(cred)
			if !isUser || owner != self {
				httpresp.WriteError(w, http.StatusForbidden, "a tenant admin cannot rotate another user's personal key; revoke it instead")
				return false
			}
		}
		return true
	}
	return true
}

func containsRole(labels []string, role string) bool {
	for _, l := range labels {
		if l == role {
			return true
		}
	}
	return false
}

func parseCredID(s string) (uuid.UUID, error) {
	s = strings.TrimPrefix(s, "key_")
	return uuid.Parse(s)
}
