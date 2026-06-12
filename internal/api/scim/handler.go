package scim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// maxBodyBytes caps a SCIM request body. IdP payloads for a single user are
// small; the cap is a cheap DoS guard.
const maxBodyBytes = 64 * 1024

// listResources is the default page size for an unfiltered SCIM List.
const (
	defaultCount = 100
	maxCount     = 200
	// maxStartIndex bounds the 1-based SCIM startIndex so the derived offset
	// stays well within int32 (no IdP paginates past this many users; the cap
	// is a DoS + overflow guard). RFC 7644 §3.4.2.4 lets the provider clamp.
	maxStartIndex = 1_000_000
)

// provisioner is the surface the handlers need from the SCIM provisioning
// store. Satisfied by *scim.Store.
type provisioner interface {
	Provision(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, in scim.ProvisionInput) (scim.DomainUser, error)
	GetByID(ctx context.Context, tenantID string, id uuid.UUID) (scim.DomainUser, error)
	List(ctx context.Context, tenantID string, limit, offset int) ([]scim.DomainUser, int, error)
	FindByUserName(ctx context.Context, tenantID, userName string) ([]scim.DomainUser, error)
	Replace(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID, in scim.ReplaceInput) (scim.DomainUser, error)
	Patch(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID, ops []scim.PatchOperation) (scim.DomainUser, error)
	Delete(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID) error
}

// Handler owns the SCIM /Users + discovery routes.
type Handler struct {
	store provisioner
}

// NewHandler constructs a Handler.
func NewHandler(store provisioner) *Handler { return &Handler{store: store} }

// Mount registers the SCIM routes onto a chi router rooted at /scim/v2. The
// caller is responsible for wrapping the subtree with Middleware (the auth +
// tenant-context gate).
func (h *Handler) Mount(r chi.Router) {
	r.Post("/scim/v2/Users", h.CreateUser)
	r.Get("/scim/v2/Users", h.ListUsers)
	r.Get("/scim/v2/Users/{id}", h.GetUser)
	r.Put("/scim/v2/Users/{id}", h.ReplaceUser)
	r.Patch("/scim/v2/Users/{id}", h.PatchUser)
	r.Delete("/scim/v2/Users/{id}", h.DeleteUser)
	r.Get("/scim/v2/ServiceProviderConfig", h.ServiceProviderConfig)
	r.Get("/scim/v2/ResourceTypes", h.ResourceTypes)
	r.Get("/scim/v2/Schemas", h.Schemas)
}

// usersLocation is the SCIM collection URL used to build meta.location.
func usersLocation(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		scheme = fp
	}
	return scheme + "://" + r.Host + "/scim/v2/Users"
}

// CreateUser handles POST /scim/v2/Users (AC-1 Create).
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	var body inboundUser
	if !decodeSCIMBody(w, r, &body) {
		return
	}
	if body.UserName == "" {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", "userName is required")
		return
	}
	in := scim.ProvisionInput{
		UserName:    body.UserName,
		DisplayName: body.resolveDisplayName(),
		ExternalID:  body.ExternalID,
		Active:      body.activeOrDefault(),
	}
	u, err := h.store.Provision(r.Context(), cred.credentialID, cred.tenantID, in)
	if err != nil {
		if errors.Is(err, scim.ErrConflict) {
			writeSCIMError(w, http.StatusConflict, "uniqueness", "a user with this userName or externalId already exists")
			return
		}
		httperr.WriteInternal(w, r, "scim create user", err)
		return
	}
	writeSCIMJSON(w, http.StatusCreated, scim.WireUser(u, usersLocation(r)))
}

// GetUser handles GET /scim/v2/Users/{id} (AC-1 Get).
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "user not found")
		return
	}
	u, err := h.store.GetByID(r.Context(), cred.tenantID, id)
	if err != nil {
		if errors.Is(err, scim.ErrUserNotFound) {
			// No oracle: a user in another tenant reads identically to "not
			// found" (P0-508-4 / STRIDE-I) because the query is RLS-confined.
			writeSCIMError(w, http.StatusNotFound, "", "user not found")
			return
		}
		httperr.WriteInternal(w, r, "scim get user", err)
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireUser(u, usersLocation(r)))
}

// ListUsers handles GET /scim/v2/Users (AC-1 List + filter).
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	loc := usersLocation(r)

	// Filter path: `filter=userName eq "x"` (AC-1 minimum).
	if filter := r.URL.Query().Get("filter"); filter != "" {
		val, present, ferr := scim.ParseUserNameFilter(filter)
		if ferr != nil {
			writeSCIMError(w, http.StatusBadRequest, "invalidFilter", ferr.Error())
			return
		}
		if present {
			users, err := h.store.FindByUserName(r.Context(), cred.tenantID, val)
			if err != nil {
				httperr.WriteInternal(w, r, "scim filter users", err)
				return
			}
			writeList(w, users, len(users), 1, loc)
			return
		}
	}

	// Unfiltered list with SCIM 1-based startIndex + count pagination
	// (RFC 7644 §3.4.2.4). Both values are clamped at the source so the
	// downstream int32 narrowing cannot overflow.
	startIndex, count, offset := scimPagination(
		r.URL.Query().Get("startIndex"), r.URL.Query().Get("count"))
	users, total, err := h.store.List(r.Context(), cred.tenantID, count, offset)
	if err != nil {
		httperr.WriteInternal(w, r, "scim list users", err)
		return
	}
	writeList(w, users, total, startIndex, loc)
}

// ReplaceUser handles PUT /scim/v2/Users/{id} (AC-1 Replace).
func (h *Handler) ReplaceUser(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "user not found")
		return
	}
	var body inboundUser
	if !decodeSCIMBody(w, r, &body) {
		return
	}
	in := scim.ReplaceInput{
		UserName:    body.UserName,
		DisplayName: body.resolveDisplayName(),
		Active:      body.activeOrDefault(),
	}
	u, err := h.store.Replace(r.Context(), cred.credentialID, cred.tenantID, id, in)
	if err != nil {
		if errors.Is(err, scim.ErrUserNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "user not found")
			return
		}
		httperr.WriteInternal(w, r, "scim replace user", err)
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireUser(u, usersLocation(r)))
}

// PatchUser handles PATCH /scim/v2/Users/{id} (AC-1 Patch). The store applies
// ONLY the {active, displayName} allow-list; a role-bearing op is ignored
// (P0-508-3).
func (h *Handler) PatchUser(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "user not found")
		return
	}
	var op scim.PatchOp
	if !decodeSCIMBody(w, r, &op) {
		return
	}
	if len(op.Operations) == 0 {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", "PatchOp requires at least one operation")
		return
	}
	u, err := h.store.Patch(r.Context(), cred.credentialID, cred.tenantID, id, op.Operations)
	if err != nil {
		if errors.Is(err, scim.ErrUserNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "user not found")
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireUser(u, usersLocation(r)))
}

// DeleteUser handles DELETE /scim/v2/Users/{id} (AC-4 / P0-508-1). It
// soft-disables (never hard-deletes) and returns 204.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "user not found")
		return
	}
	if err := h.store.Delete(r.Context(), cred.credentialID, cred.tenantID, id); err != nil {
		if errors.Is(err, scim.ErrUserNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "user not found")
			return
		}
		httperr.WriteInternal(w, r, "scim delete user", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- discovery (AC-2) ---

// ServiceProviderConfig handles GET /scim/v2/ServiceProviderConfig.
func (h *Handler) ServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	writeSCIMJSON(w, http.StatusOK, scim.ServiceProviderConfig())
}

// ResourceTypes handles GET /scim/v2/ResourceTypes.
func (h *Handler) ResourceTypes(w http.ResponseWriter, r *http.Request) {
	base := schemeHost(r) + "/scim/v2"
	writeSCIMJSON(w, http.StatusOK, scim.ResourceTypes(base))
}

// Schemas handles GET /scim/v2/Schemas.
func (h *Handler) Schemas(w http.ResponseWriter, r *http.Request) {
	writeSCIMJSON(w, http.StatusOK, scim.Schemas())
}

// --- helpers ---

func writeList(w http.ResponseWriter, users []scim.DomainUser, total, startIndex int, loc string) {
	resources := make([]any, 0, len(users))
	for _, u := range users {
		resources = append(resources, scim.WireUser(u, loc))
	}
	writeSCIMJSON(w, http.StatusOK, scim.ListResponse{
		Schemas:      []string{scim.SchemaListResponse},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

func decodeSCIMBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(dst); err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return false
	}
	return true
}

// inboundUser is the Create/Replace request projection. `Active` is a *bool so
// the handler can distinguish "omitted" (→ default enabled) from explicit
// false (→ deprovisioned). The SCIM core User struct uses a plain bool for the
// RESPONSE shape, where the field is always present.
type inboundUser struct {
	UserName    string     `json:"userName"`
	DisplayName string     `json:"displayName"`
	ExternalID  string     `json:"externalId"`
	Name        *scim.Name `json:"name"`
	Active      *bool      `json:"active"`
}

// resolveDisplayName prefers top-level displayName, then name.formatted, then
// userName.
func (u inboundUser) resolveDisplayName() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if u.Name != nil && u.Name.Formatted != "" {
		return u.Name.Formatted
	}
	return u.UserName
}

// activeOrDefault reads the SCIM `active` flag, defaulting to true (a freshly
// provisioned user is enabled unless the IdP explicitly sends active:false).
func (u inboundUser) activeOrDefault() bool {
	if u.Active == nil {
		return true
	}
	return *u.Active
}

func parsePositiveInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// scimPagination parses + CLAMPS the SCIM 1-based startIndex and count query
// params (RFC 7644 §3.4.2.4) and derives a non-negative offset. count is
// bounded to [1, maxCount]; startIndex is bounded to [1, maxStartIndex]. The
// returned (startIndex, count, offset) are all small non-negative ints, so the
// downstream int32 narrowing in the store cannot overflow. Returning the
// clamped startIndex lets the ListResponse echo the effective value.
func scimPagination(startIndexRaw, countRaw string) (startIndex, count, offset int) {
	startIndex = parsePositiveInt(startIndexRaw, 1)
	if startIndex > maxStartIndex {
		startIndex = maxStartIndex
	}
	count = parsePositiveInt(countRaw, defaultCount)
	if count > maxCount {
		count = maxCount
	}
	offset = startIndex - 1
	if offset < 0 {
		offset = 0
	}
	return startIndex, count, offset
}

func schemeHost(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		scheme = fp
	}
	return scheme + "://" + r.Host
}
