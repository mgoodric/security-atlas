package canonjson_test

import (
	"sort"
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
// for hashing a 1 MiB record. This is a PATHOLOGICAL-regression gate, not
// a microsecond budget — see decisions log D5.
//
// Observed single-hash cost at the 1 MiB ceiling: ~0.8ms on an M3 dev
// box, ~5-7ms on a shared CI runner under load (linear in payload size,
// dominated by one SHA-256 pass + one deterministic proto marshal). The
// failure mode this gate is meant to catch — an accidental O(payload^2)
// re-canonicalisation, or a per-byte allocation blowup — would cost
// HUNDREDS of milliseconds to seconds at 1 MiB, two-to-three orders of
// magnitude above the observed cost.
//
// 100ms therefore sits in the dead zone: ~14-125x above any realistic
// CI sample (so runner noise can never trip it) yet far below any
// superlinear pathology (so a real regression always trips it). An
// earlier 5ms ceiling flaked on CI because it sat ON TOP of the observed
// 5-7ms cost rather than clear of it; the gate must measure "is this
// pathological?", which is an order-of-magnitude question.
const maxPayloadHashBudget = 100 * time.Millisecond

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

// medianHashDuration hashes rec n times (after a discarded warmup that
// excludes one-time proto-reflection init) and returns the MEDIAN sample.
// The median is robust in both directions: a single slow scheduler hiccup
// on a shared runner can't inflate it (unlike a mean), and a single fast
// outlier can't hide a genuine regression (unlike best-of-N). n must be
// odd so the median is a real sample.
func medianHashDuration(t testing.TB, rec *evidencev1.EvidenceRecord, n int) time.Duration {
	t.Helper()
	if _, err := canonjson.HashRecord(rec); err != nil { // warmup, discarded
		t.Fatalf("HashRecord warmup: %v", err)
	}
	samples := make([]time.Duration, 0, n)
	for range n {
		start := time.Now()
		if _, err := canonjson.HashRecord(rec); err != nil {
			t.Fatalf("HashRecord: %v", err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[len(samples)/2]
}

// TestHashRecord_AtMaxPayloadUnderBudget is the regression gate for
// slice 381 F-ING-3 / AC-12. It catches a PATHOLOGICAL regression in
// HashRecord at the 1 MiB ceiling — an accidental O(payload^2)
// re-canonicalisation or a per-byte allocation blowup — which would push
// the cost orders of magnitude past the observed ~1-7ms and silently
// erode the 50ms ingest ack SLO. It is deliberately NOT a microsecond
// budget (see maxPayloadHashBudget + decisions log D5); the precise
// per-op number lives in BenchmarkHashRecordAtMaxPayload.
//
// Measurement is median-of-9 with a discarded warmup so shared-runner
// scheduler noise cannot trip the gate.
func TestHashRecord_AtMaxPayloadUnderBudget(t *testing.T) {
	t.Parallel()
	rec := maxPayloadRecord(t)

	median := medianHashDuration(t, rec, 9)
	if median > maxPayloadHashBudget {
		t.Fatalf("1 MiB hash median %v exceeds pathological-regression ceiling %v", median, maxPayloadHashBudget)
	}
	t.Logf("1 MiB HashRecord median-of-9: %v (pathological ceiling %v)", median, maxPayloadHashBudget)
}

// BenchmarkHashRecordAtMaxPayload is the PRECISE-NUMBER artifact for the
// 1 MiB hash cost (slice 381 F-ING-3 / AC-12). It pairs with
// TestHashRecord_AtMaxPayloadUnderBudget: the test is the coarse,
// noise-robust pathological-regression gate; this benchmark re-publishes
// a steady ns/op (`go test -bench`) so a maintainer can watch the cost
// trend over releases. The self-assert uses the SAME generous
// pathological ceiling as the test (not a tight microsecond budget), so a
// `-bench`-only run still catches an order-of-magnitude regression
// without flaking on a noisy runner (decisions log D5).
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
		b.Fatalf("mean 1 MiB hash %v exceeds pathological-regression ceiling %v", perOp, maxPayloadHashBudget)
	}
}
