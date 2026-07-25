// Slice 687 — contract-test-tier rollout (provider side: the audit-period
// single-period READ tail routes served by this package, extending slice 411's
// list-only coverage to the two remaining audit-period reads):
//
//	GET /v1/audit-periods/{id}                -> audit-period-get.golden.json
//	GET /v1/audit-periods/{id}/control-state  -> audit-period-control-state.golden.json
//
// These pin the PROVIDER half of the BFF<->atlas wire contract for the
// single-period Get + frozen-horizon control-state reads the audit-workspace
// traverses. The recorded goldens live under web/lib/contracts/ and are
// asserted by the CONSUMER halves against the BFFs at
// web/app/api/audit/periods/[id]/route.ts + .../control-state — both VERBATIM
// passthroughs, so the consumer asserts are toEqual(golden) (slice 411 D5).
//
// THE DB-SEAM DECISION (slice 687, extending slice 411's list-only Option A):
// the production Get + ControlState paths read through *period.Store, a WIDE
// surface (Create/Get/List/Freeze/ControlState/AttachPopulation). Slice 411
// carved a list-only periodLister seam for the List path; this slice adds a
// two-method periodReader seam for the two READ paths, sized to exactly the
// methods those routes call (slice 412 D2 sizing rule). The recorder injects a
// fixed-row stub via the unexported newHandlerWithReader. No Postgres. The seam
// is internal — New(*period.Store) is unchanged (P0-409-2); the write handlers
// keep using the concrete h.store.
//
// THE CHI-ROUTING + AUTHZ WRINKLE: both routes resolve {id} via
// chi.URLParam(r, "id") — a PATH param — and ControlState additionally enforces
// canWrite (admin or grc_engineer). So this recorder routes through a
// chi.NewRouter() so chi.URLParam resolves, AND binds an IsAdmin credential so
// the ControlState canWrite gate passes. (Get has no canWrite gate — it is a
// read open to any authed caller; the OPA layer is the production RBAC gate.)
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/auditperiods/ -run TestContract_AuditPeriod -update

package auditperiods

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// contractControlID is the synthetic control id used in the control-state
// recorder's ?control= query param (slice 314 / GitGuardian: synthetic UUID).
const contractControlID = "33333333-3333-4333-8333-333333333333"

// stubPeriodReader is the fixed-row implementation of the two-method
// periodReader seam. It returns deterministic rows with no Postgres.
type stubPeriodReader struct {
	period period.Period
	obs    []period.ControlStateObservation
}

func (s stubPeriodReader) Get(_ context.Context, _ uuid.UUID) (period.Period, error) {
	return s.period, nil
}

func (s stubPeriodReader) ControlState(_ context.Context, _, _ uuid.UUID) ([]period.ControlStateObservation, error) {
	return s.obs, nil
}

// recordRoutedVariant routes a GET through a chi router so chi.URLParam(r,
// "id") resolves, binds an IsAdmin credential + tenant (satisfying authnContext
// AND the ControlState canWrite gate), and canonicalizes the recorded body.
func recordRoutedVariant(t *testing.T, handler http.HandlerFunc, routePattern, target string) json.RawMessage {
	t.Helper()
	r := chi.NewRouter()
	r.Get(routePattern, handler)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_contract_687",
		TenantID: contractTenantID,
		IsAdmin:  true,
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned status %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

// ===== GET /v1/audit-periods/{id} (single-period get) =====

func TestContract_AuditPeriodGet(t *testing.T) {
	start := mustTime("2026-01-01T00:00:00Z")
	end := mustTime("2026-03-31T00:00:00Z")
	created := mustTime("2025-12-15T00:00:00Z")
	updated := mustTime("2026-04-02T00:00:00Z")
	frozenAt := mustTime("2026-04-01T12:00:00Z")

	// Variant "open" — frozen_* fields absent (omitempty) AND framework_label
	// absent (the Get path does NOT join the catalog — only List does, slice
	// 680). Pins the single-period get shape distinct from the list shape.
	open := period.Period{
		ID:                 uuid.MustParse(contractPeriodID),
		TenantID:           uuid.MustParse(contractTenantID),
		Name:               "SOC 2 Type II — H1 2026",
		FrameworkVersionID: uuid.MustParse(contractFWID),
		PeriodStart:        start,
		PeriodEnd:          end,
		Status:             period.StatusOpen,
		CreatedBy:          "key_contract_687",
		CreatedAt:          created,
		UpdatedAt:          updated,
	}
	// Variant "frozen" — frozen_at/frozen_hash/frozen_by present, pinning the
	// frozen-state wire shape (frozen_hash is the hex-encoded SHA-256 of the
	// freeze inputs).
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
		FrozenBy:  "key_contract_687",
		CreatedBy: "key_contract_687",
		CreatedAt: created,
		UpdatedAt: updated,
	}

	recorded := map[string]json.RawMessage{
		"open": recordRoutedVariant(t, newHandlerWithReader(stubPeriodReader{period: open}).Get,
			"/v1/audit-periods/{id}", "/v1/audit-periods/"+contractPeriodID),
		"frozen": recordRoutedVariant(t, newHandlerWithReader(stubPeriodReader{period: frozen}).Get,
			"/v1/audit-periods/{id}", "/v1/audit-periods/"+contractFWID),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/audit-period-get.golden.json"),
		"Slice 687 contract-tier golden. PROVIDER: internal/api/auditperiods/handler687_contract_test.go (Get, real handler over an injected fixed-row two-method periodReader stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/auditperiods/ -run TestContract_AuditPeriodGet -update`. CONSUMER: web/lib/contracts/audit-period-get.contract.test.ts asserts the BFF at web/app/api/audit/periods/[id]/route.ts — VERBATIM passthrough (toEqual). frozen_at/frozen_hash/frozen_by + framework_label are omitempty; the single-period Get does NOT join the catalog label (slice 680, List-only).",
		"GET /v1/audit-periods/{id}",
		recorded,
	)
}

// ===== GET /v1/audit-periods/{id}/control-state =====

func TestContract_AuditPeriodControlState(t *testing.T) {
	obsAt := mustTime("2026-02-15T08:30:00Z")

	// Variant "populated" — two observations: index 0 is the most-recent
	// pass/fail-driving record. Pins the observation row shape
	// (evidence_record_id / observed_at / result / hash) the audit sampling
	// surface reads. Variant "empty" — no observations in the frozen horizon;
	// observations is [] (never null) and count 0.
	populated := stubPeriodReader{obs: []period.ControlStateObservation{
		{
			EvidenceRecordID: uuid.MustParse("77777777-7777-4777-8777-777777777777"),
			ObservedAt:       obsAt,
			Result:           "pass",
			// Obviously-fake hex content-hash (synthetic, not a real sha256).
			Hash: "sha256:aaaabbbbccccdddd0000111122223333",
		},
		{
			EvidenceRecordID: uuid.MustParse("88888888-8888-4888-8888-888888888888"),
			ObservedAt:       obsAt,
			Result:           "fail",
			Hash:             "sha256:eeeeffff000011112222333344445555",
		},
	}}
	empty := stubPeriodReader{obs: []period.ControlStateObservation{}}

	target := "/v1/audit-periods/" + contractPeriodID + "/control-state?control=" + contractControlID
	recorded := map[string]json.RawMessage{
		"populated": recordRoutedVariant(t, newHandlerWithReader(populated).ControlState,
			"/v1/audit-periods/{id}/control-state", target),
		"empty": recordRoutedVariant(t, newHandlerWithReader(empty).ControlState,
			"/v1/audit-periods/{id}/control-state", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/audit-period-control-state.golden.json"),
		"Slice 687 contract-tier golden. PROVIDER: internal/api/auditperiods/handler687_contract_test.go (ControlState, real handler over an injected fixed-row two-method periodReader stub — Option A seam, no Postgres; routed through chi + IsAdmin credential for the canWrite gate). Regenerate: `go test ./internal/api/auditperiods/ -run TestContract_AuditPeriodControlState -update`. CONSUMER: web/lib/contracts/audit-period-control-state.contract.test.ts asserts the BFF at web/app/api/audit/periods/[id]/control-state/route.ts — VERBATIM passthrough (toEqual). observations is always an array (never null); empty horizon records [] + count 0.",
		"GET /v1/audit-periods/{id}/control-state",
		recorded,
	)
}
