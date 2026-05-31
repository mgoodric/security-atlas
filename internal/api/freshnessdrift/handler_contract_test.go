// Slice 409 — contract-test-tier rollout (provider side: the freshness +
// drift dashboard panel routes served by this package).
//
//	GET /v1/evidence/freshness   -> freshness.golden.json
//	GET /v1/controls/drift       -> drift.golden.json
//
// These pin the PROVIDER half of the BFF<->atlas wire contract for the
// freshness + drift dashboard panels the /e2e/ suite traverses (the
// precondition slice 394 names). The recorded goldens live under
// web/lib/contracts/ and are asserted by the CONSUMER halves against the
// BFF proxies (web/app/api/dashboard/freshness|drift/route.ts).
//
// THE DB-SEAM DECISION (slice 409 Option A): the production handler reads
// through *freshness.Store / *drift.Store (which hold a pgx pool). To
// record the wire shape on the plain `go test ./...` unit surface
// (ADR-0007, P0-409-1: no recorder on the integration surface), the
// handler depends on the unexported freshnessLister / driftReporter seams
// (handlers.go), and this recorder injects fixed-row stubs via the
// unexported newHandlerWithReaders. No Postgres, no pool. The seams are
// internal — New(*Store, *Store) is unchanged (P0-409-2).
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/freshnessdrift/ -run TestContract -update

package freshnessdrift

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID = "00000000-0000-4000-8000-000000000409"
	contractControlA = "33333333-3333-4333-8333-333333333333"
)

// stubFreshness is the fixed-row implementation of the freshnessLister
// seam. stubDrift is the driftReporter twin. Both return deterministic
// values with no Postgres.
type stubFreshness struct{ rows []freshness.ControlFreshness }

func (s stubFreshness) List(_ context.Context) ([]freshness.ControlFreshness, error) {
	return s.rows, nil
}

type stubDrift struct{ report drift.DriftReport }

func (s stubDrift) Report(_ context.Context, _ time.Duration) (drift.DriftReport, error) {
	return s.report, nil
}

// contractRequest builds a GET with a tenant on the context — the only
// gate the freshness/drift handlers enforce (tenantContext) — so the
// recorder reaches the happy path with no DB.
func contractRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx, err := tenancy.WithTenant(req.Context(), contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
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

// ===== GET /v1/evidence/freshness =====

func TestContract_Freshness(t *testing.T) {
	observed := mustTime("2026-05-10T00:00:00Z")
	validUntil := mustTime("2026-08-10T00:00:00Z")
	populated := stubFreshness{rows: []freshness.ControlFreshness{
		{
			ControlID:        uuid.MustParse(contractControlA),
			FreshnessClass:   "quarterly",
			LatestObservedAt: &observed,
			ValidUntil:       &validUntil,
			IsStale:          false,
			EvidenceCount:    3,
		},
		{
			// A stale control + an unclassified control exercise the
			// "unclassified" bucketing and the stale counts.
			ControlID:      uuid.MustParse(contractControlA),
			FreshnessClass: "",
			IsStale:        true,
			EvidenceCount:  1,
		},
	}}
	empty := stubFreshness{rows: []freshness.ControlFreshness{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReaders(populated, stubDrift{}).Freshness, "/v1/evidence/freshness"),
		"empty":     recordVariant(t, newHandlerWithReaders(empty, stubDrift{}).Freshness, "/v1/evidence/freshness"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/freshness.golden.json"),
		"Slice 409 contract-tier golden. PROVIDER: internal/api/freshnessdrift/handler_contract_test.go (Freshness, real handler over an injected fixed-row freshnessLister stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/freshnessdrift/ -run TestContract_Freshness -update`. CONSUMER: web/lib/contracts/freshness.contract.test.ts asserts the BFF at web/app/api/dashboard/freshness/route.ts.",
		"GET /v1/evidence/freshness",
		recorded,
	)
}

// ===== GET /v1/controls/drift =====

func TestContract_Drift(t *testing.T) {
	populated := stubDrift{report: drift.DriftReport{
		SinceDate:   mustTime("2026-05-08T00:00:00Z"),
		ThroughDate: mustTime("2026-05-15T00:00:00Z"),
		Delta:       -1,
		FlippedToOut: []drift.DriftRow{
			{
				ControlID:     uuid.MustParse(contractControlA),
				LastPassing:   mustTime("2026-05-12T00:00:00Z"),
				CurrentResult: "fail",
			},
		},
	}}
	empty := stubDrift{report: drift.DriftReport{
		SinceDate:    mustTime("2026-05-08T00:00:00Z"),
		ThroughDate:  mustTime("2026-05-15T00:00:00Z"),
		Delta:        0,
		FlippedToOut: nil,
	}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReaders(stubFreshness{}, populated).Drift, "/v1/controls/drift"),
		"empty":     recordVariant(t, newHandlerWithReaders(stubFreshness{}, empty).Drift, "/v1/controls/drift"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/drift.golden.json"),
		"Slice 409 contract-tier golden. PROVIDER: internal/api/freshnessdrift/handler_contract_test.go (Drift, real handler over an injected fixed-row driftReporter stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/freshnessdrift/ -run TestContract_Drift -update`. CONSUMER: web/lib/contracts/drift.contract.test.ts asserts the BFF at web/app/api/dashboard/drift/route.ts.",
		"GET /v1/controls/drift",
		recorded,
	)
}
