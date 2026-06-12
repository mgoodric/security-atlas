// Slice 689 + 690 — contract-test-tier rollout to the audit-workspace
// READ routes served by this package:
//
//	GET /v1/populations/{id}             -> population-get.golden.json       (689)
//	GET /v1/samples/{id}                 -> sample-get.golden.json           (689)
//	GET /v1/samples/{id}/annotations     -> sample-annotations.golden.json   (690)
//
// The first two are verbatim-passthrough BFF reads (toEqual consumers). The
// annotation-list read (690) has NO verbatim-passthrough GET BFF today — it
// is read by the sample-detail component, not a thin passthrough — so its
// consumer half is a FIELD-CONTRACT pin on the recorded provider golden
// (slice 687 D3 disposition). See decisions log 690 D1/D2.
//
// This is the slice-392 / slice-409 / slice-410 / slice-411 / slice-412 /
// slice-687 shared-recorder helper copied into this package because Go test
// files cannot cross a package boundary (the same reason the sibling copies
// exist in internal/api/controldetail, internal/api/auditperiods,
// internal/api/ucfcoverage, internal/api/risks, internal/api/dashboard, …).
//
// Pattern (ADR-0007 option 1, slice-411 per-route read seam):
//
//	provider test:  construct the real Handler over an injected fixed-row
//	                two-method sampleReader stub (no pgx pool) -> route the GET
//	                through a chi mux so chi.URLParam(r, "id") resolves -> drive
//	                the real GetPopulation/GetSample handler -> canonicalize the
//	                body -> diff against the committed golden under
//	                web/lib/contracts/.
//	consumer test:  read the same golden -> assert the BFF passthrough holds
//	                against the recorded upstream truth. Both BFFs
//	                (web/app/api/audit/{populations,samples}/[id]/route.ts) are
//	                VERBATIM passthroughs (forwardJSON of the single-resource
//	                read), so the consumer assert is toEqual(golden).
//
// Regenerate the goldens after an intentional shape change:
//
//	go test ./internal/api/audit/ -run TestContract -update

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	atlasaudit "github.com/mgoodric/security-atlas/internal/audit"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID     = "00000000-0000-4000-8000-000000000689"
	contractControlID    = "11111111-1111-4111-8111-111111111111"
	contractPopulationID = "22222222-2222-4222-8222-222222222222"
	contractSampleID     = "33333333-3333-4333-8333-333333333333"
	contractEvidenceID   = "44444444-4444-4444-8444-444444444444"
)

// contractUpdateFlag is registered lazily so it composes with whatever flag
// set the surrounding `go test` invocation uses without a duplicate-flag panic
// (mirrors slice 392/409/410/411/412/687's lazy lookup).
var contractUpdateFlag = func() *bool {
	if f := flag.Lookup("update"); f != nil {
		if gv, ok := f.Value.(flag.Getter); ok {
			if b, ok := gv.Get().(bool); ok {
				return &b
			}
		}
		return nil
	}
	return flag.Bool("update", false, "rewrite contract golden files")
}()

// contractGolden mirrors the committed golden JSON. The variant keys are stable
// contract identifiers shared verbatim with the consumer test.
type contractGolden struct {
	Comment  string                     `json:"_comment"`
	Endpoint string                     `json:"endpoint"`
	Variants map[string]json.RawMessage `json:"variants"`
}

// canonicalizeJSON re-marshals a body through a generic value so the golden is
// byte-stable regardless of struct/map field order.
func canonicalizeJSON(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("canonicalize: decode body: %v; body=%q", err, raw)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("canonicalize: re-marshal: %v", err)
	}
	return out
}

// assertContractGolden is the shared compare-or-update core.
func assertContractGolden(t *testing.T, path, comment, endpoint string, recorded map[string]json.RawMessage) {
	t.Helper()

	if contractUpdateFlag != nil && *contractUpdateFlag {
		out := contractGolden{Comment: comment, Endpoint: endpoint, Variants: recorded}
		buf, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		buf = append(buf, '\n')
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated contract golden at %s", path)
		return
	}

	rawGolden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to regenerate)", path, err)
	}
	var golden contractGolden
	if err := json.Unmarshal(rawGolden, &golden); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}
	if golden.Endpoint != endpoint {
		t.Errorf("golden endpoint = %q; recorder endpoint = %q (run -update)", golden.Endpoint, endpoint)
	}
	for name, gotBody := range recorded {
		wantRaw, ok := golden.Variants[name]
		if !ok {
			t.Errorf("variant %q present in handler output but missing from golden; run -update", name)
			continue
		}
		wantCanon := canonicalizeJSON(t, wantRaw)
		if !bytes.Equal(gotBody, wantCanon) {
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/api/audit/ -run TestContract -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q present in golden but missing from handler output; run -update", name)
		}
	}
}

// stubSampleReader is the fixed-row implementation of the three-method
// sampleReader seam. It returns deterministic rows with no Postgres.
type stubSampleReader struct {
	population  atlasaudit.Population
	sample      atlasaudit.Sample
	annotations []atlasaudit.Annotation
}

func (s stubSampleReader) GetPopulation(_ context.Context, _ uuid.UUID) (atlasaudit.Population, error) {
	return s.population, nil
}

func (s stubSampleReader) GetSample(_ context.Context, _ uuid.UUID) (atlasaudit.Sample, error) {
	return s.sample, nil
}

func (s stubSampleReader) ListAnnotations(_ context.Context, _ uuid.UUID) ([]atlasaudit.Annotation, error) {
	return s.annotations, nil
}

// recordRoutedVariant routes a GET through a chi router so chi.URLParam(r,
// "id") resolves, binds a tenant + credential (satisfying tenantContext), and
// canonicalizes the recorded body.
func recordRoutedVariant(t *testing.T, handler http.HandlerFunc, routePattern, target string) json.RawMessage {
	t.Helper()
	r := chi.NewRouter()
	r.Get(routePattern, handler)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_contract_689",
		TenantID: contractTenantID,
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

func mustTime(rfc3339 string) time.Time {
	tt, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic("contract fixture: bad timestamp " + rfc3339)
	}
	return tt
}

// ===== GET /v1/populations/{id} =====

func TestContract_PopulationGet(t *testing.T) {
	created := mustTime("2026-02-01T00:00:00Z")
	windowStart := mustTime("2026-01-01T00:00:00Z")
	windowEnd := mustTime("2026-01-31T00:00:00Z")
	frozenAt := mustTime("2026-02-02T12:00:00Z")

	// Variant "open" — frozen_at absent (omitempty), scope_predicate carries
	// an explicit predicate, row_count populated. Pins the live-population
	// wire shape.
	open := atlasaudit.Population{
		ID:              uuid.MustParse(contractPopulationID),
		TenantID:        uuid.MustParse(contractTenantID),
		ControlID:       uuid.MustParse(contractControlID),
		ScopePredicate:  json.RawMessage(`{"op":"eq","field":"env","value":"prod"}`),
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowEnd,
		RowCount:        42,
		CreatedBy:       "key_contract_689",
		CreatedAt:       created,
	}
	// Variant "frozen" — frozen_at present (pins the frozen-horizon wire
	// shape, invariant 10), AND an empty scope_predicate so the handler's
	// {"op":"true"} default-fill branch is recorded (populationWireFrom).
	frozen := atlasaudit.Population{
		ID:              uuid.MustParse(contractPopulationID),
		TenantID:        uuid.MustParse(contractTenantID),
		ControlID:       uuid.MustParse(contractControlID),
		ScopePredicate:  nil,
		TimeWindowStart: windowStart,
		TimeWindowEnd:   windowEnd,
		FrozenAt:        &frozenAt,
		RowCount:        42,
		CreatedBy:       "key_contract_689",
		CreatedAt:       created,
	}

	target := "/v1/populations/" + contractPopulationID
	recorded := map[string]json.RawMessage{
		"open": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{population: open}).GetPopulation,
			"/v1/populations/{id}", target),
		"frozen": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{population: frozen}).GetPopulation,
			"/v1/populations/{id}", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/population-get.golden.json"),
		"Slice 689 contract-tier golden. PROVIDER: internal/api/audit/contractrecord_test.go (GetPopulation, real handler over an injected fixed-row two-method sampleReader stub — Option A seam, no Postgres; routed through chi for the {id} path param). Regenerate: `go test ./internal/api/audit/ -run TestContract_PopulationGet -update`. CONSUMER: web/lib/contracts/population-get.contract.test.ts asserts the BFF at web/app/api/audit/populations/[id]/route.ts — VERBATIM passthrough (toEqual). frozen_at is omitempty (present only on a frozen population, invariant 10); scope_predicate is opaque JSON and defaults to {\"op\":\"true\"} when empty.",
		"GET /v1/populations/{id}",
		recorded,
	)
}

// ===== GET /v1/samples/{id} =====

func TestContract_SampleGet(t *testing.T) {
	created := mustTime("2026-02-03T09:15:00Z")

	// Variant "populated" — two realized evidence ids. Pins the sample row
	// shape (population_id / n / seed / evidence_record_ids[]) the audit
	// sampling surface reads. Variant "empty" — a sample with no realized
	// evidence: evidence_record_ids is [] (never null).
	populated := atlasaudit.Sample{
		ID:           uuid.MustParse(contractSampleID),
		TenantID:     uuid.MustParse(contractTenantID),
		PopulationID: uuid.MustParse(contractPopulationID),
		N:            2,
		Seed:         "contract-seed-689",
		CreatedBy:    "key_contract_689",
		CreatedAt:    created,
		EvidenceRecordIDs: []uuid.UUID{
			uuid.MustParse(contractEvidenceID),
			uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		},
	}
	empty := atlasaudit.Sample{
		ID:                uuid.MustParse(contractSampleID),
		TenantID:          uuid.MustParse(contractTenantID),
		PopulationID:      uuid.MustParse(contractPopulationID),
		N:                 0,
		Seed:              "contract-seed-689",
		CreatedBy:         "key_contract_689",
		CreatedAt:         created,
		EvidenceRecordIDs: []uuid.UUID{},
	}

	target := "/v1/samples/" + contractSampleID
	recorded := map[string]json.RawMessage{
		"populated": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{sample: populated}).GetSample,
			"/v1/samples/{id}", target),
		"empty": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{sample: empty}).GetSample,
			"/v1/samples/{id}", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/sample-get.golden.json"),
		"Slice 689 contract-tier golden. PROVIDER: internal/api/audit/contractrecord_test.go (GetSample, real handler over an injected fixed-row two-method sampleReader stub — Option A seam, no Postgres; routed through chi for the {id} path param). Regenerate: `go test ./internal/api/audit/ -run TestContract_SampleGet -update`. CONSUMER: web/lib/contracts/sample-get.contract.test.ts asserts the BFF at web/app/api/audit/samples/[id]/route.ts — VERBATIM passthrough (toEqual). evidence_record_ids is ALWAYS an array (never null); empty sample records [].",
		"GET /v1/samples/{id}",
		recorded,
	)
}

// ===== GET /v1/samples/{id}/annotations =====

func TestContract_SampleAnnotationsList(t *testing.T) {
	annotatedA := mustTime("2026-02-04T11:00:00Z")
	annotatedB := mustTime("2026-02-04T11:05:30Z")

	// Variant "populated" — two auditor decisions against two sampled
	// records. Pins the annotation row shape (result / annotated_by /
	// annotated_at / notes) plus the envelope (annotations[] + count).
	// One annotation carries a non-empty notes string, one is empty (notes
	// is NOT omitempty — it serializes as "" when absent).
	populated := []atlasaudit.Annotation{
		{
			ID:               uuid.MustParse(contractEvidenceID),
			TenantID:         uuid.MustParse(contractTenantID),
			SampleID:         uuid.MustParse(contractSampleID),
			EvidenceRecordID: uuid.MustParse("55555555-5555-4555-8555-555555555555"),
			Result:           "passed",
			AnnotatedBy:      "key_contract_690",
			AnnotatedAt:      annotatedA,
			Notes:            "Control owner confirmed the recert ran on schedule.",
		},
		{
			ID:               uuid.MustParse("66666666-6666-4666-8666-666666666666"),
			TenantID:         uuid.MustParse(contractTenantID),
			SampleID:         uuid.MustParse(contractSampleID),
			EvidenceRecordID: uuid.MustParse("77777777-7777-4777-8777-777777777777"),
			Result:           "failed",
			AnnotatedBy:      "key_contract_690",
			AnnotatedAt:      annotatedB,
			Notes:            "",
		},
	}
	// Variant "empty" — a sample with no annotations yet. annotations is []
	// (the handler builds make([]annotationWire, 0)), count is 0.
	empty := []atlasaudit.Annotation{}

	target := "/v1/samples/" + contractSampleID + "/annotations"
	recorded := map[string]json.RawMessage{
		"populated": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{annotations: populated}).ListAnnotations,
			"/v1/samples/{id}/annotations", target),
		"empty": recordRoutedVariant(t, newHandlerWithReader(stubSampleReader{annotations: empty}).ListAnnotations,
			"/v1/samples/{id}/annotations", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/sample-annotations.golden.json"),
		"Slice 690 contract-tier golden. PROVIDER: internal/api/audit/contractrecord_test.go (ListAnnotations, real handler over an injected fixed-row sampleReader stub — Option A seam, no Postgres; routed through chi for the {id} path param). Regenerate: `go test ./internal/api/audit/ -run TestContract_SampleAnnotationsList -update`. CONSUMER: web/lib/contracts/sample-annotations.contract.test.ts is a FIELD-CONTRACT pin (slice 687 D3) — there is NO verbatim-passthrough GET BFF for this list today (read by the sample-detail component, not a thin passthrough). The envelope is {annotations:[], count:N}; each row carries result (passed|failed|not-applicable), annotated_by, annotated_at (ISO-8601), and notes (NOT omitempty — \"\" when absent); annotations is ALWAYS an array (empty sample records []).",
		"GET /v1/samples/{id}/annotations",
		recorded,
	)
}
