//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// AC-5: latency budget. The benchmark seeds a representative-of-an-SCF-
// release fixture (~1,400 anchors + 60 SOC 2 requirements + a fan-out
// of edges + tenant controls) and then drives /v1/requirements/:id/
// coverage in a loop. Pass criterion: mean per-call latency < 200ms.
// p50 and p95 are reported as additional metrics; only the mean is the
// gate (Go's `testing.B` discipline reports ns/op which is mean).
//
// The seed runs once via t.Helper-style setup before the timer starts.
// The fixture mutates the global catalog so we wipe + reseed each
// invocation rather than leak rows across benchmark runs.
//
// This benchmark is NOT a perf-regression gate in CI — it's a slice-
// 008 acceptance gate. CI runs `go test ./...` and `go test -tags=
// integration ./...`; benchmarks run on demand via
// `go test -tags=integration -bench=BenchmarkRequirementCoverage -run=^$
// ./internal/api/ucfcoverage`.

func BenchmarkRequirementCoverage(b *testing.B) {
	dsn := adminDSN_b(b)
	appDSNStr := appDSN_b(b)

	adminPool := openPool_b(b, dsn)
	defer adminPool.Close()

	// 1. Wipe + reseed the catalog with ~1.4k anchors + 60 reqs + edges.
	seedLargeFixture(b, adminPool)

	// 2. Resolve the requirement id we'll hit in the loop. We pick one
	// with the highest fan-out so the query plan sees a realistic worst
	// case.
	var reqID uuid.UUID
	if err := adminPool.QueryRow(context.Background(), `
        SELECT framework_requirement_id
        FROM fw_to_scf_edges
        GROUP BY framework_requirement_id
        ORDER BY count(*) DESC, framework_requirement_id
        LIMIT 1`).Scan(&reqID); err != nil {
		b.Fatalf("pick high-fanout req: %v", err)
	}

	// 3. Boot HTTP server.
	ts, bearer := setupHTTPServerForBench(b, appDSNStr)

	url := ts.URL + "/v1/requirements/" + reqID.String() + "/coverage"

	// 4. Warm up the connection pool (first call may pay a TLS / pool
	// init tax that skews early ns/op).
	for i := 0; i < 3; i++ {
		hitOnce(b, url, bearer)
	}

	// 5. Benchmark loop. testing.B reports ns/op (mean). Record raw
	// wall-clock per iteration for the p50/p95 side metrics.
	samples := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		hitOnce(b, url, bearer)
		samples = append(samples, time.Since(start))
	}
	b.StopTimer()

	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := samples[len(samples)/2]
	p95 := samples[int(float64(len(samples))*0.95)]
	mean := time.Duration(0)
	for _, d := range samples {
		mean += d
	}
	mean /= time.Duration(len(samples))

	b.ReportMetric(float64(mean.Milliseconds()), "mean_ms")
	b.ReportMetric(float64(p50.Milliseconds()), "p50_ms")
	b.ReportMetric(float64(p95.Milliseconds()), "p95_ms")
	b.Logf("samples=%d mean=%s p50=%s p95=%s", len(samples), mean, p50, p95)

	if mean > 200*time.Millisecond {
		b.Fatalf("AC-5: mean per-call latency %s exceeds 200ms target", mean)
	}
}

func hitOnce(b *testing.B, url, bearer string) {
	b.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		b.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.Fatalf("Do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var sink struct {
		Anchors []map[string]any `json:"anchors"`
	}
	_ = json.Unmarshal(body, &sink)
}

// seedLargeFixture wipes the catalog + tenant controls and creates a
// representative fixture for the AC-5 benchmark:
//
//   - 1,400 SCF anchors (the production SCF release size)
//   - 60 SOC 2 requirements (the production SOC 2 TSC count + room)
//   - ~10k fw_to_scf_edges (avg fan-out 7 per requirement plus some
//     mass on the SCF spine for stress)
//   - 5,000 tenant-A controls anchored across the anchors
//
// The seed uses pgx.CopyFrom for the bulk tables and parameterized
// INSERTs for the small ones. Runs against the admin pool so RLS is
// bypassed during setup.
func seedLargeFixture(b *testing.B, pool *pgxpool.Pool) {
	b.Helper()
	ctx := context.Background()

	// Wipe in FK order. fw_to_scf_edges -> framework_requirements,
	// controls -> scf_anchors, scf_anchors -> framework_versions.
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edges`,
		`DELETE FROM controls`,
		`DELETE FROM framework_requirements`,
		`DELETE FROM scf_anchors`,
		`DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug IN ('scf','soc2') AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug IN ('scf','soc2') AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			b.Fatalf("wipe %q: %v", stmt, err)
		}
	}

	// SCF framework + current version. FK ordering: insert framework
	// with NULL latest_version_id first, then the version row, then
	// patch latest_version_id. Avoids a chicken-and-egg deadlock.
	scfFwID := uuid.New()
	scfFVID := uuid.New()
	if _, err := pool.Exec(ctx, `
        INSERT INTO frameworks (id, tenant_id, name, slug, issuer, latest_version_id)
        VALUES ($1, NULL, 'Secure Controls Framework', 'scf', 'SCF Council', NULL)`,
		scfFwID,
	); err != nil {
		b.Fatalf("insert scf framework: %v", err)
	}
	if _, err := pool.Exec(ctx, `
        INSERT INTO framework_versions (id, framework_id, version, status, effective_from, requirement_count)
        VALUES ($1, $2, '2026.1', 'current', '2026-01-01', 0)`,
		scfFVID, scfFwID,
	); err != nil {
		b.Fatalf("insert scf version: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE frameworks SET latest_version_id = $1 WHERE id = $2`, scfFVID, scfFwID); err != nil {
		b.Fatalf("patch scf latest_version: %v", err)
	}

	// SOC 2 framework + current version (same pattern).
	soc2FwID := uuid.New()
	soc2FVID := uuid.New()
	if _, err := pool.Exec(ctx, `
        INSERT INTO frameworks (id, tenant_id, name, slug, issuer, latest_version_id)
        VALUES ($1, NULL, 'SOC 2', 'soc2', 'AICPA', NULL)`,
		soc2FwID,
	); err != nil {
		b.Fatalf("insert soc2 framework: %v", err)
	}
	if _, err := pool.Exec(ctx, `
        INSERT INTO framework_versions (id, framework_id, version, status, effective_from, requirement_count)
        VALUES ($1, $2, '2017', 'current', '2017-04-01', 0)`,
		soc2FVID, soc2FwID,
	); err != nil {
		b.Fatalf("insert soc2 version: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE frameworks SET latest_version_id = $1 WHERE id = $2`, soc2FVID, soc2FwID); err != nil {
		b.Fatalf("patch soc2 latest_version: %v", err)
	}

	// 1,400 SCF anchors via COPY.
	const numAnchors = 1400
	anchorIDs := make([]uuid.UUID, numAnchors)
	anchorRows := make([][]any, numAnchors)
	for i := 0; i < numAnchors; i++ {
		anchorIDs[i] = uuid.New()
		// Families cycle through a realistic taxonomy.
		family := []string{"IAC", "NET", "DCH", "CHG", "MON", "SEA", "AST", "SPM", "VRM"}[i%9]
		anchorRows[i] = []any{
			anchorIDs[i], scfFVID,
			fmt.Sprintf("%s-%04d", family, i+1),
			family,
			fmt.Sprintf("Anchor %d", i+1),
			"",
			[]byte("[]"),
		}
	}
	if _, err := pool.CopyFrom(ctx,
		pgx.Identifier{"scf_anchors"},
		[]string{"id", "framework_version_id", "scf_id", "family", "title", "description", "subtopics"},
		pgx.CopyFromRows(anchorRows),
	); err != nil {
		b.Fatalf("copy scf_anchors: %v", err)
	}

	// 60 SOC 2 framework_requirements via COPY.
	const numReqs = 60
	reqIDs := make([]uuid.UUID, numReqs)
	reqRows := make([][]any, numReqs)
	for i := 0; i < numReqs; i++ {
		reqIDs[i] = uuid.New()
		section := []string{"CC", "A", "C", "PI"}[i%4]
		reqRows[i] = []any{
			reqIDs[i], soc2FVID,
			fmt.Sprintf("%s%d.%d", section, (i/10)+1, (i%10)+1),
			fmt.Sprintf("Requirement %s%d.%d title", section, (i/10)+1, (i%10)+1),
			"",
		}
	}
	if _, err := pool.CopyFrom(ctx,
		pgx.Identifier{"framework_requirements"},
		[]string{"id", "framework_version_id", "code", "title", "body"},
		pgx.CopyFromRows(reqRows),
	); err != nil {
		b.Fatalf("copy framework_requirements: %v", err)
	}

	// ~10,000 fw_to_scf_edges. Each requirement maps to ~7 anchors on
	// average; the rest distribute across the SCF spine for stress.
	const targetEdges = 10000
	edgeRows := make([][]any, 0, targetEdges)
	seen := make(map[string]bool, targetEdges)
	rng := rand.New(rand.NewPCG(42, 1337)) //nolint:gosec // deterministic test seed
	relTypes := []string{"equal", "subset_of", "superset_of", "intersects_with"}
	for len(edgeRows) < targetEdges {
		reqIdx := rng.IntN(numReqs)
		anchorIdx := rng.IntN(numAnchors)
		key := fmt.Sprintf("%d:%d", reqIdx, anchorIdx)
		if seen[key] {
			continue
		}
		seen[key] = true
		edgeRows = append(edgeRows, []any{
			uuid.New(),
			reqIDs[reqIdx],
			anchorIDs[anchorIdx],
			relTypes[rng.IntN(len(relTypes))],
			0.5 + 0.5*rng.Float64(), // strength in [0.5, 1.0]
			"community_draft",
			"",
		})
	}
	if _, err := pool.CopyFrom(ctx,
		pgx.Identifier{"fw_to_scf_edges"},
		[]string{"id", "framework_requirement_id", "scf_anchor_id", "relationship_type", "strength", "source_attribution", "rationale"},
		pgx.CopyFromRows(edgeRows),
	); err != nil {
		b.Fatalf("copy fw_to_scf_edges: %v", err)
	}

	// 5,000 controls for tenant A anchored across the anchors. Note:
	// can't use CopyFrom on controls because of the partial unique
	// index + multiple required columns. Use COPY anyway since
	// idempotency isn't a concern during a freshly-wiped seed.
	const numControls = 5000
	ctrlRows := make([][]any, numControls)
	now := time.Now()
	for i := 0; i < numControls; i++ {
		anchorIdx := rng.IntN(numAnchors)
		ctrlRows[i] = []any{
			uuid.New(),
			tenantA,
			fmt.Sprintf("bench-ctrl-%05d", i),
			int32(1),
			nil, // superseded_by
			nil, // scf_id (we set scf_anchor_id directly)
			anchorIDs[anchorIdx],
			fmt.Sprintf("Bench Control %d", i),
			"",
			"access_control",
			"automated",
			"security",
			"active",
			"",
			[]byte("[]"),
			nil,
			[]string{},
			nil,
			"",
			"",
			now,
			"bench",
		}
	}
	if _, err := pool.CopyFrom(ctx,
		pgx.Identifier{"controls"},
		[]string{
			"id", "tenant_id", "bundle_id", "version", "superseded_by",
			"scf_id", "scf_anchor_id", "title", "description",
			"control_family", "implementation_type", "owner_role",
			"lifecycle_state", "applicability_expr",
			"evidence_queries", "manual_evidence_schema", "linked_policy_ids",
			"freshness_class", "bundle_manifest_yaml", "bundle_manifest_hash",
			"bundle_uploaded_at", "bundle_uploaded_by",
		},
		pgx.CopyFromRows(ctrlRows),
	); err != nil {
		b.Fatalf("copy controls: %v", err)
	}

	// Run ANALYZE so the planner has stats for the just-loaded fixture.
	if _, err := pool.Exec(ctx, "ANALYZE scf_anchors, framework_requirements, fw_to_scf_edges, controls"); err != nil {
		b.Fatalf("analyze: %v", err)
	}

	b.Logf("seeded: %d anchors, %d reqs, %d edges, %d controls",
		numAnchors, numReqs, len(edgeRows), numControls)
}

// adminDSN_b / appDSN_b are testing.B variants of the testing.T helpers
// in integration_test.go. testing.B doesn't satisfy testing.T so we
// duplicate the minimal env lookup.
func adminDSN_b(b *testing.B) string {
	b.Helper()
	v := bGetenv("DATABASE_URL")
	if v == "" {
		b.Skip("DATABASE_URL not set; skipping benchmark")
	}
	return v
}

func appDSN_b(b *testing.B) string {
	b.Helper()
	v := bGetenv("DATABASE_URL_APP")
	if v == "" {
		b.Skip("DATABASE_URL_APP not set; skipping benchmark")
	}
	return v
}

func openPool_b(b *testing.B, dsn string) *pgxpool.Pool {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		b.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

func bGetenv(key string) string {
	return os.Getenv(key)
}

// setupHTTPServerForBench is the testing.B variant of setupHTTPServer.
// Returns an httptest.Server with slice-008 routes wired, plus a
// bearer credential for tenant A.
func setupHTTPServerForBench(b *testing.B, appDSNStr string) (*httptest.Server, string) {
	b.Helper()
	appPool := openPool_b(b, appDSNStr)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(appPool)
	// Slice 197: JWT bearer via slice 190 path.
	bearer := srv.IssueTestJWT(b, testjwt.ViewerFor(uuid.MustParse(tenantA)))
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		b.Fatal("HTTPHandlerForTests returned nil")
	}
	ts := httptest.NewServer(handler)
	b.Cleanup(func() {
		ts.Close()
		appPool.Close()
	})
	return ts, bearer
}
