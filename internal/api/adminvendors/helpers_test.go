// Slice 313 — pure-Go unit tests for adminvendors helpers.
//
// Load-bearing functions covered (per AC-3 of slice 313):
//
//   - parseFormat           : empty / valid / invalid query param branches
//   - vendorsExportHeader   : canonical column-order pin
//   - vendorsToRowIter      : populated + nullable + email-mask + scope_cell join
//   - callerAllowedExport   : admin / auditor / grc_engineer / unauthorized
//   - exportLimiter         : default singleton + WithLimiter override
//   - writeError            : Content-Type, status code, body shape
//   - countingWriter.Write  : per-write accumulation + error passthrough
//   - WithLimiter / NewWithClock : config setters
//   - ptrToStr / boolStr / dateStr / joinUUIDs : pure helpers
//
// Per the slice 290 unit/integration split rule: integration tests cover
// the DB-touching paths in export_integration_test.go; this file covers
// the pure-Go helpers reachable without a Postgres pool. Together they
// lift the package above the AC-2 70% merged-coverage floor.

package adminvendors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// ===== parseFormat =====

func TestParseFormat(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantFmt export.Format
		wantErr bool
	}{
		{name: "empty defaults to csv", raw: "", wantFmt: export.FormatCSV, wantErr: false},
		{name: "csv", raw: "csv", wantFmt: export.FormatCSV, wantErr: false},
		{name: "json", raw: "json", wantFmt: export.FormatJSON, wantErr: false},
		{name: "xlsx", raw: "xlsx", wantFmt: export.FormatXLSX, wantErr: false},
		{name: "JSON uppercase normalizes", raw: "JSON", wantFmt: export.FormatJSON, wantErr: false},
		{name: "unsupported", raw: "pdf", wantErr: true},
		{name: "garbage", raw: "??", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &url.URL{RawQuery: ""}
			if tc.raw != "" {
				u.RawQuery = "format=" + url.QueryEscape(tc.raw)
			}
			req := &http.Request{URL: u}
			got, err := parseFormat(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got format=%q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if got != tc.wantFmt {
				t.Fatalf("%q: got %v, want %v", tc.raw, got, tc.wantFmt)
			}
		})
	}
}

// ===== vendorsExportHeader =====

func TestVendorsExportHeader_StableOrder(t *testing.T) {
	want := []string{
		"id",
		"name",
		"domain",
		"criticality",
		"contract_start",
		"contract_end",
		"dpa_signed",
		"dpa_signed_at",
		"review_cadence",
		"last_review_date",
		"overdue",
		"owner_user_masked",
		"linked_sow_uri",
		"notes",
		"scope_cell_ids",
		"created_at",
		"updated_at",
	}
	got := vendorsExportHeader()
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("column %d: got %q, want %q", i, got[i], w)
		}
	}
}

// ===== vendorsToRowIter =====

func TestVendorsToRowIter_PopulatedAndNullable(t *testing.T) {
	vendorID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	scopeA := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	scopeB := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	domain := "example.com"
	contractStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	contractEnd := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	signedAt := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)
	lastReview := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	sowURI := "s3://bucket/sow.pdf"

	asOf := time.Date(2025, 1, 20, 0, 0, 0, 0, time.UTC) // 5 days after last review → NOT overdue under quarterly cadence

	vendors := []vendor.Vendor{
		{
			ID:             vendorID,
			Name:           "Acme Corp",
			Domain:         &domain,
			Criticality:    vendor.CriticalityHigh,
			ContractStart:  &contractStart,
			ContractEnd:    &contractEnd,
			DPASigned:      true,
			DPASignedAt:    &signedAt,
			ReviewCadence:  vendor.CadenceQuarterly,
			LastReviewDate: &lastReview,
			OwnerUser:      "owner@example.com",
			LinkedSOWURI:   &sowURI,
			Notes:          "DPA renegotiation Q3",
			ScopeCellIDs:   []uuid.UUID{scopeA, scopeB},
			CreatedAt:      contractStart,
			UpdatedAt:      lastReview,
		},
		// Nullable / empty branch.
		{
			ID:            uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd"),
			Name:          "Sparse Vendor",
			Criticality:   vendor.CriticalityMedium,
			ReviewCadence: vendor.CadenceQuarterly,
			OwnerUser:     "", // non-email; masked to ""
			Notes:         "",
			CreatedAt:     contractStart,
			UpdatedAt:     contractStart,
		},
	}

	var got [][]string
	for row := range vendorsToRowIter(vendors, asOf) {
		got = append(got, append([]string(nil), row...))
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}

	// Populated row pins
	if got[0][0] != vendorID.String() {
		t.Errorf("row[0][id]: got %q", got[0][0])
	}
	if got[0][2] != "example.com" {
		t.Errorf("row[0][domain]: got %q", got[0][2])
	}
	if got[0][3] != "high" {
		t.Errorf("row[0][criticality]: got %q", got[0][3])
	}
	if got[0][6] != "true" {
		t.Errorf("row[0][dpa_signed]: got %q", got[0][6])
	}
	if got[0][10] != "false" {
		t.Errorf("row[0][overdue]: got %q", got[0][10])
	}
	if got[0][11] != "*@example.com" {
		t.Errorf("row[0][owner_user_masked]: got %q", got[0][11])
	}
	// scope_cell_ids joined by `;`
	wantScopes := scopeA.String() + ";" + scopeB.String()
	if got[0][14] != wantScopes {
		t.Errorf("row[0][scope_cell_ids]: got %q want %q", got[0][14], wantScopes)
	}

	// Nullable row — empty cells where pointers are nil
	if got[1][2] != "" {
		t.Errorf("row[1][domain] empty: got %q", got[1][2])
	}
	if got[1][4] != "" {
		t.Errorf("row[1][contract_start] empty: got %q", got[1][4])
	}
	if got[1][6] != "false" {
		t.Errorf("row[1][dpa_signed]: got %q", got[1][6])
	}
	if got[1][7] != "" {
		t.Errorf("row[1][dpa_signed_at] empty: got %q", got[1][7])
	}
	if got[1][10] != "true" {
		// LastReviewDate=nil → IsOverdueAsOf(asOf) is true.
		t.Errorf("row[1][overdue] should be true for no-review vendor: got %q", got[1][10])
	}
	if got[1][11] != "" {
		t.Errorf("row[1][owner_user_masked] empty input → empty: got %q", got[1][11])
	}
	if got[1][14] != "" {
		t.Errorf("row[1][scope_cell_ids] empty: got %q", got[1][14])
	}
}

func TestVendorsToRowIter_EarlyStop(t *testing.T) {
	asOf := time.Now().UTC()
	vendors := []vendor.Vendor{
		{ID: uuid.New(), Name: "A", Criticality: vendor.CriticalityLow, ReviewCadence: vendor.CadenceQuarterly},
		{ID: uuid.New(), Name: "B", Criticality: vendor.CriticalityLow, ReviewCadence: vendor.CadenceQuarterly},
		{ID: uuid.New(), Name: "C", Criticality: vendor.CriticalityLow, ReviewCadence: vendor.CadenceQuarterly},
	}
	count := 0
	for range vendorsToRowIter(vendors, asOf) {
		count++
		if count >= 1 {
			break
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 row before break, got %d", count)
	}
}

func TestVendorsToRowIter_EmptySlice(t *testing.T) {
	count := 0
	for range vendorsToRowIter(nil, time.Now()) {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}
}

// ===== callerAllowedExport =====

func TestCallerAllowedExport(t *testing.T) {
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{name: "admin", cred: credstore.Credential{IsAdmin: true}, want: true},
		{name: "auditor role", cred: credstore.Credential{OwnerRoles: []string{"auditor"}}, want: true},
		{name: "grc_engineer role", cred: credstore.Credential{OwnerRoles: []string{"grc_engineer"}}, want: true},
		{name: "auditor + others", cred: credstore.Credential{OwnerRoles: []string{"viewer", "auditor"}}, want: true},
		{name: "non-allowed role only", cred: credstore.Credential{OwnerRoles: []string{"viewer"}}, want: false},
		{name: "no roles, not admin", cred: credstore.Credential{}, want: false},
		{name: "empty role string", cred: credstore.Credential{OwnerRoles: []string{""}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callerAllowedExport(tc.cred)
			if got != tc.want {
				t.Fatalf("got %v want %v (cred=%+v)", got, tc.want, tc.cred)
			}
		})
	}
}

// ===== exportLimiter + WithLimiter + NewWithClock =====

func TestHandler_WithLimiter_ReturnsReceiver(t *testing.T) {
	h := &Handler{}
	got := h.WithLimiter(export.NewLimiter(4))
	if got != h {
		t.Fatalf("WithLimiter should return the receiver")
	}
	if h.limiter == nil || h.limiter.Cap() != 4 {
		t.Fatalf("limiter not installed: %+v", h.limiter)
	}
}

func TestHandler_ExportLimiter_FallbackToDefault(t *testing.T) {
	h := &Handler{}
	got := h.exportLimiter()
	if got == nil {
		t.Fatalf("default limiter is nil")
	}
	if got.Cap() < 1 {
		t.Fatalf("default limiter cap: got %d", got.Cap())
	}
}

func TestHandler_ExportLimiter_HonorsOverride(t *testing.T) {
	override := export.NewLimiter(11)
	h := &Handler{}
	h.WithLimiter(override)
	got := h.exportLimiter()
	if got != override {
		t.Fatalf("override limiter not returned")
	}
}

func TestNewWithClock_InstallsClock(t *testing.T) {
	fixed := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	// pool can be nil for constructor-test purposes — we never call methods
	// that dereference it.
	h := NewWithClock(nil, clock)
	if h == nil {
		t.Fatalf("NewWithClock returned nil")
	}
	if h.now == nil {
		t.Fatalf("now func is nil")
	}
	if !h.now().Equal(fixed) {
		t.Fatalf("clock did not return fixed time: got %v", h.now())
	}
}

// ===== writeError =====

func TestWriteError(t *testing.T) {
	rr := httptest.NewRecorder()
	httpresp.WriteError(rr, http.StatusForbidden, "denied")
	res := rr.Result()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type: got %q", got)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "denied" {
		t.Fatalf("body: %+v", body)
	}
}

// ===== countingWriter =====

func TestCountingWriter_TallyAndPassthrough(t *testing.T) {
	var sink bytes.Buffer
	cw := &countingWriter{w: &sink}
	for _, s := range []string{"foo,", "bar\n"} {
		n, err := cw.Write([]byte(s))
		if err != nil {
			t.Fatalf("write %q: %v", s, err)
		}
		if n != len(s) {
			t.Fatalf("write %q: n=%d", s, n)
		}
	}
	if cw.n != int64(len("foo,bar\n")) {
		t.Fatalf("counter: got %d", cw.n)
	}
	if sink.String() != "foo,bar\n" {
		t.Fatalf("sink: %q", sink.String())
	}
}

type errWriter struct{ err error }

func (e errWriter) Write(p []byte) (int, error) { return 0, e.err }

func TestCountingWriter_PropagatesUnderlyingError(t *testing.T) {
	sentinel := errors.New("disk-full")
	cw := &countingWriter{w: errWriter{err: sentinel}}
	n, err := cw.Write([]byte("x"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
	if n != 0 || cw.n != 0 {
		t.Fatalf("n/count on error: n=%d count=%d", n, cw.n)
	}
}

// ===== Column projection helpers =====

func TestPtrToStr(t *testing.T) {
	if got := ptrToStr(nil); got != "" {
		t.Fatalf("nil: got %q", got)
	}
	s := "hi"
	if got := ptrToStr(&s); got != "hi" {
		t.Fatalf("populated: got %q", got)
	}
	empty := ""
	if got := ptrToStr(&empty); got != "" {
		t.Fatalf("empty string: got %q", got)
	}
}

func TestBoolStr(t *testing.T) {
	if got := boolStr(true); got != "true" {
		t.Fatalf("true: got %q", got)
	}
	if got := boolStr(false); got != "false" {
		t.Fatalf("false: got %q", got)
	}
}

func TestDateStr(t *testing.T) {
	if got := dateStr(nil); got != "" {
		t.Fatalf("nil: got %q", got)
	}
	d := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	if got := dateStr(&d); got != "2025-06-15" {
		t.Fatalf("populated: got %q", got)
	}
	// Non-UTC input rendered in UTC.
	loc := time.FixedZone("WAT", -8*3600)
	pacific := time.Date(2025, 6, 15, 22, 0, 0, 0, loc) // = 2025-06-16 06:00 UTC
	if got := dateStr(&pacific); got != "2025-06-16" {
		t.Fatalf("UTC normalization: got %q want 2025-06-16", got)
	}
}

func TestJoinUUIDs(t *testing.T) {
	if got := joinUUIDs(nil); got != "" {
		t.Fatalf("nil: got %q", got)
	}
	if got := joinUUIDs([]uuid.UUID{}); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	a := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	b := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	got := joinUUIDs([]uuid.UUID{a, b})
	want := a.String() + ";" + b.String()
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// ===== exportMetaAudit JSON shape =====

func TestExportMetaAudit_JSONEncoding(t *testing.T) {
	meta := exportMetaAudit{
		Format:    "json",
		Result:    "success",
		Reason:    "",
		RowCount:  9,
		ByteCount: 4096,
	}
	buf, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	for _, w := range []string{`"format":"json"`, `"result":"success"`, `"row_count":9`, `"byte_count":4096`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in %s", w, got)
		}
	}
	if strings.Contains(got, `"reason"`) {
		t.Errorf("empty reason should be omitted, got %s", got)
	}
}

// ===== Sanity guards =====

func TestExportVendors_Unauthorized_NoCredential(t *testing.T) {
	h := &Handler{now: time.Now}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/vendors/export", nil)
	req = req.WithContext(context.Background())
	rr := httptest.NewRecorder()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	h.ExportVendors(rr, req)
	res := rr.Result()
	if res.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status: got %d, want 401 (body=%s)", res.StatusCode, body)
	}
}
