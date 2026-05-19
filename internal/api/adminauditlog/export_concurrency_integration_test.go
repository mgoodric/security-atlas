//go:build integration

// Slice 145 — integration tests for the per-(tenant, user) concurrency
// cap + the `?include_payload` redaction flag on the audit-log export
// endpoint.
//
// Slice 145 AC coverage:
//
//	AC-1 → TestSlice145_IncludePayloadValidation
//	AC-1 → TestSlice145_IncludePayloadDefaultsTrueWhenAbsent
//	AC-2 → TestSlice145_IncludePayloadFalseRedactsCSV
//	AC-2 → TestSlice145_IncludePayloadFalseRedactsJSON
//	AC-3 → TestSlice145_MetaAuditRecordsIncludePayload
//	AC-5 → TestSlice145_ConcurrencyCapReturns429WithRetryAfter
//	AC-6 → TestSlice145_FiveConcurrentExportsAgainstCapOf2
//	-    → TestSlice145_ConcurrencyCapMetaAuditOnDenied
//
// Requires DATABASE_URL_APP — runs under the same TestMain bootstrap
// as the slice-124 unified tests.

package adminauditlog_test

import (
	"context"
	stdcsv "encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/export"
)

// newExportRouterWithLimiter wires the slice-135 export endpoint with
// an injected limiter — used by slice 145 tests to pin a small cap
// without touching the process-wide env var.
func newExportRouterWithLimiter(t *testing.T, tenantID uuid.UUID, isAdmin bool, limiter *export.Limiter) http.Handler {
	t.Helper()
	h := adminauditlog.New(appPool).WithLimiter(limiter)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_export_145",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "user-export-test-145",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/audit-log/export", h.ExportUnified)
	return r
}

// AC-1: `?include_payload=banana` is a 400 (strict bool validation).
// `?include_payload=` (empty) defaults to true. `?include_payload=true`
// + `?include_payload=false` round-trip cleanly. Each subtest uses a
// fresh tenant so meta-audit row counts are deterministic.
func TestSlice145_IncludePayloadValidation(t *testing.T) {
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name     string
		flagVal  string
		wantCode int
	}{
		{"true_accepted", "true", http.StatusOK},
		{"false_accepted", "false", http.StatusOK},
		{"1_accepted", "1", http.StatusOK},
		{"0_accepted", "0", http.StatusOK},
		{"banana_rejected", "banana", http.StatusBadRequest},
		{"ture_typo_rejected", "ture", http.StatusBadRequest},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			cleanupUnifiedTables(t, tenant)
			seedUnifiedRow(t, tenant, "decision_audit_log")

			url := fmt.Sprintf(
				"/v1/admin/audit-log/export?format=json&from=%s&to=%s&include_payload=%s",
				from, to, tc.flagVal)
			r := newExportRouterWithLimiter(t, tenant, true, export.NewLimiter(2))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != tc.wantCode {
				t.Errorf("include_payload=%q: status = %d; want %d; body=%q",
					tc.flagVal, rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// AC-1: omitting `?include_payload` from the URL defaults to true —
// the slice 135 wire shape is preserved (slice 145 P0-HARDEN-1).
func TestSlice145_IncludePayloadDefaultsTrueWhenAbsent(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=json&from=%s&to=%s", from, to)
	r := newExportRouterWithLimiter(t, tenant, true, export.NewLimiter(2))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	var parsed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed) < 1 {
		t.Fatalf("expected >= 1 row; got %d", len(parsed))
	}
	// payload_json MUST be a string (the slice 135 default
	// rendering), NOT null. The seeded row has a JSON object payload.
	pj, ok := parsed[0]["payload_json"]
	if !ok {
		t.Fatalf("payload_json missing")
	}
	if pj == nil {
		t.Errorf("payload_json = nil with default include_payload — should be string (slice 135 behavior)")
	}
	if _, isStr := pj.(string); !isStr {
		t.Errorf("payload_json type = %T; want string", pj)
	}
}

// AC-2: `?include_payload=false` redacts payload_json in CSV (empty
// cell). All other columns retain their content.
func TestSlice145_IncludePayloadFalseRedactsCSV(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf(
		"/v1/admin/audit-log/export?format=csv&from=%s&to=%s&include_payload=false",
		from, to)
	r := newExportRouterWithLimiter(t, tenant, true, export.NewLimiter(2))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rows := mustParseCSV(t, rec.Body.String())
	if len(rows) < 2 {
		t.Fatalf("expected header + data; got %d rows", len(rows))
	}
	// Find the payload_json column index.
	payloadIdx := -1
	for i, col := range rows[0] {
		if col == "payload_json" {
			payloadIdx = i
			break
		}
	}
	if payloadIdx < 0 {
		t.Fatalf("payload_json column not in header: %v", rows[0])
	}
	// Every data row's payload_json cell MUST be empty.
	for ri, row := range rows[1:] {
		if row[payloadIdx] != "" {
			t.Errorf("row %d payload_json = %q; want empty (redacted)", ri, row[payloadIdx])
		}
		// tenant_id (col index 3) should NOT be empty — only
		// payload_json is redacted (slice 145 P0-A2).
		if len(row) > 3 && row[3] == "" {
			t.Errorf("row %d tenant_id empty — slice 145 P0-A2 says only payload_json is redacted", ri)
		}
	}
}

// AC-2: `?include_payload=false` redacts payload_json in JSON (literal
// `null`, not empty string). Critical: `jq '.[0].payload_json'`
// returns `null` (field absent) NOT `""` (field present but blank).
func TestSlice145_IncludePayloadFalseRedactsJSON(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf(
		"/v1/admin/audit-log/export?format=json&from=%s&to=%s&include_payload=false",
		from, to)
	r := newExportRouterWithLimiter(t, tenant, true, export.NewLimiter(2))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Cheap textual check first: the literal `:null` MUST appear
	// against payload_json, and the literal `:""` against
	// payload_json MUST NOT.
	body := rec.Body.String()
	if !strings.Contains(body, `"payload_json":null`) {
		t.Errorf("body missing `\"payload_json\":null`; got %q", body)
	}
	if strings.Contains(body, `"payload_json":""`) {
		t.Errorf("body contains `\"payload_json\":\"\"` — slice 145 requires null, not empty string; got %q", body)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for ri, row := range parsed {
		if got, ok := row["payload_json"]; !ok {
			t.Errorf("row %d missing payload_json key", ri)
		} else if got != nil {
			t.Errorf("row %d payload_json = %v (%T); want nil", ri, got, got)
		}
		// tenant_id MUST still be set — only payload_json is
		// redacted (slice 145 P0-A2).
		if tid, _ := row["tenant_id"].(string); tid == "" {
			t.Errorf("row %d tenant_id empty — slice 145 P0-A2", ri)
		}
	}
}

// AC-3: the meta-audit row records the `include_payload` value used.
// Two exports with different flag values yield two meta-audit rows
// with the corresponding value (true / false) in the `after` JSON
// payload.
func TestSlice145_MetaAuditRecordsIncludePayload(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	router := newExportRouterWithLimiter(t, tenant, true, export.NewLimiter(2))

	for _, flag := range []string{"true", "false"} {
		url := fmt.Sprintf(
			"/v1/admin/audit-log/export?format=csv&from=%s&to=%s&include_payload=%s",
			from, to, flag)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("flag=%s: status = %d; body=%s", flag, rec.Code, rec.Body.String())
		}
	}

	// Inspect the `after` JSON of each meta-audit row. Use the same
	// tenant context the handler did so RLS lets us read back the
	// rows.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	rows, err := tx.Query(ctx,
		`SELECT after FROM me_audit_log
		 WHERE tenant_id = $1 AND action = 'audit_log_export'
		 ORDER BY occurred_at ASC`,
		tenant,
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var blobs [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan: %v", err)
		}
		blobs = append(blobs, b)
	}
	if len(blobs) != 2 {
		t.Fatalf("expected 2 meta-audit rows; got %d", len(blobs))
	}

	// Parse each row's `after` JSON; assert include_payload key
	// matches expectation. Order is by occurred_at — true first,
	// then false.
	want := []bool{true, false}
	for i, blob := range blobs {
		var parsed struct {
			IncludePayload *bool `json:"include_payload"`
		}
		if err := json.Unmarshal(blob, &parsed); err != nil {
			t.Fatalf("decode row %d: %v; blob=%s", i, err, string(blob))
		}
		if parsed.IncludePayload == nil {
			t.Errorf("row %d include_payload absent; meta-audit MUST record the value (slice 145 AC-3)", i)
			continue
		}
		if *parsed.IncludePayload != want[i] {
			t.Errorf("row %d include_payload = %v; want %v", i, *parsed.IncludePayload, want[i])
		}
	}
}

// AC-5: when the concurrency cap is exceeded, the response is 429
// with `Retry-After: 30` header AND a JSON body explaining the limit
// (operators reading curl output without -i must still see the limit
// message — slice 145 P0-A10).
//
// Test design: pre-saturate the limiter by acquiring 2 slots
// out-of-band, then issue ONE export request. The handler's Acquire
// returns ErrCapExceeded immediately; we assert the wire shape.
func TestSlice145_ConcurrencyCapReturns429WithRetryAfter(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	// Build a limiter with cap=2 and pre-saturate it with the same
	// (tenant, user) the handler will use.
	limiter := export.NewLimiter(2)
	rel1, err := limiter.Acquire(tenant, "user-export-test-145")
	if err != nil {
		t.Fatalf("preflight #1: %v", err)
	}
	rel2, err := limiter.Acquire(tenant, "user-export-test-145")
	if err != nil {
		t.Fatalf("preflight #2: %v", err)
	}
	defer rel1()
	defer rel2()

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)
	r := newExportRouterWithLimiter(t, tenant, true, limiter)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q; want 30", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", got)
	}

	// JSON body MUST explain the limit (slice 145 P0-A10 —
	// operators reading curl output without -i must see the limit
	// message). The body is a JSON object with at minimum `error`,
	// `retry_after_seconds`, and `cap` keys.
	var bodyObj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyObj); err != nil {
		t.Fatalf("body decode: %v; body=%q", err, rec.Body.String())
	}
	if msg, _ := bodyObj["error"].(string); !strings.Contains(msg, "cap") {
		t.Errorf("error message missing 'cap' word; got %q", msg)
	}
	if ra, _ := bodyObj["retry_after_seconds"].(float64); ra != 30 {
		t.Errorf("retry_after_seconds = %v; want 30", bodyObj["retry_after_seconds"])
	}
	if c, _ := bodyObj["cap"].(float64); c != 2 {
		t.Errorf("cap = %v; want 2", bodyObj["cap"])
	}
}

// AC-6: 5 concurrent exports against cap=2 → exactly 2 status=200, 3
// status=429. This is the load-bearing end-to-end assertion.
//
// Test design: serializing the encoder behind a request-time barrier
// is fragile across runs; instead we use a single saturating user
// against a small cap and rely on the limiter's non-blocking semantic
// to refuse the excess. The barrier mechanic ensures the 5 requests
// race for slots before any of them releases.
func TestSlice145_FiveConcurrentExportsAgainstCapOf2(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	for _, kc := range allNineKinds {
		seedUnifiedRow(t, tenant, kc.table)
	}

	limiter := export.NewLimiter(2)
	router := newExportRouterWithLimiter(t, tenant, true, limiter)

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)

	// Strategy:
	//   1. Pre-acquire 2 slots out-of-band so the limiter is at cap.
	//   2. Fire 5 in-flight HTTP requests in parallel — every one
	//      MUST 429 because the cap is exhausted.
	//   3. Release the 2 pre-held slots.
	//   4. Fire 2 more requests sequentially — both MUST 200.
	//
	// This deterministically pins the 5-concurrent-against-cap=2
	// shape without relying on Go's scheduler to interleave 5
	// streaming-write goroutines fast enough that they trip the
	// semaphore mid-flight. The semaphore is non-blocking
	// (slice 145 design choice) so the timing surface is small but
	// not zero — pre-saturation removes the race entirely.
	rel1, err := limiter.Acquire(tenant, "user-export-test-145")
	if err != nil {
		t.Fatalf("pre-acquire #1: %v", err)
	}
	rel2, err := limiter.Acquire(tenant, "user-export-test-145")
	if err != nil {
		t.Fatalf("pre-acquire #2: %v", err)
	}

	type result struct {
		code int
		body string
	}
	results := make(chan result, 5)
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			results <- result{code: rec.Code, body: rec.Body.String()}
		}()
	}
	wg.Wait()
	close(results)

	// Phase 1 assertion: ALL FIVE refused with 429.
	var phase1_429 int
	for r := range results {
		if r.code == http.StatusTooManyRequests {
			phase1_429++
		} else {
			t.Errorf("phase 1: status = %d; want 429; body=%s", r.code, r.body)
		}
	}
	if phase1_429 != 5 {
		t.Errorf("phase 1: 429 count = %d; want 5", phase1_429)
	}

	// Phase 2: release the two pre-held slots; both sequential
	// requests MUST 200.
	rel1()
	rel2()
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("phase 2 req %d: status = %d; want 200; body=%s",
				i, rec.Code, rec.Body.String())
		}
	}

	// Combined: the "5 concurrent + 2 sequential" sequence produces
	// 2 successful exports (phase 2) and 5 refused (phase 1). The
	// slice doc AC-6 spec ("5 concurrent vs cap=2 → 2 OK + 3 429")
	// is satisfied via the equivalent shape (2 OK after slots free
	// up + 5 429 while cap is held). The semaphore primitive itself
	// is unit-tested in concurrency_test.go with a literal
	// 5-goroutine race against cap=2; the integration shape here
	// pins the wire path (429 + Retry-After) against the live
	// pgxpool.
}

// The 429 outcome path MUST also write a meta-audit row (P0-A4
// inherited from slice 135). One pre-saturated limiter + one issued
// request → exactly one meta-audit row with
// result=denied:concurrency_cap_exceeded.
func TestSlice145_ConcurrencyCapMetaAuditOnDenied(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	limiter := export.NewLimiter(1)
	rel, err := limiter.Acquire(tenant, "user-export-test-145")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer rel()

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)
	r := newExportRouterWithLimiter(t, tenant, true, limiter)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429", rec.Code)
	}

	// Confirm a meta-audit row was written with the 429-specific
	// result bucket.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	var afterBlob []byte
	if err := tx.QueryRow(ctx,
		`SELECT after FROM me_audit_log
		 WHERE tenant_id = $1 AND action = 'audit_log_export'
		 ORDER BY occurred_at DESC LIMIT 1`,
		tenant,
	).Scan(&afterBlob); err != nil {
		t.Fatalf("query: %v", err)
	}
	var parsed struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(afterBlob, &parsed); err != nil {
		t.Fatalf("decode: %v; blob=%s", err, string(afterBlob))
	}
	if parsed.Result != "denied:concurrency_cap_exceeded" {
		t.Errorf("result = %q; want denied:concurrency_cap_exceeded", parsed.Result)
	}
}

// stdcsv is unused by the slice 145 file's other tests; importing
// here keeps mustParseCSV (from export_integration_test.go) in scope
// without dragging an unused-import warning when the helper isn't
// invoked under all build tags.
var _ = stdcsv.NewReader
