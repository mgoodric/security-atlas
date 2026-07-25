// Slice 692 — contract-test-tier rollout (provider side: GET
// /v1/controls/{id}/attest-form, the manual-attestation form descriptor the
// control-detail attest surface renders).
//
// PROVIDER: this file records the real AttestForm handler's bodies into
// control-attest-form.golden.json. CONSUMER:
// web/lib/contracts/control-attest-form.contract.test.ts asserts the BFF
// (web/app/api/controls/[id]/attest-form/route.ts) against them — a VERBATIM
// passthrough (getAttestForm returns res.json() unchanged; the route does
// NextResponse.json(form)), so the consumer assert is toEqual(golden).
//
// THE DB-SEAM DECISION (slice 692 per-route read seam, Option A): the
// production handler reads the control row through *pgxpool.Pool inside a
// tenant-GUC read tx. To record the wire shape on the plain `go test ./...`
// unit surface (ADR-0007, P0-409-1: no recorder on the integration surface)
// AttestForm reads through a narrow one-method controlByIDReader seam
// (attest.go). This recorder injects a fixed dbx.GetControlByIDRow (including
// the JSONB manual_evidence_schema) via the unexported
// newAttestHandlerWithReader. No Postgres, no pool. NewAttestHandler's
// signature is unchanged (P0-409-2).
//
// caller_can_attest is the cred.HasOwnerRole(owner_role) branch — recorded
// both true (credential holds the control's owner_role) and false (a
// control-read credential that does NOT hold it). The non-manual 400 branch
// is pinned by a sibling unit test (TestAttestForm_NonManual400) — it is an
// error envelope, not a form body, so it is not a golden variant.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/controls/ -run TestContract_AttestForm -update

package controls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	attestTenantID  = "00000000-0000-4000-8000-000000000692"
	attestControlID = "11111111-1111-4111-8111-111111111111"
	attestOwnerRole = "control_owner"
)

// stubControlReader is the fixed-row implementation of the per-route
// controlByIDReader seam. It returns a deterministic dbx.GetControlByIDRow
// (including the JSONB manual_evidence_schema) with no Postgres.
type stubControlReader struct {
	row dbx.GetControlByIDRow
}

func (s stubControlReader) ControlByID(_ context.Context, _ string, _ uuid.UUID) (dbx.GetControlByIDRow, error) {
	return s.row, nil
}

// pgID wraps a UUID string as a pgtype.UUID.
func pgID(s string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true}
}

// manualControlRow is the fixed manual-attested control row the recorder pins.
// manual_evidence_schema is a small JSON Schema fragment (the control bundle's
// per-control declaration, slice 009) so the golden exercises the JSONB
// decode + passthrough path.
func manualControlRow() dbx.GetControlByIDRow {
	return dbx.GetControlByIDRow{
		ID:                 pgID(attestControlID),
		TenantID:           pgID(attestTenantID),
		BundleID:           "soc2-access-review",
		Title:              "Quarterly Access Review",
		ImplementationType: dbx.ControlImplementationTypeManualAttested,
		OwnerRole:          attestOwnerRole,
		FreshnessClass:     strPtr("quarterly"),
		ManualEvidenceSchema: []byte(`{` +
			`"type":"object",` +
			`"properties":{"reviewer":{"type":"string"},"completed_on":{"type":"string","format":"date"}},` +
			`"required":["reviewer","completed_on"]}`),
	}
}

func strPtr(s string) *string { return &s }

// recordAttestFormVariant drives the AttestForm handler through a chi mux so
// chi.URLParam(r, "id") resolves, binds the supplied credential + a tenant on
// the context, and canonicalizes the recorded body.
func recordAttestFormVariant(t *testing.T, h http.HandlerFunc, cred credstore.Credential, target string) json.RawMessage {
	t.Helper()
	router := chi.NewRouter()
	router.Method(http.MethodGet, "/v1/controls/{id}/attest-form", h)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), cred)
	ctx, err := tenancy.WithTenant(ctx, attestTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned status %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

// ===== GET /v1/controls/{id}/attest-form =====

func TestContract_AttestForm(t *testing.T) {
	reader := stubControlReader{row: manualControlRow()}
	target := "/v1/controls/" + attestControlID + "/attest-form"

	// owner — credential holds the control's owner_role: caller_can_attest
	// is true. viewer — a control-read credential (IsApprover -> grc_engineer)
	// that does NOT hold the owner_role: caller_can_attest is false. The two
	// variants pin both branches of the cred.HasOwnerRole gate that drives
	// the attest button's enabled state in the frontend.
	ownerCredential := credstore.Credential{
		ID:         "key_contract_692",
		TenantID:   attestTenantID,
		UserID:     "key_contract_692",
		OwnerRoles: []string{attestOwnerRole},
	}
	viewerCredential := credstore.Credential{
		ID:         "key_contract_692_viewer",
		TenantID:   attestTenantID,
		UserID:     "key_contract_692_viewer",
		IsApprover: true,
	}

	recorded := map[string]json.RawMessage{
		"owner":  recordAttestFormVariant(t, newAttestHandlerWithReader(reader).AttestForm, ownerCredential, target),
		"viewer": recordAttestFormVariant(t, newAttestHandlerWithReader(reader).AttestForm, viewerCredential, target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-attest-form.golden.json"),
		"Slice 692 contract-tier golden. PROVIDER: internal/api/controls/attest_contract_test.go (AttestForm, real handler over an injected fixed-row controlByIDReader stub — Option A per-route seam, no Postgres; routed through chi for the {id} path param). Regenerate: `go test ./internal/api/controls/ -run TestContract_AttestForm -update`. CONSUMER: web/lib/contracts/control-attest-form.contract.test.ts asserts the BFF at web/app/api/controls/[id]/attest-form/route.ts — VERBATIM passthrough (toEqual). manual_evidence_schema is an opaque JSON object; caller_can_attest is a bool (true when the credential holds the control's owner_role); platform_schema_requires is a string array; freshness_class is string-or-null (omitempty). The non-manual implementation 400 branch is pinned by the sibling unit test TestAttestForm_NonManual400.",
		"GET /v1/controls/{id}/attest-form",
		recorded,
	)
}

// TestAttestForm_NonManual400 pins the non-manual rejection branch: a control
// whose implementation_type is not manual_attested / manual_periodic returns
// 400 (attestation rejected). This is an error envelope, not a form body, so
// it is asserted directly rather than recorded as a golden variant.
func TestAttestForm_NonManual400(t *testing.T) {
	t.Parallel()
	row := manualControlRow()
	row.ImplementationType = dbx.ControlImplementationTypeAutomated
	reader := stubControlReader{row: row}
	h := newAttestHandlerWithReader(reader)

	router := chi.NewRouter()
	router.Method(http.MethodGet, "/v1/controls/{id}/attest-form", http.HandlerFunc(h.AttestForm))

	req := httptest.NewRequest(http.MethodGet, "/v1/controls/"+attestControlID+"/attest-form", nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:         "key_contract_692",
		TenantID:   attestTenantID,
		IsApprover: true,
	})
	ctx, err := tenancy.WithTenant(ctx, attestTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-manual control: want 400; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not manual") {
		t.Fatalf("400 body must explain non-manual rejection; got %s", rec.Body.String())
	}
}
