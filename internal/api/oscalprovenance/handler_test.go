// Slice 599 — unit tests for the OSCAL resolved-chain provenance read
// handler. These run on the plain `go test ./...` surface (no build tag, no
// Postgres): a fixed-row stub satisfies the read seam so the wire shape +
// error-mapping branches are exercised without a pool. The RLS / tenant-
// isolation assertions live in the integration suite (integration_test.go).
package oscalprovenance

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

// stubReader is a fixed-row read seam for the unit tests.
type stubReader struct {
	row provenanceRow
	err error
}

func (s stubReader) ProvenanceForBaseline(context.Context, uuid.UUID) (provenanceRow, error) {
	return s.row, s.err
}

// withCred wraps a request context with a credential + a tenant GUC so the
// handler's authz guard and tenantContext check both pass.
func withCred(t *testing.T, cred credstore.Credential, tenant uuid.UUID) context.Context {
	t.Helper()
	ctx := authctx.WithCredential(context.Background(), cred)
	ctx, err := tenancy.WithTenant(ctx, tenant.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// serve drives the handler through a chi router so chi.URLParam resolves the
// {id} path param exactly as production.
func serve(t *testing.T, h *Handler, ctx context.Context, baselineID string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/oscal/imported-profiles/{id}/provenance", h.Provenance)
	req := httptest.NewRequest(http.MethodGet, "/v1/oscal/imported-profiles/"+baselineID+"/provenance", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func pgUUIDFrom(u uuid.UUID) pgtype.UUID  { return pgtype.UUID{Bytes: u, Valid: true} }
func pgTS(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// ----- AC-1: a chained import returns its ordered chain -----

func TestProvenance_ChainedImport_ReturnsOrderedChain(t *testing.T) {
	baseline := uuid.New()
	tenant := uuid.New()
	detail, _ := json.Marshal(map[string]any{
		"mapped":      3,
		"unmapped":    1,
		"kind":        "profile",
		"chain_depth": 2,
		"chain": []map[string]any{
			{"role": "entry-profile", "sha256": "aa", "bytes": 100},
			{"role": "profile", "sha256": "bb", "bytes": 200},
			{"role": "catalog", "sha256": "cc", "bytes": 300},
		},
	})
	h := newHandlerWithReader(stubReader{row: provenanceRow{
		BaselineID:   pgUUIDFrom(baseline),
		ProfileTitle: "FedRAMP Moderate (agency overlay)",
		SourceLabel:  "fedramp-moderate-overlay",
		OscalVersion: "1.1.2",
		SourceSha256: "deadbeef",
		ImportedAt:   pgTS(time.Now().UTC()),
		Detail:       detail,
	}})

	ctx := withCred(t, credstore.Credential{IsAdmin: true}, tenant)
	w := serve(t, h, ctx, baseline.String())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got provenanceWire
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.BaselineID != baseline.String() {
		t.Errorf("baseline_id = %q, want %q", got.BaselineID, baseline.String())
	}
	if got.ChainDepth != 2 {
		t.Errorf("chain_depth = %d, want 2", got.ChainDepth)
	}
	if len(got.Chain) != 3 {
		t.Fatalf("chain len = %d, want 3", len(got.Chain))
	}
	wantRoles := []string{"entry-profile", "profile", "catalog"}
	for i, link := range got.Chain {
		if link.Role != wantRoles[i] {
			t.Errorf("chain[%d].role = %q, want %q", i, link.Role, wantRoles[i])
		}
	}
	if got.Chain[0].Sha256 != "aa" || got.Chain[2].Bytes != 300 {
		t.Errorf("chain content mismatch: %+v", got.Chain)
	}
}

// ----- AC-2: a single-level import returns its two-element chain -----

func TestProvenance_SingleLevelImport_ReturnsTwoElementChain(t *testing.T) {
	baseline := uuid.New()
	detail, _ := json.Marshal(map[string]any{
		"kind":        "profile",
		"chain_depth": 1,
		"chain": []map[string]any{
			{"role": "entry-profile", "sha256": "11", "bytes": 50},
			{"role": "catalog", "sha256": "22", "bytes": 500},
		},
	})
	h := newHandlerWithReader(stubReader{row: provenanceRow{
		BaselineID: pgUUIDFrom(baseline),
		Detail:     detail,
	}})

	ctx := withCred(t, credstore.Credential{IsApprover: true}, uuid.New())
	w := serve(t, h, ctx, baseline.String())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got provenanceWire
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ChainDepth != 1 {
		t.Errorf("chain_depth = %d, want 1", got.ChainDepth)
	}
	if len(got.Chain) != 2 {
		t.Fatalf("chain len = %d, want 2 (entry-profile + catalog)", len(got.Chain))
	}
	if got.Chain[0].Role != "entry-profile" || got.Chain[1].Role != "catalog" {
		t.Errorf("two-element chain roles wrong: %+v", got.Chain)
	}
}

// ----- empty/nil detail renders an empty chain, not null -----

func TestProvenance_EmptyDetail_RendersEmptyChain(t *testing.T) {
	baseline := uuid.New()
	h := newHandlerWithReader(stubReader{row: provenanceRow{
		BaselineID: pgUUIDFrom(baseline),
		Detail:     nil,
	}})
	ctx := withCred(t, credstore.Credential{IsAdmin: true}, uuid.New())
	w := serve(t, h, ctx, baseline.String())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// chain must serialize as [] (not null) so the client can iterate.
	if got := w.Body.String(); !strings.Contains(got, `"chain":[]`) {
		t.Errorf("empty detail should render \"chain\":[], got %s", got)
	}
}

// ----- malformed detail JSON maps to 500 -----

func TestProvenance_MalformedDetail(t *testing.T) {
	baseline := uuid.New()
	h := newHandlerWithReader(stubReader{row: provenanceRow{
		BaselineID: pgUUIDFrom(baseline),
		Detail:     []byte(`{not valid json`),
	}})
	ctx := withCred(t, credstore.Credential{IsAdmin: true}, uuid.New())
	w := serve(t, h, ctx, baseline.String())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// ----- 404: an unknown / cross-tenant id (ErrNoRows) maps to 404 -----

func TestProvenance_NotFound(t *testing.T) {
	h := newHandlerWithReader(stubReader{err: pgx.ErrNoRows})
	ctx := withCred(t, credstore.Credential{IsAdmin: true}, uuid.New())
	w := serve(t, h, ctx, uuid.New().String())
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// ----- 400: a non-uuid path id -----

func TestProvenance_BadUUID(t *testing.T) {
	h := newHandlerWithReader(stubReader{})
	ctx := withCred(t, credstore.Credential{IsAdmin: true}, uuid.New())
	w := serve(t, h, ctx, "not-a-uuid")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ----- 403: a credential without an oscal-read role signal -----

func TestProvenance_Forbidden_NoRole(t *testing.T) {
	h := newHandlerWithReader(stubReader{})
	// A bare credential — no IsAdmin, no IsApprover, no OwnerRoles.
	ctx := withCred(t, credstore.Credential{}, uuid.New())
	w := serve(t, h, ctx, uuid.New().String())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// ----- 401: no tenant context (guard passes, tenant missing) -----

func TestProvenance_Unauthorized_NoTenant(t *testing.T) {
	h := newHandlerWithReader(stubReader{})
	// Credential present (role passes) but no tenant GUC on the context.
	ctx := authctx.WithCredential(context.Background(), credstore.Credential{IsAdmin: true})
	w := serve(t, h, ctx, uuid.New().String())
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// ----- hasOscalRead role matrix -----

func TestHasOscalRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner", credstore.Credential{OwnerRoles: []string{"control_owner"}}, true},
		{"bare", credstore.Credential{}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasOscalRead(tc.cred); got != tc.want {
				t.Errorf("hasOscalRead(%+v) = %v, want %v", tc.cred, got, tc.want)
			}
		})
	}
}

// ----- helper-fn NULL branches -----

func TestUUIDString_NullInvalid(t *testing.T) {
	t.Parallel()
	if got := uuidString(pgtype.UUID{Valid: false}); got != "" {
		t.Errorf("uuidString(invalid) = %q, want \"\"", got)
	}
	id := uuid.New()
	if got := uuidString(pgUUIDFrom(id)); got != id.String() {
		t.Errorf("uuidString(valid) = %q, want %q", got, id.String())
	}
}

func TestTSString_NullInvalid(t *testing.T) {
	t.Parallel()
	if got := tsString(pgtype.Timestamptz{Valid: false}); got != "" {
		t.Errorf("tsString(invalid) = %q, want \"\"", got)
	}
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if got := tsString(pgTS(now)); got != now.Format(time.RFC3339) {
		t.Errorf("tsString(valid) = %q, want %q", got, now.Format(time.RFC3339))
	}
}

// guard against an accidental dbx import drift in the alias: the package row
// alias must stay the generated row type verbatim.
var _ provenanceRow = dbx.GetProfileImportProvenanceRow{}
