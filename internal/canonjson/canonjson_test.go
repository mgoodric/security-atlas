package canonjson_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/canonjson"
)

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
