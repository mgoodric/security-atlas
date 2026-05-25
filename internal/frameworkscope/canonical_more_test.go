// Additional unit tests for the pure canonicalization surface in
// internal/frameworkscope/canonical.go.
//
// Load-bearing functions exercised (and their uncovered branches at slice
// 279 audit time):
//
//   - writeCanonical — the recursive JSON emitter. Pre-279 coverage: 61.8%.
//     Uncovered branches included `null`, `bool false`, `float64`, nested
//     `[]any`, and the catch-all `default` arm. Each branch is the
//     authoritative encoder for its JSON shape; a regression here would
//     desync the canonical bytes from the trigger's predicate_hash compare,
//     causing the framework_scope lifecycle to bounce on a no-op edit.
//   - writeJSONString — JSON string escaping. Pre-279 coverage: 53.3%.
//     Uncovered branches included `\\`, `\n`, `\r`, `\t`, and the low
//     control-char `\uXXXX` escape. Predicate authors include none of these
//     in practice today, but the encoder MUST be byte-stable across them
//     since the trigger relies on it.
//   - canonicalEncode — the writeCanonical wrapper. Coverage flows up from
//     writeCanonical.
//
// Branches deliberately NOT covered here: the `default` arm of
// writeCanonical that returns "unsupported JSON value type" — that arm is
// only reachable for types `json.Decoder.UseNumber` cannot emit (custom
// types, channels, etc.); a unit-level test would need to construct an
// `any` of an exotic shape, which would be testing the test harness, not
// the encoder. Left for a future audit.
//
// Slice 279 — coverage lift target. Pre-lift merged %: 21.8 (predicate.go
// + canonical.go path only). These tests target the encoder branch surface
// and push toward the 70% bar by closing the writeCanonical + writeJSONString
// holes.

package frameworkscope_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

func TestCanonicalize_NumericValuePreserved(t *testing.T) {
	t.Parallel()
	// json.Decoder.UseNumber preserves the exact textual representation of
	// the number. The encoder must round-trip it verbatim.
	in := []byte(`{"op":"eq","dim":"port","value":443}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"port","op":"eq","value":443}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_BoolTrueValueEmitted(t *testing.T) {
	t.Parallel()
	// `{"k":true}` exercises the bool-true branch of writeCanonical.
	// (Note: the bare `true` predicate is short-circuited above into the
	// canonical `{"op":"true"}` form — to reach the bool-true branch we
	// need a nested boolean value inside an object.)
	in := []byte(`{"op":"flag","enabled":true}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"enabled":true,"op":"flag"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_BoolFalseValueEmitted(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"flag","enabled":false}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"enabled":false,"op":"flag"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_NullValuePreserved(t *testing.T) {
	t.Parallel()
	// A nested `null` value (NOT the bare `null` input — that collapses).
	in := []byte(`{"op":"is_null","value":null}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"op":"is_null","value":null}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_NestedArrayPreservesOrder(t *testing.T) {
	t.Parallel()
	// Arrays preserve order (unlike object keys); the encoder must walk
	// them in source order with no sort.
	in := []byte(`{"op":"in","dim":"env","values":["prod","staging","dev"]}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"env","op":"in","values":["prod","staging","dev"]}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_EmptyArrayHandled(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"in","dim":"env","values":[]}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"env","op":"in","values":[]}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_Backslash(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"eq","dim":"path","value":"C:\\Users"}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	// The canonical bytes for the string value must contain a literal
	// double-backslash; that's the JSON-source escape for a single
	// backslash in the decoded value.
	want := `{"dim":"path","op":"eq","value":"C:\\Users"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_DoubleQuote(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"eq","dim":"motto","value":"it's \"fine\""}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"motto","op":"eq","value":"it's \"fine\""}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_Newline(t *testing.T) {
	t.Parallel()
	// JSON source uses the \u000A unicode escape; the decoder yields a Go
	// string with a real newline byte; writeJSONString re-emits it as the
	// canonical \n escape.
	in := []byte(`{"op":"eq","dim":"d","value":"a\u000Ab"}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"d","op":"eq","value":"a\nb"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_Tab(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"eq","dim":"d","value":"a\u0009b"}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"d","op":"eq","value":"a\tb"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_CarriageReturn(t *testing.T) {
	t.Parallel()
	in := []byte(`{"op":"eq","dim":"d","value":"a\u000Db"}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"d","op":"eq","value":"a\rb"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_StringEscape_LowControlChar(t *testing.T) {
	t.Parallel()
	// JSON source escapes SOH (0x01) as a unicode escape; the decoder yields
	// a Go string with byte 0x01; writeJSONString's c<0x20 default branch
	// re-emits as a unicode escape (lowercase hex per the encoder's fmt format).
	in := []byte(`{"op":"eq","dim":"d","value":"a\u0001b"}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"dim":"d","op":"eq","value":"a\u0001b"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
}

func TestCanonicalize_HashStability(t *testing.T) {
	t.Parallel()
	// The sha256 returned must match a hand-computed digest over the
	// canonical bytes — guards against any future drift in the hash step.
	in := []byte(`{"op":"true"}`)
	canon, hash, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sum := sha256.Sum256(canon)
	want := hex.EncodeToString(sum[:])
	if hash != want {
		t.Fatalf("hash: got=%s want=%s", hash, want)
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d; want 64 (sha256 hex)", len(hash))
	}
}

func TestCanonicalize_DeeplyNested(t *testing.T) {
	t.Parallel()
	// `and` over `or` over `not` over `eq` — exercises the recursive
	// writeCanonical traversal.
	in := []byte(`{"op":"and","args":[{"op":"or","args":[{"op":"not","arg":{"op":"eq","dim":"x","value":"y"}}]}]}`)
	canon, _, err := frameworkscope.Canonicalize(in)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"args":[{"args":[{"arg":{"dim":"x","op":"eq","value":"y"},"op":"not"}],"op":"or"}],"op":"and"}`
	if string(canon) != want {
		t.Fatalf("canonical:\n got: %s\nwant: %s", canon, want)
	}
	// Validate must accept the canonical form back (round-trip).
	if err := frameworkscope.Validate(canon); err != nil {
		t.Fatalf("validate canonical: %v", err)
	}
}
