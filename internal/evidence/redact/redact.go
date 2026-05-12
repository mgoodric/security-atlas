// Package redact applies per-evidence_kind redaction rules to evidence
// payloads at the ingestion stage. Rules are JSONPath-like expressions
// declared in the schema-registry entry under the `x-redaction-rules`
// extension key (slice 014 stores schema bodies as JSONB; the extension
// is additive — no migration required).
//
// Slice 015's AC-6 mandates that redaction runs at ingestion (not
// evaluation): the ledger never sees the unredacted payload. The
// rule grammar is a small subset of JSONPath, deliberately narrow so
// the security boundary is auditable:
//
//	$.field                  // top-level field
//	$.parent.child           // nested object walk
//	$.array[*].field         // every element of an array
//
// Matched values are replaced with the literal Marker string. The hash
// computed downstream of redact.Apply is the hash of the REDACTED form,
// so idempotency dedup remains deterministic.
//
// Anti-criterion (slice 015 P0): the redactor MUST NOT log the payload
// it sees. Callers also MUST NOT log the unredacted payload. Apply is
// pure (no I/O); the only opportunity for leakage is the caller.
package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

// Marker is the literal that replaces redacted scalar values. Chosen for
// visual distinctness in audit log inspection.
const Marker = "<<REDACTED>>"

// ErrMalformedRule is returned by Apply / ExtractRulesFromSchema when a
// rule does not match the supported grammar. Wrapped so the caller can
// route it to a 400-class HTTP response if the schema registry tries to
// load an invalid schema at runtime.
var ErrMalformedRule = errors.New("redact: malformed rule")

// Apply walks `payload` and replaces every value matched by any rule in
// `rules` with the Marker constant. Returns a new Struct; the input is
// not mutated. Missing paths are a silent no-op (a schema may declare a
// rule for an optional field).
//
// Returns ErrMalformedRule (wrapped) when any rule fails to parse.
func Apply(payload *structpb.Struct, rules []string) (*structpb.Struct, error) {
	if payload == nil {
		return nil, nil
	}
	if len(rules) == 0 {
		return payload, nil
	}
	// Clone to avoid mutating the caller's payload — Process expects to
	// hash the original-shape record after redaction, but the caller
	// downstream of Process may still hold a reference.
	clone, err := cloneStruct(payload)
	if err != nil {
		return nil, fmt.Errorf("redact: clone payload: %w", err)
	}
	for _, raw := range rules {
		parsed, perr := parseRule(raw)
		if perr != nil {
			return nil, perr
		}
		applyParsed(clone, parsed)
	}
	return clone, nil
}

// ExtractRulesFromSchema reads `x-redaction-rules` from a JSON Schema
// body. Returns an empty slice if the key is absent. Errors out if the
// key is present but is not a JSON array of strings.
func ExtractRulesFromSchema(schemaJSON []byte) ([]string, error) {
	if len(schemaJSON) == 0 {
		return nil, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		// Not our problem — the schema registry's own validator catches
		// malformed JSON Schema. We treat this as "no rules" so the
		// pipeline does not break on a schema we cannot parse here.
		return nil, nil
	}
	raw, ok := doc["x-redaction-rules"]
	if !ok {
		return nil, nil
	}
	var rules []string
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("%w: x-redaction-rules must be an array of strings: %v", ErrMalformedRule, err)
	}
	// Validate each rule shape up-front so a malformed entry surfaces at
	// load time, not at the first push.
	for _, r := range rules {
		if _, perr := parseRule(r); perr != nil {
			return nil, perr
		}
	}
	return rules, nil
}

// ---- internal parsing ----

type segment struct {
	name     string // empty when wildcard
	wildcard bool   // true for [*]
}

type parsedRule struct {
	segments []segment
}

// parseRule accepts:
//
//	$.field
//	$.a.b.c
//	$.findings[*].token
//	$.matrix[*][*].cell      (multi-dim wildcards permitted)
func parseRule(raw string) (parsedRule, error) {
	if raw == "" {
		return parsedRule{}, fmt.Errorf("%w: empty rule", ErrMalformedRule)
	}
	if !strings.HasPrefix(raw, "$.") {
		return parsedRule{}, fmt.Errorf("%w: rule must start with $.: %q", ErrMalformedRule, raw)
	}
	rest := raw[2:]
	if rest == "" {
		return parsedRule{}, fmt.Errorf("%w: rule has no path after $.: %q", ErrMalformedRule, raw)
	}
	var segs []segment
	// Tokenize: split on '.' but recognize '[*]' as its own token attached
	// to the preceding name segment.
	for _, part := range strings.Split(rest, ".") {
		if part == "" {
			return parsedRule{}, fmt.Errorf("%w: empty segment in %q", ErrMalformedRule, raw)
		}
		// Pull off any trailing [*] occurrences.
		name := part
		wildcards := 0
		for strings.HasSuffix(name, "[*]") {
			name = strings.TrimSuffix(name, "[*]")
			wildcards++
		}
		if strings.ContainsAny(name, "[]") {
			return parsedRule{}, fmt.Errorf("%w: only [*] subscripts allowed: %q", ErrMalformedRule, raw)
		}
		if name == "" && wildcards == 0 {
			return parsedRule{}, fmt.Errorf("%w: empty segment in %q", ErrMalformedRule, raw)
		}
		if name != "" {
			segs = append(segs, segment{name: name})
		}
		for i := 0; i < wildcards; i++ {
			segs = append(segs, segment{wildcard: true})
		}
	}
	if len(segs) == 0 {
		return parsedRule{}, fmt.Errorf("%w: rule resolved to no segments: %q", ErrMalformedRule, raw)
	}
	return parsedRule{segments: segs}, nil
}

// applyParsed walks the struct following segments, replacing the leaf
// scalar (or list/struct) with Marker. Missing intermediate segments are
// silently skipped (no-op) per the missing-path contract.
func applyParsed(s *structpb.Struct, rule parsedRule) {
	walk(structValue(s), rule.segments, func(parent *structpb.Value, key string, idx int) {
		// Replace whatever was at this leaf with the marker.
		marker := structpb.NewStringValue(Marker)
		if idx >= 0 {
			lv := parent.GetListValue()
			if lv == nil || idx >= len(lv.Values) {
				return
			}
			lv.Values[idx] = marker
			return
		}
		sv := parent.GetStructValue()
		if sv == nil {
			return
		}
		if _, ok := sv.Fields[key]; !ok {
			return
		}
		sv.Fields[key] = marker
	})
}

// walk recursively descends `node` per `segs`. When the leaf segment is
// reached, calls `leaf(parent, key, idx)`:
//   - for an object-leaf: parent=structValue, key=segment name, idx=-1
//   - for a list-leaf:    parent=listValue,   key="",            idx=item idx
func walk(node *structpb.Value, segs []segment, leaf func(parent *structpb.Value, key string, idx int)) {
	if node == nil || len(segs) == 0 {
		return
	}
	seg := segs[0]
	rest := segs[1:]

	if seg.wildcard {
		lv := node.GetListValue()
		if lv == nil {
			return
		}
		for i, item := range lv.Values {
			if len(rest) == 0 {
				leaf(node, "", i)
			} else {
				walk(item, rest, leaf)
			}
		}
		return
	}

	sv := node.GetStructValue()
	if sv == nil {
		return
	}
	child, ok := sv.Fields[seg.name]
	if !ok {
		return
	}
	if len(rest) == 0 {
		leaf(node, seg.name, -1)
		return
	}
	walk(child, rest, leaf)
}

// cloneStruct deep-copies a Struct by round-tripping through map[string]any.
// Fast enough for the per-record path (typical payload is small JSON), and
// avoids an external dependency on a proto deep-clone helper.
func cloneStruct(s *structpb.Struct) (*structpb.Struct, error) {
	if s == nil {
		return nil, nil
	}
	return structpb.NewStruct(s.AsMap())
}

func structValue(s *structpb.Struct) *structpb.Value {
	return structpb.NewStructValue(s)
}
