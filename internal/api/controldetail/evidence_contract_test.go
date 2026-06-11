// Slice 692 — contract-test-tier rollout (provider side: the PER-CONTROL
// evidence-ledger window served by this package's Evidence handler):
//
//	GET /v1/evidence?control_id=<id>  -> control-evidence.golden.json
//
// This pins the PROVIDER half of the BFF<->atlas wire contract for the
// per-control evidence window the control-detail view's evidence card reads.
// The recorded golden lives under web/lib/contracts/ and is asserted by the
// CONSUMER half (web/lib/contracts/control-evidence.contract.test.ts) against
// the BFF (web/app/api/evidence/route.ts). That BFF is a VERBATIM passthrough
// — it forwards the upstream body bytes + status unchanged — and getAttestForm-
// style lib readers return res.json() unchanged, so the consumer assert is
// toEqual(golden) like the slice-411 control-detail tabs (NOT field-contract).
//
// THE DB-SEAM DECISION (slice 692 per-route read seam, Option A): the
// production per-control path reads through *Store (a pgx pool). To record
// the wire shape on the plain `go test ./...` unit surface (ADR-0007,
// P0-409-1: no recorder on the integration surface) the per-control branch
// depends on a narrow two-method evidenceWindowReader seam (handler.go) —
// just EvidenceForControl + CountEvidenceForTenant. This recorder injects a
// fixed-row stub via the unexported newHandlerWithEvidenceReader. No Postgres,
// no pool. The seam is internal — New(*Store) is unchanged (P0-409-2); the
// tenant-wide branch keeps using the concrete *Store (decisions log D3).
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/controldetail/ -run TestContract_ControlEvidence -update

package controldetail

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

const contractEvidenceID = "55555555-5555-4555-8555-555555555555"

// strptr returns a pointer to s — the sqlc nullable evidence_kind column is a
// *string at the Go boundary.
func strptr(s string) *string { return &s }

// stubEvidenceReader is the fixed-row implementation of the per-control
// evidenceWindowReader seam. It returns deterministic rows + a fixed
// tenant-wide total with no Postgres.
type stubEvidenceReader struct {
	rows  []dbx.ListEvidenceForControlPagedRow
	total int64
}

func (s stubEvidenceReader) EvidenceForControl(_ context.Context, _ uuid.UUID, _ evidencePage) ([]dbx.ListEvidenceForControlPagedRow, error) {
	return s.rows, nil
}

func (s stubEvidenceReader) CountEvidenceForTenant(_ context.Context) (int64, error) {
	return s.total, nil
}

// recordEvidenceVariant drives the Evidence handler directly (the control id
// is a QUERY param, not a path param, so no chi mux is needed — unlike the
// slice-411 policies/risks/history recorder). It binds the two gates the
// handler enforces (control-read credential + tenant context) and
// canonicalizes the recorded body.
func recordEvidenceVariant(t *testing.T, h http.HandlerFunc, target string) json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	// IsApprover -> grc_engineer grants control-read (authz.go hasControlRead).
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:         "key_contract_692",
		TenantID:   contractTenantID,
		IsApprover: true,
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned status %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

// ===== GET /v1/evidence?control_id=<id> (per-control window) =====

func TestContract_ControlEvidence(t *testing.T) {
	observed := mustTime("2026-05-14T08:00:00Z")

	// populated — two ledger rows. Row 1 fully populated: evidence_kind
	// present, scope_cell present, a provenance JSONB blob, result pass.
	// Row 2 minimal: evidence_kind NULL (nullable wire shape), scope_cell
	// NULL (uuidPtr -> null), empty provenance (jsonOrNull -> JSON null),
	// result fail. The two-row set pins both the present and absent branches
	// of every nullable field. The fixed total (7) is the tenant-wide ledger
	// count, NOT the page length (count) — the wire surfaces both so the
	// frontend can render "Showing N of M".
	populated := stubEvidenceReader{
		total: 7,
		rows: []dbx.ListEvidenceForControlPagedRow{
			{
				ID:           pgID(contractEvidenceID),
				TenantID:     pgID(contractTenantID),
				ControlID:    pgID(contractControlID),
				ControlRef:   contractControlID,
				ScopeID:      pgID(contractScopeID),
				ObservedAt:   pgTS(observed),
				EvidenceKind: strptr("manual.attestation.v1"),
				Provenance:   []byte(`{"connector":"manual","runner":"operator-console"}`),
				Hash:         "sha256:0000000000000000000000000000000000000000000000000000000000000000",
				Result:       dbx.EvidenceResult("pass"),
			},
			{
				ID:           pgID(contractPolicyID),
				TenantID:     pgID(contractTenantID),
				ControlID:    pgID(contractControlID),
				ControlRef:   contractControlID,
				ScopeID:      pgtype.UUID{Valid: false},
				ObservedAt:   pgTS(observed.Add(-12 * time.Hour)),
				EvidenceKind: nil,
				Provenance:   nil,
				Hash:         "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Result:       dbx.EvidenceResult("fail"),
			},
		},
	}
	// empty — no ledger rows in the window, but the tenant-wide total is
	// still non-zero (3): pins the "filters narrowed to zero, ledger not
	// empty" disambiguation (slice 236). evidence is [] (never null).
	empty := stubEvidenceReader{total: 3, rows: nil}

	target := "/v1/evidence?control_id=" + contractControlID
	recorded := map[string]json.RawMessage{
		"populated": recordEvidenceVariant(t, newHandlerWithEvidenceReader(populated).Evidence, target),
		"empty":     recordEvidenceVariant(t, newHandlerWithEvidenceReader(empty).Evidence, target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/control-evidence.golden.json"),
		"Slice 692 contract-tier golden. PROVIDER: internal/api/controldetail/evidence_contract_test.go (Evidence per-control branch, real handler over an injected fixed-row evidenceWindowReader stub — Option A two-method seam, no Postgres). Regenerate: `go test ./internal/api/controldetail/ -run TestContract_ControlEvidence -update`. CONSUMER: web/lib/contracts/control-evidence.contract.test.ts asserts the BFF at web/app/api/evidence/route.ts — VERBATIM passthrough (toEqual). Envelope is {control_id, evidence:[], count, total, next_cursor}; each row carries evidence_id/observed_at/content_hash/result (strings), evidence_kind (string-or-null), scope_cell (string-or-null), source (opaque JSON, null when absent); evidence is ALWAYS an array; total is the tenant-wide ledger count (NOT the page length). JUDGMENT (decisions log D3): seam covers ONLY the per-control branch; the slice-106 tenant-wide branch is deferred + spilled.",
		"GET /v1/evidence?control_id={id}",
		recorded,
	)
}
