package oscalcomponents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// stubStore drives the handler branches without a Postgres pool.
type stubStore struct {
	defs       []dbx.ImportedCatalog
	detail     DefinitionWithClaims
	detailErr  error
	dispResult dbx.ImportedComponentClaim
	dispErr    error
	listErr    error

	gotDispClaimID uuid.UUID
	gotDispStatus  string
	gotDispActor   string
	gotDispNote    string
}

func (s *stubStore) ListDefinitions(_ context.Context) ([]dbx.ImportedCatalog, error) {
	return s.defs, s.listErr
}

func (s *stubStore) GetDefinitionWithClaims(_ context.Context, _ uuid.UUID) (DefinitionWithClaims, error) {
	return s.detail, s.detailErr
}

func (s *stubStore) Disposition(_ context.Context, claimID uuid.UUID, toStatus, actor, note string) (dbx.ImportedComponentClaim, error) {
	s.gotDispClaimID = claimID
	s.gotDispStatus = toStatus
	s.gotDispActor = actor
	s.gotDispNote = note
	return s.dispResult, s.dispErr
}

// ----- helpers -----

func pgID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func nowTs() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC), Valid: true}
}

// reqWithCred builds a request carrying the tenant context + a credential.
func reqWithCred(method, target string, body string, cred credstore.Credential) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
	if err != nil {
		panic(err)
	}
	ctx = authctx.WithCredential(ctx, cred)
	return r.WithContext(ctx)
}

func ownerCred(tenant string) credstore.Credential {
	return credstore.Credential{ID: "owner-1", TenantID: tenant, OwnerRoles: []string{"control_owner"}}
}

func approverCred(tenant string) credstore.Credential {
	return credstore.Credential{ID: "grc-1", TenantID: tenant, IsApprover: true}
}

func bareCred(tenant string) credstore.Credential {
	return credstore.Credential{ID: "push-1", TenantID: tenant}
}

// route serves one request through a chi router so chi.URLParam resolves.
func route(h *Handler, method, pattern, target string, r *http.Request) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	switch method {
	case http.MethodGet:
		router.Get(pattern, h.routeFor(pattern))
	case http.MethodPost:
		router.Post(pattern, h.routeFor(pattern))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

// routeFor maps a pattern to its handler method (test-only dispatch).
func (h *Handler) routeFor(pattern string) http.HandlerFunc {
	switch pattern {
	case "/v1/oscal/component-definitions":
		return h.ListDefinitions
	case "/v1/oscal/component-definitions/{id}":
		return h.GetDefinition
	case "/v1/oscal/component-claims/{id}:accept":
		return h.Accept
	case "/v1/oscal/component-claims/{id}:reject":
		return h.Reject
	case "/v1/oscal/component-claims/{id}:needs-info":
		return h.NeedsInfo
	}
	return nil
}

const testTenant = "11111111-1111-1111-1111-111111111111"

// ----- list -----

func TestListDefinitions_OK(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	st := &stubStore{defs: []dbx.ImportedCatalog{{
		ID: pgID(id), SourceLabel: "Acme SaaS", CatalogTitle: "Acme Component Def",
		OscalVersion: "1.1.2", SourceSha256: "abc", ControlCount: 3,
		ImportedBy: "op", ImportedAt: nowTs(),
	}}}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodGet, "/v1/oscal/component-definitions", "", ownerCred(testTenant))
	rec := route(h, http.MethodGet, "/v1/oscal/component-definitions", "/v1/oscal/component-definitions", r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ComponentDefinitions []definitionSummaryWire `json:"component_definitions"`
		Count                int                     `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || out.ComponentDefinitions[0].SourceLabel != "Acme SaaS" {
		t.Fatalf("unexpected body: %+v", out)
	}
	if out.ComponentDefinitions[0].ClaimCount != 3 {
		t.Fatalf("claim_count = %d, want 3", out.ComponentDefinitions[0].ClaimCount)
	}
}

func TestListDefinitions_Forbidden_BareCred(t *testing.T) {
	t.Parallel()
	h := newHandlerWithStore(&stubStore{})
	r := reqWithCred(http.MethodGet, "/v1/oscal/component-definitions", "", bareCred(testTenant))
	rec := route(h, http.MethodGet, "/v1/oscal/component-definitions", "/v1/oscal/component-definitions", r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// ----- get detail -----

func TestGetDefinition_OK_UnmappedFlag(t *testing.T) {
	t.Parallel()
	defID := uuid.New()
	claimID := uuid.New()
	compID := uuid.New()
	mapped := "SCF-IAC-06"
	st := &stubStore{detail: DefinitionWithClaims{
		Definition: dbx.ImportedCatalog{ID: pgID(defID), SourceLabel: "Acme", ImportedAt: nowTs()},
		Claims: []dbx.ListImportedComponentClaimsForDefinitionRow{
			{ClaimID: pgID(claimID), ImportedComponentID: pgID(compID), ControlID: "ac-2",
				ScfAnchorID: nil, IsVendorClaim: true, ClaimStatus: "asserted"},
			{ClaimID: pgID(uuid.New()), ImportedComponentID: pgID(compID), ControlID: "ac-3",
				ScfAnchorID: &mapped, IsVendorClaim: true, ClaimStatus: "accepted",
				DispositionedBy: strptr("grc-1"), DispositionedAt: nowTs(), DispositionNote: "credited"},
		},
	}}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodGet, "/v1/oscal/component-definitions/"+defID.String(), "", ownerCred(testTenant))
	rec := route(h, http.MethodGet, "/v1/oscal/component-definitions/{id}", "/v1/oscal/component-definitions/"+defID.String(), r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out definitionDetailWire
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(out.Claims))
	}
	if !out.Claims[0].Unmapped {
		t.Fatal("claim 0 (nil scf_anchor_id) should be unmapped=true")
	}
	if out.Claims[1].Unmapped {
		t.Fatal("claim 1 (mapped) should be unmapped=false")
	}
	// The claim-is-assertion boundary: is_vendor_claim is always surfaced true.
	if !out.Claims[0].IsVendorClaim || !out.Claims[1].IsVendorClaim {
		t.Fatal("is_vendor_claim must be true on every claim")
	}
}

func TestGetDefinition_NotFound(t *testing.T) {
	t.Parallel()
	defID := uuid.New()
	st := &stubStore{detailErr: pgx.ErrNoRows}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodGet, "/v1/oscal/component-definitions/"+defID.String(), "", ownerCred(testTenant))
	rec := route(h, http.MethodGet, "/v1/oscal/component-definitions/{id}", "/v1/oscal/component-definitions/"+defID.String(), r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetDefinition_BadUUID(t *testing.T) {
	t.Parallel()
	h := newHandlerWithStore(&stubStore{})
	r := reqWithCred(http.MethodGet, "/v1/oscal/component-definitions/not-a-uuid", "", ownerCred(testTenant))
	rec := route(h, http.MethodGet, "/v1/oscal/component-definitions/{id}", "/v1/oscal/component-definitions/not-a-uuid", r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ----- disposition -----

func TestAccept_OK(t *testing.T) {
	t.Parallel()
	claimID := uuid.New()
	st := &stubStore{dispResult: dbx.ImportedComponentClaim{
		ID: pgID(claimID), ControlID: "ac-2", IsVendorClaim: true, ClaimStatus: "accepted",
		DispositionedBy: strptr("grc-1"), DispositionedAt: nowTs(), DispositionNote: "looks good",
	}}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/"+claimID.String()+":accept", `{"note":"looks good"}`, approverCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:accept", "/v1/oscal/component-claims/"+claimID.String()+":accept", r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if st.gotDispStatus != "accepted" {
		t.Fatalf("toStatus = %q, want accepted", st.gotDispStatus)
	}
	if st.gotDispActor != "grc-1" {
		t.Fatalf("actor = %q, want grc-1", st.gotDispActor)
	}
	if st.gotDispNote != "looks good" {
		t.Fatalf("note = %q", st.gotDispNote)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["claim_status"] != "accepted" {
		t.Fatalf("claim_status = %v", out["claim_status"])
	}
	// Boundary: accepting still reports is_vendor_claim=true.
	if out["is_vendor_claim"] != true {
		t.Fatalf("is_vendor_claim = %v, want true", out["is_vendor_claim"])
	}
}

func TestReject_MapsStatus(t *testing.T) {
	t.Parallel()
	claimID := uuid.New()
	st := &stubStore{dispResult: dbx.ImportedComponentClaim{ID: pgID(claimID), IsVendorClaim: true, ClaimStatus: "rejected"}}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/"+claimID.String()+":reject", "", approverCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:reject", "/v1/oscal/component-claims/"+claimID.String()+":reject", r)
	if rec.Code != http.StatusOK || st.gotDispStatus != "rejected" {
		t.Fatalf("status=%d gotStatus=%q", rec.Code, st.gotDispStatus)
	}
}

func TestNeedsInfo_MapsStatus(t *testing.T) {
	t.Parallel()
	claimID := uuid.New()
	st := &stubStore{dispResult: dbx.ImportedComponentClaim{ID: pgID(claimID), IsVendorClaim: true, ClaimStatus: "needs_info"}}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/"+claimID.String()+":needs-info", "", approverCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:needs-info", "/v1/oscal/component-claims/"+claimID.String()+":needs-info", r)
	if rec.Code != http.StatusOK || st.gotDispStatus != "needs_info" {
		t.Fatalf("status=%d gotStatus=%q", rec.Code, st.gotDispStatus)
	}
}

func TestDisposition_Forbidden_NonApprover(t *testing.T) {
	t.Parallel()
	claimID := uuid.New()
	h := newHandlerWithStore(&stubStore{})
	// An owner (read-capable) cannot disposition — write needs approver/admin.
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/"+claimID.String()+":accept", "", ownerCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:accept", "/v1/oscal/component-claims/"+claimID.String()+":accept", r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestDisposition_NotFound(t *testing.T) {
	t.Parallel()
	claimID := uuid.New()
	st := &stubStore{dispErr: ErrClaimNotFound}
	h := newHandlerWithStore(st)
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/"+claimID.String()+":accept", "", approverCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:accept", "/v1/oscal/component-claims/"+claimID.String()+":accept", r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDisposition_BadUUID(t *testing.T) {
	t.Parallel()
	h := newHandlerWithStore(&stubStore{})
	r := reqWithCred(http.MethodPost, "/v1/oscal/component-claims/nope:accept", "", approverCred(testTenant))
	rec := route(h, http.MethodPost, "/v1/oscal/component-claims/{id}:accept", "/v1/oscal/component-claims/nope:accept", r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func strptr(s string) *string { return &s }
