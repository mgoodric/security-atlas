package scim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scim"
)

// Pure-Go unit tests for the slice-733 SCIM /Groups handlers — the no-DB
// surface: the auth gate + the re-derivation REUSE contract (P0-733-1). The
// DB-backed CRUD + cross-tenant RLS proofs live in the integration suite.

func TestGroupHandlers_RejectMissingCredential(t *testing.T) {
	t.Parallel()
	h := NewGroupHandler(nil, nil) // credential check fires before any store call
	cases := []struct {
		name   string
		method string
		fn     http.HandlerFunc
	}{
		{"create", http.MethodPost, h.CreateGroup},
		{"get", http.MethodGet, h.GetGroup},
		{"list", http.MethodGet, h.ListGroups},
		{"replace", http.MethodPut, h.ReplaceGroup},
		{"patch", http.MethodPatch, h.PatchGroup},
		{"delete", http.MethodDelete, h.DeleteGroup},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, "/scim/v2/Groups", nil)
			rec := httptest.NewRecorder()
			tc.fn(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d; want 401", rec.Code)
			}
		})
	}
}

// fakeGroupStore is an in-memory groupProvisioner for the handler tests. It
// records the AffectedUsers a mutation reports + the group_refs it would feed
// the resolver, so a test can assert the handler drove re-derivation through
// the resolver (and did NOT compute roles itself — P0-733-1).
type fakeGroupStore struct {
	created       scim.CreateGroupInput
	affected      []string
	groupRefs     map[string][]string // userID -> group refs
	patchAffected []string
	listGroups    []scim.DomainGroup
	listTotal     int
	notFound      bool  // GetGroup/ReplaceGroup/PatchGroup/DeleteGroup → ErrGroupNotFound
	conflict      bool  // CreateGroup → ErrConflict
	storeErr      error // any method → generic error (500 path)
}

func (f *fakeGroupStore) CreateGroup(_ context.Context, _ string, in scim.CreateGroupInput) (scim.GroupResult, error) {
	f.created = in
	if f.conflict {
		return scim.GroupResult{}, scim.ErrConflict
	}
	if f.storeErr != nil {
		return scim.GroupResult{}, f.storeErr
	}
	return scim.GroupResult{
		Group:         scim.DomainGroup{ID: uuid.New(), DisplayName: in.DisplayName, ExternalID: in.ExternalID},
		MemberIDs:     in.MemberIDs,
		AffectedUsers: in.MemberIDs,
	}, nil
}
func (f *fakeGroupStore) GetGroup(_ context.Context, _ string, id uuid.UUID) (scim.GroupResult, error) {
	if f.notFound {
		return scim.GroupResult{}, scim.ErrGroupNotFound
	}
	return scim.GroupResult{Group: scim.DomainGroup{ID: id, DisplayName: "g"}}, nil
}
func (f *fakeGroupStore) ListGroups(_ context.Context, _ string, _, _ int) ([]scim.DomainGroup, int, error) {
	if f.storeErr != nil {
		return nil, 0, f.storeErr
	}
	return f.listGroups, f.listTotal, nil
}
func (f *fakeGroupStore) FindGroupsByDisplayName(_ context.Context, _, _ string) ([]scim.DomainGroup, error) {
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	return f.listGroups, nil
}
func (f *fakeGroupStore) ReplaceGroup(_ context.Context, _ string, id uuid.UUID, in scim.ReplaceGroupInput) (scim.GroupResult, error) {
	if f.notFound {
		return scim.GroupResult{}, scim.ErrGroupNotFound
	}
	return scim.GroupResult{Group: scim.DomainGroup{ID: id, DisplayName: in.DisplayName}, MemberIDs: in.MemberIDs, AffectedUsers: in.MemberIDs}, nil
}
func (f *fakeGroupStore) PatchGroup(_ context.Context, _ string, id uuid.UUID, _ []scim.PatchOperation) (scim.GroupResult, error) {
	if f.notFound {
		return scim.GroupResult{}, scim.ErrGroupNotFound
	}
	return scim.GroupResult{Group: scim.DomainGroup{ID: id, DisplayName: "g"}, AffectedUsers: f.patchAffected}, nil
}
func (f *fakeGroupStore) DeleteGroup(_ context.Context, _ string, _ uuid.UUID) ([]string, error) {
	if f.notFound {
		return nil, scim.ErrGroupNotFound
	}
	return f.affected, nil
}
func (f *fakeGroupStore) GroupRefsForUser(_ context.Context, _, userID string) ([]string, error) {
	return f.groupRefs[userID], nil
}

// recordingDeriver records every Derive call so a test can assert the handler
// REUSED the resolver (called Derive with the store-provided group set) rather
// than computing roles itself (P0-733-1). It never returns a role — it cannot,
// because deriving a role is the resolver's job, not the handler's.
type recordingDeriver struct {
	calls []DeriveRequest
}

func (d *recordingDeriver) Derive(_ context.Context, in DeriveRequest) error {
	d.calls = append(d.calls, in)
	return nil
}

func withCred(req *http.Request, tenantID string) *http.Request {
	ctx := withSCIMCredential(req.Context(), credentialContext{
		credentialID: uuid.New(), tenantID: tenantID,
	})
	return req.WithContext(ctx)
}

// TestPatchGroup_ReusesResolver proves AC-3 + P0-733-1: a membership PATCH
// drives a re-derivation through the injected resolver, feeding it the user's
// FULL current group set from the store (not a role the handler computed). The
// handler has no role logic of its own.
func TestPatchGroup_ReusesResolver(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()
	store := &fakeGroupStore{
		patchAffected: []string{"user-1"},
		groupRefs:     map[string][]string{"user-1": {"Engineering", "Auditors"}},
	}
	deriver := &recordingDeriver{}
	h := NewGroupHandler(store, deriver)

	groupID := uuid.New().String()
	body := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"add","path":"members","value":[{"value":"user-1"}]}]}`
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/"+groupID, strings.NewReader(body))
	// Build the context once: SCIM credential + chi route param.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", groupID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	req = withCred(req, tenant)

	rec := httptest.NewRecorder()
	h.PatchGroup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(deriver.calls) != 1 {
		t.Fatalf("expected exactly 1 Derive call (REUSE), got %d", len(deriver.calls))
	}
	got := deriver.calls[0]
	if got.UserID != "user-1" {
		t.Fatalf("Derive user = %q; want user-1", got.UserID)
	}
	// The resolver received the user's FULL group set FROM THE STORE — the
	// handler did not invent or filter roles (P0-733-1).
	if len(got.Groups) != 2 || got.Groups[0] != "Engineering" || got.Groups[1] != "Auditors" {
		t.Fatalf("Derive groups = %v; want [Engineering Auditors] (store-provided)", got.Groups)
	}
}

// reqWithCredAndID builds a request carrying a SCIM credential context + a chi
// "id" URL param (for the single-resource routes).
func reqWithCredAndID(method, body, groupID, tenantID string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/scim/v2/Groups/"+groupID, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/scim/v2/Groups/"+groupID, nil)
	}
	rctx := chi.NewRouteContext()
	if groupID != "" {
		rctx.URLParams.Add("id", groupID)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	r = r.WithContext(ctx)
	return withCred(r, tenantID)
}

// TestCreateGroup_Branches covers the Create handler's happy + error branches.
func TestCreateGroup_Branches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()

	// Missing displayName → 400.
	h := NewGroupHandler(&fakeGroupStore{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(`{"members":[]}`))
	req = withCred(req, tenant)
	rec := httptest.NewRecorder()
	h.CreateGroup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing displayName status = %d; want 400", rec.Code)
	}

	// Conflict → 409.
	h = NewGroupHandler(&fakeGroupStore{conflict: true}, nil)
	req = httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(`{"displayName":"X","externalId":"e"}`))
	req = withCred(req, tenant)
	rec = httptest.NewRecorder()
	h.CreateGroup(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d; want 409", rec.Code)
	}

	// Happy create (no deriver) → 201.
	h = NewGroupHandler(&fakeGroupStore{}, nil)
	req = httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(`{"displayName":"X","members":[{"value":"u1"}]}`))
	req = withCred(req, tenant)
	rec = httptest.NewRecorder()
	h.CreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201", rec.Code)
	}
}

// TestGetGroup_Branches covers bad-uuid + not-found + happy.
func TestGetGroup_Branches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()

	// Bad UUID → 404.
	h := NewGroupHandler(&fakeGroupStore{}, nil)
	rec := httptest.NewRecorder()
	h.GetGroup(rec, reqWithCredAndID(http.MethodGet, "", "not-a-uuid", tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad-uuid get status = %d; want 404", rec.Code)
	}

	// Not found → 404 (no cross-tenant oracle).
	h = NewGroupHandler(&fakeGroupStore{notFound: true}, nil)
	rec = httptest.NewRecorder()
	h.GetGroup(rec, reqWithCredAndID(http.MethodGet, "", uuid.New().String(), tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found get status = %d; want 404", rec.Code)
	}

	// Happy → 200.
	h = NewGroupHandler(&fakeGroupStore{}, nil)
	rec = httptest.NewRecorder()
	h.GetGroup(rec, reqWithCredAndID(http.MethodGet, "", uuid.New().String(), tenant))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d; want 200", rec.Code)
	}
}

// TestListGroups_Branches covers filter (valid), filter (invalid → 400), and
// the unfiltered list path.
func TestListGroups_Branches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()
	store := &fakeGroupStore{
		listGroups: []scim.DomainGroup{{ID: uuid.New(), DisplayName: "G"}},
		listTotal:  1,
	}
	h := NewGroupHandler(store, nil)

	// Valid displayName filter → 200.
	req := httptest.NewRequest(http.MethodGet,
		`/scim/v2/Groups?filter=`+url.QueryEscape(`displayName eq "G"`), nil)
	req = withCred(req, tenant)
	rec := httptest.NewRecorder()
	h.ListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter list status = %d; want 200", rec.Code)
	}

	// Invalid filter → 400.
	req = httptest.NewRequest(http.MethodGet,
		`/scim/v2/Groups?filter=`+url.QueryEscape(`foo eq "bar"`), nil)
	req = withCred(req, tenant)
	rec = httptest.NewRecorder()
	h.ListGroups(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-filter list status = %d; want 400", rec.Code)
	}

	// Unfiltered list → 200.
	req = httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil)
	req = withCred(req, tenant)
	rec = httptest.NewRecorder()
	h.ListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfiltered list status = %d; want 200", rec.Code)
	}
}

// TestReplaceGroup_Branches covers bad-uuid, not-found, and happy replace.
func TestReplaceGroup_Branches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()

	h := NewGroupHandler(&fakeGroupStore{}, nil)
	rec := httptest.NewRecorder()
	h.ReplaceGroup(rec, reqWithCredAndID(http.MethodPut, `{"displayName":"X"}`, "bad", tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad-uuid replace status = %d; want 404", rec.Code)
	}

	h = NewGroupHandler(&fakeGroupStore{notFound: true}, nil)
	rec = httptest.NewRecorder()
	h.ReplaceGroup(rec, reqWithCredAndID(http.MethodPut, `{"displayName":"X"}`, uuid.New().String(), tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found replace status = %d; want 404", rec.Code)
	}

	deriver := &recordingDeriver{}
	h = NewGroupHandler(&fakeGroupStore{groupRefs: map[string][]string{"u1": {"G"}}}, deriver)
	rec = httptest.NewRecorder()
	h.ReplaceGroup(rec, reqWithCredAndID(http.MethodPut, `{"displayName":"X","members":[{"value":"u1"}]}`, uuid.New().String(), tenant))
	if rec.Code != http.StatusOK {
		t.Fatalf("replace status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(deriver.calls) != 1 {
		t.Fatalf("replace should re-derive affected member, got %d calls", len(deriver.calls))
	}
}

// TestPatchGroup_ErrorBranches covers empty ops + not-found + bad-uuid.
func TestPatchGroup_ErrorBranches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()

	h := NewGroupHandler(&fakeGroupStore{}, nil)
	rec := httptest.NewRecorder()
	h.PatchGroup(rec, reqWithCredAndID(http.MethodPatch, "bad", "not-a-uuid", tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad-uuid patch status = %d; want 404", rec.Code)
	}

	// Empty Operations → 400.
	h = NewGroupHandler(&fakeGroupStore{}, nil)
	rec = httptest.NewRecorder()
	body := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[]}`
	h.PatchGroup(rec, reqWithCredAndID(http.MethodPatch, body, uuid.New().String(), tenant))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty-ops patch status = %d; want 400", rec.Code)
	}

	// Not found → 404.
	h = NewGroupHandler(&fakeGroupStore{notFound: true}, nil)
	rec = httptest.NewRecorder()
	body = `{"Operations":[{"op":"add","path":"members","value":[{"value":"u"}]}]}`
	h.PatchGroup(rec, reqWithCredAndID(http.MethodPatch, body, uuid.New().String(), tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found patch status = %d; want 404", rec.Code)
	}
}

// TestDeleteGroup_Branches covers bad-uuid, not-found, and happy delete.
func TestDeleteGroup_Branches(t *testing.T) {
	t.Parallel()
	tenant := uuid.New().String()

	h := NewGroupHandler(&fakeGroupStore{}, nil)
	rec := httptest.NewRecorder()
	h.DeleteGroup(rec, reqWithCredAndID(http.MethodDelete, "", "bad", tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad-uuid delete status = %d; want 404", rec.Code)
	}

	h = NewGroupHandler(&fakeGroupStore{notFound: true}, nil)
	rec = httptest.NewRecorder()
	h.DeleteGroup(rec, reqWithCredAndID(http.MethodDelete, "", uuid.New().String(), tenant))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-found delete status = %d; want 404", rec.Code)
	}

	deriver := &recordingDeriver{}
	h = NewGroupHandler(&fakeGroupStore{affected: []string{"u1"}, groupRefs: map[string][]string{"u1": {}}}, deriver)
	rec = httptest.NewRecorder()
	h.DeleteGroup(rec, reqWithCredAndID(http.MethodDelete, "", uuid.New().String(), tenant))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204", rec.Code)
	}
	if len(deriver.calls) != 1 {
		t.Fatalf("delete should re-derive former members, got %d", len(deriver.calls))
	}
}

// TestRederive_NilDeriverIsNoOp proves a membership change still succeeds when
// no resolver is wired (the OIDC login path re-derives on next sign-in).
func TestRederive_NilDeriverIsNoOp(t *testing.T) {
	t.Parallel()
	store := &fakeGroupStore{groupRefs: map[string][]string{"u": {"G"}}}
	h := NewGroupHandler(store, nil)
	if err := h.rederive(context.Background(), uuid.New().String(), []string{"u"}); err != nil {
		t.Fatalf("nil deriver should be a no-op, got %v", err)
	}
}

// TestRederive_SkipsEmptyUserIDs proves the re-derivation loop skips blank
// affected-user entries (defensive — never a spurious Derive on "").
func TestRederive_SkipsEmptyUserIDs(t *testing.T) {
	t.Parallel()
	store := &fakeGroupStore{groupRefs: map[string][]string{"u": {"G"}}}
	deriver := &recordingDeriver{}
	h := NewGroupHandler(store, deriver)
	if err := h.rederive(context.Background(), uuid.New().String(), []string{"", "u", ""}); err != nil {
		t.Fatalf("rederive: %v", err)
	}
	if len(deriver.calls) != 1 || deriver.calls[0].UserID != "u" {
		t.Fatalf("expected 1 Derive for 'u' only, got %v", deriver.calls)
	}
}
