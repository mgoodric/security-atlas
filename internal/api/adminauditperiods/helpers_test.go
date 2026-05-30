// Slice 313 — pure-Go unit tests for adminauditperiods helpers.
//
// Load-bearing functions covered (per AC-3 of slice 313):
//
//   - parseFormat           : empty / valid / invalid query param branches
//   - auditPeriodsExportHeader : canonical column-order pin
//   - periodsToRowIter      : open + frozen + early-yield-stop branches
//   - callerAllowedExport   : admin / auditor / grc_engineer / unauthorized
//   - exportLimiter         : default singleton + WithLimiter override
//   - writeError            : Content-Type, status code, body shape
//   - countingWriter.Write  : per-write accumulation + error passthrough
//   - WithLimiter           : returns receiver and stores reference
//
// Per the slice 290 unit/integration split rule: integration tests cover
// the DB-touching paths in export_integration_test.go; this file covers
// the pure-Go helpers reachable without a Postgres pool. Together they
// lift the package above the AC-2 70% merged-coverage floor.

package adminauditperiods

import (
	"bytes"
	"context"
	"encoding/hex"
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
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/export"
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
		{name: "csv lowercase", raw: "csv", wantFmt: export.FormatCSV, wantErr: false},
		{name: "json lowercase", raw: "json", wantFmt: export.FormatJSON, wantErr: false},
		{name: "xlsx lowercase", raw: "xlsx", wantFmt: export.FormatXLSX, wantErr: false},
		{name: "csv mixed case", raw: "Csv", wantFmt: export.FormatCSV, wantErr: false},
		{name: "json upper case", raw: "JSON", wantFmt: export.FormatJSON, wantErr: false},
		{name: "unsupported format", raw: "pdf", wantErr: true},
		{name: "garbage", raw: "not-a-format", wantErr: true},
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
					t.Fatalf("expected error for raw=%q, got nil (format=%q)", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for raw=%q: %v", tc.raw, err)
			}
			if got != tc.wantFmt {
				t.Fatalf("raw=%q: got %v, want %v", tc.raw, got, tc.wantFmt)
			}
		})
	}
}

// ===== auditPeriodsExportHeader =====

func TestAuditPeriodsExportHeader_StableOrder(t *testing.T) {
	want := []string{
		"id",
		"name",
		"framework_version_id",
		"period_start",
		"period_end",
		"status",
		"frozen_at",
		"frozen_by",
		"frozen_hash",
		"created_by",
		"created_at",
		"updated_at",
	}
	got := auditPeriodsExportHeader()
	if len(got) != len(want) {
		t.Fatalf("header length: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("column %d: got %q, want %q", i, got[i], w)
		}
	}
}

// ===== periodsToRowIter =====

func TestPeriodsToRowIter_OpenAndFrozen(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	fvID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 3, 31, 0, 0, 0, 0, time.UTC)
	frozenAt := time.Date(2025, 4, 1, 12, 30, 0, 0, time.UTC)
	frozenHash := []byte{0xde, 0xad, 0xbe, 0xef}

	periods := []period.Period{
		// Open period — no frozen_*
		{
			ID:                 id1,
			Name:               "Open Period",
			FrameworkVersionID: fvID,
			PeriodStart:        start,
			PeriodEnd:          end,
			Status:             period.StatusOpen,
			CreatedBy:          "user-a",
			CreatedAt:          start,
			UpdatedAt:          start,
		},
		// Frozen period — frozen_* populated
		{
			ID:                 id2,
			Name:               "Frozen Period",
			FrameworkVersionID: fvID,
			PeriodStart:        start,
			PeriodEnd:          end,
			Status:             period.StatusFrozen,
			FrozenAt:           &frozenAt,
			FrozenHash:         frozenHash,
			FrozenBy:           "user-b",
			CreatedBy:          "user-a",
			CreatedAt:          start,
			UpdatedAt:          frozenAt,
		},
	}

	var got [][]string
	for row := range periodsToRowIter(periods) {
		got = append(got, append([]string(nil), row...))
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}

	// Open row — frozen_* are empty strings
	if got[0][0] != id1.String() {
		t.Fatalf("row[0][id]: got %q, want %q", got[0][0], id1.String())
	}
	if got[0][5] != "open" {
		t.Fatalf("row[0][status]: got %q, want %q", got[0][5], "open")
	}
	if got[0][6] != "" {
		t.Fatalf("row[0][frozen_at] open period: got %q, want empty", got[0][6])
	}
	if got[0][7] != "" {
		t.Fatalf("row[0][frozen_by] open period: got %q, want empty", got[0][7])
	}
	if got[0][8] != "" {
		t.Fatalf("row[0][frozen_hash] open period: got %q, want empty", got[0][8])
	}

	// Frozen row — frozen_* populated
	if got[1][5] != "frozen" {
		t.Fatalf("row[1][status]: got %q, want %q", got[1][5], "frozen")
	}
	if got[1][6] == "" {
		t.Fatalf("row[1][frozen_at]: expected RFC3339, got empty")
	}
	if got[1][7] != "user-b" {
		t.Fatalf("row[1][frozen_by]: got %q, want %q", got[1][7], "user-b")
	}
	wantHash := hex.EncodeToString(frozenHash)
	if got[1][8] != wantHash {
		t.Fatalf("row[1][frozen_hash]: got %q, want %q", got[1][8], wantHash)
	}
}

func TestPeriodsToRowIter_EarlyStop(t *testing.T) {
	id := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	fvID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	now := time.Now().UTC()
	periods := []period.Period{
		{ID: id, FrameworkVersionID: fvID, PeriodStart: now, PeriodEnd: now, Status: period.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: id, FrameworkVersionID: fvID, PeriodStart: now, PeriodEnd: now, Status: period.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: id, FrameworkVersionID: fvID, PeriodStart: now, PeriodEnd: now, Status: period.StatusOpen, CreatedAt: now, UpdatedAt: now},
	}
	// Stop after first row — the yield-false early-return branch.
	count := 0
	for range periodsToRowIter(periods) {
		count++
		if count >= 1 {
			break
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row before break, got %d", count)
	}
}

func TestPeriodsToRowIter_EmptySlice(t *testing.T) {
	count := 0
	for range periodsToRowIter(nil) {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 rows from nil slice, got %d", count)
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
		{name: "auditor among others", cred: credstore.Credential{OwnerRoles: []string{"viewer", "auditor"}}, want: true},
		{name: "non-allowed role only", cred: credstore.Credential{OwnerRoles: []string{"viewer"}}, want: false},
		{name: "no roles + not admin", cred: credstore.Credential{}, want: false},
		{name: "empty role string", cred: credstore.Credential{OwnerRoles: []string{""}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callerAllowedExport(tc.cred)
			if got != tc.want {
				t.Fatalf("got %v, want %v (cred=%+v)", got, tc.want, tc.cred)
			}
		})
	}
}

// ===== exportLimiter + WithLimiter =====

func TestHandler_WithLimiter_ReturnsReceiver(t *testing.T) {
	h := &Handler{}
	got := h.WithLimiter(export.NewLimiter(3))
	if got != h {
		t.Fatalf("WithLimiter should return the receiver pointer for chaining")
	}
	if h.limiter == nil {
		t.Fatalf("WithLimiter did not install limiter")
	}
	if h.limiter.Cap() != 3 {
		t.Fatalf("limiter cap: got %d, want 3", h.limiter.Cap())
	}
}

func TestHandler_ExportLimiter_FallbackToDefault(t *testing.T) {
	h := &Handler{}
	// No WithLimiter call -> exportLimiter returns the default singleton.
	got := h.exportLimiter()
	if got == nil {
		t.Fatalf("exportLimiter should return a non-nil limiter by default")
	}
	// Sanity: cap > 0 (DefaultLimiter clamps via NewLimiter).
	if got.Cap() < 1 {
		t.Fatalf("default limiter cap should be >= 1, got %d", got.Cap())
	}
}

func TestHandler_ExportLimiter_HonorsOverride(t *testing.T) {
	override := export.NewLimiter(7)
	h := &Handler{}
	h.WithLimiter(override)
	got := h.exportLimiter()
	if got != override {
		t.Fatalf("exportLimiter should return the WithLimiter-installed limiter")
	}
	if got.Cap() != 7 {
		t.Fatalf("override cap: got %d, want 7", got.Cap())
	}
}

// ===== writeError =====

func TestWriteError(t *testing.T) {
	rr := httptest.NewRecorder()
	httpresp.WriteError(rr, http.StatusBadRequest, "boom")
	res := rr.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type: got %q, want application/json", got)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "boom" {
		t.Fatalf("body: got %+v, want error=boom", body)
	}
}

// ===== countingWriter =====

func TestCountingWriter_TallyAndPassthrough(t *testing.T) {
	var sink bytes.Buffer
	cw := &countingWriter{w: &sink}
	for _, s := range []string{"hello ", "world", "\n"} {
		n, err := cw.Write([]byte(s))
		if err != nil {
			t.Fatalf("write %q: %v", s, err)
		}
		if n != len(s) {
			t.Fatalf("write %q: n=%d, want %d", s, n, len(s))
		}
	}
	if cw.n != int64(len("hello world\n")) {
		t.Fatalf("n: got %d, want %d", cw.n, len("hello world\n"))
	}
	if sink.String() != "hello world\n" {
		t.Fatalf("sink: got %q", sink.String())
	}
}

// errWriter returns a fixed error on Write — used to verify error passthrough.
type errWriter struct{ err error }

func (e errWriter) Write(p []byte) (int, error) { return 0, e.err }

func TestCountingWriter_PropagatesUnderlyingError(t *testing.T) {
	sentinel := errors.New("disk full")
	cw := &countingWriter{w: errWriter{err: sentinel}}
	n, err := cw.Write([]byte("anything"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("n on error: got %d, want 0", n)
	}
	if cw.n != 0 {
		t.Fatalf("counter on error: got %d, want 0", cw.n)
	}
}

// ===== exportMetaAudit JSON shape =====

// TestExportMetaAudit_JSONEncoding pins the wire shape of the meta-audit
// payload so a downstream consumer reading me_audit_log.after does not
// silently break on a field rename. The shape mirrors slice 135 minus
// the audit-log-specific filter columns.
func TestExportMetaAudit_JSONEncoding(t *testing.T) {
	meta := exportMetaAudit{
		Format:    "csv",
		Result:    "success",
		Reason:    "",
		RowCount:  3,
		ByteCount: 1024,
	}
	buf, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	wantParts := []string{
		`"format":"csv"`,
		`"result":"success"`,
		`"row_count":3`,
		`"byte_count":1024`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	// reason is omitempty when empty
	if strings.Contains(got, `"reason"`) {
		t.Errorf("empty reason should be omitted, got %s", got)
	}

	// Populated reason renders.
	meta.Reason = "x"
	buf2, _ := json.Marshal(meta)
	if !strings.Contains(string(buf2), `"reason":"x"`) {
		t.Errorf("non-empty reason should render, got %s", string(buf2))
	}
}

// ===== Sanity guards =====

// TestExportAuditPeriods_Unauthorized_NoCredential exercises the
// pre-DB unauthenticated branch — the handler returns 500 without
// any DB I/O because the credential is missing. (The post-auth 401
// branch is also exercised by integration tests; this unit test pins
// the no-credential-context shape.)
func TestExportAuditPeriods_Unauthorized_NoCredential(t *testing.T) {
	// Empty Handler — no pool, no store. We never reach the pool because
	// the credential check short-circuits first.
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/audit-periods/export", nil)
	req = req.WithContext(context.Background())
	rr := httptest.NewRecorder()

	// Recover from the nil-store dereference if the credential gate fails
	// to short-circuit — the test is then a clear regression signal.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic — credential check did not short-circuit: %v", r)
		}
	}()
	h.ExportAuditPeriods(rr, req)

	res := rr.Result()
	if res.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status: got %d, want 401 (body=%s)", res.StatusCode, body)
	}
}
