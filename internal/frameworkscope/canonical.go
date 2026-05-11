// Package frameworkscope implements the slice-018 FrameworkScope primitive:
// the per-framework subset of scope cells, governed by the four-state
// lifecycle in docs/adr/0001-framework-scope-workflow.md.
//
// The package exposes:
//
//   - Canonicalize: deterministic JSON encoding + sha256 hash of a predicate
//     so the trigger comparison (OLD.predicate_hash <> NEW.predicate_hash)
//     and the application-side compute see identical bytes.
//   - Validate: parses the predicate using the existing slice-017 scope-engine
//     AST. The same operator surface (`true`, `eq`, `in`, `and`, `or`, `not`)
//     applies — FrameworkScope.predicate is the same shape as
//     Control.applicability_expr, by canvas §5.5 design.
//   - Store: pgxpool-backed CRUD + workflow-transition operations. Same
//     RLS-via-tx-GUC pattern as internal/scope.Store.
//   - EffectiveScope: intersection of a control's applicability set with the
//     currently-active framework_scope predicate (canvas §5.5; AC-11).
package frameworkscope

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Canonicalize takes raw predicate JSON, normalises it (recursive key-sort
// for object members, no extra whitespace), and returns the canonical bytes
// plus the sha256 hex digest. The trigger
// `framework_scopes_bounce_on_predicate_change_trg` compares the stored
// predicate_hash to the new one bit-for-bit, so this canonicalizer is the
// single source of truth for the encoding.
//
// The empty-true forms (nil, "", "null", "{}") all collapse to the canonical
// "true" predicate (`{"op":"true"}`), matching slice-017's scope.Evaluate
// AC-4 ("match every cell").
//
// Returns ErrPredicateMalformed if the input is not valid JSON.
func Canonicalize(predicate []byte) (canonical []byte, hashHex string, err error) {
	// Empty / true forms — collapse to the canonical true predicate.
	trimmed := strings.TrimSpace(string(predicate))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "true" {
		canon := []byte(`{"op":"true"}`)
		sum := sha256.Sum256(canon)
		return canon, hex.EncodeToString(sum[:]), nil
	}

	var v any
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrPredicateMalformed, err)
	}
	canon, err := canonicalEncode(v)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canon)
	return canon, hex.EncodeToString(sum[:]), nil
}

// ErrPredicateMalformed is returned by Canonicalize and Validate when the
// input is not valid JSON or does not parse as a scope-engine AST.
var ErrPredicateMalformed = errors.New("frameworkscope: predicate malformed")

// canonicalEncode emits a deterministic JSON encoding of v: keys sorted
// inside every object, no trailing whitespace, json.Number values preserved
// exactly as parsed. The output is byte-stable so the predicate_hash is
// reproducible across runs and across Go-vs-Postgres equality.
func canonicalEncode(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		return writeJSONString(b, t)
	case json.Number:
		b.WriteString(t.String())
	case float64:
		// Marshalling through encoding/json keeps the same shape Go would
		// emit elsewhere; predicate authors rarely use floats but the path
		// is here for robustness.
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(raw)
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		b.WriteByte('{')
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSONString(b, k); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("%w: unsupported JSON value type %T", ErrPredicateMalformed, v)
	}
	return nil
}

// writeJSONString writes s as a JSON string literal with the same escapes
// the standard encoder uses for the characters predicates actually contain.
// Mirrors internal/scope/canonical.go so the two surfaces stay in lockstep.
func writeJSONString(b *strings.Builder, s string) error {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				_, _ = fmt.Fprintf(b, `\u%04x`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return nil
}
