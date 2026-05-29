package canonjson_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/canonjson"
)

// maxPayloadBytes mirrors ingest.MaxPayloadBytes (1 MiB). Duplicated here
// rather than imported because canonjson must not depend on the ingest
// package (layering: ingest depends on canonjson, never the reverse).
const maxPayloadBytes = 1 << 20

// maxPayloadHashBudget is the wall-clock ceiling slice 381 F-ING-3 locks
// for hashing a 1 MiB record. The slice 332 audit characterised the cost
// at ~1-2ms on commodity hardware; 5ms gives generous headroom for slow
// CI runners while still catching an accidental O(payload^2) regression
// (which would blow well past 5ms at 1 MiB).
const maxPayloadHashBudget = 5 * time.Millisecond

func sampleRecord() *evidencev1.EvidenceRecord {
	payload, _ := structpb.NewStruct(map[string]any{"tool": "semgrep"})
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: "ci-run-1",
		EvidenceKind:   "sast.scan_result.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"prod"}},
			{Key: "cloud_account", Values: []string{"aws:111122223333"}},
		},
		ObservedAt: timestamppb.New(time.Date(2026, 5, 10, 14, 23, 0, 0, time.UTC)),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "service_account",
			ActorId:   "ci.gitlab.com/sec-product/main",
		},
	}
}

func TestHashRecord_Deterministic(t *testing.T) {
	t.Parallel()
	r := sampleRecord()

	first, err := canonjson.HashRecord(r)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}

	for i := 0; i < 100; i++ {
		got, err := canonjson.HashRecord(r)
		if err != nil {
			t.Fatalf("HashRecord iter %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("hash drift at iter %d: first=%s got=%s", i, first, got)
		}
	}
}

func TestHashRecord_ScopeOrderInvariant(t *testing.T) {
	t.Parallel()

	a := sampleRecord()
	b := sampleRecord()
	// Reverse scope order in b.
	b.Scope[0], b.Scope[1] = b.Scope[1], b.Scope[0]

	ha, err := canonjson.HashRecord(a)
	if err != nil {
		t.Fatalf("HashRecord a: %v", err)
	}
	hb, err := canonjson.HashRecord(b)
	if err != nil {
		t.Fatalf("HashRecord b: %v", err)
	}
	if ha != hb {
		t.Fatalf("hash differs by scope order: %s vs %s", ha, hb)
	}
}

func TestHashRecord_ScopeValuesOrderInvariant(t *testing.T) {
	t.Parallel()

	a := sampleRecord()
	a.Scope[0].Values = []string{"prod", "staging"}
	b := sampleRecord()
	b.Scope[0].Values = []string{"staging", "prod"}

	ha, _ := canonjson.HashRecord(a)
	hb, _ := canonjson.HashRecord(b)
	if ha != hb {
		t.Fatalf("hash differs by scope-values order: %s vs %s", ha, hb)
	}
}

func TestHashRecord_DifferentContentDifferentHash(t *testing.T) {
	t.Parallel()

	a := sampleRecord()
	b := sampleRecord()
	b.Result = evidencev1.Result_RESULT_FAIL

	ha, _ := canonjson.HashRecord(a)
	hb, _ := canonjson.HashRecord(b)
	if ha == hb {
		t.Fatal("hash identical for different content")
	}
}

// maxPayloadRecord builds an EvidenceRecord whose serialized proto sits
// right at the 1 MiB ceiling — the worst-case input HashRecord ever sees
// on the ingest hot path (slice 015 rejects anything larger). The bulk is
// a single large string field inside the payload struct.
func maxPayloadRecord(t testing.TB) *evidencev1.EvidenceRecord {
	t.Helper()
	rec := sampleRecord()
	// Grow the payload toward the cap, then trim so the marshaled record
	// is <= 1 MiB but as close to it as a single-field blob gets us. The
	// 4 KiB headroom covers the record's non-payload fields + proto
	// framing so we never construct an over-cap fixture.
	blob := strings.Repeat("a", maxPayloadBytes-4096)
	payload, err := structpb.NewStruct(map[string]any{"blob": blob})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	rec.Payload = payload
	if got := proto.Size(rec); got > maxPayloadBytes {
		t.Fatalf("fixture exceeds 1 MiB ceiling: %d > %d", got, maxPayloadBytes)
	}
	return rec
}

// TestHashRecord_AtMaxPayloadUnderBudget is the regression gate for
// slice 381 F-ING-3 / AC-12: a 1 MiB payload must hash in well under the
// wall-clock budget. A future change that makes HashRecord superlinear in
// payload size (e.g. an accidental O(payload^2) re-canonicalisation) trips
// this on CI rather than silently eroding the 50ms ingest ack SLO.
func TestHashRecord_AtMaxPayloadUnderBudget(t *testing.T) {
	t.Parallel()
	rec := maxPayloadRecord(t)

	// Warm once (excludes any one-time proto-reflection init from the
	// timed run), then take the best of a few samples to suppress
	// scheduler noise on a shared CI runner.
	if _, err := canonjson.HashRecord(rec); err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	best := time.Duration(1<<63 - 1)
	for range 5 {
		start := time.Now()
		if _, err := canonjson.HashRecord(rec); err != nil {
			t.Fatalf("HashRecord: %v", err)
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	if best > maxPayloadHashBudget {
		t.Fatalf("1 MiB hash took %v, exceeds budget %v", best, maxPayloadHashBudget)
	}
	t.Logf("1 MiB HashRecord best-of-5: %v (budget %v)", best, maxPayloadHashBudget)
}

// BenchmarkHashRecordAtMaxPayload locks the CPU cost of hashing a 1 MiB
// record (slice 381 F-ING-3 / AC-12). Pairs with the under-budget test
// above: the test is the hard gate, the benchmark is the trend surface
// (`go test -bench` re-publishes ns/op so a maintainer can watch the
// 1 MiB hash cost over time). It also self-asserts the per-op budget so a
// `-bench`-only run still catches a superlinear regression.
func BenchmarkHashRecordAtMaxPayload(b *testing.B) {
	rec := maxPayloadRecord(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := canonjson.HashRecord(rec); err != nil {
			b.Fatalf("HashRecord: %v", err)
		}
	}
	b.StopTimer()
	if perOp := b.Elapsed() / time.Duration(max(b.N, 1)); perOp > maxPayloadHashBudget {
		b.Fatalf("mean 1 MiB hash %v exceeds budget %v", perOp, maxPayloadHashBudget)
	}
}
