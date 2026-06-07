//go:build integration

// Slice 477 — integration tests for the walkthrough PDF export degradation
// contract. Real Postgres + the assembled platform router so the test exercises
// the full request path (tenancy middleware, RLS, the walkthrough.Store, and
// walkthrough.RenderPDF routed through the shared pdfrender.Limiter). The DB is
// never mocked.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/walkthroughs/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// The load-bearing test is TestPDF_RenderDeadlineDegradesTo503: it mirrors the
// slice-475 board/questionnaire proof. Before slice 477 a walkthrough render
// that exceeded its 45s deadline returned a wrapped context.DeadlineExceeded
// that did NOT match walkthrough.ErrChromeUnavailable, so it fell through to a
// 500. Routing the render through the shared pdfrender.Limiter now classifies
// that case as pdfrender.ErrRenderDeadline → a deterministic 503.

package walkthroughs_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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
		for _, stmt := range []string{
			`DELETE FROM walkthrough_audit_log WHERE tenant_id = $1`,
			`DELETE FROM walkthrough_attachments WHERE tenant_id = $1`,
			`DELETE FROM walkthroughs WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'slice 477 walkthrough test control', 'AAA', 'automated', 'test-bundle-477')
	`, ctrlID, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedWalkthrough creates a draft walkthrough via the real store (RLS-enforced
// app pool) and returns its id. The handler renders this walkthrough to PDF.
func seedWalkthrough(t *testing.T, app *pgxpool.Pool, tenant string, ctrlID uuid.UUID) string {
	t.Helper()
	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	w, err := store.Create(ctxFor(t, tenant), walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "Slice 477 walkthrough PDF degradation test. The team rotates keys every 90 days.",
		CreatedBy: "key_test_477",
	})
	if err != nil {
		t.Fatalf("seed walkthrough: %v", err)
	}
	return w.ID.String()
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	// Owner bearer — Export is a read path, any role with tenant context works.
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func doRaw(t *testing.T, env testEnv, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, raw
}

// assertPDFOrServiceUnavailable enforces the slice-475/477 contract: the
// walkthrough PDF export returns EITHER a real 200 %PDF- body OR a 503 graceful
// degradation — and NEVER a 500 / hang / any other status. The 503 path is the
// documented, exhaustive non-200 outcome (chrome absent OR render deadline
// exceeded OR queue saturated), so a third status here is a hard failure.
func assertPDFOrServiceUnavailable(t *testing.T, resp *http.Response, raw []byte) {
	t.Helper()
	switch resp.StatusCode {
	case http.StatusOK:
		if len(raw) < 5 || string(raw[:5]) != "%PDF-" {
			t.Errorf("200 body is not a PDF (prefix=%q)", safePrefix(raw))
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
			t.Errorf("PDF Content-Type = %q, want application/pdf", ct)
		}
	case http.StatusServiceUnavailable:
		// Graceful degradation — the handler 503s rather than 500ing or
		// hanging (slice 475 AC-1, fanned out by slice 477).
	default:
		t.Fatalf("AC-1: GET export?format=pdf = %d, want exactly 200 or 503; body=%q",
			resp.StatusCode, raw)
	}
}

func safePrefix(b []byte) string {
	if len(b) > 16 {
		return string(b[:16])
	}
	return string(b)
}

func exportPDFPath(id string) string {
	return "/v1/walkthroughs/" + id + "/export?format=pdf"
}

// ----- tests -----

// TestPDF_ReturnsPDFOrServiceUnavailable is the baseline contract: under the
// real (default) limiter, the walkthrough PDF export is 200 (chrome present) or
// 503 (chrome absent) — never a 500.
func TestPDF_ReturnsPDFOrServiceUnavailable(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	wtID := seedWalkthrough(t, app, tenant, ctrlID)
	env := testServer(t, app, tenant)

	resp, raw := doRaw(t, env, exportPDFPath(wtID))
	assertPDFOrServiceUnavailable(t, resp, raw)
}

// TestPDF_RenderDeadlineDegradesTo503 is the load-bearing slice-477 proof
// (mirrors slice-475 board.TestPDF_RenderDeadlineDegradesTo503): when the
// bounded render deadline elapses (here forced to 1ns so even a healthy chrome
// — or no chrome — exceeds it), the endpoint returns 503, NOT a 500 and NOT a
// hang. We swap in a 1ns-deadline limiter so the render context is already past
// deadline before any chrome work can finish.
func TestPDF_RenderDeadlineDegradesTo503(t *testing.T) {
	restore := pdfrender.SetDefaultForTest(pdfrender.New(2, time.Nanosecond, time.Second))
	defer restore()

	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	wtID := seedWalkthrough(t, app, tenant, ctrlID)
	env := testServer(t, app, tenant)

	resp, raw := doRaw(t, env, exportPDFPath(wtID))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("AC-1: render-deadline path = %d, want 503 (never 500/hang); body=%q",
			resp.StatusCode, raw)
	}
}

// TestPDF_QueueSaturationDegradesTo503 proves a burst over the concurrency cap
// degrades to 503: a 1-slot, fail-fast (0 queue-wait) limiter means a
// concurrent request is rejected with 503, never 500. With a 1-slot fail-fast
// cap, AT LEAST one of the concurrent renders saturates → 503 and NONE is a 500.
func TestPDF_QueueSaturationDegradesTo503(t *testing.T) {
	restore := pdfrender.SetDefaultForTest(pdfrender.New(1, 5*time.Second, 0))
	defer restore()

	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	wtID := seedWalkthrough(t, app, tenant, ctrlID)
	env := testServer(t, app, tenant)

	const n = 4
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, raw := doRaw(t, env, exportPDFPath(wtID))
			statuses[idx] = resp.StatusCode
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("concurrent render %d = %d, want 200 or 503; body=%q",
					idx, resp.StatusCode, raw)
			}
		}(i)
	}
	wg.Wait()

	sawSaturation := false
	for _, s := range statuses {
		if s == http.StatusServiceUnavailable {
			sawSaturation = true
		}
	}
	if !sawSaturation {
		t.Fatalf("expected at least one 503 under a 1-slot fail-fast cap; got %v", statuses)
	}
}

// TestPDF_StressNoNonGraceful runs the PDF endpoint Nx under simulated
// contention (a tight 1-slot cap + tiny render deadline) and asserts EVERY
// response is graceful — exactly 200 or 503, never a 500 / other status / hang
// (slice-340 stress pattern).
func TestPDF_StressNoNonGraceful(t *testing.T) {
	restore := pdfrender.SetDefaultForTest(pdfrender.New(1, 50*time.Millisecond, 80*time.Millisecond))
	defer restore()

	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	wtID := seedWalkthrough(t, app, tenant, ctrlID)
	env := testServer(t, app, tenant)

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, raw := doRaw(t, env, exportPDFPath(wtID))
			assertPDFOrServiceUnavailable(t, resp, raw)
		}()
	}
	wg.Wait()
}
