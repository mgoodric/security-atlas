// Package canonjson produces deterministic byte representations of
// EvidenceRecord values for tamper-evident hashing.
//
// We hash the protobuf deterministic encoding (not JSON) — proto3
// deterministic serialization is library-stable across Go, Python, and
// protobuf-es, which a hand-rolled canonical JSON spec is not. See the
// EvidenceReceipt.hash field comment in proto/evidence/v1/evidence.proto.
package canonjson

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// HashRecord returns the sha256-hex of the deterministic protobuf encoding
// of r. r.Scope is replaced with a sorted copy before marshaling so client
// ordering of scope dimensions does not affect the hash; other fields are
// not touched. Caller-supplied payloads (potentially large) are not cloned.
func HashRecord(r *evidencev1.EvidenceRecord) (string, error) {
	original := r.Scope
	r.Scope = sortedScope(original)
	defer func() { r.Scope = original }()

	bytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("canonjson: marshal: %w", err)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

// scopeDimensionJSON is the on-disk shape of one canonical scope dimension.
// It mirrors evidencev1.ScopeDimension (key + sorted values) in a form that
// round-trips losslessly through JSONB.
type scopeDimensionJSON struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// MarshalCanonicalScope serializes scope into the canonical (sorted-by-key,
// values-sorted) JSON the slice-474 ledger column persists. Both ingest
// (writing the column) and the verify walk (reading it back) call this so
// the persisted bytes exactly describe the scope HashRecord normalized
// before hashing. An empty/nil scope marshals to `null` so a NULL JSONB
// column round-trips to "no scope".
func MarshalCanonicalScope(scope []*evidencev1.ScopeDimension) ([]byte, error) {
	if len(scope) == 0 {
		return []byte("null"), nil
	}
	sorted := sortedScope(scope)
	out := make([]scopeDimensionJSON, len(sorted))
	for i, d := range sorted {
		out[i] = scopeDimensionJSON{Key: d.GetKey(), Values: d.GetValues()}
	}
	return json.Marshal(out)
}

// UnmarshalCanonicalScope rebuilds the scope dimensions persisted by
// MarshalCanonicalScope. Empty input, JSON `null`, or `[]` all yield a nil
// scope (the "legacy / no scope" case). The returned scope is already
// canonical (it was stored canonical), but HashRecord re-sorts defensively,
// so callers need not re-normalize.
func UnmarshalCanonicalScope(raw []byte) ([]*evidencev1.ScopeDimension, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var in []scopeDimensionJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("canonjson: decode scope_canonical: %w", err)
	}
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*evidencev1.ScopeDimension, len(in))
	for i, d := range in {
		out[i] = &evidencev1.ScopeDimension{Key: d.Key, Values: d.Values}
	}
	return out, nil
}

// sortedScope returns a copy of scope with dimensions sorted by key and
// each dimension's values sorted lexicographically. Deterministic protobuf
// serialization preserves repeated-field order, so caller order would
// otherwise leak into the hash.
func sortedScope(scope []*evidencev1.ScopeDimension) []*evidencev1.ScopeDimension {
	if len(scope) == 0 {
		return scope
	}
	out := make([]*evidencev1.ScopeDimension, len(scope))
	for i, d := range scope {
		values := append([]string(nil), d.Values...)
		sort.Strings(values)
		out[i] = &evidencev1.ScopeDimension{Key: d.Key, Values: values}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
