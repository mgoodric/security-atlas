// Slice 689 + 690 — contract-test-tier rollout to the audit-workspace
// walkthrough READ routes served by this package:
//
//	GET /v1/walkthroughs/{id} -> walkthrough-get.golden.json   (689, toEqual)
//	GET /v1/walkthroughs      -> walkthroughs-list.golden.json (690, field-contract)
//
// The single-walkthrough read (689) has a verbatim-passthrough BFF (toEqual
// consumer). The list read (690) has NO GET BFF today — the list BFF
// (web/app/api/audit/walkthroughs/route.ts) is POST-only — so its consumer
// half is a FIELD-CONTRACT pin on the recorded provider golden (slice 687 D3).
// See decisions log 690 D1/D2.
//
// This is the slice-392 / slice-409 / slice-410 / slice-411 / slice-412 /
// slice-687 shared-recorder helper copied into this package because Go test
// files cannot cross a package boundary (the same reason the sibling copies
// exist in internal/api/audit, internal/api/auditnotes, internal/api/controldetail,
// internal/api/auditperiods, …).
//
// Pattern (ADR-0007 option 1, slice-411 per-route read seam):
//
//	provider test:  construct the real Handler over an injected fixed-row
//	                single-method walkthroughReader stub (no pgx pool) -> route
//	                the GET through a chi mux so chi.URLParam(r, "id") resolves
//	                -> drive the real Get handler -> canonicalize the body ->
//	                diff against the committed golden under web/lib/contracts/.
//	consumer test:  read the same golden -> assert the BFF passthrough holds
//	                against the recorded upstream truth. The BFF
//	                (web/app/api/audit/walkthroughs/[id]/route.ts) is a VERBATIM
//	                passthrough (forwardJSON of the single read), so the consumer
//	                assert is toEqual(golden).
//
// Regenerate the golden after an intentional shape change:
//
//	go test ./internal/api/walkthroughs/ -run TestContract -update

package walkthroughs

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

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID      = "00000000-0000-4000-8000-000000000689"
	contractControlID     = "11111111-1111-4111-8111-111111111111"
	contractWalkthroughID = "22222222-2222-4222-8222-222222222222"
	contractPeriodID      = "33333333-3333-4333-8333-333333333333"
	contractAttachmentID  = "44444444-4444-4444-8444-444444444444"
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
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/api/walkthroughs/ -run TestContract -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q present in golden but missing from handler output; run -update", name)
		}
	}
}

// stubWalkthroughReader is the fixed-row implementation of the
// walkthroughReader seam. It returns deterministic rows with no Postgres.
type stubWalkthroughReader struct {
	wt   walkthrough.Walkthrough
	list []walkthrough.Walkthrough
}

func (s stubWalkthroughReader) Get(_ context.Context, _ uuid.UUID) (walkthrough.Walkthrough, error) {
	return s.wt, nil
}

func (s stubWalkthroughReader) List(_ context.Context) ([]walkthrough.Walkthrough, error) {
	return s.list, nil
}

// recordRoutedVariant routes a GET through a chi router so chi.URLParam(r,
// "id") resolves, binds a tenant + credential (satisfying authnContext), and
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

// ===== GET /v1/walkthroughs/{id} =====

func TestContract_WalkthroughGet(t *testing.T) {
	created := mustTime("2026-02-10T08:00:00Z")
	updated := mustTime("2026-02-11T10:30:00Z")
	uploaded := mustTime("2026-02-11T09:45:00Z")
	periodID := uuid.MustParse(contractPeriodID)

	// Obviously-fake 32-byte digest (not a real hash) — synthetic bytes that
	// hex-encode to 64 lowercase hex chars (canonical_hash wire shape).
	canonicalHash := []byte{
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
	}

	// Variant "with_attachment" — a finalized walkthrough with one attachment
	// and an audit_period_id pin. Pins the attachment row shape + the
	// canonical_hash + tamper flag. transcript present.
	withAttachment := walkthrough.Walkthrough{
		ID:            uuid.MustParse(contractWalkthroughID),
		TenantID:      uuid.MustParse(contractTenantID),
		AuditPeriodID: &periodID,
		ControlID:     uuid.MustParse(contractControlID),
		Narrative:     "Reviewed the quarterly access-recertification run with the control owner.",
		Transcript:    "Owner walked through the recert workflow end to end.",
		CanonicalHash: canonicalHash,
		Status:        walkthrough.StatusFinalized,
		CreatedBy:     "key_contract_689",
		CreatedAt:     created,
		UpdatedAt:     updated,
		Attachments: []walkthrough.Attachment{
			{
				ID:             uuid.MustParse(contractAttachmentID),
				TenantID:       uuid.MustParse(contractTenantID),
				WalkthroughID:  uuid.MustParse(contractWalkthroughID),
				StorageKey:     "tenant-689/walkthroughs/recert-screenshot.png",
				ContentType:    "image/png",
				SizeBytes:      20480,
				SHA256Hex:      "aaaabbbbccccdddd0000111122223333aaaabbbbccccdddd0000111122223333",
				AnnotationsRaw: []byte(`{"region":"top-left","note":"approver list"}`),
				UploadedBy:     "key_contract_689",
				UploadedAt:     uploaded,
			},
		},
		TamperDetected: false,
	}
	// Variant "draft_no_attachments" — a draft walkthrough with no
	// attachments (attachments omitempty -> absent) and no audit_period_id
	// pin (omitempty -> absent) and no transcript. Pins the minimal shape.
	draft := walkthrough.Walkthrough{
		ID:             uuid.MustParse(contractWalkthroughID),
		TenantID:       uuid.MustParse(contractTenantID),
		ControlID:      uuid.MustParse(contractControlID),
		Narrative:      "Initial draft pending the control owner interview.",
		CanonicalHash:  canonicalHash,
		Status:         walkthrough.StatusDraft,
		CreatedBy:      "key_contract_689",
		CreatedAt:      created,
		UpdatedAt:      created,
		TamperDetected: false,
	}

	target := "/v1/walkthroughs/" + contractWalkthroughID
	recorded := map[string]json.RawMessage{
		"with_attachment": recordRoutedVariant(t, newHandlerWithReader(stubWalkthroughReader{wt: withAttachment}).Get,
			"/v1/walkthroughs/{id}", target),
		"draft_no_attachments": recordRoutedVariant(t, newHandlerWithReader(stubWalkthroughReader{wt: draft}).Get,
			"/v1/walkthroughs/{id}", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/walkthrough-get.golden.json"),
		"Slice 689 contract-tier golden. PROVIDER: internal/api/walkthroughs/contractrecord_test.go (Get, real handler over an injected fixed-row single-method walkthroughReader stub — Option A seam, no Postgres; routed through chi for the {id} path param). Regenerate: `go test ./internal/api/walkthroughs/ -run TestContract_WalkthroughGet -update`. CONSUMER: web/lib/contracts/walkthrough-get.contract.test.ts asserts the BFF at web/app/api/audit/walkthroughs/[id]/route.ts — VERBATIM passthrough (toEqual). canonical_hash is hex-encoded (64 lowercase hex chars); tamper_detected is always present (AC-6); attachments + audit_period_id + transcript are omitempty.",
		"GET /v1/walkthroughs/{id}",
		recorded,
	)
}

// ===== GET /v1/walkthroughs =====

func TestContract_WalkthroughsList(t *testing.T) {
	created := mustTime("2026-02-10T08:00:00Z")
	updated := mustTime("2026-02-11T10:30:00Z")
	periodID := uuid.MustParse(contractPeriodID)

	// Obviously-fake 32-byte digest (not a real hash) — synthetic bytes that
	// hex-encode to 64 lowercase hex chars (canonical_hash wire shape).
	canonicalHash := []byte{
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
	}

	// Variant "populated" — a two-row list: one finalized walkthrough with an
	// audit_period_id pin, one draft without. The list shape omits attachments
	// (the List query is the row-summary read; attachments hydrate only on the
	// single-walkthrough Get) — pins the {walkthroughs:[], count:N} envelope +
	// the per-row summary shape the list surface reads.
	finalized := walkthrough.Walkthrough{
		ID:             uuid.MustParse(contractWalkthroughID),
		TenantID:       uuid.MustParse(contractTenantID),
		AuditPeriodID:  &periodID,
		ControlID:      uuid.MustParse(contractControlID),
		Narrative:      "Reviewed the quarterly access-recertification run with the control owner.",
		CanonicalHash:  canonicalHash,
		Status:         walkthrough.StatusFinalized,
		CreatedBy:      "key_contract_690",
		CreatedAt:      created,
		UpdatedAt:      updated,
		TamperDetected: false,
	}
	draft := walkthrough.Walkthrough{
		ID:             uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		TenantID:       uuid.MustParse(contractTenantID),
		ControlID:      uuid.MustParse(contractControlID),
		Narrative:      "Initial draft pending the control owner interview.",
		CanonicalHash:  canonicalHash,
		Status:         walkthrough.StatusDraft,
		CreatedBy:      "key_contract_690",
		CreatedAt:      created,
		UpdatedAt:      created,
		TamperDetected: false,
	}
	// Variant "empty" — no walkthroughs yet. walkthroughs is [] (the handler
	// builds make([]walkthroughWire, 0)), count is 0.
	empty := []walkthrough.Walkthrough{}

	target := "/v1/walkthroughs"
	recorded := map[string]json.RawMessage{
		"populated": recordRoutedVariant(t, newHandlerWithReader(stubWalkthroughReader{list: []walkthrough.Walkthrough{finalized, draft}}).List,
			"/v1/walkthroughs", target),
		"empty": recordRoutedVariant(t, newHandlerWithReader(stubWalkthroughReader{list: empty}).List,
			"/v1/walkthroughs", target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/walkthroughs-list.golden.json"),
		"Slice 690 contract-tier golden. PROVIDER: internal/api/walkthroughs/contractrecord_test.go (List, real handler over an injected fixed-row walkthroughReader stub — Option A seam, no Postgres). Regenerate: `go test ./internal/api/walkthroughs/ -run TestContract_WalkthroughsList -update`. CONSUMER: web/lib/contracts/walkthroughs-list.contract.test.ts is a FIELD-CONTRACT pin (slice 687 D3) — the list BFF (web/app/api/audit/walkthroughs/route.ts) is POST-only, no GET consumer today. Envelope is {walkthroughs:[], count:N}; each row carries id/control_id/narrative/status/canonical_hash (64 hex)/created_by/created_at/updated_at + tamper_detected (always present); audit_period_id is omitempty; walkthroughs is ALWAYS an array (empty list records []).",
		"GET /v1/walkthroughs",
		recorded,
	)
}
