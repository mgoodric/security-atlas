// Slice 687 — contract-test-tier rollout (provider side: the control-detail
// coverage tail route GET /v1/controls/{id}/coverage served by this package).
//
//	GET /v1/controls/{id}/coverage -> control-coverage.golden.json
//
// This pins the PROVIDER half of the BFF<->atlas wire contract for the
// load-bearing control-detail tail read the /e2e/ suite still hand-mocks after
// slice 412 (web/e2e/control-detail-tabs.spec.ts route-fulfills /coverage at
// the BFF). The recorded golden lives under web/lib/contracts/ and is asserted
// by the CONSUMER half against the BFF at
// web/app/api/controls/[id]/coverage/route.ts — a VERBATIM passthrough, so the
// consumer assert is toEqual(golden) (slice 411 D5).
//
// THE DB-SEAM DECISION (slice 687, mirroring slice 412 D5): the production
// ControlCoverage path is a transaction-orchestrating multi-query ASSEMBLER —
// it opens a tenant-tx, reads the control, branches anchored/unanchored, reads
// the catalog anchor, branches pinned/unpinned, and runs the slice-256 per-row
// coverage computation across three more stores. An HONEST Option-A seam over
// the wider dbx.Queries surface would be a 6+-method interface plus an
// inTenantTx fake plus the three coverage stores — to record one golden (slice
// 412 D5 deferred it on exactly this seam-cost asymmetry). Instead the read is
// split from the serialization: assembleCoverage (control_coverage.go) does ALL
// the DB work and returns an assembled coverageView; ControlCoverage serializes
// it. The recorder injects a fixed-VIEW stub via the unexported
// newHandlerWithAssembler so the three wire-shape forks record with no pool.
// The interesting variants (anchored/unanchored + pinned/unpinned) are
// SERIALIZATION branches, and they live in the handler, not in any store
// method — so a thin read-model seam captures them faithfully. No eval.Engine,
// no Postgres. The seam is internal — New(*pgxpool.Pool) is unchanged
// (P0-409-2).
//
// THE CHI-ROUTING WRINKLE (slice 411 D3 / slice 412): ControlCoverage resolves
// the control id via chi.URLParam(r, "id") — a PATH param. So the recorder
// routes the request through a chi.NewRouter() mounting the handler at the real
// /v1/controls/{id}/coverage pattern so chi.URLParam resolves; a raw
// httptest.NewRequest would yield an empty {id} -> 400. ControlCoverage has no
// tenant/credential gate of its own (the seam owns tenant isolation in
// production via inTenantTx; the recorder bypasses it with the stub), so no
// WithTenant binding is needed for the recorder to reach the happy path.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/ucfcoverage/ -run TestContract -update

package ucfcoverage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractControlID = "11111111-1111-4111-8111-111111111111"
	contractAnchorID  = "22222222-2222-4222-8222-222222222222"
	contractEdgeID    = "44444444-4444-4444-8444-444444444444"
	contractReqID     = "55555555-5555-4555-8555-555555555555"
	contractFWVersID  = "66666666-6666-4666-8666-666666666666"
)

// stubCoverageAssembler is the fixed-view implementation of the single-method
// coverageAssembler seam. It returns a deterministic coverageView with no
// Postgres pool.
type stubCoverageAssembler struct {
	view  coverageView
	found bool
}

func (s stubCoverageAssembler) assembleCoverage(_ context.Context, _ uuid.UUID, _ string) (coverageView, bool, error) {
	return s.view, s.found, nil
}

// recordVariant routes a GET through a chi router so chi.URLParam(r, "id")
// resolves the {id} path param, then canonicalizes the recorded body.
func recordVariant(t *testing.T, handler http.HandlerFunc) json.RawMessage {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/controls/{id}/coverage", handler)

	req := httptest.NewRequest(http.MethodGet, "/v1/controls/"+contractControlID+"/coverage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned status %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

func contractControlWire() controlWire {
	return controlWire{
		ID:                 contractControlID,
		BundleID:           "ctl.access-review",
		Version:            3,
		SCFID:              "IAC-06",
		SCFAnchorID:        contractAnchorID,
		Title:              "Periodic access review",
		ControlFamily:      "IAC",
		ImplementationType: "manual",
		OwnerRole:          "grc_engineer",
		LifecycleState:     "active",
		FreshnessClass:     "30d",
	}
}

func contractAnchorWire() anchorWire {
	return anchorWire{
		ID:          contractAnchorID,
		SCFID:       "IAC-06",
		Family:      "IAC",
		Name:        "Identity & Access Management",
		Description: "Govern logical access to systems and data.",
	}
}

// contractRequirement builds one requirementForAnchorWire with an optional
// per-row coverage score (nil = out-of-scope/no-data; non-nil = in-scope).
func contractRequirement(coverage *float64) requirementForAnchorWire {
	return requirementForAnchorWire{
		EdgeID:                 contractEdgeID,
		RequirementID:          contractReqID,
		Code:                   "CC6.1",
		Title:                  "Logical access controls",
		Body:                   "The entity implements logical access security measures.",
		FrameworkSlug:          "soc2",
		FrameworkName:          "SOC 2",
		FrameworkVersion:       "2017",
		FrameworkVersionID:     contractFWVersID,
		FrameworkVersionStatus: "current",
		RelationshipType:       "subset",
		Strength:               0.8,
		Coverage:               coverage,
		SourceAttribution:      "scf-strm",
		Rationale:              "SCF anchor satisfies the SOC 2 logical-access requirement.",
	}
}

// ===== GET /v1/controls/{id}/coverage =====

func TestContract_ControlCoverage(t *testing.T) {
	inScope := 0.64 // strength 0.8 × 30d pass-rate 0.8 — the in-scope coverage value.

	// Variant "anchored_unpinned" — the default control-detail tab fetch (no
	// ?framework_version=): control + non-null anchor + a requirements list
	// carrying BOTH the in-scope (coverage non-null) and out-of-scope
	// (coverage null) row shapes, pinning the `number | null` coverage typing
	// the consumer depends on (slice 256: null must NOT degrade to 0).
	anchoredUnpinned := stubCoverageAssembler{
		found: true,
		view: coverageView{
			Control:  contractControlWire(),
			Anchored: true,
			Anchor:   contractAnchorWire(),
			Requirements: []requirementForAnchorWire{
				contractRequirement(&inScope),
				contractRequirement(nil),
			},
		},
	}

	// Variant "anchored_pinned" — the ?framework_version= fork. Same anchor,
	// a single pinned requirement row. Pins the wire shape the consumer sees
	// when the control-detail tab filters to one framework version.
	anchoredPinned := stubCoverageAssembler{
		found: true,
		view: coverageView{
			Control:      contractControlWire(),
			Anchored:     true,
			Anchor:       contractAnchorWire(),
			Requirements: []requirementForAnchorWire{contractRequirement(&inScope)},
		},
	}

	// Variant "unanchored" — control exists but has no SCF anchor. The handler
	// serializes `anchor: null` + `requirements: []` (NOT 404). Pins the
	// "not yet mapped to the canonical graph" wire shape the dashboard reads.
	unanchored := stubCoverageAssembler{
		found: true,
		view: coverageView{
			Control:      contractControlWire(),
			Anchored:     false,
			Requirements: []requirementForAnchorWire{},
		},
	}

	recorded := map[string]json.RawMessage{
		"anchored_unpinned": recordVariant(t, newHandlerWithAssembler(anchoredUnpinned).ControlCoverage),
		"anchored_pinned":   recordVariant(t, newHandlerWithAssembler(anchoredPinned).ControlCoverage),
		"unanchored":        recordVariant(t, newHandlerWithAssembler(unanchored).ControlCoverage),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-coverage.golden.json"),
		"Slice 687 contract-tier golden. PROVIDER: internal/api/ucfcoverage/handler_contract_test.go (ControlCoverage, real handler over an injected fixed-view single-method coverageAssembler stub — thin read-model seam over the tx-orchestrating assembler, no Postgres/eval/scope stores; slice 412 D5 rationale). Regenerate: `go test ./internal/api/ucfcoverage/ -run TestContract_ControlCoverage -update`. CONSUMER: web/lib/contracts/control-coverage.contract.test.ts asserts the BFF at web/app/api/controls/[id]/coverage/route.ts — VERBATIM passthrough (toEqual). anchor is null on the unanchored variant; requirements[].coverage is number|null (null = out-of-scope/no-data, never degraded to 0).",
		"GET /v1/controls/{id}/coverage",
		recorded,
	)
}
