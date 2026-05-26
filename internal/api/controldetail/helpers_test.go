// Slice 290 — unit tests for the pure-Go helpers + the remaining
// non-DB-touching branches in the control-detail handler. Companion to
// handler_test.go (which already pinned the Evidence endpoint's 4xx
// branches); this file covers:
//
//   Load-bearing functions targeted:
//     - splitEvidencePage / splitEvidenceListPage / splitHistoryPage
//       (the +1-probe-row pagination split — both "no next page" and
//       "next page" branches)
//     - encodeCursor / decodeCursor (round-trip + all four malformed
//       paths: bad base64, missing separator, bad RFC3339 timestamp,
//       bad UUID)
//     - parseLimit / parseRFC3339 (already partially covered; this file
//       pins the boundary cases — limit at the cap, RFC3339 fallback,
//       and the malformed branches)
//     - firstPageCursor (sentinel selects every real row)
//     - jsonOrNull / numericToFloat / uuidString / uuidPtr / tsString /
//       pgUUID / pgTimestamptz (the seven wire-conversion helpers, both
//       valid and null/empty branches)
//     - evidenceWireFrom / evidenceWireFromListRow (the two row->wire
//       converters — both per-control and tenant-wide row types)
//     - guardAndResolvePathControl via Policies / Risks / History
//       handlers (the 400 path-param branch, 403 no-role branch, and
//       401 missing-tenant branch — none touch the DB)
//     - History handler's cursor / limit 400 branches
//     - hasControlRead role-derivation (all three accept signals + the
//       compound reject case)
//
// Branches NOT covered here (covered by integration_test.go against
// real Postgres + RLS): the happy paths through Evidence / Policies /
// Risks / History that round-trip the store.
//
// NO vendor token prefixes in test fixtures — neutral test-* tokens
// only (matches handler_test.go's posture).

package controldetail

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- cursor encode / decode round-trip + malformed branches -----

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 5, 25, 12, 34, 56, 789, time.UTC)
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	encoded := encodeCursor(keyset{ts: ts, id: id})
	if encoded == "" {
		t.Fatalf("encodeCursor returned empty for a non-zero keyset")
	}
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !decoded.ts.Equal(ts) {
		t.Fatalf("round-trip ts = %v, want %v", decoded.ts, ts)
	}
	if decoded.id != id {
		t.Fatalf("round-trip id = %v, want %v", decoded.id, id)
	}
}

func TestEncodeCursor_ZeroTimestampYieldsEmpty(t *testing.T) {
	// Zero keyset must encode to "" so the handler can omit `next_cursor`
	// when there is no next page (handler.go splitEvidencePage / etc.
	// returns the zero keyset when rows fit on a single page).
	got := encodeCursor(keyset{})
	if got != "" {
		t.Fatalf("encodeCursor(zero) = %q, want empty string", got)
	}
}

func TestDecodeCursor_EmptyYieldsFirstPageSentinel(t *testing.T) {
	got, err := decodeCursor("")
	if err != nil {
		t.Fatalf("decodeCursor(\"\"): %v", err)
	}
	want := firstPageCursor()
	if !got.ts.Equal(want.ts) {
		t.Fatalf("first-page sentinel ts = %v, want %v", got.ts, want.ts)
	}
	if got.id != want.id {
		t.Fatalf("first-page sentinel id = %v, want %v", got.id, want.id)
	}
}

func TestDecodeCursor_BadBase64(t *testing.T) {
	_, err := decodeCursor("!!!not-base64!!!")
	if err == nil {
		t.Fatalf("expected error on bad base64, got nil")
	}
}

func TestDecodeCursor_MissingSeparator(t *testing.T) {
	// Base64-decode-able payload with no "|" separator -> errBadCursor.
	raw := base64.RawURLEncoding.EncodeToString([]byte("no-separator-here"))
	_, err := decodeCursor(raw)
	if err == nil {
		t.Fatalf("expected error on missing separator, got nil")
	}
}

func TestDecodeCursor_BadTimestamp(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte("not-a-timestamp|11111111-1111-1111-1111-111111111111"))
	_, err := decodeCursor(raw)
	if err == nil {
		t.Fatalf("expected error on bad timestamp, got nil")
	}
}

func TestDecodeCursor_BadUUID(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte("2026-05-25T00:00:00Z|not-a-uuid"))
	_, err := decodeCursor(raw)
	if err == nil {
		t.Fatalf("expected error on bad uuid, got nil")
	}
}

// ----- parseLimit boundary cases -----

func TestParseLimit_Default(t *testing.T) {
	got, err := parseLimit("")
	if err != nil {
		t.Fatalf("parseLimit(\"\"): %v", err)
	}
	if got != defaultLimit {
		t.Fatalf("parseLimit(\"\") = %d, want %d", got, defaultLimit)
	}
}

func TestParseLimit_AtCap(t *testing.T) {
	got, err := parseLimit("200")
	if err != nil {
		t.Fatalf("parseLimit(\"200\"): %v", err)
	}
	if got != maxLimit {
		t.Fatalf("parseLimit(\"200\") = %d, want %d", got, maxLimit)
	}
}

func TestParseLimit_BelowMin(t *testing.T) {
	if _, err := parseLimit("0"); err == nil {
		t.Fatalf("parseLimit(\"0\") expected error, got nil")
	}
}

func TestParseLimit_AboveMax(t *testing.T) {
	if _, err := parseLimit("201"); err == nil {
		t.Fatalf("parseLimit(\"201\") expected error, got nil")
	}
}

func TestParseLimit_NonInt(t *testing.T) {
	if _, err := parseLimit("twelve"); err == nil {
		t.Fatalf("parseLimit(\"twelve\") expected error, got nil")
	}
}

// ----- parseRFC3339 fallback + malformed branches -----

func TestParseRFC3339_EmptyUsesFallback(t *testing.T) {
	fallback := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := parseRFC3339("", fallback)
	if err != nil {
		t.Fatalf("parseRFC3339(\"\"): %v", err)
	}
	if !got.Equal(fallback) {
		t.Fatalf("parseRFC3339(\"\") = %v, want fallback %v", got, fallback)
	}
}

func TestParseRFC3339_Valid(t *testing.T) {
	in := "2026-05-25T12:00:00Z"
	got, err := parseRFC3339(in, time.Time{})
	if err != nil {
		t.Fatalf("parseRFC3339(%q): %v", in, err)
	}
	want := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parseRFC3339(%q) = %v, want %v", in, got, want)
	}
}

func TestParseRFC3339_Malformed(t *testing.T) {
	if _, err := parseRFC3339("not-a-time", time.Time{}); err == nil {
		t.Fatalf("parseRFC3339(\"not-a-time\") expected error, got nil")
	}
}

// ----- page-splitting (the +1 probe-row idiom) -----

func TestSplitEvidencePage_NoNextPage(t *testing.T) {
	// rows fit in the requested page size -> next_cursor is "".
	rows := []dbx.ListEvidenceForControlPagedRow{
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(time.Now().UTC())},
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(time.Now().UTC())},
	}
	page, next := splitEvidencePage(rows, 2)
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if next != "" {
		t.Fatalf("next_cursor = %q, want empty", next)
	}
}

func TestSplitEvidencePage_HasNextPage(t *testing.T) {
	// rows > pageRows -> trim to pageRows + emit next_cursor.
	now := time.Now().UTC()
	lastObserved := now.Add(-time.Hour)
	lastID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	rows := []dbx.ListEvidenceForControlPagedRow{
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(now)},
		{ID: pgUUID(lastID), ObservedAt: pgTimestamptz(lastObserved)},
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(now.Add(-2 * time.Hour))}, // probe row trimmed
	}
	page, next := splitEvidencePage(rows, 2)
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if next == "" {
		t.Fatalf("next_cursor empty, want non-empty")
	}
	decoded, err := decodeCursor(next)
	if err != nil {
		t.Fatalf("decodeCursor(next): %v", err)
	}
	if !decoded.ts.Equal(lastObserved) {
		t.Fatalf("next_cursor ts = %v, want %v", decoded.ts, lastObserved)
	}
	if decoded.id != lastID {
		t.Fatalf("next_cursor id = %v, want %v", decoded.id, lastID)
	}
}

func TestSplitEvidenceListPage_NoNextPage(t *testing.T) {
	rows := []dbx.ListEvidencePagedRow{
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(time.Now().UTC())},
	}
	page, next := splitEvidenceListPage(rows, 1)
	if len(page) != 1 {
		t.Fatalf("len(page) = %d, want 1", len(page))
	}
	if next != "" {
		t.Fatalf("next_cursor = %q, want empty", next)
	}
}

func TestSplitEvidenceListPage_HasNextPage(t *testing.T) {
	now := time.Now().UTC()
	lastObserved := now.Add(-time.Hour)
	lastID := uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
	rows := []dbx.ListEvidencePagedRow{
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(now)},
		{ID: pgUUID(lastID), ObservedAt: pgTimestamptz(lastObserved)},
		{ID: pgUUID(uuid.New()), ObservedAt: pgTimestamptz(now.Add(-2 * time.Hour))},
	}
	page, next := splitEvidenceListPage(rows, 2)
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if next == "" {
		t.Fatalf("next_cursor empty, want non-empty")
	}
	decoded, err := decodeCursor(next)
	if err != nil {
		t.Fatalf("decodeCursor(next): %v", err)
	}
	if decoded.id != lastID {
		t.Fatalf("next_cursor id = %v, want %v", decoded.id, lastID)
	}
}

func TestSplitHistoryPage_NoNextPage(t *testing.T) {
	rows := []dbx.ListControlEvaluationHistoryPagedRow{
		{ID: pgUUID(uuid.New()), EvaluatedAt: pgTimestamptz(time.Now().UTC())},
	}
	page, next := splitHistoryPage(rows, 1)
	if len(page) != 1 {
		t.Fatalf("len(page) = %d, want 1", len(page))
	}
	if next != "" {
		t.Fatalf("next_cursor = %q, want empty", next)
	}
}

func TestSplitHistoryPage_HasNextPage(t *testing.T) {
	now := time.Now().UTC()
	lastEvaluated := now.Add(-time.Hour)
	lastID := uuid.MustParse("ffffffff-0000-1111-2222-333333333333")
	rows := []dbx.ListControlEvaluationHistoryPagedRow{
		{ID: pgUUID(uuid.New()), EvaluatedAt: pgTimestamptz(now)},
		{ID: pgUUID(lastID), EvaluatedAt: pgTimestamptz(lastEvaluated)},
		{ID: pgUUID(uuid.New()), EvaluatedAt: pgTimestamptz(now.Add(-2 * time.Hour))},
	}
	page, next := splitHistoryPage(rows, 2)
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if next == "" {
		t.Fatalf("next_cursor empty, want non-empty")
	}
	decoded, err := decodeCursor(next)
	if err != nil {
		t.Fatalf("decodeCursor(next): %v", err)
	}
	if decoded.id != lastID {
		t.Fatalf("next_cursor id = %v, want %v", decoded.id, lastID)
	}
}

// ----- wire-conversion helpers -----

func TestPgUUID(t *testing.T) {
	u := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := pgUUID(u)
	if !got.Valid {
		t.Fatalf("pgUUID(%v).Valid = false, want true", u)
	}
	if got.Bytes != u {
		t.Fatalf("pgUUID(%v).Bytes = %v, want %v", u, got.Bytes, u)
	}
}

func TestPgTimestamptz_Zero(t *testing.T) {
	got := pgTimestamptz(time.Time{})
	if got.Valid {
		t.Fatalf("pgTimestamptz(zero).Valid = true, want false")
	}
}

func TestPgTimestamptz_NonZero(t *testing.T) {
	now := time.Date(2026, 5, 25, 1, 2, 3, 0, time.UTC)
	got := pgTimestamptz(now)
	if !got.Valid {
		t.Fatalf("pgTimestamptz(non-zero).Valid = false, want true")
	}
	if !got.Time.Equal(now) {
		t.Fatalf("pgTimestamptz(%v).Time = %v, want %v", now, got.Time, now)
	}
}

func TestUUIDString_Invalid(t *testing.T) {
	if got := uuidString(pgtype.UUID{Valid: false}); got != "" {
		t.Fatalf("uuidString(invalid) = %q, want empty", got)
	}
}

func TestUUIDString_Valid(t *testing.T) {
	u := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := uuidString(pgUUID(u))
	if got != u.String() {
		t.Fatalf("uuidString = %q, want %q", got, u.String())
	}
}

func TestUUIDPtr_Invalid(t *testing.T) {
	if got := uuidPtr(pgtype.UUID{Valid: false}); got != nil {
		t.Fatalf("uuidPtr(invalid) = %v, want nil", got)
	}
}

func TestUUIDPtr_Valid(t *testing.T) {
	u := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := uuidPtr(pgUUID(u))
	if got == nil {
		t.Fatalf("uuidPtr(valid) = nil, want non-nil")
	}
	if *got != u.String() {
		t.Fatalf("uuidPtr = %q, want %q", *got, u.String())
	}
}

func TestTSString_Invalid(t *testing.T) {
	if got := tsString(pgtype.Timestamptz{Valid: false}); got != "" {
		t.Fatalf("tsString(invalid) = %q, want empty", got)
	}
}

func TestTSString_Valid(t *testing.T) {
	ts := time.Date(2026, 5, 25, 1, 2, 3, 0, time.UTC)
	got := tsString(pgTimestamptz(ts))
	if got == "" || !strings.HasPrefix(got, "2026-05-25T01:02:03") {
		t.Fatalf("tsString = %q, want a 2026-05-25T01:02:03 prefix", got)
	}
}

func TestJSONOrNull_Empty(t *testing.T) {
	got := jsonOrNull(nil)
	if string(got) != "null" {
		t.Fatalf("jsonOrNull(nil) = %q, want null", string(got))
	}
	got = jsonOrNull([]byte{})
	if string(got) != "null" {
		t.Fatalf("jsonOrNull(empty) = %q, want null", string(got))
	}
}

func TestJSONOrNull_NonEmpty(t *testing.T) {
	in := []byte(`{"k":"v"}`)
	got := jsonOrNull(in)
	if string(got) != `{"k":"v"}` {
		t.Fatalf("jsonOrNull = %q, want passthrough", string(got))
	}
}

func TestNumericToFloat_Invalid(t *testing.T) {
	got := numericToFloat(pgtype.Numeric{Valid: false})
	if got != nil {
		t.Fatalf("numericToFloat(invalid) = %v, want nil", got)
	}
}

func TestNumericToFloat_Valid(t *testing.T) {
	// 0.875 — a value pgtype.Numeric can losslessly round-trip via
	// Float64Value(). The risk_control_links.design_score is NUMERIC(4,3)
	// per slice 020, so a three-decimal value is the realistic shape.
	var n pgtype.Numeric
	if err := n.Scan("0.875"); err != nil {
		t.Fatalf("Scan numeric: %v", err)
	}
	got := numericToFloat(n)
	if got == nil {
		t.Fatalf("numericToFloat(0.875) = nil, want non-nil")
	}
	if *got != 0.875 {
		t.Fatalf("numericToFloat = %v, want 0.875", *got)
	}
}

// ----- evidenceWireFrom + evidenceWireFromListRow -----

func TestEvidenceWireFrom_RoundTrip(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	scopeID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	observedAt := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	kind := "sast.scan_result.v1"
	row := dbx.ListEvidenceForControlPagedRow{
		ID:           pgUUID(id),
		ScopeID:      pgUUID(scopeID),
		ObservedAt:   pgTimestamptz(observedAt),
		EvidenceKind: &kind,
		Provenance:   []byte(`{"connector_id":"aws"}`),
		Hash:         "sha256:abc",
		Result:       dbx.EvidenceResultPass,
	}
	out := evidenceWireFrom(row)
	if out.EvidenceID != id.String() {
		t.Fatalf("EvidenceID = %q, want %q", out.EvidenceID, id.String())
	}
	if out.EvidenceKind == nil || *out.EvidenceKind != kind {
		t.Fatalf("EvidenceKind = %v, want %q", out.EvidenceKind, kind)
	}
	if out.ContentHash != "sha256:abc" {
		t.Fatalf("ContentHash = %q, want sha256:abc", out.ContentHash)
	}
	if out.ScopeCell == nil || *out.ScopeCell != scopeID.String() {
		t.Fatalf("ScopeCell = %v, want %q", out.ScopeCell, scopeID.String())
	}
	if out.Result != "pass" {
		t.Fatalf("Result = %q, want pass", out.Result)
	}
	if out.ObservedAt == "" {
		t.Fatalf("ObservedAt is empty, want RFC3339 string")
	}
	if string(out.Source) != `{"connector_id":"aws"}` {
		t.Fatalf("Source = %q, want passthrough", string(out.Source))
	}
}

func TestEvidenceWireFrom_NullScope(t *testing.T) {
	// scope is nullable on the evidence ledger; a NULL scope renders as
	// a nil *string on the wire.
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	row := dbx.ListEvidenceForControlPagedRow{
		ID:         pgUUID(id),
		ScopeID:    pgtype.UUID{Valid: false},
		ObservedAt: pgTimestamptz(time.Now().UTC()),
		Provenance: nil,
		Hash:       "sha256:def",
		Result:     dbx.EvidenceResultFail,
	}
	out := evidenceWireFrom(row)
	if out.ScopeCell != nil {
		t.Fatalf("ScopeCell = %v, want nil", out.ScopeCell)
	}
	if string(out.Source) != "null" {
		t.Fatalf("Source = %q, want \"null\"", string(out.Source))
	}
}

func TestEvidenceWireFromListRow_RoundTrip(t *testing.T) {
	// Tenant-wide row type — structurally identical to the per-control
	// row type but emitted as a distinct sqlc type. Confirms the twin
	// converter (handler.go:419) emits the same wire shape.
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	scopeID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	kind := "access_review.completion.v1"
	row := dbx.ListEvidencePagedRow{
		ID:           pgUUID(id),
		ScopeID:      pgUUID(scopeID),
		ObservedAt:   pgTimestamptz(time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)),
		EvidenceKind: &kind,
		Provenance:   []byte(`{"runner":"r1"}`),
		Hash:         "sha256:xyz",
		Result:       dbx.EvidenceResultInconclusive,
	}
	out := evidenceWireFromListRow(row)
	if out.EvidenceID != id.String() {
		t.Fatalf("EvidenceID = %q, want %q", out.EvidenceID, id.String())
	}
	if out.Result != "inconclusive" {
		t.Fatalf("Result = %q, want inconclusive", out.Result)
	}
	if out.EvidenceKind == nil || *out.EvidenceKind != kind {
		t.Fatalf("EvidenceKind = %v, want %q", out.EvidenceKind, kind)
	}
	if out.ScopeCell == nil || *out.ScopeCell != scopeID.String() {
		t.Fatalf("ScopeCell = %v, want %q", out.ScopeCell, scopeID.String())
	}
}

// ----- hasControlRead role-derivation -----

func TestHasControlRead_Admin(t *testing.T) {
	if !hasControlRead(credstore.Credential{IsAdmin: true}) {
		t.Fatalf("admin should be granted control-read")
	}
}

func TestHasControlRead_Approver(t *testing.T) {
	if !hasControlRead(credstore.Credential{IsApprover: true}) {
		t.Fatalf("approver should be granted control-read")
	}
}

func TestHasControlRead_OwnerRoles(t *testing.T) {
	if !hasControlRead(credstore.Credential{OwnerRoles: []string{"control_owner"}}) {
		t.Fatalf("owner-role-bearing cred should be granted control-read")
	}
}

func TestHasControlRead_NoSignals(t *testing.T) {
	// Bare credential with no flags + no owner roles is the v1 viewer-
	// only shape; the guard is deliberately stricter than the
	// authz.derivedRolesFor default (P0-064 acceptance: AC-5 must
	// distinguish a viewer-only credential).
	if hasControlRead(credstore.Credential{UserID: "test-viewer"}) {
		t.Fatalf("bare cred should NOT be granted control-read")
	}
}

// ----- handler 400/401/403 branches via Policies / Risks / History -----
//
// These hit guardAndResolvePathControl on a Handler with a nil store; the
// store is never reached because the guard fails first. The route is
// registered on a chi.Mux so the {id} path param is populated correctly.

func mountForPath(method, urlPath string, h func(http.ResponseWriter, *http.Request), pattern string) (*chi.Mux, *http.Request, *httptest.ResponseRecorder) {
	r := chi.NewRouter()
	r.Method(method, pattern, http.HandlerFunc(h))
	req := httptest.NewRequest(method, urlPath, nil)
	rec := httptest.NewRecorder()
	return r, req, rec
}

func withFakeTenant(t *testing.T, r *http.Request, tenantID string) *http.Request {
	t.Helper()
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID:   tenantID,
		UserID:     "test-user",
		OwnerRoles: []string{"control_owner"},
	})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return r.WithContext(ctx)
}

func TestPolicies_Handler_400_NonUUIDPathID(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/not-a-uuid/policies", h.Policies, "/v1/controls/{id}/policies")
	req = withFakeTenant(t, req, uuid.NewString())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "uuid") {
		t.Fatalf("body should mention uuid, got %s", rec.Body.String())
	}
}

func TestPolicies_Handler_403_NoControlReadRole(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/policies", h.Policies, "/v1/controls/{id}/policies")
	// credential carries no role signal
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-viewer"}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPolicies_Handler_401_MissingTenantContext(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/policies", h.Policies, "/v1/controls/{id}/policies")
	// admit via IsAdmin but never apply tenancy.WithTenant
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-admin", IsAdmin: true}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["error"], "tenant") {
		t.Fatalf("error should mention tenant, got %q", body["error"])
	}
}

func TestRisks_Handler_400_NonUUIDPathID(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/not-a-uuid/risks", h.Risks, "/v1/controls/{id}/risks")
	req = withFakeTenant(t, req, uuid.NewString())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRisks_Handler_403_NoControlReadRole(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/risks", h.Risks, "/v1/controls/{id}/risks")
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-viewer"}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRisks_Handler_401_MissingTenantContext(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/risks", h.Risks, "/v1/controls/{id}/risks")
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-admin", IsAdmin: true}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistory_Handler_400_NonUUIDPathID(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/not-a-uuid/history", h.History, "/v1/controls/{id}/history")
	req = withFakeTenant(t, req, uuid.NewString())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistory_Handler_403_NoControlReadRole(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/history", h.History, "/v1/controls/{id}/history")
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-viewer"}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistory_Handler_401_MissingTenantContext(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/history", h.History, "/v1/controls/{id}/history")
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{UserID: "test-admin", IsAdmin: true}))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistory_Handler_400_MalformedCursor(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/history?cursor=@@@", h.History, "/v1/controls/{id}/history")
	req = withFakeTenant(t, req, uuid.NewString())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistory_Handler_400_LimitOutOfRange(t *testing.T) {
	h := handlerOver()
	router, req, rec := mountForPath(http.MethodGet, "/v1/controls/11111111-1111-1111-1111-111111111111/history?limit=999", h.History, "/v1/controls/{id}/history")
	req = withFakeTenant(t, req, uuid.NewString())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
