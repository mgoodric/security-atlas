// Slice 409 — contract-test-tier rollout (provider side: the dashboard
// panel routes served by this package).
//
//	GET /v1/frameworks/posture   -> framework-posture.golden.json
//	GET /v1/activity             -> activity.golden.json
//
// (the upcoming rollup and its upcoming.golden.json were retired by #2130.)
//
// These pin the PROVIDER half of the BFF<->atlas wire contract for the
// dashboard panels the /e2e/ suite traverses (the precondition slice 394
// names). The recorded goldens live under web/lib/contracts/ and are
// asserted by the CONSUMER halves (web/lib/contracts/<endpoint>.contract.
// test.ts) against the BFF proxies (web/app/api/dashboard/<panel>/route.ts).
//
// THE DB-SEAM DECISION (slice 409 Option A): the production handler reads
// tenant data through *Store (which holds a pgx pool). To record the wire
// shape on the plain `go test ./...` unit surface (ADR-0007, P0-409-1: no
// recorder on the integration surface), the handler depends on the
// unexported `reader` seam (handler.go), and this recorder injects a
// fixed-row stub via the unexported newHandlerWithReader. No Postgres, no
// pool. The seam is internal — New(*Store) is unchanged (P0-409-2).
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/dashboard/ -run TestContract -update

package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no JWT- or
// vendor-shaped literals — synthetic UUIDs and plain strings only).
const (
	contractTenantID   = "00000000-0000-4000-8000-000000000409"
	contractFrameworkA = "11111111-1111-4111-8111-111111111111"
	contractControlA   = "22222222-2222-4222-8222-222222222222"
	contractFrameworkB = "33333333-3333-4333-8333-333333333333"
	contractVersionA   = "44444444-4444-4444-8444-444444444444"
	contractVersionB   = "55555555-5555-4555-8555-555555555555"
)

// stubReader is the fixed-row implementation of the handler's `reader`
// seam. It returns deterministic rows with no Postgres, so the handler's
// real row->wire transformation records on the unit surface.
type stubReader struct {
	posture  []dbx.FrameworkPostureRow
	activity []dbx.ListEvidenceActivityRow
}

func (s stubReader) FrameworkPosture(_ context.Context, _ pgtype.Timestamptz) ([]dbx.FrameworkPostureRow, error) {
	return s.posture, nil
}

func (s stubReader) ActivityFeed(_ context.Context, _ keyset, _ int32) ([]dbx.ListEvidenceActivityRow, error) {
	return s.activity, nil
}

// contractRequest builds a GET with a program-read admin credential and a
// tenant on the context — the two gates every dashboard handler enforces
// (requireProgramRead + tenantContext) — so the recorder reaches the
// happy path with no DB.
func contractRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "contract-recorder",
		TenantID: contractTenantID,
		IsAdmin:  true, // grants program-read (authz.go hasProgramRead)
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	return req.WithContext(ctx)
}

// recordVariant drives the given handler func and returns the
// canonicalized 200 body.
func recordVariant(t *testing.T, h http.HandlerFunc, target string) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, contractRequest(t, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned status %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

func pgTS(rfc3339 string) pgtype.Timestamptz {
	tt, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic("contract fixture: bad timestamp " + rfc3339)
	}
	return pgtype.Timestamptz{Time: tt, Valid: true}
}

func pgUUIDFromString(s string) pgtype.UUID {
	return pgUUID(uuid.MustParse(s))
}

// ===== GET /v1/frameworks/posture =====

func TestContract_FrameworkPosture(t *testing.T) {
	// Two-framework population + the empty set: the BFF must tolerate both
	// a populated `frameworks` array and `[]` with count 0.
	populated := stubReader{posture: []dbx.FrameworkPostureRow{
		{
			FrameworkID:           pgUUIDFromString(contractFrameworkA),
			FrameworkVersion:      "2017",
			FrameworkVersionID:    pgUUIDFromString(contractVersionA),
			CoveragePct:           87.5,
			CoveredByExceptionPct: 12.5,
			FreshnessComposite:    0.92,
			TrendDelta90d:         3.0,
			ScopeActivated:        true,
		},
		{
			// No activated scope (#2114): the row carries scope_set=false
			// and null percentages, whatever the query computed.
			FrameworkID:        pgUUIDFromString(contractFrameworkB),
			FrameworkVersion:   "4.0",
			FrameworkVersionID: pgUUIDFromString(contractVersionB),
			CoveragePct:        100,
			FreshnessComposite: 100,
			TrendDelta90d:      100,
			ScopeActivated:     false,
		},
	}}
	empty := stubReader{posture: []dbx.FrameworkPostureRow{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReader(populated).FrameworkPosture, "/v1/frameworks/posture"),
		"empty":     recordVariant(t, newHandlerWithReader(empty).FrameworkPosture, "/v1/frameworks/posture"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/framework-posture.golden.json"),
		"Slice 409 contract-tier golden. PROVIDER: internal/api/dashboard/handler_contract_test.go (FrameworkPosture, real handler over an injected fixed-row reader stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/dashboard/ -run TestContract_FrameworkPosture -update`. CONSUMER: web/lib/contracts/framework-posture.contract.test.ts asserts the BFF at web/app/api/dashboard/framework-posture/route.ts.",
		"GET /v1/frameworks/posture",
		recorded,
	)
}

// ===== GET /v1/activity =====

func TestContract_Activity(t *testing.T) {
	populated := stubReader{activity: []dbx.ListEvidenceActivityRow{
		{
			Ts:           pgTS("2026-05-15T15:04:05Z"),
			EventType:    "evidence.ingested",
			Actor:        "connector:aws",
			ResourceType: "evidence",
			ResourceID:   contractControlA,
			Summary:      []byte(`{"kind":"sast.scan_result.v1"}`),
		},
		{
			// Null summary -> the handler renders JSON `null` (jsonOrNull).
			Ts:           pgTS("2026-05-15T14:00:00Z"),
			EventType:    "evidence.ingested",
			Actor:        "user:contract",
			ResourceType: "evidence",
			ResourceID:   contractControlA,
			Summary:      nil,
		},
	}}
	empty := stubReader{activity: []dbx.ListEvidenceActivityRow{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithReader(populated).Activity, "/v1/activity"),
		"empty":     recordVariant(t, newHandlerWithReader(empty).Activity, "/v1/activity"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/activity.golden.json"),
		"Slice 409 contract-tier golden. PROVIDER: internal/api/dashboard/handler_contract_test.go (Activity, real handler over an injected fixed-row reader stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/dashboard/ -run TestContract_Activity -update`. CONSUMER: web/lib/contracts/activity.contract.test.ts asserts the BFF at web/app/api/dashboard/activity/route.ts.",
		"GET /v1/activity",
		recorded,
	)
}
