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
