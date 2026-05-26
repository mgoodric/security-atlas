// Unit tests for the metrics scheduler load-bearing helpers that do NOT
// require a real Postgres. The DB-touching paths (SweepOnce, sweepTenant)
// are exercised by integration_test.go in this package — those tests run
// against a real database under the `integration` build tag and the
// scheduler package is enrolled in CI's integration job.
//
// Functions covered here:
//
//   - New                — constructor's nil-logger fallback branch
//   - encodeDimensions   — JSON shape for both empty and populated maps
//   - discardWriter.Write — the io.Writer the nil-logger fallback uses
//   - Run                — the cron loop's context-cancellation branch +
//                          default-interval fallback (interval<=0)
//   - SweepReport        — zero value + field assignment
//
// Branches deliberately NOT covered here (DB-only; covered by
// integration_test.go):
//   - SweepOnce success + per-tenant iteration
//   - sweepTenant tx lifecycle, ApplyTenant, evaluator failure path,
//     InsertMetricObservation error path
//
// Run with: go test ./internal/metrics/scheduler/...

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/metrics/eval"
)

// defunctPool constructs a pgxpool pointed at an unreachable host. The
// pool object is usable (so calls into dbx.New(pool) don't nil-deref),
// but any Query/Exec returns a connection error which the scheduler
// logs-and-drops via its sweep wrapper. This lets us exercise the
// Run-loop's cancellation arm without standing up a real Postgres.
func defunctPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// 127.0.0.1:1 is the canonical "nothing listens here" address;
	// connect_timeout=1 keeps any background dial loops fast; pool_max_conns=1
	// keeps the pool tiny.
	cfg, err := pgxpool.ParseConfig("postgres://nobody@127.0.0.1:1/none?pool_max_conns=1&connect_timeout=1")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ---- New() ---------------------------------------------------------------

func TestNew_NilLoggerFallsBackToDiscardWriter(t *testing.T) {
	// The nil-logger branch installs a slog.TextHandler over the
	// package's discardWriter. We can't introspect the handler directly,
	// but we can verify (a) the constructor returns non-nil, (b) it does
	// not panic on a subsequent Info call (the discardWriter accepts
	// writes), and (c) the logger field is non-nil.
	s := New(nil, nil, eval.NewRegistry(nil), nil)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.logger == nil {
		t.Fatal("logger should be initialised to the discard logger when nil is passed")
	}
	// Sanity check: writing through the logger should not panic and
	// should not produce visible output. We can't capture the discard
	// writer's bytes directly (it has no buffer), but the call must
	// succeed without an unrecovered panic.
	s.logger.Info("ping")
}

func TestNew_PassedLoggerIsRetained(t *testing.T) {
	var buf bytes.Buffer
	customLogger := slog.New(slog.NewTextHandler(&buf, nil))
	s := New(nil, nil, eval.NewRegistry(nil), customLogger)
	if s.logger != customLogger {
		t.Fatal("New replaced the caller's logger; expected to retain it")
	}
	s.logger.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("custom logger did not write through: %q", buf.String())
	}
}

func TestNew_StoresPoolsAndRegistry(t *testing.T) {
	reg := eval.NewRegistry(nil)
	s := New(nil, nil, reg, nil)
	if s.registry != reg {
		t.Fatal("registry pointer not retained")
	}
	// Both pools are nil in this unit test; the field assignment is the
	// asserted behaviour.
	if s.migratorPool != nil {
		t.Error("migratorPool: expected nil")
	}
	if s.appPool != nil {
		t.Error("appPool: expected nil")
	}
}

// ---- discardWriter -------------------------------------------------------

func TestDiscardWriter_AcceptsAnyByteSliceAndReturnsLen(t *testing.T) {
	w := discardWriter{}

	cases := [][]byte{
		nil,
		{},
		[]byte("hello world"),
		make([]byte, 4096),
	}
	for _, c := range cases {
		n, err := w.Write(c)
		if err != nil {
			t.Errorf("Write(%d bytes) returned err=%v; want nil", len(c), err)
		}
		if n != len(c) {
			t.Errorf("Write(%d bytes) returned n=%d; want %d", len(c), n, len(c))
		}
	}
}

// ---- encodeDimensions ----------------------------------------------------

func TestEncodeDimensions_PopulatedMapMarshalsSortedJSON(t *testing.T) {
	// json.Marshal of a map[string]string sorts keys alphabetically by
	// default. The function's docstring promises this determinism.
	in := map[string]string{
		"zeta":  "z",
		"alpha": "a",
		"mu":    "m",
	}
	got := encodeDimensions(in)

	// Round-trip and compare the decoded value rather than asserting on
	// the literal byte order beyond what the docstring guarantees.
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (raw=%q)", err, got)
	}
	if len(decoded) != len(in) {
		t.Errorf("decoded len=%d; want %d", len(decoded), len(in))
	}
	for k, v := range in {
		if decoded[k] != v {
			t.Errorf("decoded[%q] = %q; want %q", k, decoded[k], v)
		}
	}

	// Determinism: same input must produce byte-identical output across
	// calls. This is the property the scheduler relies on for stable
	// observation rows.
	got2 := encodeDimensions(in)
	if !bytes.Equal(got, got2) {
		t.Errorf("encodeDimensions not deterministic: %q vs %q", got, got2)
	}
}

func TestEncodeDimensions_EmptyMapMarshalsToObjectLiteral(t *testing.T) {
	got := encodeDimensions(map[string]string{})
	if string(got) != "{}" {
		t.Errorf("encodeDimensions(empty) = %q; want %q", got, "{}")
	}
}

func TestEncodeDimensions_SingleEntryRoundTrips(t *testing.T) {
	got := encodeDimensions(map[string]string{"framework": "soc2"})
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (raw=%q)", err, got)
	}
	if decoded["framework"] != "soc2" {
		t.Errorf("decoded[framework] = %q; want %q", decoded["framework"], "soc2")
	}
}

// ---- Run() ---------------------------------------------------------------

// TestRun_ReturnsImmediatelyOnCancelledContext exercises the
// context-cancellation branch of Run without needing a real DB. A defunct
// migrator pool routes the inline SweepOnce to its error path (logged then
// dropped), and the cancelled context routes the ticker loop to the
// ctx.Done() arm.
func TestRun_ReturnsImmediatelyOnCancelledContext(t *testing.T) {
	pool := defunctPool(t)
	s := New(pool, pool, eval.NewRegistry(nil), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled — first ticker tick will see ctx.Done

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, 10*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err=%v; want nil on graceful cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context cancellation")
	}
}

// TestRun_NonPositiveIntervalFallsBackToDefault exercises the
// `if interval <= 0` branch. We can't directly observe the chosen
// interval, but we can confirm the function (a) accepts a zero
// interval without panicking and (b) returns when the context is
// cancelled. The default interval is 15 minutes, so the function would
// otherwise block far past our deadline — returning at all proves the
// cancellation arm fired.
func TestRun_NonPositiveIntervalFallsBackToDefault(t *testing.T) {
	pool := defunctPool(t)
	s := New(pool, pool, eval.NewRegistry(nil), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// Pass interval=0 -> exercises the fallback branch on line 62.
		done <- s.Run(ctx, 0)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err=%v; want nil on graceful cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context-timeout cancel")
	}
}

// TestRun_NegativeIntervalFallsBackToDefault is the symmetrical case
// to the zero-interval test — exercises the strict-inequality side of
// the `interval <= 0` branch.
func TestRun_NegativeIntervalFallsBackToDefault(t *testing.T) {
	pool := defunctPool(t)
	s := New(pool, pool, eval.NewRegistry(nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, -1*time.Second)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err=%v; want nil on graceful cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context-timeout cancel")
	}
}

// TestSweepOnce_ListTenantsErrorIsWrapped exercises the
// list-tenants-failure arm of SweepOnce: the function returns a wrapped
// "list tenants" error rather than panicking. We hit this via the same
// defunct pool used by the Run tests above.
func TestSweepOnce_ListTenantsErrorIsWrapped(t *testing.T) {
	pool := defunctPool(t)
	s := New(pool, pool, eval.NewRegistry(nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rep, err := s.SweepOnce(ctx)
	if err == nil {
		t.Fatal("SweepOnce returned nil err with a defunct migrator pool")
	}
	if !strings.Contains(err.Error(), "list tenants") {
		t.Errorf("error %q does not name the list-tenants stage", err.Error())
	}
	if rep.TenantsSwept != 0 || rep.ObservationsWritten != 0 || rep.EvaluatorFailures != 0 {
		t.Errorf("SweepOnce on list-tenants failure returned non-zero report: %+v", rep)
	}
}

// ---- SweepReport --------------------------------------------------------

func TestSweepReport_ZeroValueAndFieldAssignment(t *testing.T) {
	r := SweepReport{}
	if r.TenantsSwept != 0 || r.ObservationsWritten != 0 || r.EvaluatorFailures != 0 {
		t.Errorf("zero SweepReport has nonzero fields: %+v", r)
	}
	r.TenantsSwept = 3
	r.ObservationsWritten = 24
	r.EvaluatorFailures = 1
	if r.TenantsSwept != 3 {
		t.Errorf("TenantsSwept = %d; want 3", r.TenantsSwept)
	}
	if r.ObservationsWritten != 24 {
		t.Errorf("ObservationsWritten = %d; want 24", r.ObservationsWritten)
	}
	if r.EvaluatorFailures != 1 {
		t.Errorf("EvaluatorFailures = %d; want 1", r.EvaluatorFailures)
	}
}

// ---- DefaultInterval ----------------------------------------------------

func TestDefaultInterval_MatchesSliceDoc(t *testing.T) {
	if DefaultInterval != 15*time.Minute {
		t.Errorf("DefaultInterval = %s; want 15m0s (slice 076)", DefaultInterval)
	}
}
