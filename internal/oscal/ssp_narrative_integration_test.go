//go:build integration

// Integration tests for slice 493: the SSP carries the control bundle's
// authored implementation narrative (not the synthesized placeholder).
//
// These run against a REAL Postgres (RLS enforced via the app role). The
// load-bearing content + tenant-isolation assertions (AC-6 / AC-7 / AC-8)
// run WITHOUT the Python oscal-bridge: a capturing fake BridgeClient
// serializes the SSP proto input with protojson, so the statement text is
// observable in the produced JSON. The leak surface (P0-493-2) is the
// tenant-scoped DB read, which runs unchanged whether the bridge is real
// or fake — so the isolation guarantee is proven on every CI run, not only
// when the Python bridge happens to be installed (decision D-test).
//
// A separate bridge-gated test (TestSSP_RealBridgeRoundTrip) exercises the
// real compliance-trestle round trip for full fidelity; it skips when the
// bridge is unavailable (decision D2, mirrors slice 030).
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...
//
// Required env: DATABASE_URL (migration role), DATABASE_URL_APP (app role).

package oscal_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

// captureBridge is a BridgeClient that serializes each proto input with
// protojson and records the SSP JSON. It needs no Python process, so the
// full DB read path (the tenant-isolation surface) runs in CI. Round-trip
// validation always succeeds — structural OSCAL conformance is the real
// bridge's job and is covered by TestSSP_RealBridgeRoundTrip.
type captureBridge struct {
	sspJSON []byte
}

func (b *captureBridge) SerializeSSP(_ context.Context, in *oscalv1.SspInput) ([]byte, error) {
	out, err := protojson.Marshal(in)
	if err != nil {
		return nil, err
	}
	b.sspJSON = out
	return out, nil
}

func (b *captureBridge) SerializeAssessment(_ context.Context, in *oscalv1.AssessmentInput) ([]byte, []byte, error) {
	out, err := protojson.Marshal(in)
	return out, out, err
}

func (b *captureBridge) SerializePOAM(_ context.Context, in *oscalv1.PoamInput) ([]byte, error) {
	return protojson.Marshal(in)
}

func (b *captureBridge) RoundTripValidate(_ context.Context, _ string, _ []byte) (bool, []string, error) {
	return true, nil, nil
}

func (b *captureBridge) ImportCatalog(_ context.Context, _ []byte, _ string) (*oscalv1.ImportCatalogResponse, error) {
	return &oscalv1.ImportCatalogResponse{}, nil
}

func (b *captureBridge) Close() error { return nil }

// seedControlWithDescription seeds a control with an explicit authored
// description (the bundle narrative slice 009 carries). Returns the id.
func seedControlWithDescription(t *testing.T, admin *pgxpool.Pool, tenant, family, description string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO controls (id, tenant_id, title, description, control_family, implementation_type, bundle_id, owner_role)
		 VALUES ($1, $2, $3, $4, $5, 'manual_attested', $6, 'control_owner')`,
		id, tenant, "Slice 493 control "+family, description, family, "bundle-493-"+family); err != nil {
		t.Fatalf("seed control with description: %v", err)
	}
	return id
}

// exportSSPJSONFake runs a full export through the real DB read path with a
// capturing fake bridge, returning the ssp.json member bytes. No Python
// process required — runs on every CI integration shard.
func exportSSPJSONFake(t *testing.T, app *pgxpool.Pool, tenant string, periodID uuid.UUID) []byte {
	t.Helper()
	bridge := &captureBridge{}
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
	for _, m := range bundle.Members {
		if m.Filename == "ssp.json" {
			return m.JSON
		}
	}
	t.Fatal("bundle has no ssp.json member")
	return nil
}

// AC-6: an SSP exported for a tenant with authored control descriptions
// carries those descriptions verbatim as the implementation statements.
func TestSSP_CarriesAuthoredDescriptionVerbatim(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)

	const authored = "All customer data at rest is encrypted using AES-256 via KMS-managed keys; key rotation is enforced every 90 days by an automated job."
	seedControlWithDescription(t, admin, tenant, "IAC", authored)

	sspJSON := exportSSPJSONFake(t, app, tenant, periodID)

	if !bytes.Contains(sspJSON, []byte(authored)) {
		t.Errorf("SSP JSON does not carry the authored description verbatim.\nwant substring: %q\ngot: %s", authored, sspJSON)
	}
	// AC-2/AC-4: the synthesized placeholder boilerplate MUST NOT appear for
	// a control that has an authored description (wrong-branch / double-fill
	// regression).
	if bytes.Contains(sspJSON, []byte("Auto-generated summary")) {
		t.Error("SSP JSON contains the auto-generated fallback label for a control that HAS an authored description")
	}
}

// AC-7: a control with no authored description falls back to the
// clearly-labeled synthesized summary — never an empty statement, no panic.
func TestSSP_NoDescriptionFallsBackToLabeledSummary(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)

	// seedControl (from integration_test.go) inserts NO description -> the
	// description column defaults to '' -> fallback path (AC-3).
	seedControl(t, admin, tenant, "IAM")

	sspJSON := exportSSPJSONFake(t, app, tenant, periodID)

	// P0-493-1: never empty. The fallback label must be present and the
	// statement must include the title so the auditor can still identify
	// the control.
	if !bytes.Contains(sspJSON, []byte("Auto-generated summary")) {
		t.Errorf("SSP JSON for a description-less control must carry the labeled fallback (AC-3).\ngot: %s", sspJSON)
	}
	// seedControl titles its control "Slice 030 control <family>".
	if !bytes.Contains(sspJSON, []byte("Slice 030 control IAM")) {
		t.Errorf("fallback statement should include the control title.\ngot: %s", sspJSON)
	}
}

// AC-8 (threat-model I, P0-493-2): Tenant A's control descriptions never
// appear in Tenant B's SSP. The leak surface is the tenant-scoped read;
// this runs through the real DB read path (capturing fake bridge), so the
// guarantee is proven on every CI run.
func TestSSP_TenantIsolation_DescriptionDoesNotLeak(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)

	const secretA = "TENANT-A-CONFIDENTIAL break-glass procedure routes through bastion 10.0.0.7 with hardware-token MFA."
	seedControlWithDescription(t, admin, tenantA, "IAC", secretA)
	const narrativeB = "Tenant B encrypts at rest with provider-managed keys."
	seedControlWithDescription(t, admin, tenantB, "IAC", narrativeB)

	periodB, _ := seedPeriod(t, admin, tenantB, fwVersion, true /* frozen */)

	sspJSON := exportSSPJSONFake(t, app, tenantB, periodB)

	if bytes.Contains(sspJSON, []byte(secretA)) {
		t.Fatalf("CROSS-TENANT LEAK: Tenant A's control description appeared in Tenant B's SSP (P0-493-2).\ngot: %s", sspJSON)
	}
	if !bytes.Contains(sspJSON, []byte(narrativeB)) {
		t.Errorf("Tenant B's own authored narrative should be present in its SSP.\ngot: %s", sspJSON)
	}
}

// TestSSP_RealBridgeRoundTrip exercises the FULL pipeline against the real
// Python oscal-bridge: the authored description survives compliance-trestle
// serialization + round-trip validation into canonical OSCAL JSON. Skips
// when the bridge is unavailable (decision D2). The fake-bridge tests above
// already prove the read-path correctness; this proves the wire fidelity.
func TestSSP_RealBridgeRoundTrip(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)

	const authored = "Access is reviewed quarterly by the GRC team against the HR system of record; deviations are remediated within five business days."
	seedControlWithDescription(t, admin, tenant, "IAM", authored)

	addr, stop := startBridge(t) // skips if the bridge is unavailable
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()

	signer, _ := oscal.NewEphemeralSigner()
	e := oscal.NewExporter(app, bridge, signer)
	bundle, err := e.Export(ctxFor(t, tenant), oscal.ExportInput{
		AuditPeriodID:    periodID,
		OrganizationName: "Acme Security Inc.",
		SystemName:       "Acme Compliance Platform",
		RequestedBy:      "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var sspJSON []byte
	for _, m := range bundle.Members {
		if m.Filename == "ssp.json" {
			sspJSON = m.JSON
		}
	}
	if !bytes.Contains(sspJSON, []byte(authored)) {
		t.Errorf("real-bridge SSP JSON dropped the authored description.\nwant substring: %q\ngot: %s", authored, sspJSON)
	}
}
