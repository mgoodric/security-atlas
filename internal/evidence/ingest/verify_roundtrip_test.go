// Slice 633 — AC-3 pure-Go regression guard for the ingest/verify hash
// round-trip. No database: it simulates the ledger persistence the way
// Service.Process writes it (observed_at -> lossless nanos column + lossy
// TIMESTAMPTZ; payload -> protojson; scope -> canonical JSONB;
// source_attribution -> JSONB), reconstructs via the REAL
// RecordFromLedgerRow, re-hashes, and pins
//
//	HashRecord(RecordFromLedgerRow(persist(rec))) == HashRecord(rec)
//
// for a table of records. The regression case is an observed_at with a
// sub-microsecond nanosecond component: slice 474 persisted scope but left
// observed_at reconstructed from the microsecond-precision TIMESTAMPTZ
// column, so the re-hash diverged whenever the wire timestamp carried
// sub-us nanos (the CI-Linux case — macOS time.Now() is often us-aligned,
// which is why this hid in 474's local run while failing RED in CI).
//
// Why no integration tag: this does NOT touch Postgres. It models the
// microsecond truncation Postgres TIMESTAMPTZ applies (time.Truncate
// (time.Microsecond)) so the round-trip is exercised end-to-end in-process
// — the fast pre-DB guard the integration test (slice 474) failed to be.
package ingest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// persistLikeIngest mirrors the column writes Service.Process performs for
// the hash-contributing fields, INCLUDING the Postgres TIMESTAMPTZ
// microsecond truncation of observed_at. It returns the reconstructable
// ledger row. legacyObservedAtOnly=true simulates a pre-slice-633 row
// (observed_at_nanos NULL) to exercise the legacy fallback.
func persistLikeIngest(t *testing.T, rec *evidencev1.EvidenceRecord, legacyObservedAtOnly bool) dbx.EvidenceRecord {
	t.Helper()

	row := dbx.EvidenceRecord{
		EvidenceKind:  ptrStr(rec.GetEvidenceKind()),
		SchemaVersion: ptrStr(rec.GetSchemaVersion()),
		ControlRef:    rec.GetControlId(),
	}
	if rec.GetIdempotencyKey() != "" {
		k := rec.GetIdempotencyKey()
		row.IdempotencyKey = &k
	}
	resultEnum, _ := protoResultToEnum(rec.GetResult())
	row.Result = resultEnum

	// observed_at: TIMESTAMPTZ truncates to microsecond precision.
	observed := rec.GetObservedAt().AsTime()
	row.ObservedAt = pgtype.Timestamptz{Time: observed.Truncate(time.Microsecond), Valid: true}
	if !legacyObservedAtOnly {
		nanos := observed.UnixNano()
		row.ObservedAtNanos = &nanos
	}

	// payload: protojson bytes.
	if rec.GetPayload() != nil {
		pj, err := protojson.Marshal(rec.GetPayload())
		if err != nil {
			t.Fatalf("payload marshal: %v", err)
		}
		row.Payload = pj
	}

	// scope: canonical JSONB.
	sc, err := canonjson.MarshalCanonicalScope(rec.GetScope())
	if err != nil {
		t.Fatalf("scope marshal: %v", err)
	}
	row.ScopeCanonical = sc

	// source_attribution: JSONB.
	sa, err := json.Marshal(map[string]any{
		"actor_type": rec.GetSourceAttribution().GetActorType(),
		"actor_id":   rec.GetSourceAttribution().GetActorId(),
		"session_id": rec.GetSourceAttribution().GetSessionId(),
	})
	if err != nil {
		t.Fatalf("source_attribution marshal: %v", err)
	}
	row.SourceAttribution = sa

	if rec.PayloadUri != nil {
		row.PayloadUri = rec.PayloadUri
	}
	return row
}

func numericPayload(t *testing.T) *structpb.Struct {
	t.Helper()
	p, err := structpb.NewStruct(map[string]any{
		"findings_count": float64(42),
		"scanned_files":  float64(1247),
		"ratio":          0.5,
	})
	if err != nil {
		t.Fatalf("numericPayload: %v", err)
	}
	return p
}

func structPayload(t *testing.T) *structpb.Struct {
	t.Helper()
	p, err := structpb.NewStruct(map[string]any{
		"tool":         "neutral-scanner",
		"tool_version": "0.0.0",
		"ruleset":      "baseline",
	})
	if err != nil {
		t.Fatalf("structPayload: %v", err)
	}
	return p
}

// TestVerifyRoundTrip_PinsHash_AC3 is the load-bearing pure-Go guard.
func TestVerifyRoundTrip_PinsHash_AC3(t *testing.T) {
	t.Parallel()

	// A fixed base instant carrying a sub-microsecond nanosecond component
	// (123 ns past the microsecond boundary) — the deterministic regression
	// case. Neutral, not real-looking.
	subUS := time.Unix(1780954473, 585946123).UTC()
	usAligned := time.Unix(1780954473, 585946000).UTC()

	base := func(mut func(*evidencev1.EvidenceRecord)) *evidencev1.EvidenceRecord {
		r := &evidencev1.EvidenceRecord{
			IdempotencyKey: "neutral-idem-key",
			EvidenceKind:   "sast.scan_result.v1",
			SchemaVersion:  "1.0.0",
			ControlId:      "scf:VPM-04",
			Scope: []*evidencev1.ScopeDimension{
				{Key: "environment", Values: []string{"prod"}},
			},
			ObservedAt: timestamppb.New(usAligned),
			Result:     evidencev1.Result_RESULT_PASS,
			Payload:    structPayload(t),
			SourceAttribution: &evidencev1.SourceAttribution{
				ActorType: "service_account",
				ActorId:   "ci.test",
			},
		}
		if mut != nil {
			mut(r)
		}
		return r
	}

	cases := []struct {
		name string
		rec  *evidencev1.EvidenceRecord
	}{
		{"empty-scope-not-allowed-use-single", base(nil)},
		{"sub-microsecond-observed-at (regression)", base(func(r *evidencev1.EvidenceRecord) {
			r.ObservedAt = timestamppb.New(subUS)
		})},
		{"microsecond-aligned-observed-at", base(func(r *evidencev1.EvidenceRecord) {
			r.ObservedAt = timestamppb.New(usAligned)
		})},
		{"multi-dim-scope", base(func(r *evidencev1.EvidenceRecord) {
			r.Scope = []*evidencev1.ScopeDimension{
				{Key: "environment", Values: []string{"prod"}},
				{Key: "cloud_account", Values: []string{"acct-a", "acct-b"}},
			}
		})},
		{"unsorted-scope-input", base(func(r *evidencev1.EvidenceRecord) {
			r.Scope = []*evidencev1.ScopeDimension{
				{Key: "zone", Values: []string{"z2", "z1"}},
				{Key: "environment", Values: []string{"staging", "prod"}},
			}
		})},
		{"numeric-payload", base(func(r *evidencev1.EvidenceRecord) {
			r.Payload = numericPayload(t)
			r.ObservedAt = timestamppb.New(subUS)
		})},
		{"nil-payload", base(func(r *evidencev1.EvidenceRecord) {
			r.Payload = nil
			r.ObservedAt = timestamppb.New(subUS)
		})},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ingestHash, err := canonjson.HashRecord(tc.rec)
			if err != nil {
				t.Fatalf("HashRecord(ingest): %v", err)
			}

			// Round-trip through the ledger persistence + REAL reconstruction.
			row := persistLikeIngest(t, tc.rec, false /*lossless column present*/)
			rebuilt, err := RecordFromLedgerRow(row)
			if err != nil {
				t.Fatalf("RecordFromLedgerRow: %v", err)
			}
			rebuiltHash, err := canonjson.HashRecord(rebuilt)
			if err != nil {
				t.Fatalf("HashRecord(rebuilt): %v", err)
			}
			if rebuiltHash != ingestHash {
				t.Fatalf("round-trip hash mismatch:\n ingest  = %s\n rebuilt = %s\n observed_at nanos: orig=%d rebuilt=%d",
					ingestHash, rebuiltHash,
					tc.rec.GetObservedAt().AsTime().UnixNano(),
					rebuilt.GetObservedAt().AsTime().UnixNano())
			}
		})
	}
}

// TestVerifyRoundTrip_LegacyFallback_AC4 proves a pre-slice-633 row
// (observed_at_nanos NULL) still reconstructs via the lossy TIMESTAMPTZ
// column — no panic, no error, and it verifies clean when its OWN baseline
// hash (the ledger-reconstructable hash) is used (the slice-464/474 contract
// for legacy rows). It deliberately does NOT assert equality with the
// nanosecond ingest hash, because a legacy row's persisted observed_at is
// microsecond-truncated by definition.
func TestVerifyRoundTrip_LegacyFallback_AC4(t *testing.T) {
	t.Parallel()

	rec := &evidencev1.EvidenceRecord{
		IdempotencyKey: "neutral-legacy-key",
		EvidenceKind:   "sast.scan_result.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope:          []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}},
		ObservedAt:     timestamppb.New(time.Unix(1780954473, 585946123).UTC()),
		Result:         evidencev1.Result_RESULT_PASS,
		Payload:        structPayload(t),
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "service_account", ActorId: "ci.test",
		},
	}

	row := persistLikeIngest(t, rec, true /*legacy: no lossless column*/)
	if row.ObservedAtNanos != nil {
		t.Fatalf("legacy row should have NULL observed_at_nanos")
	}
	rebuilt, err := RecordFromLedgerRow(row)
	if err != nil {
		t.Fatalf("RecordFromLedgerRow (legacy): %v", err)
	}
	// Reconstructed observed_at is the microsecond-truncated value.
	if got := rebuilt.GetObservedAt().AsTime().UnixNano(); got != 1780954473585946000 {
		t.Fatalf("legacy reconstruction observed_at = %d, want microsecond-truncated 1780954473585946000", got)
	}
	// Its ledger-reconstructable baseline hash is self-consistent (the
	// slice-464 baseline-stamp contract): re-deriving twice is stable.
	h1, err := canonjson.HashRecord(rebuilt)
	if err != nil {
		t.Fatalf("HashRecord(legacy rebuilt): %v", err)
	}
	rebuilt2, _ := RecordFromLedgerRow(row)
	h2, _ := canonjson.HashRecord(rebuilt2)
	if h1 != h2 {
		t.Fatalf("legacy reconstruction not stable: %s != %s", h1, h2)
	}
}
