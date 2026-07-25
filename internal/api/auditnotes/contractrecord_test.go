// Slice 689 + 690 — contract-test-tier rollout to the audit-workspace
// audit-notes READ routes served by this package:
//
//	GET /v1/audit-notes/thread -> audit-notes-thread.golden.json  (689, toEqual)
//	GET /v1/audit-notes        -> audit-notes-list.golden.json    (690, field-contract)
//
// The thread read (689) has a verbatim-passthrough BFF (toEqual consumer).
// The legacy author-scoped list read (690) has NO GET BFF today — the
// workspace reads /thread, not the legacy list — so its consumer half is a
// FIELD-CONTRACT pin on the recorded provider golden (slice 687 D3). See
// decisions log 690 D1/D2.
//
// This is the slice-392 / slice-409 / slice-410 / slice-411 / slice-412 /
// slice-687 shared-recorder helper copied into this package because Go test
// files cannot cross a package boundary (the same reason the sibling copies
// exist in internal/api/audit, internal/api/walkthroughs, internal/api/controldetail,
// internal/api/auditperiods, …).
//
// Pattern (ADR-0007 option 1, slice-411 per-route read seam):
//
//	provider test:  construct the real Handler over an injected fixed-row
//	                single-method threadReader stub (no pgx pool) -> drive the
//	                real Thread handler (the route reads {audit_period_id,
//	                scope_type, scope_id} from QUERY params, so no chi mux is
//	                needed) -> canonicalize the body -> diff against the
//	                committed golden under web/lib/contracts/.
//	consumer test:  read the same golden -> assert the BFF passthrough holds
//	                against the recorded upstream truth. The thread BFF
//	                (web/app/api/audit/audit-notes/thread/route.ts) is a VERBATIM
//	                passthrough (forwardJSON of the upstream thread response — it
//	                rebuilds the query string but does NOT reshape the body), so
//	                the consumer assert is toEqual(golden). P0-2: the platform
//	                filters auditor_only notes to their author at the QUERY
//	                LAYER; the UI never client-side-filters visibility, so the
//	                wire shape this golden pins IS the caller's full picture.
//
// Regenerate the golden after an intentional shape change:
//
//	go test ./internal/api/auditnotes/ -run TestContract -update

package auditnotes

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

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Obviously-fake fixture values (slice 314 / GitGuardian: no vendor- or
// JWT-shaped literals — synthetic UUIDs only).
const (
	contractTenantID = "00000000-0000-4000-8000-000000000689"
	contractPeriodID = "11111111-1111-4111-8111-111111111111"
	contractScopeID  = "22222222-2222-4222-8222-222222222222"
	contractRootID   = "33333333-3333-4333-8333-333333333333"
	contractReplyID  = "44444444-4444-4444-8444-444444444444"
	contractAuthorID = "user_contract_689"
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
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/api/auditnotes/ -run TestContract -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q present in golden but missing from handler output; run -update", name)
		}
	}
}

// stubThreadReader is the fixed-row implementation of the threadReader seam.
// It returns deterministic rows with no Postgres.
type stubThreadReader struct {
	rows       []notes.Note
	authorRows []notes.Note
}

func (s stubThreadReader) ListThreadForScope(_ context.Context, _ uuid.UUID, _, _, _ string) ([]notes.Note, error) {
	return s.rows, nil
}

func (s stubThreadReader) ListForAuthorAndPeriod(_ context.Context, _ uuid.UUID, _ string) ([]notes.Note, error) {
	return s.authorRows, nil
}

// recordThreadVariant drives the Thread handler directly (the route params are
// QUERY params, not path params, so no chi mux is needed), binding a tenant +
// credential carrying a UserID (the Thread handler requires a non-empty
// callerID), and canonicalizes the recorded body.
func recordThreadVariant(t *testing.T, handler http.HandlerFunc, target string) json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "key_contract_689",
		TenantID: contractTenantID,
		UserID:   contractAuthorID,
	})
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler(rec, req)
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

// ===== GET /v1/audit-notes/thread =====

func TestContract_AuditNotesThread(t *testing.T) {
	rootAt := mustTime("2026-02-14T13:00:00Z")
	replyAt := mustTime("2026-02-14T14:30:00Z")
	rootID := uuid.MustParse(contractRootID)

	// Variant "populated" — a two-note thread: a shared root note (depth 0)
	// and a shared reply (depth 1, parent_note_id present). Pins the note row
	// shape (visibility / depth / parent_note_id / created_at formatting) the
	// Audit Hub thread surface reads.
	populated := stubThreadReader{rows: []notes.Note{
		{
			ID:            rootID,
			TenantID:      uuid.MustParse(contractTenantID),
			AuditPeriodID: uuid.MustParse(contractPeriodID),
			AuthorUserID:  contractAuthorID,
			ScopeType:     "control",
			ScopeID:       contractScopeID,
			Body:          "Requesting the Q1 access-review completion evidence for this control.",
			Visibility:    "shared",
			ParentNoteID:  nil,
			CreatedAt:     rootAt,
			UpdatedAt:     rootAt,
			Depth:         0,
		},
		{
			ID:            uuid.MustParse(contractReplyID),
			TenantID:      uuid.MustParse(contractTenantID),
			AuditPeriodID: uuid.MustParse(contractPeriodID),
			AuthorUserID:  "user_owner_689",
			ScopeType:     "control",
			ScopeID:       contractScopeID,
			Body:          "Attached the completion export to the walkthrough record.",
			Visibility:    "shared",
			ParentNoteID:  &rootID,
			CreatedAt:     replyAt,
			UpdatedAt:     replyAt,
			Depth:         1,
		},
	}}
	// Variant "empty" — no visible notes on the anchor. audit_notes is []
	// (never null) and count 0.
	empty := stubThreadReader{rows: []notes.Note{}}

	target := "/v1/audit-notes/thread?audit_period_id=" + contractPeriodID +
		"&scope_type=control&scope_id=" + contractScopeID
	recorded := map[string]json.RawMessage{
		"populated": recordThreadVariant(t, newHandlerWithReader(populated).Thread, target),
		"empty":     recordThreadVariant(t, newHandlerWithReader(empty).Thread, target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/audit-notes-thread.golden.json"),
		"Slice 689 contract-tier golden. PROVIDER: internal/api/auditnotes/contractrecord_test.go (Thread, real handler over an injected fixed-row single-method threadReader stub — Option A seam, no Postgres; route params are query params so no chi mux). Regenerate: `go test ./internal/api/auditnotes/ -run TestContract_AuditNotesThread -update`. CONSUMER: web/lib/contracts/audit-notes-thread.contract.test.ts asserts the BFF at web/app/api/audit/audit-notes/thread/route.ts — VERBATIM passthrough (toEqual). audit_notes is ALWAYS an array (never null); empty thread records [] + count 0. P0-2: auditor_only notes are filtered to their author at the query layer; the UI never client-side-filters visibility.",
		"GET /v1/audit-notes/thread",
		recorded,
	)
}

// ===== GET /v1/audit-notes (legacy author-scoped list) =====

func TestContract_AuditNotesList(t *testing.T) {
	noteAt := mustTime("2026-02-15T09:00:00Z")
	updatedAt := mustTime("2026-02-15T09:30:00Z")
	rootID := uuid.MustParse(contractRootID)

	// Variant "populated" — two of the caller's own notes in a period: a
	// shared root and an auditor_only private note (the legacy List is
	// author-scoped, so both belong to the caller). Pins the note row shape
	// the slice-025 legacy author list reads (depth omitempty is 0 → absent;
	// scope_id present on one, period-level on the other).
	populated := stubThreadReader{authorRows: []notes.Note{
		{
			ID:            rootID,
			TenantID:      uuid.MustParse(contractTenantID),
			AuditPeriodID: uuid.MustParse(contractPeriodID),
			AuthorUserID:  contractAuthorID,
			ScopeType:     "control",
			ScopeID:       contractScopeID,
			Body:          "Drafted the access-review note for my own follow-up.",
			Visibility:    "shared",
			ParentNoteID:  nil,
			CreatedAt:     noteAt,
			UpdatedAt:     updatedAt,
			Depth:         0,
		},
		{
			ID:            uuid.MustParse(contractReplyID),
			TenantID:      uuid.MustParse(contractTenantID),
			AuditPeriodID: uuid.MustParse(contractPeriodID),
			AuthorUserID:  contractAuthorID,
			ScopeType:     "period",
			ScopeID:       "",
			Body:          "Private reminder to revisit the vendor SOC 2 bridge letter.",
			Visibility:    "auditor_only",
			ParentNoteID:  nil,
			CreatedAt:     noteAt,
			UpdatedAt:     noteAt,
			Depth:         0,
		},
	}}
	// Variant "empty" — the caller has no notes in this period. audit_notes is
	// [] (never null) and count 0.
	empty := stubThreadReader{authorRows: []notes.Note{}}

	target := "/v1/audit-notes?audit_period_id=" + contractPeriodID
	recorded := map[string]json.RawMessage{
		"populated": recordThreadVariant(t, newHandlerWithReader(populated).List, target),
		"empty":     recordThreadVariant(t, newHandlerWithReader(empty).List, target),
	}
	assertContractGolden(t,
		filepath.Clean("../../../web/lib/contracts/audit-notes-list.golden.json"),
		"Slice 690 contract-tier golden. PROVIDER: internal/api/auditnotes/contractrecord_test.go (List, real handler over an injected fixed-row threadReader stub — Option A seam, no Postgres; route param is a query param so no chi mux). Regenerate: `go test ./internal/api/auditnotes/ -run TestContract_AuditNotesList -update`. CONSUMER: web/lib/contracts/audit-notes-list.contract.test.ts is a FIELD-CONTRACT pin (slice 687 D3) — there is NO GET BFF for the legacy list today (the workspace reads /thread). Envelope is {audit_notes:[], count:N}; each row carries id/audit_period_id/author_user_id/scope_type/body/visibility/created_at/updated_at; scope_id is omitempty (period-level notes omit it); parent_note_id + depth are omitempty; created_at/updated_at use the millisecond 2006-01-02T15:04:05.000Z format; audit_notes is ALWAYS an array (empty records []).",
		"GET /v1/audit-notes",
		recorded,
	)
}
