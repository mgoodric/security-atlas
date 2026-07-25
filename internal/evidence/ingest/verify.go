package ingest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Slice 464 — ledger-integrity verify support.
//
// `atlas evidence verify` walks the append-only evidence ledger and, for
// each record, recomputes the canonical hash of the record AS IT IS
// RECONSTRUCTABLE FROM THE LEDGER and compares it to the stored `hash`
// column. A divergence means a row's persisted content (most importantly
// the `payload` column) was mutated outside the ingest path — i.e. a
// silently-corrupted or tampered record.
//
// READ-ONLY: nothing here writes to the ledger. The functions take a
// `dbx.EvidenceRecord` (already read under RLS by the caller) and never
// touch the database. Constitutional invariant #2 (append-only ledger,
// point-in-time replay) is preserved — verify only re-derives and compares.
//
// Reconstruction fidelity (load-bearing — slice 464 decisions log D1, closed
// by slice 474): the ingest write path stores the record's fields across
// columns. Before slice 474 it discarded the original wire `scope` (only the
// empty `scope_id` column existed), so the verify could not reproduce the
// ingest scope-inclusive hash for a production record. Slice 474 adds the
// `scope_canonical` JSONB column: ingest now persists the canonical (sorted)
// wire scope the hash was computed over, and `RecordFromLedgerRow` rehydrates
// it. For a record ingested AFTER slice 474, the reconstructed record is
// byte-identical (under canonical normalization) to what ingest hashed, so
// the verify walk validates production records cleanly. For a legacy
// (pre-slice-474) row, `scope_canonical` is NULL; the reconstruction falls
// back to scope-free, preserving the slice-464 baseline contract for those
// rows. The CLI compares THIS recomputed hash against the stored `hash`
// column. The corruption AC-3 introduces (mutating the `payload` column in
// place) still changes the recomputed hash and is reported.
//
// Slice 633 closes the SAME divergence class for `observed_at`. The hash
// covers a nanosecond-precision proto Timestamp, but the `observed_at`
// TIMESTAMPTZ column is microsecond-precision and truncates sub-us nanos —
// so reconstructing from it diverged from the ingest hash for any record
// whose observed_at carried sub-microsecond nanoseconds (the CI-Linux case;
// slice 474 missed it because macOS time.Now() is often us-aligned). Ingest
// now also persists `observed_at_nanos BIGINT` (lossless UnixNano), and
// `RecordFromLedgerRow` reconstructs observed_at from it when present,
// falling back to the lossy TIMESTAMPTZ column for legacy (pre-633, NULL)
// rows — the same legacy-fallback shape 474 used for scope.

// RecordFromLedgerRow reconstructs the canonical EvidenceRecord proto from a
// persisted ledger row. Used by the verify walk to recompute the canonical
// hash. The reconstruction mirrors the fields that ingest persists:
// idempotency_key, evidence_kind, schema_version, control_ref, observed_at,
// result, payload (the stored protojson bytes), payload_uri, and
// source_attribution (the stored JSONB).
//
// Reconstruction is total: every persisted field that contributes to the
// hash is restored from its column. The function returns an error only when
// a persisted column cannot be decoded (corrupt protojson payload, corrupt
// source_attribution JSON) — which is itself an integrity signal the caller
// surfaces as a mismatch.
func RecordFromLedgerRow(row dbx.EvidenceRecord) (*evidencev1.EvidenceRecord, error) {
	rec := &evidencev1.EvidenceRecord{
		EvidenceKind:  deref(row.EvidenceKind),
		SchemaVersion: deref(row.SchemaVersion),
		ControlId:     row.ControlRef,
		Result:        resultEnumToProto(row.Result),
	}
	if row.IdempotencyKey != nil {
		rec.IdempotencyKey = *row.IdempotencyKey
	}
	// Slice 633: reconstruct observed_at from the LOSSLESS nanosecond column
	// when present, so the recomputed hash matches the ingest hash (which
	// covered the full nanosecond proto Timestamp). The `observed_at`
	// TIMESTAMPTZ column is microsecond-precision and truncates sub-us nanos;
	// rebuilding from it would diverge from the ingest hash for any record
	// whose observed_at carried sub-microsecond nanoseconds. A NULL
	// observed_at_nanos (a pre-slice-633 legacy row) falls back to the lossy
	// TIMESTAMPTZ column — the slice-464/474 baseline for those rows.
	if row.ObservedAtNanos != nil {
		rec.ObservedAt = timestamppb.New(time.Unix(0, *row.ObservedAtNanos).UTC())
	} else if row.ObservedAt.Valid {
		rec.ObservedAt = timestamppb.New(row.ObservedAt.Time)
	}
	if row.PayloadUri != nil {
		rec.PayloadUri = row.PayloadUri
	}

	// Slice 474: rehydrate the canonical wire scope persisted at ingest so
	// the recomputed hash matches the scope-inclusive ingest hash. A NULL /
	// empty column (a pre-slice-474 legacy row) yields a nil scope — the
	// slice-464 scope-free reconstruction.
	if len(row.ScopeCanonical) > 0 {
		scope, err := canonjson.UnmarshalCanonicalScope(row.ScopeCanonical)
		if err != nil {
			return nil, fmt.Errorf("ingest: decode scope_canonical column: %w", err)
		}
		rec.Scope = scope
	}

	// Payload column holds the protojson-marshaled Struct bytes that landed
	// in the ledger (post-redaction). Unmarshal back into a Struct.
	if len(row.Payload) > 0 {
		var payload structpb.Struct
		if err := protojson.Unmarshal(row.Payload, &payload); err != nil {
			return nil, fmt.Errorf("ingest: decode payload column: %w", err)
		}
		rec.Payload = &payload
	}

	// source_attribution column holds the JSONB the ingest path wrote:
	// {"actor_type","actor_id","session_id"}.
	if len(row.SourceAttribution) > 0 {
		var sa struct {
			ActorType string `json:"actor_type"`
			ActorID   string `json:"actor_id"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(row.SourceAttribution, &sa); err != nil {
			return nil, fmt.Errorf("ingest: decode source_attribution column: %w", err)
		}
		rec.SourceAttribution = &evidencev1.SourceAttribution{
			ActorType: sa.ActorType,
			ActorId:   sa.ActorID,
			SessionId: sa.SessionID,
		}
	}

	return rec, nil
}

// LedgerRowHash recomputes the canonical hash of the record reconstructed
// from a persisted ledger row. This is the value the verify walk compares
// against the stored `hash` column.
func LedgerRowHash(row dbx.EvidenceRecord) (string, error) {
	rec, err := RecordFromLedgerRow(row)
	if err != nil {
		return "", err
	}
	return canonjson.HashRecord(rec)
}

// VerifyLedgerRow recomputes the canonical hash of a ledger row and reports
// whether it matches the stored `hash`. ok=false means the row is corrupt
// (content mutated outside ingest, or an undecodable persisted column).
// recomputed is the value verify derived; on a decode error it is empty and
// err is non-nil (the caller treats any error as a mismatch — a row whose
// payload no longer decodes is, by definition, corrupt).
func VerifyLedgerRow(row dbx.EvidenceRecord) (ok bool, recomputed string, err error) {
	recomputed, err = LedgerRowHash(row)
	if err != nil {
		return false, "", err
	}
	return recomputed == row.Hash, recomputed, nil
}

// RowID returns the record id as a string for reporting.
func RowID(row dbx.EvidenceRecord) string {
	if !row.ID.Valid {
		return ""
	}
	return uuid.UUID(row.ID.Bytes).String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resultEnumToProto is the inverse of protoResultToEnum (defined in
// ingest.go): map the SQL evidence_result enum back to the proto Result.
func resultEnumToProto(r dbx.EvidenceResult) evidencev1.Result {
	switch r {
	case dbx.EvidenceResultPass:
		return evidencev1.Result_RESULT_PASS
	case dbx.EvidenceResultFail:
		return evidencev1.Result_RESULT_FAIL
	case dbx.EvidenceResultNa:
		return evidencev1.Result_RESULT_NA
	case dbx.EvidenceResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	}
	return evidencev1.Result_RESULT_UNSPECIFIED
}
