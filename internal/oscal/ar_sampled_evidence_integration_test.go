//go:build integration

// Integration tests for slice 494: the Assessment-Results artifact carries
// (1) the DRAWN sample evidence ids per population and (2) walkthrough
// attachment references.
//
// These run against a REAL Postgres (RLS enforced via the app role). The
// load-bearing correctness assertions (AC-6 / AC-7 / AC-8 / AC-9) run WITHOUT
// the Python oscal-bridge: the capturing fake BridgeClient (captureBridge,
// defined in ssp_narrative_integration_test.go) serializes the assessment
// proto input with protojson, so the sampled ids + attachment refs are
// observable in the produced JSON. The leak / freeze surfaces are the
// tenant-scoped, frozen-horizon DB reads, which run unchanged whether the
// bridge is real or fake — so the guarantees are proven on EVERY CI run, not
// only when the Python bridge happens to be installed (slice-493 D-test
// pattern; slice-494 decision D1).
//
// A separate bridge-gated test (TestAR_RealBridgeRoundTrip) exercises the
// real compliance-trestle round trip for full wire fidelity; it skips when
// the bridge is unavailable (mirrors slice 030 / 493 D2).
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...
//
// Required env: DATABASE_URL (migration role), DATABASE_URL_APP (app role).

package oscal_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/sample"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

// registerSampleCleanup deletes the slice-026/027 child tables that
// freshTenant's cleanup does not (and cannot, because the FKs are ON DELETE
// RESTRICT). t.Cleanup is LIFO, so registering this AFTER freshTenant makes
// it run BEFORE the shared cleanup — the populations/walkthroughs deletes in
// freshTenant then succeed.
func registerSampleCleanup(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM sample_evidence WHERE tenant_id = $1`,
			`DELETE FROM sample_annotations WHERE tenant_id = $1`,
			`DELETE FROM samples WHERE tenant_id = $1`,
			`DELETE FROM walkthrough_attachments WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("sample cleanup %s: %v", stmt, err)
			}
		}
	})
}

// seedEvidenceRecord inserts one evidence_record for a control with the given
// observed_at. Returns the id. The minimal NOT-NULL columns are filled.
func seedEvidenceRecord(t *testing.T, admin *pgxpool.Pool, tenant string, control uuid.UUID, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO evidence_records
		   (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
		 VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, 'pass', $6)`,
		id, tenant, control, control.String(), observedAt, "hash-"+id.String()); err != nil {
		t.Fatalf("seed evidence_record: %v", err)
	}
	return id
}

// seedPopulationWithDraw seeds a population pinned to the period, a sample
// over it (with the given seed + n), and the realized sample_evidence rows in
// the exact deterministic order sample.Sample produces. Returns the
// population id and the drawn ids (in shuffle order) so the test can assert
// the AR carries the same set/order. This mirrors what the slice-026 handler
// does at draw-time: it materializes the frozen-population draw.
func seedPopulationWithDraw(
	t *testing.T, admin *pgxpool.Pool, tenant string, control, period uuid.UUID,
	frozenAt time.Time, frozenPopulation []uuid.UUID, n int, seed string,
) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	popID := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO populations
		   (id, tenant_id, control_id, scope_predicate, time_window_start,
		    time_window_end, frozen_at, row_count, created_by, audit_period_id)
		 VALUES ($1, $2, $3, '{}'::jsonb, $4, $5, $6, $7, 'tester', $8)`,
		popID, tenant, control,
		frozenAt.Add(-90*24*time.Hour), frozenAt, frozenAt,
		int64(len(frozenPopulation)), period); err != nil {
		t.Fatalf("seed population: %v", err)
	}

	// The realized draw: run the SAME deterministic sampler the handler ran.
	drawn, err := sample.Sample(frozenPopulation, n, seed)
	if err != nil {
		t.Fatalf("sample.Sample: %v", err)
	}

	sampleID := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO samples (id, tenant_id, population_id, n, seed, created_by)
		 VALUES ($1, $2, $3, $4, $5, 'tester')`,
		sampleID, tenant, popID, n, seed); err != nil {
		t.Fatalf("seed sample: %v", err)
	}
	for ordinal, evID := range drawn {
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO sample_evidence (sample_id, tenant_id, evidence_record_id, ordinal)
			 VALUES ($1, $2, $3, $4)`,
			sampleID, tenant, evID, ordinal); err != nil {
			t.Fatalf("seed sample_evidence: %v", err)
		}
	}
	return popID, drawn
}

// seedWalkthroughWithAttachments seeds a walkthrough pinned to the period plus
// the given number of attachments. Returns the walkthrough id and the
// attachment storage keys / hashes seeded.
func seedWalkthroughWithAttachments(
	t *testing.T, admin *pgxpool.Pool, tenant string, control, period uuid.UUID, nAttachments int,
) (uuid.UUID, []string) {
	t.Helper()
	wtID := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO walkthroughs
		   (id, tenant_id, audit_period_id, control_id, narrative, canonical_hash, status, created_by)
		 VALUES ($1, $2, $3, $4, 'Walkthrough of the control.', decode(repeat('00',32),'hex'), 'finalized', 'tester')`,
		wtID, tenant, period, control); err != nil {
		t.Fatalf("seed walkthrough: %v", err)
	}
	hashes := make([]string, 0, nAttachments)
	for i := 0; i < nAttachments; i++ {
		attID := uuid.New()
		// 64-char lowercase hex sha256 (the schema CHECK enforces length 64).
		h := uuidToHex64(attID)
		storageKey := "tenant-" + tenant + "/" + attID.String()
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO walkthrough_attachments
			   (id, tenant_id, walkthrough_id, storage_key, content_type, size_bytes,
			    sha256_hash, annotations, uploaded_by)
			 VALUES ($1, $2, $3, $4, 'image/png', 1024, $5, $6, 'tester')`,
			attID, tenant, wtID, storageKey, h,
			[]byte(`{"regions":[{"x":1,"y":2,"w":3,"h":4,"label":"finding"}]}`)); err != nil {
			t.Fatalf("seed walkthrough_attachment: %v", err)
		}
		hashes = append(hashes, h)
	}
	return wtID, hashes
}

// uuidToHex64 expands a uuid into a deterministic 64-char lowercase hex
// string (a stand-in sha256) for attachment fixtures.
func uuidToHex64(u uuid.UUID) string {
	const hexdigits = "0123456789abcdef"
	b := u[:] // 16 bytes -> 32 hex chars; duplicate to reach 64.
	out := make([]byte, 0, 64)
	for range 2 {
		for _, by := range b {
			out = append(out, hexdigits[by>>4], hexdigits[by&0x0f])
		}
	}
	return string(out)
}

// stablePopulation returns n evidence_record ids in id-sorted order — the
// same order ListPopulationEvidenceIDs (ORDER BY id) yields, so sample.Sample
// produces the same draw the handler would.
func stablePopulation(ids []uuid.UUID) []uuid.UUID {
	sorted := make([]uuid.UUID, len(ids))
	copy(sorted, ids)
	// simple insertion sort by byte order (small n in tests)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && bytes.Compare(sorted[j-1][:], sorted[j][:]) > 0; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	return sorted
}

// exportARJSONFake runs a full export through the real DB read path with the
// capturing fake bridge, returning the ar.json member bytes. No Python
// process required — runs on every CI integration shard.
func exportARJSONFake(t *testing.T, app *pgxpool.Pool, tenant string, periodID uuid.UUID) []byte {
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
		if m.Filename == "assessment-results.json" {
			return m.JSON
		}
	}
	t.Fatal("bundle has no assessment-results.json member")
	return nil
}

// AC-6: an AR exported for a frozen period carries the expected drawn sample
// ids (reproducible) and walkthrough attachment references.
func TestAR_CarriesDrawnSampleAndAttachmentRefs(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenant)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	control := seedControl(t, admin, tenant, "IAC")

	// 10 frozen-population evidence records, observed BEFORE frozen_at.
	var pop []uuid.UUID
	for i := 0; i < 10; i++ {
		pop = append(pop, seedEvidenceRecord(t, admin, tenant, control,
			frozenAt.Add(-time.Duration(i+1)*time.Hour)))
	}
	sortedPop := stablePopulation(pop)
	_, drawn := seedPopulationWithDraw(t, admin, tenant, control, periodID, frozenAt, sortedPop, 4, "soc2-2026q2-seed")

	wtID, _ := seedWalkthroughWithAttachments(t, admin, tenant, control, periodID, 3)

	arJSON := exportARJSONFake(t, app, tenant, periodID)

	// AC-1: every drawn id appears in the AR's sampled_evidence_ids.
	for _, id := range drawn {
		if !bytes.Contains(arJSON, []byte(id.String())) {
			t.Errorf("AR JSON missing drawn sample id %s.\ngot: %s", id, arJSON)
		}
	}
	// AC-4: the walkthrough id and its attachment metadata are present.
	if !bytes.Contains(arJSON, []byte(wtID.String())) {
		t.Errorf("AR JSON missing walkthrough id %s", wtID)
	}
	if !bytes.Contains(arJSON, []byte("image/png")) {
		t.Errorf("AR JSON missing attachment content type.\ngot: %s", arJSON)
	}
	// AC-5: a storage URI is referenced (hash + URI), not embedded bytes.
	if !bytes.Contains(arJSON, []byte("tenant-"+tenant+"/")) {
		t.Errorf("AR JSON missing attachment storage URI reference.\ngot: %s", arJSON)
	}
}

// AC-7 (invariant #10, P0-494-1): freeze a period, add a post-frozen_at
// evidence record, export — the post-freeze record NEVER appears in the AR's
// sampled_evidence_ids. The draw is over the persisted frozen draw; a record
// observed after frozen_at was never in that draw.
func TestAR_FreezeIntegrity_PostFreezeEvidenceNeverSampled(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenant)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true /* frozen */)
	control := seedControl(t, admin, tenant, "IAC")

	// The frozen population: observed before frozen_at.
	var pop []uuid.UUID
	for i := 0; i < 8; i++ {
		pop = append(pop, seedEvidenceRecord(t, admin, tenant, control,
			frozenAt.Add(-time.Duration(i+1)*time.Hour)))
	}
	sortedPop := stablePopulation(pop)
	seedPopulationWithDraw(t, admin, tenant, control, periodID, frozenAt, sortedPop, 5, "freeze-seed")

	// The tamper attempt: a record observed AFTER frozen_at, inserted after
	// the freeze. It is NOT in the persisted sample_evidence draw.
	postFreeze := seedEvidenceRecord(t, admin, tenant, control, frozenAt.Add(48*time.Hour))

	arJSON := exportARJSONFake(t, app, tenant, periodID)

	if bytes.Contains(arJSON, []byte(postFreeze.String())) {
		t.Fatalf("INVARIANT #10 VIOLATION: post-frozen_at evidence %s appeared in the AR's sampled set (P0-494-1).\ngot: %s",
			postFreeze, arJSON)
	}
}

// AC-8 (threat-model I, P0-494-3): Tenant A's sampled evidence / attachment
// references never appear in Tenant B's AR. The leak surface is the
// tenant-scoped DB read; runs through the real read path (fake bridge).
func TestAR_TenantIsolation_SampledEvidenceAndAttachmentsDoNotLeak(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenantA := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenantA)
	tenantB := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenantB)
	fwVersion := seedFrameworkVersion(t, admin)

	// Tenant A: secret sampled evidence + a walkthrough attachment.
	periodA, frozenA := seedPeriod(t, admin, tenantA, fwVersion, true)
	controlA := seedControl(t, admin, tenantA, "IAC")
	var popA []uuid.UUID
	for i := 0; i < 6; i++ {
		popA = append(popA, seedEvidenceRecord(t, admin, tenantA, controlA,
			frozenA.Add(-time.Duration(i+1)*time.Hour)))
	}
	_, drawnA := seedPopulationWithDraw(t, admin, tenantA, controlA, periodA, frozenA, stablePopulation(popA), 3, "tenant-a-seed")
	_, hashesA := seedWalkthroughWithAttachments(t, admin, tenantA, controlA, periodA, 2)

	// Tenant B: its own period (the AR we export).
	periodB, frozenB := seedPeriod(t, admin, tenantB, fwVersion, true)
	controlB := seedControl(t, admin, tenantB, "IAC")
	var popB []uuid.UUID
	for i := 0; i < 4; i++ {
		popB = append(popB, seedEvidenceRecord(t, admin, tenantB, controlB,
			frozenB.Add(-time.Duration(i+1)*time.Hour)))
	}
	_, drawnB := seedPopulationWithDraw(t, admin, tenantB, controlB, periodB, frozenB, stablePopulation(popB), 2, "tenant-b-seed")

	arJSON := exportARJSONFake(t, app, tenantB, periodB)

	// Tenant A's drawn sample ids must NOT appear in Tenant B's AR.
	for _, id := range drawnA {
		if bytes.Contains(arJSON, []byte(id.String())) {
			t.Fatalf("CROSS-TENANT LEAK: Tenant A's sampled evidence %s appeared in Tenant B's AR (P0-494-3).\ngot: %s", id, arJSON)
		}
	}
	// Tenant A's attachment hashes must NOT appear.
	for _, h := range hashesA {
		if bytes.Contains(arJSON, []byte(h)) {
			t.Fatalf("CROSS-TENANT LEAK: Tenant A's attachment hash %s appeared in Tenant B's AR (P0-494-3).\ngot: %s", h, arJSON)
		}
	}
	// Tenant B's own drawn ids SHOULD appear (sanity: the read works).
	for _, id := range drawnB {
		if !bytes.Contains(arJSON, []byte(id.String())) {
			t.Errorf("Tenant B's own sampled evidence %s should appear in its AR.\ngot: %s", id, arJSON)
		}
	}
}

// AC-9 (threat-model R): re-running the sampler with the persisted seed
// against the frozen population yields the same drawn set the AR carries.
func TestAR_Reproducibility_PersistedSeedReproducesARDraw(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenant)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true)
	control := seedControl(t, admin, tenant, "IAC")

	var pop []uuid.UUID
	for i := 0; i < 12; i++ {
		pop = append(pop, seedEvidenceRecord(t, admin, tenant, control,
			frozenAt.Add(-time.Duration(i+1)*time.Hour)))
	}
	sortedPop := stablePopulation(pop)
	const seed = "reproduce-2026q2"
	const n = 5
	_, drawn := seedPopulationWithDraw(t, admin, tenant, control, periodID, frozenAt, sortedPop, n, seed)

	// Re-run the sampler INDEPENDENTLY with the persisted seed against the
	// frozen population — must reproduce the exact AR draw (set + order).
	reproduced, err := sample.Sample(sortedPop, n, seed)
	if err != nil {
		t.Fatalf("re-run Sample: %v", err)
	}
	if len(reproduced) != len(drawn) {
		t.Fatalf("reproduced length %d != AR draw length %d", len(reproduced), len(drawn))
	}
	for i := range drawn {
		if reproduced[i] != drawn[i] {
			t.Fatalf("reproduced draw diverges at %d: %v vs %v", i, reproduced[i], drawn[i])
		}
	}

	// And the AR carries exactly that reproduced set.
	arJSON := exportARJSONFake(t, app, tenant, periodID)
	for _, id := range reproduced {
		if !bytes.Contains(arJSON, []byte(id.String())) {
			t.Errorf("AR draw is not reproducible from the persisted seed: %s missing.\ngot: %s", id, arJSON)
		}
	}
}

// D3: a walkthrough with more attachments than the cap carries the capped
// prefix plus an honest overflow note — evidence is never silently dropped.
func TestAR_AttachmentCap_OverflowNotePresent(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenant)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, _ := seedPeriod(t, admin, tenant, fwVersion, true)
	control := seedControl(t, admin, tenant, "IAC")

	// 52 attachments > cap of 50 -> 2 overflow.
	seedWalkthroughWithAttachments(t, admin, tenant, control, periodID, 52)

	arJSON := exportARJSONFake(t, app, tenant, periodID)

	if !bytes.Contains(arJSON, []byte("additional attachment(s) not shown")) {
		t.Errorf("AR JSON missing the attachment-cap overflow note (D3).\ngot: %s", arJSON)
	}
}

// TestAR_RealBridgeRoundTrip exercises the FULL pipeline against the real
// Python oscal-bridge: the drawn sample ids + attachment refs survive
// compliance-trestle serialization + round-trip validation into canonical
// OSCAL JSON (relevant-evidence). Skips when the bridge is unavailable. The
// fake-bridge tests above prove the read-path correctness; this proves wire
// fidelity (decision D2).
func TestAR_RealBridgeRoundTrip(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	registerSampleCleanup(t, admin, tenant)
	fwVersion := seedFrameworkVersion(t, admin)
	periodID, frozenAt := seedPeriod(t, admin, tenant, fwVersion, true)
	control := seedControl(t, admin, tenant, "IAC")

	var pop []uuid.UUID
	for i := 0; i < 6; i++ {
		pop = append(pop, seedEvidenceRecord(t, admin, tenant, control,
			frozenAt.Add(-time.Duration(i+1)*time.Hour)))
	}
	_, drawn := seedPopulationWithDraw(t, admin, tenant, control, periodID, frozenAt, stablePopulation(pop), 3, "bridge-seed")
	seedWalkthroughWithAttachments(t, admin, tenant, control, periodID, 2)

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
	var arJSON []byte
	for _, m := range bundle.Members {
		if m.Filename == "assessment-results.json" {
			arJSON = m.JSON
		}
	}
	for _, id := range drawn {
		if !bytes.Contains(arJSON, []byte(id.String())) {
			t.Errorf("real-bridge AR JSON dropped drawn sample id %s.\ngot: %s", id, arJSON)
		}
	}
	if !bytes.Contains(arJSON, []byte("relevant-evidence")) {
		t.Errorf("real-bridge AR JSON has no relevant-evidence block for the walkthrough attachments.\ngot: %s", arJSON)
	}
}
