// Slice 411 — contract-test-tier rollout (provider side: the control-detail
// per-control read routes served by this package).
//
//	GET /v1/controls/{id}/policies  -> control-policies.golden.json
//	GET /v1/controls/{id}/risks     -> control-risks.golden.json
//	GET /v1/controls/{id}/history   -> control-history.golden.json
//
// These pin the PROVIDER half of the BFF<->atlas wire contract for the
// control-detail tab cluster the /e2e/ suite traverses
// (web/e2e/control-detail-tabs.spec.ts route.fulfills /api/controls/{id}/
// policies, /risks, /history). The recorded goldens live under
// web/lib/contracts/ and are asserted by the CONSUMER half against the BFFs
// (web/app/api/controls/[id]/{policies,risks,history}/route.ts). Unlike the
// slice-410 risks BFF, these are VERBATIM passthroughs — getControlPolicies /
// getControlRisks / getControlHistory (web/lib/api/control-detail.ts) return
// res.json() unchanged and the route does NextResponse.json(body) — so the
// consumer asserts are toEqual(golden) like the slice-409 dashboard panels.
//
// THE DB-SEAM DECISION (slice 411 per-route read seam): the production paths
// read through *Store, which holds a pgx pool. To record the wire shapes on
// the plain `go test ./...` unit surface (ADR-0007, P0-409-1: no recorder on
// the integration surface) the three paths depend on a narrow
// controlDetailReader seam (handler.go) — just the three read methods those
// routes need. This recorder injects a fixed-row stub via the unexported
// newHandlerWithReader. No Postgres, no pool. The seam is internal —
// New(*Store) is unchanged (P0-409-2); the Evidence handler keeps using the
// concrete *Store.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/controldetail/ -run TestContract -update

package controldetail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
	contractTenantID  = "00000000-0000-4000-8000-000000000411"
	contractControlID = "11111111-1111-4111-8111-111111111111"
	contractPolicyID  = "22222222-2222-4222-8222-222222222222"
	contractRiskID    = "33333333-3333-4333-8333-333333333333"
	contractScopeID   = "44444444-4444-4444-8444-444444444444"
)

// stubReader is the fixed-row implementation of the per-route
// controlDetailReader seam. It returns deterministic rows with no Postgres.
type stubReader struct {
	policies []dbx.ListPoliciesLinkedToControlRow
	risks    []dbx.ListRisksLinkedToControlRow
	history  []dbx.ListControlEvaluationHistoryPagedRow
}

func (s stubReader) PoliciesForControl(_ context.Context, _ uuid.UUID) ([]dbx.ListPoliciesLinkedToControlRow, error) {
	return s.policies, nil
}

func (s stubReader) RisksForControl(_ context.Context, _ uuid.UUID) ([]dbx.ListRisksLinkedToControlRow, error) {
	return s.risks, nil
}

func (s stubReader) HistoryForControl(_ context.Context, _ uuid.UUID, _ historyPage) ([]dbx.ListControlEvaluationHistoryPagedRow, error) {
	return s.history, nil
}

// contractRequest builds a GET carrying both the gates the per-control
// handlers enforce: an authctx credential with a control-read signal
// (requireControlRead) AND a tenant on the context (tenantContext). The {id}
// is a PATH param, so the request is routed through a chi mux so
// chi.URLParam(r, "id") resolves (unlike the slice-410 risks recorder, where
// the filter is a query param). With both gates satisfied the recorder
// reaches the happy path with no DB.
func recordVariant(t *testing.T, h http.HandlerFunc, pattern, target string) json.RawMessage {
	t.Helper()
	router := chi.NewRouter()
	router.Method(http.MethodGet, pattern, h)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	// IsApprover -> grc_engineer grants control-read (authz.go hasControlRead).
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:         "key_contract_411",
		TenantID:   contractTenantID,
		IsApprover: true,
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
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

func mustTime(rfc3339 string) time.Time {
	tt, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic("contract fixture: bad timestamp " + rfc3339)
	}
	return tt
}

func pgTS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgID(s string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true}
}

// mustNumeric scans a decimal string into a pgtype.Numeric (the per-link
// design_score, NUMERIC(4,3)).
func mustNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		panic("contract fixture: bad numeric " + s)
	}
	return n
}

// ===== GET /v1/controls/{id}/policies =====

func TestContract_ControlPolicies(t *testing.T) {
	// populated — two rows so the golden pins the row shape across distinct
	// version/status values.
	populated := stubReader{policies: []dbx.ListPoliciesLinkedToControlRow{
		{ID: pgID(contractPolicyID), Title: "Access Control Policy", Version: "2.1.0", Status: "published"},
		{ID: pgID(contractScopeID), Title: "Acceptable Use Policy", Version: "1.0.0", Status: "draft"},
	}}
	empty := stubReader{policies: nil}

	target := "/v1/controls/" + contractControlID + "/policies"
	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReader(populated).Policies, "/v1/controls/{id}/policies", target),
		"empty":     recordVariant(t, newHandlerWithReader(empty).Policies, "/v1/controls/{id}/policies", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-policies.golden.json"),
		"Slice 411 contract-tier golden. PROVIDER: internal/api/controldetail/handler_contract_test.go (Policies, real handler over an injected fixed-row controlDetailReader stub — per-route seam, no Postgres). Regenerate: `go test ./internal/api/controldetail/ -run TestContract_ControlPolicies -update`. CONSUMER: web/lib/contracts/control-policies.contract.test.ts asserts the BFF at web/app/api/controls/[id]/policies/route.ts — VERBATIM passthrough (toEqual).",
		"GET /v1/controls/{id}/policies",
		recorded,
	)
}

// ===== GET /v1/controls/{id}/risks =====

func TestContract_ControlRisks(t *testing.T) {
	created := mustTime("2026-05-01T00:00:00Z")
	// Row 1 — fully populated: opaque inherent/residual score blobs and a
	// present link_weight (design_score). Row 2 — minimal: empty score blobs
	// (handler's jsonOrNull defaults them to JSON null) and a NULL design_score
	// (numericToFloat -> link_weight: null). A different shape so the recorder
	// exercises both the present and absent branches of the nullable fields.
	populated := stubReader{risks: []dbx.ListRisksLinkedToControlRow{
		{
			ID:            pgID(contractRiskID),
			Title:         "Unpatched perimeter dependency",
			InherentScore: []byte(`{"likelihood":4,"impact":5,"severity":20}`),
			ResidualScore: []byte(`{"likelihood":2,"impact":4,"severity":8}`),
			DesignScore:   mustNumeric("0.750"),
			CreatedAt:     pgTS(created),
		},
		{
			ID:            pgID(contractScopeID),
			Title:         "Vendor SOC 2 report pending renewal",
			InherentScore: nil,
			ResidualScore: nil,
			DesignScore:   pgtype.Numeric{Valid: false},
			CreatedAt:     pgTS(created),
		},
	}}
	empty := stubReader{risks: nil}

	target := "/v1/controls/" + contractControlID + "/risks"
	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReader(populated).Risks, "/v1/controls/{id}/risks", target),
		"empty":     recordVariant(t, newHandlerWithReader(empty).Risks, "/v1/controls/{id}/risks", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-risks.golden.json"),
		"Slice 411 contract-tier golden. PROVIDER: internal/api/controldetail/handler_contract_test.go (Risks, real handler over an injected fixed-row controlDetailReader stub — per-route seam, no Postgres). Regenerate: `go test ./internal/api/controldetail/ -run TestContract_ControlRisks -update`. CONSUMER: web/lib/contracts/control-risks.contract.test.ts asserts the BFF at web/app/api/controls/[id]/risks/route.ts — VERBATIM passthrough (toEqual). inherent_score/residual_score are opaque JSON; link_weight is number-or-null.",
		"GET /v1/controls/{id}/risks",
		recorded,
	)
}

// ===== GET /v1/controls/{id}/history =====

func TestContract_ControlHistory(t *testing.T) {
	evaluated := mustTime("2026-05-12T09:30:00Z")
	// Row 1 — scope_cell present. Row 2 — scope_cell NULL (uuidPtr -> null),
	// pinning the nullable scope_cell wire shape. The handler keyset-paginates;
	// with the fixed two-row set under the default limit, next_cursor is "".
	populated := stubReader{history: []dbx.ListControlEvaluationHistoryPagedRow{
		{
			ID:                    pgID(contractRiskID),
			EvaluatedAt:           pgTS(evaluated),
			ScopeCellID:           pgID(contractScopeID),
			Result:                dbx.EvidenceResult("pass"),
			FreshnessStatus:       "fresh",
			EvidenceCountInWindow: 3,
		},
		{
			ID:                    pgID(contractPolicyID),
			EvaluatedAt:           pgTS(evaluated.Add(-24 * time.Hour)),
			ScopeCellID:           pgtype.UUID{Valid: false},
			Result:                dbx.EvidenceResult("fail"),
			FreshnessStatus:       "stale",
			EvidenceCountInWindow: 0,
		},
	}}
	empty := stubReader{history: nil}

	target := "/v1/controls/" + contractControlID + "/history"
	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReader(populated).History, "/v1/controls/{id}/history", target),
		"empty":     recordVariant(t, newHandlerWithReader(empty).History, "/v1/controls/{id}/history", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-history.golden.json"),
		"Slice 411 contract-tier golden. PROVIDER: internal/api/controldetail/handler_contract_test.go (History, real handler over an injected fixed-row controlDetailReader stub — per-route seam, no Postgres). Regenerate: `go test ./internal/api/controldetail/ -run TestContract_ControlHistory -update`. CONSUMER: web/lib/contracts/control-history.contract.test.ts asserts the BFF at web/app/api/controls/[id]/history/route.ts — VERBATIM passthrough (toEqual). scope_cell is string-or-null; next_cursor is a string ('' when no next page).",
		"GET /v1/controls/{id}/history",
		recorded,
	)
}
