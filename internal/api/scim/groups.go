package scim

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// Slice 733 — the SCIM /scim/v2/Groups resource (RFC 7644). It rides the SAME
// router + SAME per-tenant SCIM credential + SAME RLS as the slice-508 /Users
// resource. A Group carries membership ONLY; the membership is fed to the
// slice-509 grouprole resolver (the sole path to a role), so /Groups can NEVER
// grant a role outside the 509 mapping (P0-733-3). The resolver is REUSED, not
// re-implemented (P0-733-1).

// groupProvisioner is the surface the Group handlers need from the SCIM Group
// store. Satisfied by *scim.GroupStore.
type groupProvisioner interface {
	CreateGroup(ctx context.Context, tenantID string, in scim.CreateGroupInput) (scim.GroupResult, error)
	GetGroup(ctx context.Context, tenantID string, id uuid.UUID) (scim.GroupResult, error)
	ListGroups(ctx context.Context, tenantID string, limit, offset int) ([]scim.DomainGroup, int, error)
	FindGroupsByDisplayName(ctx context.Context, tenantID, displayName string) ([]scim.DomainGroup, error)
	ReplaceGroup(ctx context.Context, tenantID string, id uuid.UUID, in scim.ReplaceGroupInput) (scim.GroupResult, error)
	PatchGroup(ctx context.Context, tenantID string, id uuid.UUID, ops []scim.PatchOperation) (scim.GroupResult, error)
	DeleteGroup(ctx context.Context, tenantID string, id uuid.UUID) ([]string, error)
	GroupRefsForUser(ctx context.Context, tenantID, userID string) ([]string, error)
}

// RoleDeriver is the slice-509 group-to-role resolver surface the Group
// handlers REUSE to reconcile a user's group-derived roles after a membership
// change (AC-3). Satisfied by *grouprole.Resolver. It is injected as an
// interface so this package never re-implements derivation logic (P0-733-1) and
// stays decoupled from the resolver's concrete type.
type RoleDeriver interface {
	// Derive maps a VALIDATED group set to roles + reconciles. ctx MUST carry
	// the tenant RLS context (the SCIM middleware sets it). The grouprole
	// resolver's last-admin guard + fail-closed + no-auto-create hold here
	// exactly as on the OIDC path (P0-733-3 / P0-733-4 / AC-4).
	Derive(ctx context.Context, in DeriveRequest) error
}

// DeriveRequest is the minimal, package-local projection of grouprole's
// DeriveInput. The adapter in cmd/atlas (or httpserver) maps this to the
// concrete grouprole.DeriveInput so this package carries no grouprole import
// (no import cycle, P0-733-1 boundary stays explicit).
type DeriveRequest struct {
	UserID string
	Groups []string
}

// GroupHandler owns the SCIM /Groups routes. deriver may be nil (membership
// changes still persist; the re-derivation is simply skipped — the OIDC login
// path re-derives on next sign-in). When wired, every membership-affecting op
// re-derives the affected users' roles via the slice-509 resolver (AC-3).
type GroupHandler struct {
	store   groupProvisioner
	deriver RoleDeriver
}

// NewGroupHandler constructs a GroupHandler.
func NewGroupHandler(store groupProvisioner, deriver RoleDeriver) *GroupHandler {
	return &GroupHandler{store: store, deriver: deriver}
}

// MountGroups registers the SCIM /Groups routes onto a chi router rooted at
// /scim/v2. The caller wraps the subtree with the SCIM auth Middleware.
func (h *GroupHandler) MountGroups(r chi.Router) {
	r.Post("/scim/v2/Groups", h.CreateGroup)
	r.Get("/scim/v2/Groups", h.ListGroups)
	r.Get("/scim/v2/Groups/{id}", h.GetGroup)
	r.Put("/scim/v2/Groups/{id}", h.ReplaceGroup)
	r.Patch("/scim/v2/Groups/{id}", h.PatchGroup)
	r.Delete("/scim/v2/Groups/{id}", h.DeleteGroup)
}

// groupsLocation is the SCIM collection URL for groups (meta.location).
func groupsLocation(r *http.Request) string {
	return schemeHost(r) + "/scim/v2/Groups"
}

// CreateGroup handles POST /scim/v2/Groups (AC-2 Create).
func (h *GroupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	var body inboundGroupReq
	if !decodeSCIMBody(w, r, &body) {
		return
	}
	if body.DisplayName == "" {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", "displayName is required")
		return
	}
	res, err := h.store.CreateGroup(r.Context(), cred.tenantID, scim.CreateGroupInput{
		DisplayName: body.DisplayName,
		ExternalID:  body.ExternalID,
		MemberIDs:   memberIDsFromReq(body.Members),
	})
	if err != nil {
		if errors.Is(err, scim.ErrConflict) {
			writeSCIMError(w, http.StatusConflict, "uniqueness", "a group with this externalId already exists")
			return
		}
		httperr.WriteInternal(w, r, "scim create group", err)
		return
	}
	// AC-3: re-derive every initial member's roles via the slice-509 resolver.
	if derr := h.rederive(r.Context(), cred.tenantID, res.AffectedUsers); derr != nil {
		httperr.WriteInternal(w, r, "scim create group rederive", derr)
		return
	}
	writeSCIMJSON(w, http.StatusCreated, scim.WireGroup(res.Group, res.MemberIDs, groupsLocation(r)))
}

// GetGroup handles GET /scim/v2/Groups/{id} (AC-2 Get).
func (h *GroupHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "group not found")
		return
	}
	res, err := h.store.GetGroup(r.Context(), cred.tenantID, id)
	if err != nil {
		if errors.Is(err, scim.ErrGroupNotFound) {
			// No oracle: a group in another tenant reads identically to "not
			// found" (P0-733-4) because the query is RLS-confined.
			writeSCIMError(w, http.StatusNotFound, "", "group not found")
			return
		}
		httperr.WriteInternal(w, r, "scim get group", err)
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireGroup(res.Group, res.MemberIDs, groupsLocation(r)))
}

// ListGroups handles GET /scim/v2/Groups (AC-2 List + displayName filter).
func (h *GroupHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	loc := groupsLocation(r)

	if filter := r.URL.Query().Get("filter"); filter != "" {
		val, present, ferr := scim.ParseDisplayNameFilter(filter)
		if ferr != nil {
			writeSCIMError(w, http.StatusBadRequest, "invalidFilter", ferr.Error())
			return
		}
		if present {
			groups, err := h.store.FindGroupsByDisplayName(r.Context(), cred.tenantID, val)
			if err != nil {
				httperr.WriteInternal(w, r, "scim filter groups", err)
				return
			}
			writeGroupList(w, groups, len(groups), 1, loc)
			return
		}
	}

	startIndex, count, offset := scimPagination(
		r.URL.Query().Get("startIndex"), r.URL.Query().Get("count"))
	groups, total, err := h.store.ListGroups(r.Context(), cred.tenantID, count, offset)
	if err != nil {
		httperr.WriteInternal(w, r, "scim list groups", err)
		return
	}
	writeGroupList(w, groups, total, startIndex, loc)
}

// ReplaceGroup handles PUT /scim/v2/Groups/{id} (AC-2 Replace — wholesale
// membership replace).
func (h *GroupHandler) ReplaceGroup(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "group not found")
		return
	}
	var body inboundGroupReq
	if !decodeSCIMBody(w, r, &body) {
		return
	}
	res, err := h.store.ReplaceGroup(r.Context(), cred.tenantID, id, scim.ReplaceGroupInput{
		DisplayName: body.DisplayName,
		MemberIDs:   memberIDsFromReq(body.Members),
	})
	if err != nil {
		if errors.Is(err, scim.ErrGroupNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "group not found")
			return
		}
		httperr.WriteInternal(w, r, "scim replace group", err)
		return
	}
	if derr := h.rederive(r.Context(), cred.tenantID, res.AffectedUsers); derr != nil {
		httperr.WriteInternal(w, r, "scim replace group rederive", derr)
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireGroup(res.Group, res.MemberIDs, groupsLocation(r)))
}

// PatchGroup handles PATCH /scim/v2/Groups/{id} (AC-2 Patch — add/remove
// members). A membership change drives a re-derivation via the 509 resolver
// (AC-3).
func (h *GroupHandler) PatchGroup(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "group not found")
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
	res, err := h.store.PatchGroup(r.Context(), cred.tenantID, id, op.Operations)
	if err != nil {
		if errors.Is(err, scim.ErrGroupNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "group not found")
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	if derr := h.rederive(r.Context(), cred.tenantID, res.AffectedUsers); derr != nil {
		httperr.WriteInternal(w, r, "scim patch group rederive", derr)
		return
	}
	writeSCIMJSON(w, http.StatusOK, scim.WireGroup(res.Group, res.MemberIDs, groupsLocation(r)))
}

// DeleteGroup handles DELETE /scim/v2/Groups/{id} (AC-2 Delete). Soft-disables
// the group + clears membership; every former member is re-derived (their
// membership in this group is gone).
func (h *GroupHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	cred, ok := scimCredentialFromContext(r.Context())
	if !ok {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing SCIM credential")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", "group not found")
		return
	}
	affected, err := h.store.DeleteGroup(r.Context(), cred.tenantID, id)
	if err != nil {
		if errors.Is(err, scim.ErrGroupNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", "group not found")
			return
		}
		httperr.WriteInternal(w, r, "scim delete group", err)
		return
	}
	if derr := h.rederive(r.Context(), cred.tenantID, affected); derr != nil {
		httperr.WriteInternal(w, r, "scim delete group rederive", derr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rederive reconciles each affected user's group-derived roles through the
// slice-509 resolver (AC-3 / P0-733-1). For each user it gathers the user's
// FULL current validated group set (every active group they remain a member of)
// and calls Derive — so the resolver reconciles to current membership exactly
// as it does on the OIDC login path. An unmapped group contributes nothing
// (fail-closed, P0-733-3); the last-admin guard + manual-role preservation hold
// inside the resolver (AC-4). A nil deriver skips re-derivation (membership is
// persisted; the next OIDC login reconciles).
func (h *GroupHandler) rederive(ctx context.Context, tenantID string, users []string) error {
	if h.deriver == nil {
		return nil
	}
	for _, uid := range users {
		if uid == "" {
			continue
		}
		groups, err := h.store.GroupRefsForUser(ctx, tenantID, uid)
		if err != nil {
			return err
		}
		if err := h.deriver.Derive(ctx, DeriveRequest{UserID: uid, Groups: groups}); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---

// inboundGroupReq is the Create/Replace request projection. No role field
// (P0-733-3).
type inboundGroupReq struct {
	DisplayName string             `json:"displayName"`
	ExternalID  string             `json:"externalId"`
	Members     []scim.GroupMember `json:"members"`
}

func memberIDsFromReq(members []scim.GroupMember) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		if m.Value != "" {
			out = append(out, m.Value)
		}
	}
	return out
}

func writeGroupList(w http.ResponseWriter, groups []scim.DomainGroup, total, startIndex int, loc string) {
	resources := make([]any, 0, len(groups))
	for _, g := range groups {
		// List is a summary surface — members omitted per common IdP expectation.
		resources = append(resources, scim.WireGroup(g, nil, loc))
	}
	writeSCIMJSON(w, http.StatusOK, scim.ListResponse{
		Schemas:      []string{scim.SchemaListResponse},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}
