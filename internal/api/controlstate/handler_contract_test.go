// Slice 412 — contract-test-tier rollout (provider side: the control-detail
// tail routes GET /v1/controls/{id}/state + /effectiveness served by this
// package).
//
//	GET /v1/controls/{id}/state         -> control-state.golden.json
//	GET /v1/controls/{id}/effectiveness -> control-effectiveness.golden.json
//
// These pin the PROVIDER half of the BFF<->atlas wire contract for two of the
// three control-detail tail reads the /e2e/ suite still hand-mocks after slice
// 411 (web/e2e/control-detail-tabs.spec.ts route-fulfills /state, /coverage,
// /effectiveness). The recorded goldens live under web/lib/contracts/ and are
// asserted by the CONSUMER halves against the BFFs at
// web/app/api/controls/[id]/{state,effectiveness}/route.ts — both VERBATIM
// passthroughs, so the consumer asserts are toEqual(golden) (slice 411 D5).
//
// THE DB-SEAM DECISION (slice 412, mirroring slice 411's per-route Option A):
// the production State/Effectiveness paths read through *eval.Engine, which
// holds a pgx pool. To record the wire shape on the plain `go test ./...` unit
// surface (ADR-0007, P0-409-1: no recorder on the integration surface) without
// a pool, the two paths depend on a two-method controlEvaluator seam
// (handlers.go) — just the ControlState + Effectiveness methods those two
// routes use. This recorder injects a fixed-row stub via the unexported
// newHandlerWithEvaluator. No eval.Engine, no Postgres. The seam is internal —
// New(*eval.Engine) is unchanged (P0-409-2).
//
// THE CHI-ROUTING WRINKLE (slice 411 D3): both handlers resolve the control id
// via chi.URLParam(r, "id") — a PATH param, not a query param. So the recorder
// routes the request through a chi.NewRouter() mounting the handler at the real
// path so chi.URLParam resolves; a raw httptest.NewRequest would yield an empty
// {id} -> 400. The tenant gate (tenantContext) is satisfied by binding a tenant
// onto the request context; State/Effectiveness have no canWrite/credential
// gate (they are reads open to any authed caller; OPA is the production RBAC
// gate).
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/controlstate/ -run TestContract -update

package controlstate

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

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID  = "00000000-0000-4000-8000-000000000412"
	contractControlID = "11111111-1111-4111-8111-111111111111"
	contractCellID    = "33333333-3333-4333-8333-333333333333"
)

// stubEvaluator is the fixed-row implementation of the two-method
// controlEvaluator seam. It returns deterministic rows with no eval.Engine.
type stubEvaluator struct {
	states []eval.State
	eff    eval.Effectiveness
}

func (s stubEvaluator) ControlState(_ context.Context, _ uuid.UUID, _ string, _ time.Time) ([]eval.State, error) {
	return s.states, nil
}

func (s stubEvaluator) Effectiveness(_ context.Context, _ uuid.UUID) (eval.Effectiveness, error) {
	return s.eff, nil
}

// contractRequest builds a GET carrying the gate the handlers enforce via
// tenantContext: a tenant bound onto the request context. The request is routed
// through a chi router so chi.URLParam(r, "id") resolves the {id} path param.
func recordVariant(t *testing.T, handler http.HandlerFunc, routePattern, target string) json.RawMessage {
	t.Helper()
	r := chi.NewRouter()
	r.Get(routePattern, handler)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx, err := tenancy.WithTenant(req.Context(), contractTenantID)
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

func mustTime(rfc3339 string) time.Time {
	tt, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic("contract fixture: bad timestamp " + rfc3339)
	}
	return tt
}

// ===== GET /v1/controls/{id}/state =====

func TestContract_ControlState(t *testing.T) {
	evaluatedAt := mustTime("2026-04-02T09:00:00Z")
	observedAt := mustTime("2026-04-01T12:00:00Z")
	cell := uuid.MustParse(contractCellID)
	ctrl := uuid.MustParse(contractControlID)

	// Row 1 — scoped cell, fully populated: scope_cell_id + last_observed_at
	// both present, pinning the present-shape of the nullable fields. Row 2 —
	// whole-tenant cell (scope_cell_id nil) with no observation yet
	// (last_observed_at nil), pinning the null-shape the consumer's
	// `string | null` typing depends on.
	scoped := eval.State{
		ControlID:             ctrl,
		ScopeCellID:           &cell,
		Result:                "pass",
		FreshnessStatus:       "fresh",
		EvidenceCountInWindow: 3,
		LastObservedAt:        &observedAt,
		EvaluatedAt:           evaluatedAt,
		FreshnessClass:        "30d",
		Trigger:               "evidence",
	}
	wholeTenant := eval.State{
		ControlID:             ctrl,
		ScopeCellID:           nil,
		Result:                "na",
		FreshnessStatus:       "stale",
		EvidenceCountInWindow: 0,
		LastObservedAt:        nil,
		EvaluatedAt:           evaluatedAt,
		FreshnessClass:        "30d",
		Trigger:               "schedule",
	}

	populated := stubEvaluator{states: []eval.State{scoped, wholeTenant}}
	empty := stubEvaluator{states: []eval.State{}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithEvaluator(populated).State,
			"/v1/controls/{id}/state", "/v1/controls/"+contractControlID+"/state"),
		"empty": recordVariant(t, newHandlerWithEvaluator(empty).State,
			"/v1/controls/{id}/state", "/v1/controls/"+contractControlID+"/state"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-state.golden.json"),
		"Slice 412 contract-tier golden. PROVIDER: internal/api/controlstate/handler_contract_test.go (State, real handler over an injected fixed-row two-method controlEvaluator stub — per-route Option-A seam, no eval.Engine/Postgres). Regenerate: `go test ./internal/api/controlstate/ -run TestContract_ControlState -update`. CONSUMER: web/lib/contracts/control-state.contract.test.ts asserts the BFF at web/app/api/controls/[id]/state/route.ts — VERBATIM passthrough (toEqual). scope_cell_id + last_observed_at are nullable (null on the whole-tenant/no-observation row).",
		"GET /v1/controls/{id}/state",
		recorded,
	)
}

// ===== GET /v1/controls/{id}/effectiveness =====

func TestContract_ControlEffectiveness(t *testing.T) {
	windowStart := mustTime("2026-03-03T00:00:00Z")
	windowEnd := mustTime("2026-04-02T00:00:00Z")
	ctrl := uuid.MustParse(contractControlID)

	// Row "populated" — a control with evaluation data: pass_rate is the
	// pass_count/total_count ratio. Row "empty" — a control with no
	// evaluations in the window: total_count 0, pass_rate 0. The consumer
	// must NOT degrade "no data" to "perfectly failing"; the wire shape
	// records both so the field typing (all-numbers-present) is pinned.
	populated := stubEvaluator{eff: eval.Effectiveness{
		ControlID:   ctrl,
		PassRate:    0.8,
		PassCount:   12,
		TotalCount:  15,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}}
	empty := stubEvaluator{eff: eval.Effectiveness{
		ControlID:   ctrl,
		PassRate:    0,
		PassCount:   0,
		TotalCount:  0,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}}

	recorded := map[string]json.RawMessage{
		"populated": recordVariant(t, newHandlerWithEvaluator(populated).Effectiveness,
			"/v1/controls/{id}/effectiveness", "/v1/controls/"+contractControlID+"/effectiveness"),
		"empty": recordVariant(t, newHandlerWithEvaluator(empty).Effectiveness,
			"/v1/controls/{id}/effectiveness", "/v1/controls/"+contractControlID+"/effectiveness"),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-effectiveness.golden.json"),
		"Slice 412 contract-tier golden. PROVIDER: internal/api/controlstate/handler_contract_test.go (Effectiveness, real handler over an injected fixed-row two-method controlEvaluator stub — per-route Option-A seam, no eval.Engine/Postgres). Regenerate: `go test ./internal/api/controlstate/ -run TestContract_ControlEffectiveness -update`. CONSUMER: web/lib/contracts/control-effectiveness.contract.test.ts asserts the BFF at web/app/api/controls/[id]/effectiveness/route.ts — VERBATIM passthrough (toEqual). The empty variant pins total_count=0 + pass_rate=0 so the consumer never confuses 'no data' with 'perfectly failing'.",
		"GET /v1/controls/{id}/effectiveness",
		recorded,
	)
}
