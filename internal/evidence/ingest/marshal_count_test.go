// Slice 379 (closes slice 332 F-ING-1 MEDIUM): unit test + benchmark
// that assert Service.Process issues exactly ONE marshal-of-record
// on the redaction-bearing code path.
//
// "Marshal-of-record" = a marshal whose output BYTES are retained for
// the ledger payload column and feed the canonjson hash. The pre-redact
// `protojson.Marshal` for size-check + schema-validate produces bytes
// that are DISCARDED after the checks; it is not a marshal-of-record
// and is not counted by AC-1. See `internal/evidence/ingest/ingest.go`
// `Service.marshalLedger` for the seam design and decisions log D1
// for the contradiction resolution between AC-3 (slice-doc typo) and
// P0-4 (canonical slice 015 D2 order: size-check → schema-validate →
// redact → hash → write).
//
// Why no integration tag: these tests do NOT touch the database. They
// exercise `Service.Process` up to (but not into) the DB transaction
// by short-circuiting at the redaction step via a marshal seam that
// returns a sentinel error after counting. The marshal-count and
// flow-order assertions are pure-Go; Postgres is not on the path.
package ingest

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// errStopAfterMarshal is returned by the test seam after counting.
// We use it to short-circuit Process before the DB write so the test
// stays pool-free. Process maps the error to DecisionRejectedInternalError;
// the test only cares about the marshal count + decision tag.
var errStopAfterMarshal = errors.New("test seam: stop after ledger marshal")

// countingValidator is a SchemaValidator that:
//
//   - Always reports the kind as registered (no probe rejection).
//   - Returns nil from ValidatePayload (no schema rejection).
//   - Optionally returns redaction rules from RedactionRulesFor so the
//     redaction-bearing code path is exercised. An empty rules slice
//     suppresses the redaction step (no-rules path).
type countingValidator struct {
	rules []string
}

func (v *countingValidator) ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error {
	return nil
}
func (v *countingValidator) IsRegistered(kind, version string) bool { return true }
func (v *countingValidator) RedactionRulesFor(ctx context.Context, tenantID, kind, version string) ([]string, error) {
	return v.rules, nil
}

// fixedFixture returns a minimal-but-valid EvidenceRecord with a payload
// containing a `secret_value` field that the redaction rule targets. The
// fixture is deterministic — no time.Now, no RNG. The clock pinned on
// the Service makes observed_at-skew assertions reproducible.
func fixedFixture(t *testing.T) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"secret_value":  "shh-this-is-a-secret",
		"kept_field":    "this-stays",
		"numeric_field": 42.0,
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	observed := time.Unix(1735689600, 0).UTC() // 2025-01-01T00:00:00Z — well within the 24h skew window of the test clock below
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: "slice-379-test-idem",
		EvidenceKind:   "secret.scan.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:IAC-06",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "env", Values: []string{"prod"}},
		},
		ObservedAt: timestamppb.New(observed),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "service",
			ActorId:   "ci-runner-1",
			SessionId: "session-1",
		},
	}
}

// serviceForCountTest builds a Service with NO pool (Process will never
// reach the DB write) and the supplied validator + ledger-marshal seam.
// The clock is pinned to ObservedAt so skew is exactly zero.
func serviceForCountTest(t *testing.T, validator SchemaValidator, marshalSeam func(proto.Message) ([]byte, error)) *Service {
	t.Helper()
	pinned := time.Unix(1735689600, 0).UTC()
	s := &Service{
		valid:         validator,
		clock:         func() time.Time { return pinned },
		path:          "test",
		marshalLedger: marshalSeam,
	}
	return s
}

// TestProcess_RedactionPath_LedgerMarshalCountIsOne — AC-1.
//
// Wires Service.Process with a counting `marshalLedger` seam that:
//   - Increments an atomic counter on each call.
//   - Returns errStopAfterMarshal so Process short-circuits BEFORE
//     reaching the DB write (no pool needed, no GUC handling).
//
// Asserts the counter is exactly 1 — confirming that the post-redact
// marshal is the ONLY marshal whose output is retained for the ledger
// on the redaction-bearing code path. The pre-redact `protojson.Marshal`
// for size-check + schema-validate happens via the direct call and is
// NOT routed through the seam.
func TestProcess_RedactionPath_LedgerMarshalCountIsOne(t *testing.T) {
	t.Parallel()

	var ledgerCalls atomic.Int64
	seam := func(m proto.Message) ([]byte, error) {
		ledgerCalls.Add(1)
		// Still return the real bytes so any subsequent dereferences inside
		// Process see valid input. The sentinel comes back as an error from
		// the seam, which Process maps to DecisionRejectedInternalError.
		b, err := protojson.Marshal(m)
		if err != nil {
			return nil, err
		}
		_ = b // bytes intentionally unused — sentinel below ends the path
		return nil, errStopAfterMarshal
	}

	validator := &countingValidator{rules: []string{"$.secret_value"}}
	svc := serviceForCountTest(t, validator, seam)

	rec := fixedFixture(t)
	cred := credstore.Credential{
		ID:       "test-cred-1",
		TenantID: "11111111-1111-1111-1111-111111111111",
	}

	_, decision, err := svc.Process(context.Background(), rec, cred)

	// Sentinel short-circuit is expected — confirms the seam fired AND that
	// Process did not silently bypass it.
	if !errors.Is(err, errStopAfterMarshal) {
		t.Fatalf("expected errStopAfterMarshal, got err=%v decision=%s", err, decision)
	}
	if got := ledgerCalls.Load(); got != 1 {
		t.Fatalf("AC-1: expected exactly 1 ledger-marshal call on redaction-bearing path, got %d", got)
	}
}

// TestProcess_NoRedactionPath_LedgerMarshalCountIsZero — companion guard
// to AC-1. On the no-rules path, the pre-redact `protojson.Marshal` bytes
// are reused for the ledger write; the seam MUST NOT fire. If the seam
// fires here, the refactor accidentally collapsed the rules-present and
// rules-absent branches and we'd be marshaling twice on the no-rules path
// (a regression). This test catches that.
//
// Process is run all the way through here would need a DB; instead we
// observe that the seam was never called and that Process gets to the
// post-redaction code (which requires NO sentinel because the seam is
// not invoked on the no-rules branch). We expect an error from the
// downstream DB-touching code (nil pool), but we only care about the
// seam counter.
func TestProcess_NoRedactionPath_LedgerMarshalCountIsZero(t *testing.T) {
	t.Parallel()

	var ledgerCalls atomic.Int64
	seam := func(m proto.Message) ([]byte, error) {
		ledgerCalls.Add(1)
		return protojson.Marshal(m)
	}

	// Empty rules → redaction block is taken but the inner `if len(rules) > 0`
	// is false, so the seam never fires.
	validator := &countingValidator{rules: nil}
	svc := serviceForCountTest(t, validator, seam)

	rec := fixedFixture(t)
	cred := credstore.Credential{
		ID:       "test-cred-1",
		TenantID: "11111111-1111-1111-1111-111111111111",
	}

	// Will panic on the nil pool when Process reaches the DB write — recover
	// so the test still observes the counter. If the panic does NOT happen,
	// either the path changed (Process now short-circuits before DB) which
	// is fine, OR the seam fired (which we'll catch on the counter check).
	defer func() {
		_ = recover()
		if got := ledgerCalls.Load(); got != 0 {
			t.Fatalf("no-redaction-path guard: expected 0 ledger-marshal calls, got %d (seam should fire ONLY when rules apply)", got)
		}
	}()

	_, _, _ = svc.Process(context.Background(), rec, cred)
}

// TestProcess_RedactionApplied_BytesAreRedacted — guards AC-4 + AC-5
// + P0-2: confirms that on the redaction-bearing path, the bytes that
// would have flowed to the ledger (via the seam) are the REDACTED form.
// We capture the bytes the seam was called with and assert they contain
// the redaction marker AND do NOT contain the original secret value.
//
// This also doubles as a P0-2 anti-criterion guard: the bytes the seam
// is invoked with are the marshaled redacted proto — the unredacted form
// never reaches the post-redact marshal call.
func TestProcess_RedactionApplied_BytesAreRedacted(t *testing.T) {
	t.Parallel()

	var captured []byte
	seam := func(m proto.Message) ([]byte, error) {
		b, err := protojson.Marshal(m)
		if err != nil {
			return nil, err
		}
		captured = append(captured[:0], b...) // copy so concurrent calls don't race the slice header
		return nil, errStopAfterMarshal
	}

	validator := &countingValidator{rules: []string{"$.secret_value"}}
	svc := serviceForCountTest(t, validator, seam)

	rec := fixedFixture(t)
	cred := credstore.Credential{
		ID:       "test-cred-1",
		TenantID: "11111111-1111-1111-1111-111111111111",
	}

	_, _, err := svc.Process(context.Background(), rec, cred)
	if !errors.Is(err, errStopAfterMarshal) {
		t.Fatalf("expected errStopAfterMarshal, got err=%v", err)
	}
	got := string(captured)
	if !strings.Contains(got, "<<REDACTED>>") {
		t.Fatalf("AC-5: expected redaction marker in ledger-bound bytes, got: %q", got)
	}
	if strings.Contains(got, "shh-this-is-a-secret") {
		t.Fatalf("P0-2: original secret value leaked into ledger-bound bytes: %q", got)
	}
}

// BenchmarkIngestRedactionPath — AC-8.
//
// Runs Process on the redaction-bearing code path and asserts via b.Fatal
// that the ledger-marshal seam fires exactly once per iteration. We test
// the SHAPE, not the wall-clock (slice doc note 3: "the benchmark should
// assert marshal count, not wall-clock time — wall-clock will be noisy").
//
// The post-condition is checked at the end of the benchmark loop:
// `ledgerCalls.Load() == int64(b.N)`. Any iteration that fires the seam
// more than once (or fewer than once) breaks the equality.
//
// The benchmark short-circuits via errStopAfterMarshal — same shape
// as TestProcess_RedactionPath_LedgerMarshalCountIsOne — so it stays
// pool-free.
func BenchmarkIngestRedactionPath(b *testing.B) {
	var ledgerCalls atomic.Int64
	seam := func(m proto.Message) ([]byte, error) {
		ledgerCalls.Add(1)
		_, _ = protojson.Marshal(m) // fair-comparison: still pay the real marshal cost
		return nil, errStopAfterMarshal
	}

	validator := &countingValidator{rules: []string{"$.secret_value"}}
	pinned := time.Unix(1735689600, 0).UTC()
	svc := &Service{
		valid:         validator,
		clock:         func() time.Time { return pinned },
		path:          "bench",
		marshalLedger: seam,
	}

	// Build the fixture once outside the timed loop — slice doc note 3
	// emphasises deterministic inputs.
	tmpl, err := structpb.NewStruct(map[string]any{
		"secret_value":  "shh-this-is-a-secret",
		"kept_field":    "this-stays",
		"numeric_field": 42.0,
	})
	if err != nil {
		b.Fatalf("structpb.NewStruct: %v", err)
	}
	observed := time.Unix(1735689600, 0).UTC()
	cred := credstore.Credential{
		ID:       "test-cred-1",
		TenantID: "11111111-1111-1111-1111-111111111111",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Fresh fixture per iter so the redactor's clone-don't-mutate
		// semantic does not accumulate across iters. proto.Clone is the
		// safe deep-copy; we pay for it once before the timed-region
		// matters but it doesn't change the marshal-count assertion.
		rec := &evidencev1.EvidenceRecord{
			IdempotencyKey: "bench-379-idem",
			EvidenceKind:   "secret.scan.v1",
			SchemaVersion:  "1.0.0",
			ControlId:      "scf:IAC-06",
			Scope: []*evidencev1.ScopeDimension{
				{Key: "env", Values: []string{"prod"}},
			},
			ObservedAt: timestamppb.New(observed),
			Result:     evidencev1.Result_RESULT_PASS,
			Payload:    proto.Clone(tmpl).(*structpb.Struct),
			SourceAttribution: &evidencev1.SourceAttribution{
				ActorType: "service",
				ActorId:   "ci-runner-1",
				SessionId: "session-1",
			},
		}
		_, _, _ = svc.Process(context.Background(), rec, cred)
	}
	b.StopTimer()

	if got, want := ledgerCalls.Load(), int64(b.N); got != want {
		b.Fatalf("AC-8: expected ledger-marshal count == b.N=%d, got %d", want, got)
	}
}

// TestWithLedgerMarshaler_OverridesDefault confirms the test-only
// constructor seam returns a copy with the supplied marshaler and
// preserves the rest of Service state. Belt-and-braces — keeps the
// seam wired even if the call sites above stop exercising it.
func TestWithLedgerMarshaler_OverridesDefault(t *testing.T) {
	t.Parallel()

	original := &Service{
		valid:         &countingValidator{},
		clock:         func() time.Time { return time.Unix(0, 0).UTC() },
		path:          "original",
		marshalLedger: protojson.Marshal,
	}
	var called atomic.Int64
	override := func(m proto.Message) ([]byte, error) {
		called.Add(1)
		return nil, nil
	}
	derived := original.withLedgerMarshaler(override)

	// Derived has the override; original is untouched (functional value
	// equality is not comparable in Go, so we observe behavior via call
	// counts).
	_, _ = derived.marshalLedger(nil)
	_, _ = original.marshalLedger(nil)

	if got := called.Load(); got != 1 {
		t.Fatalf("withLedgerMarshaler should route derived calls through override; got %d calls", got)
	}
	if derived.path != original.path {
		t.Fatalf("withLedgerMarshaler should preserve other fields; got path=%q want %q", derived.path, original.path)
	}
}
