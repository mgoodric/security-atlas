//go:build integration

// Slice 155 — integration tests for the questionnaire tracer-bullet
// HTTP API. Real Postgres + the assembled platform router so the tests
// exercise the full request path (tenancy middleware, RLS, the Store).
// The DB is never mocked.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/questionnaires/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-A1 / AC-A6 — create + list questionnaires (round-trip 201 / 200)
//	AC-A2         — import-excel parses + inserts questions
//	AC-A3         — GET .../{id} returns the questionnaire + questions + answers
//	AC-A4         — PATCH .../answers/{qid} upserts an answer
//	AC-A4         — save_to_library=true appends a canonical entry
//	AC-A5         — POST .../export-pdf returns %PDF- bytes (skipped if Chrome unavailable)
//	AC-A7         — RLS isolation: tenant A cannot see tenant B's library entries

package questionnaires_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		// Order matters — child rows first.
		for _, stmt := range []string{
			`DELETE FROM questionnaire_answers WHERE tenant_id = $1`,
			`DELETE FROM questionnaire_questions WHERE tenant_id = $1`,
			`DELETE FROM questionnaires WHERE tenant_id = $1`,
			`DELETE FROM answer_library WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func doJSON(t *testing.T, env testEnv, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, env.server.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := map[string]any{}
	dec := json.NewDecoder(resp.Body)
	_ = dec.Decode(&out)
	return resp, out
}

func makeXLSXBytes(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			_ = f.SetCellStr(sheet, cell, val)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func doMultipart(t *testing.T, env testEnv, path string, filename string, payload []byte) (*http.Response, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, env.server.URL+path, &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// ===== AC-A1 / AC-A6: create + list =====

func TestCreateAndList(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "Acme CAIQ v4.1", "source_label": "CAIQ v4.1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%v", resp.StatusCode, body)
	}
	if body["name"] != "Acme CAIQ v4.1" {
		t.Errorf("name = %v", body["name"])
	}

	resp, listBody := doJSON(t, env, http.MethodGet, "/v1/questionnaires", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d, want 200", resp.StatusCode)
	}
	list, _ := listBody["questionnaires"].([]any)
	if len(list) != 1 {
		t.Errorf("list length = %d, want 1", len(list))
	}
}

// ===== AC-A2 / AC-A3 / AC-A4: import + read + answer round-trip =====

func TestImportAnswerExportRoundTrip(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Create
	_, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "test caiq"})
	id := body["id"].(string)

	// Import 2 questions
	xlsx := makeXLSXBytes(t, [][]string{
		{"Question ID", "Question", "Domain"},
		{"IAM-02", "Do you require MFA?", "IAM"},
		{"DSI-01", "Encrypted at rest?", "DSI"},
	})
	resp, importBody := doMultipart(t, env, "/v1/questionnaires/"+id+"/import-excel", "test.xlsx", xlsx)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d, want 201; body=%v", resp.StatusCode, importBody)
	}
	added, _ := importBody["questions"].([]any)
	if len(added) != 2 {
		t.Fatalf("imported %d questions, want 2", len(added))
	}

	// GET
	resp, getBody := doJSON(t, env, http.MethodGet, "/v1/questionnaires/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", resp.StatusCode)
	}
	questions, _ := getBody["questions"].([]any)
	if len(questions) != 2 {
		t.Errorf("questions in GET = %d, want 2", len(questions))
	}

	// PATCH answer for first question (needs question id)
	first := questions[0].(map[string]any)
	qid := first["id"].(string)
	resp, ansBody := doJSON(t, env, http.MethodPatch,
		"/v1/questionnaires/"+id+"/answers/"+qid,
		map[string]any{
			"answer_value": "yes",
			"narrative":    "We enforce MFA via Okta workforce policy.",
			"authored_by":  "test-author",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH answer = %d; body=%v", resp.StatusCode, ansBody)
	}
	if ansBody["answer_value"] != "yes" {
		t.Errorf("answer_value = %v", ansBody["answer_value"])
	}
}

// ===== AC-X-5 / RLS: cross-tenant suggestion isolation =====

func TestCrossTenantSuggestionIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	// Pick an SCF anchor known to exist post slice 006. If the seed
	// hasn't run, the test best-effort tries to insert one.
	anchor := pickExistingAnchor(t, admin)
	if anchor == "" {
		t.Skip("no SCF anchor available; skipping cross-tenant suggestion test")
	}

	// Tenant A: create a questionnaire, import a question, answer with
	// save_to_library=true so an answer_library entry is created.
	_, body := doJSON(t, envA, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "tenant A"})
	qnIDA := body["id"].(string)
	xlsx := makeXLSXBytes(t, [][]string{
		{"Question ID", "Question"},
		{"X-1", "tenant A question"},
	})
	_, importBody := doMultipart(t, envA, "/v1/questionnaires/"+qnIDA+"/import-excel", "a.xlsx", xlsx)
	questions := importBody["questions"].([]any)
	qid := questions[0].(map[string]any)["id"].(string)

	_, _ = doJSON(t, envA, http.MethodPatch,
		"/v1/questionnaires/"+qnIDA+"/answers/"+qid,
		map[string]any{
			"answer_value":    "yes",
			"narrative":       "tenant A canonical answer",
			"authored_by":     "tenant-a",
			"save_to_library": true,
			"scf_anchor_id":   anchor,
			"source_label":    "tenant A test",
		})

	// Tenant A should see the suggestion.
	_, body = doJSON(t, envA, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "tenant A second"})
	qnIDA2 := body["id"].(string)
	resp, sugBody := doJSON(t, envA, http.MethodGet,
		"/v1/questionnaires/"+qnIDA2+"/suggestions?anchor="+anchor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("A suggestions = %d", resp.StatusCode)
	}
	listA, _ := sugBody["suggestions"].([]any)
	if len(listA) == 0 {
		t.Fatalf("tenant A expected to see its own suggestion, got empty")
	}

	// Tenant B must NOT see tenant A's library entry.
	_, body = doJSON(t, envB, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "tenant B"})
	qnIDB := body["id"].(string)
	resp, sugBody = doJSON(t, envB, http.MethodGet,
		"/v1/questionnaires/"+qnIDB+"/suggestions?anchor="+anchor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B suggestions = %d", resp.StatusCode)
	}
	listB, _ := sugBody["suggestions"].([]any)
	if len(listB) > 0 {
		t.Fatalf("RLS BREACH: tenant B saw %d suggestion(s) from tenant A's library", len(listB))
	}
}

func pickExistingAnchor(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	var anchor string
	err := admin.QueryRow(context.Background(),
		`SELECT id FROM scf_anchors ORDER BY id LIMIT 1`).Scan(&anchor)
	if err != nil {
		t.Logf("pickExistingAnchor: %v", err)
		return ""
	}
	return anchor
}

// ===== AC-A5: PDF export — skipped if Chrome unavailable =====

func TestExportPDF_SmokeTest(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	_, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "pdf test"})
	id := body["id"].(string)

	resp, buf := exportPDF(t, env, id)
	defer resp.Body.Close()
	assertQuestionnairePDFOrServiceUnavailable(t, resp, buf)
}

// exportPDF POSTs the export-pdf endpoint and returns the response + body.
func exportPDF(t *testing.T, env testEnv, id string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost,
		env.server.URL+"/v1/questionnaires/"+id+"/export-pdf", nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, buf
}

// assertQuestionnairePDFOrServiceUnavailable enforces the slice-475 contract:
// the export-pdf endpoint returns EITHER a real 200 %PDF- body OR a 503
// graceful degradation — NEVER a 500 / hang / other status. The old test
// t.Skip'd on 503; the contract now asserts it deterministically.
func assertQuestionnairePDFOrServiceUnavailable(t *testing.T, resp *http.Response, buf []byte) {
	t.Helper()
	switch resp.StatusCode {
	case http.StatusOK:
		if !bytes.HasPrefix(buf, []byte("%PDF-")) {
			t.Fatalf("AC-6: PDF magic byte missing; first 16: %q", string(buf[:min(16, len(buf))]))
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
			t.Errorf("AC-6: PDF Content-Type = %q, want application/pdf", ct)
		}
	case http.StatusServiceUnavailable:
		// Graceful degradation — never a 500 / hang (slice 475 AC-1).
	default:
		t.Fatalf("AC-1: export-pdf = %d, want exactly 200 or 503; body=%q",
			resp.StatusCode, string(buf[:min(64, len(buf))]))
	}
}

// TestExportPDF_RenderDeadlineDegradesTo503 is the slice-475 AC-1 proof for the
// questionnaire renderer: a tiny render deadline forces the render context past
// deadline → the handler returns 503, NOT a 500 / hang.
func TestExportPDF_RenderDeadlineDegradesTo503(t *testing.T) {
	restore := pdfrender.SetDefaultForTest(pdfrender.New(2, time.Nanosecond, time.Second))
	defer restore()

	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	_, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "deadline test"})
	id := body["id"].(string)

	resp, buf := exportPDF(t, env, id)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("AC-1: render-deadline path = %d, want 503 (never 500/hang); body=%q",
			resp.StatusCode, string(buf[:min(64, len(buf))]))
	}
}

// TestExportPDF_StressNoNonGraceful runs export-pdf Nx under a tight 1-slot cap
// + tiny deadline and asserts every response is graceful — exactly 200 or 503
// (AC-4, slice-340 stress pattern).
func TestExportPDF_StressNoNonGraceful(t *testing.T) {
	restore := pdfrender.SetDefaultForTest(pdfrender.New(1, 50*time.Millisecond, 80*time.Millisecond))
	defer restore()

	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	_, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "stress test"})
	id := body["id"].(string)

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, buf := exportPDF(t, env, id)
			defer resp.Body.Close()
			assertQuestionnairePDFOrServiceUnavailable(t, resp, buf)
		}()
	}
	wg.Wait()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ===== oversize upload =====

func TestImportExcel_OversizeRejected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	_, body := doJSON(t, env, http.MethodPost, "/v1/questionnaires",
		map[string]any{"name": "oversize"})
	id := body["id"].(string)

	// 6 MB payload — exceeds 5 MB cap.
	payload := bytes.Repeat([]byte("A"), 6*1024*1024)
	resp, _ := doMultipart(t, env, "/v1/questionnaires/"+id+"/import-excel", "big.xlsx", payload)
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversize upload = %d, want 413 or 400", resp.StatusCode)
	}
}

var _ = errors.Is // silence import when not used in all builds
var _ = strings.Contains
