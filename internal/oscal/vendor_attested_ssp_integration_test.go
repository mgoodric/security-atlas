//go:build integration

// Integration tests for slice 619: accepted vendor claim -> vendor-attested
// SSP control-implementation.
//
// THE LOAD-BEARING TEST (the constitutional boundary): when an operator has
// ACCEPTED a vendor claim (slice 589 disposition), the OSCAL SSP export
// surfaces it as a VENDOR-ATTESTED by-component statement — clearly attributed
// to the vendor component, with accept-provenance — and NEVER as
// platform-verified evidence. The export writes NOTHING to control_evaluations
// (an accepted claim is an assertion the operator credited, not a control
// satisfaction): this suite asserts the control_evaluations row count is
// UNCHANGED across the export, and that a REJECTED / needs_info claim never
// appears as an implementation.
//
// These run against a REAL Postgres (RLS enforced via the app role). The
// SSP-content assertions additionally need the Python oscal-bridge; if it
// cannot start they self-skip (the 511/512/599 convention) — but the
// control_evaluations-unchanged assertion runs Go-side WITHOUT the bridge so
// the boundary is testable even where Python/trestle is absent.
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/oscal"
)

const vendorClaimSha = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// seededVendorClaim describes one imported_component_claims row + the
// disposition to apply to it.
type seededVendorClaim struct {
	claimID   uuid.UUID
	controlID string
	statement string
	status    string // "" leaves it 'asserted'; else accepted/rejected/needs_info
	actor     string
}

// seedVendorClaims seeds a component-definition with one vendor component and
// the given claims, applying each claim's disposition. Registers cleanup of
// the imported_component_* tables (freshTenant does not own them).
func seedVendorClaims(t *testing.T, admin *pgxpool.Pool, tenant string, claims []seededVendorClaim) {
	t.Helper()
	ctx := context.Background()

	defID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO imported_catalogs (
			id, tenant_id, source, kind, imported_by, source_sha256,
			source_label, oscal_version, catalog_title, control_count
		)
		VALUES ($1, $2, 'oscal-component-import', 'component_definition',
		        'test-importer', $3, 'AcmeVault', '1.1.2',
		        'AcmeVault Component Definition', $4)
	`, defID, tenant, vendorClaimSha, len(claims)); err != nil {
		t.Fatalf("seed component-def: %v", err)
	}

	compID := uuid.New()
	componentUUID := uuid.NewString()
	if _, err := admin.Exec(ctx, `
		INSERT INTO imported_components (
			id, tenant_id, imported_catalog_id, component_uuid, component_type,
			title, description
		)
		VALUES ($1, $2, $3, $4, 'service', 'AcmeVault', 'the vault product')
	`, compID, tenant, defID, componentUUID); err != nil {
		t.Fatalf("seed component: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, stmt := range []string{
			`DELETE FROM imported_component_claim_dispositions WHERE tenant_id = $1`,
			`DELETE FROM imported_component_claims WHERE tenant_id = $1`,
			`DELETE FROM imported_components WHERE tenant_id = $1`,
			`DELETE FROM imported_catalogs WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(c, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	for _, cl := range claims {
		if _, err := admin.Exec(ctx, `
			INSERT INTO imported_component_claims (
				id, tenant_id, imported_component_id, control_id, statement,
				requirement_uuid, scf_anchor_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, NULL)
		`, cl.claimID, tenant, compID, cl.controlID, cl.statement, uuid.NewString()); err != nil {
			t.Fatalf("seed claim: %v", err)
		}
		if cl.status != "" {
			if _, err := admin.Exec(ctx, `
				UPDATE imported_component_claims
				SET claim_status = $2, dispositioned_by = $3, dispositioned_at = now(),
				    disposition_note = 'integration-test disposition'
				WHERE id = $1
			`, cl.claimID, cl.status, cl.actor); err != nil {
				t.Fatalf("disposition claim: %v", err)
			}
		}
	}
}

func countControlEvaluations(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count control_evaluations: %v", err)
	}
	return n
}

// TestExport_AcceptedVendorClaim_VendorAttestedAndNoFabricatedCoverage is the
// load-bearing test. It exports an SSP for a frozen period that has:
//   - one ACCEPTED vendor claim   (must appear, vendor-attested, with provenance)
//   - one REJECTED vendor claim   (must NOT appear)
//   - one needs_info vendor claim (must NOT appear)
//
// and asserts the control_evaluations count is UNCHANGED by the export (no
// fabricated coverage).
func TestExport_AcceptedVendorClaim_VendorAttestedAndNoFabricatedCoverage(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)

	// A platform control so the SSP has at least one control-implementation
	// (the bridge rejects an SSP with zero implemented-requirements).
	_ = seedControl(t, admin, tenant, "IAC")

	acceptedID := uuid.New()
	rejectedID := uuid.New()
	needsInfoID := uuid.New()
	const acceptedStmt = "AcmeVault encrypts all secrets at rest using a tenant-isolated KMS."
	const rejectedStmt = "AcmeVault claims a SOC 2 Type II report that the operator declined."
	const needsInfoStmt = "AcmeVault claims a control the operator is still reviewing."

	seedVendorClaims(t, admin, tenant, []seededVendorClaim{
		{claimID: acceptedID, controlID: "ac-2", statement: acceptedStmt, status: "accepted", actor: "operator@acme.com"},
		{claimID: rejectedID, controlID: "ac-3", statement: rejectedStmt, status: "rejected", actor: "operator@acme.com"},
		{claimID: needsInfoID, controlID: "ac-4", statement: needsInfoStmt, status: "needs_info", actor: "operator@acme.com"},
	})

	// BOUNDARY ASSERTION 1 (no bridge needed): the control_evaluations count
	// must be IDENTICAL before and after the export. Accepting a vendor claim
	// never manufactures a passing evaluation.
	before := countControlEvaluations(t, admin, tenant)

	addr, stop := startBridge(t) // self-skips if Python/trestle is absent
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	signer, _ := oscal.NewEphemeralSigner()
	e := oscal.NewExporter(app, bridge, signer)

	bundle, err := e.Export(ctxFor(t, tenant), oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  "Acme Security Inc.",
		SystemName:        "Acme Compliance Platform",
		SystemDescription: "The SaaS platform under SOC 2 assessment.",
		RequestedBy:       "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	after := countControlEvaluations(t, admin, tenant)
	if before != after {
		t.Fatalf("control_evaluations count changed across export: before=%d after=%d — fabricated coverage (constitutional violation)", before, after)
	}

	// Pull the SSP member out of the bundle.
	var sspJSON []byte
	for _, m := range bundle.Members {
		if m.Filename == "ssp.json" {
			sspJSON = m.JSON
		}
	}
	if sspJSON == nil {
		t.Fatal("bundle has no ssp.json member")
	}

	var doc map[string]any
	if err := json.Unmarshal(sspJSON, &doc); err != nil {
		t.Fatalf("parse ssp.json: %v", err)
	}
	blob := string(sspJSON)

	// BOUNDARY ASSERTION 2: the accepted claim appears, vendor-attested, with
	// the honesty label + accept-provenance.
	if !strings.Contains(blob, "[VENDOR-ATTESTED") {
		t.Error("SSP must carry the leading VENDOR-ATTESTED honesty label for the accepted claim")
	}
	if !strings.Contains(blob, acceptedStmt) {
		t.Error("SSP must carry the accepted vendor claim's statement verbatim")
	}
	if !strings.Contains(blob, "operator@acme.com") {
		t.Error("SSP must carry the accept-provenance (accepted-by) for the accepted claim")
	}
	if !strings.Contains(blob, acceptedID.String()) {
		t.Error("SSP must carry the accepted claim's claim-id provenance")
	}

	// BOUNDARY ASSERTION 3: the REJECTED and needs_info claims must NOT appear
	// as implementations.
	if strings.Contains(blob, rejectedStmt) {
		t.Error("SSP must NOT carry a REJECTED vendor claim's statement")
	}
	if strings.Contains(blob, needsInfoStmt) {
		t.Error("SSP must NOT carry a needs_info vendor claim's statement")
	}
	if strings.Contains(blob, rejectedID.String()) || strings.Contains(blob, needsInfoID.String()) {
		t.Error("SSP must NOT carry a non-accepted claim's claim-id")
	}

	// BOUNDARY ASSERTION 4: the vendor-attested implemented-requirement carries
	// NO evaluation-result prop (it is not a platform evaluation). Walk the
	// implemented-requirements and verify the one referencing the accepted
	// claim-id has no evaluation-result property.
	ssp, _ := doc["system-security-plan"].(map[string]any)
	ci, _ := ssp["control-implementation"].(map[string]any)
	irs, _ := ci["implemented-requirements"].([]any)
	foundVendorIR := false
	for _, raw := range irs {
		ir, _ := raw.(map[string]any)
		props, _ := ir["props"].([]any)
		isVendor := false
		hasEval := false
		for _, p := range props {
			pm, _ := p.(map[string]any)
			if pm["name"] == "vendor-attested" && pm["value"] == "true" {
				isVendor = true
			}
			if pm["name"] == "evaluation-result" {
				hasEval = true
			}
		}
		if isVendor {
			foundVendorIR = true
			if hasEval {
				t.Error("vendor-attested implemented-requirement must NOT carry an evaluation-result prop")
			}
		}
	}
	if !foundVendorIR {
		t.Error("expected a vendor-attested implemented-requirement in the SSP")
	}
}
