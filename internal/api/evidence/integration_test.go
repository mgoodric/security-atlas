//go:build integration

// OE-426 — integration test pinning the API-boundary contract when the
// platform's Postgres connection pool is saturated.
//
// Background: slice 354 (docs/audit-log/354-db-pool-exhaustion-execution-
// decisions.md D8) found that NOTHING in the test suite asserts what the
// platform does when its own pgx pool is exhausted. Slice 335's Experiment 1
// design CLAIMED writes fail fast with a structured 4xx carrying a
// retry_after hint; the chaos run could never verify that because its
// injection saturated the server's connection slots, not the platform pool.
//
// This suite saturates the platform pool FROM THE INSIDE — a pool
// constrained to smallPoolMax connections, every slot held by the test —
// and drives more concurrent requests than the pool can serve. What it
// pins is the OBSERVED contract, not slice 335's aspiration (the
// divergence is recorded in docs/audit-log/426-pool-saturation-
// integration-test-decisions.md):
//
//   - Requests do NOT fail fast. Pool acquisition queues until the
//     request context is done; the failure below is forced by a bounded
//     per-request deadline, exactly as a timing-out caller would.
//   - The read path (GET /v1/evidence) returns 500 with the slice-367
//     generic body {"error":"internal error","request_id":"..."} — a
//     structured shape, but a 5xx, not the claimed 4xx.
//   - The write path (POST /v1/evidence:push) returns 500 with the
//     structured errorBody shape carrying code "internal_error" and the
//     rejected_internal_error decision token.
//   - Neither path emits a Retry-After header or a retry_after hint.
//   - Neither path leaks a stack trace, DSN component, credential, or
//     file path into the response body.
//   - The append-only ledger is untouched by saturated writes, and both
//     paths recover to their success status the moment slots free up.
//
// Determinism (integration tier has NO retry — a flake is a hard fail):
// saturation is not a race. The test holds every pool slot for the whole
// assertion window, so a saturated request can never acquire regardless
// of scheduling; its context deadline is a bound, not a sleep. Baseline
// and recovery requests run against a fully released pool.
package evidence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/controldetail"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

const (
	// smallPoolMax is the constrained pool size N. The pool needs no
	// production seam to be constrained: pgxpool.Config.MaxConns is
	// settable at construction, which is exactly how cmd/atlas would
	// bound it via pool_max_conns in DATABASE_URL_APP.
	smallPoolMax = 2

	// saturatedRequestTimeout bounds each request issued against the
	// saturated pool. It is the caller's patience, not a test sleep:
	// with every slot held, acquisition can NEVER succeed, so the
	// deadline firing is guaranteed, not raced.
	saturatedRequestTimeout = 1 * time.Second

	// harnessTimeout bounds every harness-side operation (dial,
	// saturation acquires, baseline/recovery requests) so a broken DB
	// fails fast instead of hanging the suite.
	harnessTimeout = 15 * time.Second
)

// newSmallPool opens an app-role (RLS-enforcing) pool capped at maxConns.
// dbtest.NewAppPool is not used because it offers no pool-size control;
// this helper preserves its skip contract and cleanup shape.
func newSmallPool(t *testing.T, maxConns int32) (*pgxpool.Pool, *pgxpool.Config) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig: %v", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	ctx, cancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, cfg
}

// saturatePool acquires every slot of the pool and returns an
// idempotent release func (also registered as a cleanup, so a failing
// assertion cannot leave connections held — the bounded-test AC).
func saturatePool(t *testing.T, pool *pgxpool.Pool, n int32) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer cancel()
	conns := make([]*pgxpool.Conn, 0, n)
	var once sync.Once
	release := func() {
		once.Do(func() {
			for _, c := range conns {
				c.Release()
			}
		})
	}
	t.Cleanup(release)
	for i := int32(0); i < n; i++ {
		c, err := pool.Acquire(ctx)
		if err != nil {
			release()
			t.Fatalf("saturatePool: acquire %d/%d: %v", i+1, n, err)
		}
		conns = append(conns, c)
	}
	return release
}

// assertNoLeakage asserts the response body carries none of the
// substrings a pool-acquisition failure could smuggle to the client:
// stack-trace markers, Go file references, DSN components, or the
// connection credentials the pool was built from (CWE-209).
func assertNoLeakage(t *testing.T, body string, cfg *pgxpool.Config) {
	t.Helper()
	needles := []string{
		"goroutine ", // stack trace header
		".go:",       // file:line frame
		"runtime.",   // runtime frames
		"postgres://",
		"postgresql://",
		"SQLSTATE",
		"/Users/",
		"/home/",
	}
	cc := cfg.ConnConfig
	// DSN components. Skip trivially short values that could collide
	// with ordinary response prose by coincidence.
	for _, v := range []string{cc.User, cc.Password, cc.Database, cc.Host} {
		if len(v) >= 4 {
			needles = append(needles, v)
		}
	}
	for _, n := range needles {
		if strings.Contains(body, n) {
			t.Errorf("response body leaks %q: %s", n, body)
		}
	}
}

// assertNoRetryHint pins the divergence from slice 335's expected
// outcome: NO Retry-After header and NO retry_after body field is
// emitted on pool saturation. (The push path's rate limiter does send
// Retry-After on 429 — that is a different, unsaturated code path.)
func assertNoRetryHint(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if got := rr.Header().Get("Retry-After"); got != "" {
		t.Errorf("unexpected Retry-After header %q — slice 335 divergence log needs updating", got)
	}
	if strings.Contains(rr.Body.String(), "retry_after") {
		t.Errorf("unexpected retry_after hint in body — slice 335 divergence log needs updating: %s", rr.Body.String())
	}
}

// ---- read path: GET /v1/evidence ----------------------------------------

// readRouter wires the evidence-ledger read handler behind the same
// credential + tenancy middleware production mounts, with a control_owner
// credential so the requireControlRead guard admits it.
func readRouter(pool *pgxpool.Pool, tenant string) http.Handler {
	h := controldetail.New(controldetail.NewStore(pool))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "key_oe426_read",
				TenantID:   tenant,
				UserID:     "oe426-reader",
				OwnerRoles: []string{"control_owner"},
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/evidence", h.Evidence)
	return r
}

func TestEvidenceRead_PoolSaturation_APIBoundaryContract(t *testing.T) {
	pool, cfg := newSmallPool(t, smallPoolMax)
	tenant := uuid.NewString() // pure reads under a fresh tenant: no rows, no cleanup needed
	router := readRouter(pool, tenant)

	get := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/evidence", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	// Baseline: the read serves 200 through the constrained pool.
	baseCtx, baseCancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer baseCancel()
	if rr := get(baseCtx); rr.Code != http.StatusOK {
		t.Fatalf("baseline GET /v1/evidence: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}

	release := saturatePool(t, pool, smallPoolMax)

	// Drive more concurrent requests than the pool has slots. Every one
	// of them queues on acquisition and fails at its context deadline.
	const inflight = smallPoolMax + 1
	results := make([]*httptest.ResponseRecorder, inflight)
	var wg sync.WaitGroup
	for i := 0; i < inflight; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), saturatedRequestTimeout)
			defer cancel()
			results[i] = get(ctx)
		}(i)
	}
	wg.Wait()

	for i, rr := range results {
		// OBSERVED contract: 500, NOT the 4xx slice 335 claimed. See the
		// slice-426 decisions log for the divergence record. Do not
		// "fix" this assertion to 4xx without changing the handler —
		// that change is a separate slice (issue boundary).
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("saturated GET %d: status %d, want 500 (observed contract)", i, rr.Code)
		}
		// Body shape: the slice-367 generic-5xx envelope, structured.
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("saturated GET %d: body is not JSON: %v (body=%q)", i, err, rr.Body.String())
		}
		if body["error"] != "internal error" {
			t.Errorf("saturated GET %d: error field %q, want %q", i, body["error"], "internal error")
		}
		if body["request_id"] == "" {
			t.Errorf("saturated GET %d: missing request_id", i)
		}
		assertNoLeakage(t, rr.Body.String(), cfg)
		assertNoRetryHint(t, rr)
	}

	// Recovery: releasing the slots restores the 200 with no residue.
	release()
	recCtx, recCancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer recCancel()
	if rr := get(recCtx); rr.Code != http.StatusOK {
		t.Fatalf("recovery GET /v1/evidence: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	// Bounded-test AC: nothing is left holding a connection.
	if held := pool.Stat().AcquiredConns(); held != 0 {
		t.Fatalf("pool still holds %d connections after recovery", held)
	}
}

// ---- write path: POST /v1/evidence:push ---------------------------------

// allowAllValidator satisfies ingest.SchemaValidator without a
// schema-registry round-trip. Schema validation runs BEFORE pool
// acquisition in Service.Process, so it is irrelevant to the saturation
// contract this suite pins; stubbing it keeps the test independent of
// the Leg-A evidence_kind_schemas catalog seed.
type allowAllValidator struct{}

func (allowAllValidator) ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error {
	return nil
}
func (allowAllValidator) IsRegistered(kind, version string) bool { return true }

// pushBody builds the single-record push wire body (the 201 shorthand).
func pushBody(idem string) string {
	observed := time.Now().UTC().Add(-1 * time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{
		"record": {
			"idempotency_key": %q,
			"evidence_kind": "sast.scan_result.v1",
			"schema_version": "1.0.0",
			"control_id": "scf:VPM-04",
			"scope": [{"key": "environment", "values": ["prod"]}],
			"observed_at": %q,
			"result": "pass",
			"payload": {"tool": "semgrep", "findings_count": 0},
			"source_attribution": {"actor_type": "service_account", "actor_id": "oe426.test"}
		}
	}`, idem, observed)
}

func countEvidenceRecords(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer cancel()
	var n int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count evidence_records: %v", err)
	}
	return n
}

// TestEvidencePush_PoolSaturation_APIBoundaryContract pins the write
// path through the direct Service.Process dispatch — the path where
// pool acquisition sits on the request's critical path. (When the
// JetStream publisher is wired, the push acks at stream-commit time and
// the ledger write is decoupled from the HTTP response; see the
// slice-426 decisions log scope note.)
func TestEvidencePush_PoolSaturation_APIBoundaryContract(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "evidence_audit_log", "evidence_records")
	pool, cfg := newSmallPool(t, smallPoolMax)

	svc := ingest.New(pool, allowAllValidator{})
	handler := apievidence.NewHTTPHandler(svc, 0) // 0 disables the rate limiter: 429 is not the path under test
	cred := credstore.Credential{ID: "key_oe426_push", TenantID: tenant}

	push := func(ctx context.Context, idem string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/evidence:push", strings.NewReader(pushBody(idem)))
		req = req.WithContext(authctx.WithCredential(ctx, cred))
		rr := httptest.NewRecorder()
		handler.PushHTTP(rr, req)
		return rr
	}

	// Baseline: a push through the constrained pool lands in the ledger.
	baseCtx, baseCancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer baseCancel()
	if rr := push(baseCtx, "oe426-baseline"); rr.Code != http.StatusCreated {
		t.Fatalf("baseline push: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if n := countEvidenceRecords(t, admin, tenant); n != 1 {
		t.Fatalf("baseline ledger count = %d, want 1", n)
	}

	release := saturatePool(t, pool, smallPoolMax)

	// Concurrent pushes past N. NOTE the wall-clock shape: each saturated
	// push burns its request deadline on the ledger-write acquire, then
	// up to ingest's 3s auditWriteTimeout on the best-effort reject-audit
	// acquire (also starved) before the response is written. Bounded and
	// deterministic, but this is why the deadline here is short.
	const inflight = smallPoolMax + 1
	results := make([]*httptest.ResponseRecorder, inflight)
	var wg sync.WaitGroup
	for i := 0; i < inflight; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), saturatedRequestTimeout)
			defer cancel()
			results[i] = push(ctx, fmt.Sprintf("oe426-saturated-%d", i))
		}(i)
	}
	wg.Wait()

	for i, rr := range results {
		// OBSERVED contract: 500 with the structured errorBody shape,
		// NOT the fail-fast 4xx slice 335 claimed. Divergence recorded
		// in the slice-426 decisions log; changing the behaviour is a
		// separate slice (issue boundary).
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("saturated push %d: status %d, want 500 (observed contract)", i, rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("saturated push %d: body is not JSON: %v (body=%q)", i, err, rr.Body.String())
		}
		if body["code"] != "internal_error" {
			t.Errorf("saturated push %d: code %q, want %q", i, body["code"], "internal_error")
		}
		// The error field carries the audit decision token — structured,
		// and pinned so a future refactor that starts reflecting raw
		// driver errors here fails this test.
		if !strings.Contains(body["error"], "rejected_internal_error") {
			t.Errorf("saturated push %d: error %q missing decision token rejected_internal_error", i, body["error"])
		}
		assertNoLeakage(t, rr.Body.String(), cfg)
		assertNoRetryHint(t, rr)
	}

	// Append-only integrity: the saturated pushes wrote nothing.
	if n := countEvidenceRecords(t, admin, tenant); n != 1 {
		t.Fatalf("ledger count after saturation = %d, want 1 (saturated pushes must not land)", n)
	}

	// Recovery: releasing the slots restores the 201 path.
	release()
	recCtx, recCancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer recCancel()
	if rr := push(recCtx, "oe426-recovery"); rr.Code != http.StatusCreated {
		t.Fatalf("recovery push: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if n := countEvidenceRecords(t, admin, tenant); n != 2 {
		t.Fatalf("recovery ledger count = %d, want 2", n)
	}
}
