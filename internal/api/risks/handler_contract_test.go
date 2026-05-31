// Slice 410 — contract-test-tier rollout (provider side: GET /v1/risks, the
// dashboard top-risks panel route served by this package).
//
//	GET /v1/risks  -> risks.golden.json
//
// This pins the PROVIDER half of the BFF<->atlas wire contract for the
// dashboard top-risks panel the /e2e/ suite traverses. The recorded golden
// lives under web/lib/contracts/ and is asserted by the CONSUMER half against
// the BFF (web/app/api/dashboard/risks/route.ts). Unlike the slice-409
// dashboard panels, that BFF is NOT a verbatim passthrough: getMitigateRisks
// unwraps body.risks and the route re-wraps {risks, count}. So the golden
// pins the UPSTREAM /v1/risks envelope ({risks: riskWire[], count}) and the
// consumer assert is transform-aware (slice 410 spec / 409 D1).
//
// THE DB-SEAM DECISION (slice 410 list-only Option A): the production ListRisks
// path reads through *risk.Store, which holds a pgx pool AND exposes a WIDE
// surface (Create/List/Get/Delete/Heatmap/ThemeOrgUnitHeatmap/…). To record
// the wire shape on the plain `go test ./...` unit surface (ADR-0007,
// P0-409-1: no recorder on the integration surface) without a ~7-method
// interface, the ListRisks path depends on a LIST-ONLY riskLister seam
// (handlers.go) — just the one List method that endpoint needs. This recorder
// injects a fixed-row stub via the unexported newHandlerWithLister. No
// Postgres, no pool. The seam is internal — New(*risk.Store) is unchanged
// (P0-409-2); every other handler keeps using the concrete *risk.Store.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/risks/ -run TestContract -update

package risks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID = "00000000-0000-4000-8000-000000000410"
	contractRiskID   = "44444444-4444-4444-8444-444444444444"
	contractCtrlA    = "11111111-1111-4111-8111-111111111111"
	contractCtrlB    = "22222222-2222-4222-8222-222222222222"
)

// stubRiskLister is the fixed-row implementation of the list-only riskLister
// seam. It returns deterministic rows with no Postgres.
type stubRiskLister struct{ rows []risk.Risk }

func (s stubRiskLister) List(_ context.Context, _ risk.ListFilter) ([]risk.Risk, error) {
	return s.rows, nil
}

// contractRequest builds a GET carrying both the gates ListRisks enforces: an
// authctx credential with a program-read signal (requireProgramRead) AND a
// tenant on the context (tenantContext). With both present the recorder
// reaches the happy path with no DB.
func contractRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx, err := tenancy.WithTenant(req.Context(), contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	// IsApprover -> grc_engineer grants program-read (authz.go hasProgramRead).
	ctx = authctx.WithCredential(ctx, credstore.Credential{
		ID:         "key_contract_410",
		TenantID:   contractTenantID,
		IsApprover: true,
	})
	return req.WithContext(ctx)
}

func recordVariant(t *testing.T, h http.HandlerFunc, target string) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, contractRequest(t, target))
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

func mustDate(ymd string) time.Time {
	tt, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		panic("contract fixture: bad date " + ymd)
	}
	return tt
}

// ===== GET /v1/risks (dashboard top-risks panel) =====

func TestContract_Risks(t *testing.T) {
	created := mustTime("2026-05-01T00:00:00Z")
	updated := mustTime("2026-05-12T00:00:00Z")
	reviewDue := mustTime("2026-08-01T00:00:00Z")
	acceptedUntil := mustDate("2026-12-31")

	// Row 1 — fully populated: opaque inherent/residual blobs, a two-element
	// linked_control_ids array, and the nullable review_due_at / accepted_until
	// BOTH set (pins their present-on-wire shape).
	full := risk.Risk{
		ID:                  uuid.MustParse(contractRiskID),
		Title:               "Unpatched perimeter dependency",
		Description:         "A high-severity CVE in an internet-facing dependency.",
		Category:            dbx.RiskCategory("technology"),
		Methodology:         dbx.RiskMethodologyNist80030,
		InherentScore:       []byte(`{"likelihood":4,"impact":5,"severity":20}`),
		Treatment:           dbx.RiskTreatmentMitigate,
		TreatmentOwner:      "security-eng",
		ResidualScore:       []byte(`{"likelihood":2,"impact":4,"severity":8}`),
		ReviewDueAt:         &reviewDue,
		AcceptedUntil:       &acceptedUntil,
		Accepter:            "ciso",
		InstrumentReference: "JIRA-410",
		LinkedControlIDs:    []uuid.UUID{uuid.MustParse(contractCtrlA), uuid.MustParse(contractCtrlB)},
		CreatedAt:           created,
		UpdatedAt:           updated,
	}

	// Row 2 — minimal: empty opaque blobs (handler defaults to {}), NO linked
	// controls, and the nullable review_due_at / accepted_until BOTH nil (pins
	// their omitempty / null behavior on the wire). A different shape from row
	// 1 so the recorder exercises both the present and absent branches of the
	// nullable fields in a single golden.
	minimal := risk.Risk{
		ID:               uuid.MustParse(contractRiskID),
		Title:            "Vendor SOC 2 report pending renewal",
		Category:         dbx.RiskCategory("third_party"),
		Methodology:      dbx.RiskMethodologyNist80030,
		Treatment:        dbx.RiskTreatmentMitigate,
		LinkedControlIDs: nil,
		CreatedAt:        created,
		UpdatedAt:        updated,
	}

	populated := stubRiskLister{rows: []risk.Risk{full, minimal}}
	empty := stubRiskLister{rows: []risk.Risk{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithLister(populated).ListRisks, "/v1/risks?treatment=mitigate&sort=residual,age"),
		"empty":     recordVariant(t, newHandlerWithLister(empty).ListRisks, "/v1/risks?treatment=mitigate&sort=residual,age"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/risks.golden.json"),
		"Slice 410 contract-tier golden. PROVIDER: internal/api/risks/handler_contract_test.go (Risks, real ListRisks handler over an injected fixed-row list-only riskLister stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/risks/ -run TestContract_Risks -update`. CONSUMER: web/lib/contracts/risks.contract.test.ts asserts the BFF at web/app/api/dashboard/risks/route.ts — TRANSFORM-AWARE (the BFF unwraps body.risks and re-wraps {risks, count}; it is NOT a verbatim passthrough).",
		"GET /v1/risks",
		recorded,
	)
}
