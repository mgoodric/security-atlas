// Slice 411 — contract-test-tier rollout (provider side: GET /v1/audit-periods,
// the audit-workspace period-index route served by this package).
//
//	GET /v1/audit-periods  -> audit-periods.golden.json
//
// This pins the PROVIDER half of the BFF<->atlas wire contract for the
// audit-workspace period index the /e2e/ suite traverses (the /audits view;
// web/e2e/audits-header.spec.ts asserts the pill reads the /api/audits BFF).
// The recorded golden lives under web/lib/contracts/ and is asserted by the
// CONSUMER half against the BFF (web/app/api/audits/route.ts). Unlike the
// slice-410 risks BFF, /api/audits is a VERBATIM passthrough — it forwards the
// upstream body text unchanged — so the consumer assert is toEqual(golden)
// like the slice-409 dashboard panels.
//
// THE DB-SEAM DECISION (slice 411 list-only Option A): the production List path
// reads through *period.Store, which holds a pgx pool AND exposes a WIDE
// surface (Create/Get/List/Freeze/ControlState/AttachPopulation/…). To record
// the wire shape on the plain `go test ./...` unit surface (ADR-0007,
// P0-409-1: no recorder on the integration surface) without a wide interface,
// the List path depends on a LIST-ONLY periodLister seam (handlers.go) — just
// the one List method that endpoint needs. This recorder injects a fixed-row
// stub via the unexported newHandlerWithLister. No Postgres, no pool. The seam
// is internal — New(*period.Store) is unchanged (P0-409-2); every other
// handler keeps using the concrete *period.Store.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/auditperiods/ -run TestContract -update

package auditperiods

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
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID = "00000000-0000-4000-8000-000000000411"
	contractPeriodID = "11111111-1111-4111-8111-111111111111"
	contractFWID     = "22222222-2222-4222-8222-222222222222"
)

// stubPeriodLister is the fixed-row implementation of the list-only
// periodLister seam. It returns deterministic rows with no Postgres.
type stubPeriodLister struct{ rows []period.Period }

func (s stubPeriodLister) List(_ context.Context) ([]period.Period, error) {
	return s.rows, nil
}

// contractRequest builds a GET carrying both the gates List enforces via
// authnContext: an authctx credential with a non-empty TenantID AND a tenant
// on the context. With both present the recorder reaches the happy path with
// no DB. (List itself has no canWrite gate — it is a read open to any authed
// caller; the OPA layer is the production RBAC gate.)
func contractRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_contract_411",
		TenantID: contractTenantID,
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
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

// ===== GET /v1/audit-periods (audit-workspace period index) =====

func TestContract_AuditPeriods(t *testing.T) {
	start := mustTime("2026-01-01T00:00:00Z")
	end := mustTime("2026-03-31T00:00:00Z")
	created := mustTime("2025-12-15T00:00:00Z")
	updated := mustTime("2026-04-02T00:00:00Z")
	frozenAt := mustTime("2026-04-01T12:00:00Z")

	// Row 1 — open period: nullable frozen_* fields ABSENT on the wire
	// (omitempty), pinning their absent-not-null shape. Row 2 — frozen period:
	// frozen_at + frozen_hash + frozen_by all present, pinning the frozen-state
	// wire shape (frozen_hash is the hex-encoded SHA-256 of the freeze inputs).
	open := period.Period{
		ID:                 uuid.MustParse(contractPeriodID),
		TenantID:           uuid.MustParse(contractTenantID),
		Name:               "SOC 2 Type II — H1 2026",
		FrameworkVersionID: uuid.MustParse(contractFWID),
		// Slice 680 / ATLAS-033: the LIST path resolves a readable
		// framework_label from the catalog. The open row carries one,
		// pinning the present-shape; the frozen row below leaves it
		// empty, pinning the omitempty absence (e.g. an unresolved
		// framework version).
		FrameworkLabel: "SCF 2025.2",
		PeriodStart:    start,
		PeriodEnd:      end,
		Status:         period.StatusOpen,
		CreatedBy:      "key_contract_411",
		CreatedAt:      created,
		UpdatedAt:      updated,
	}
	frozen := period.Period{
		ID:                 uuid.MustParse(contractFWID),
		TenantID:           uuid.MustParse(contractTenantID),
		Name:               "ISO 27001 — 2025 surveillance",
		FrameworkVersionID: uuid.MustParse(contractPeriodID),
		PeriodStart:        start,
		PeriodEnd:          end,
		Status:             period.StatusFrozen,
		FrozenAt:           &frozenAt,
		// Obviously-fake 32-byte digest (not a real hash) — synthetic bytes.
		FrozenHash: []byte{
			0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
			0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
			0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
			0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
		},
		FrozenBy:  "key_contract_411",
		CreatedBy: "key_contract_411",
		CreatedAt: created,
		UpdatedAt: updated,
	}

	populated := stubPeriodLister{rows: []period.Period{open, frozen}}
	empty := stubPeriodLister{rows: []period.Period{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithLister(populated).List, "/v1/audit-periods"),
		"empty":     recordVariant(t, newHandlerWithLister(empty).List, "/v1/audit-periods"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/audit-periods.golden.json"),
		"Slice 411 contract-tier golden. PROVIDER: internal/api/auditperiods/handler_contract_test.go (List, real handler over an injected fixed-row list-only periodLister stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/auditperiods/ -run TestContract_AuditPeriods -update`. CONSUMER: web/lib/contracts/audit-periods.contract.test.ts asserts the BFF at web/app/api/audits/route.ts — VERBATIM passthrough (toEqual). frozen_at/frozen_hash/frozen_by are omitempty (absent on open periods).",
		"GET /v1/audit-periods",
		recorded,
	)
}
