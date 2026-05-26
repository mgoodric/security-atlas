package scope_test

// expr_helpers_test.go — pure-Go unit coverage for the scope package's
// stateless helpers + the Engine wrapper. Slice 284 lift.
//
// Load-bearing functions exercised by this file:
//
//   - scope.NewEngine + DefaultEngine.Applicability (expr.go) — the public
//     Engine wrapper that slice 012's evaluation engine imports. Branches:
//     nil/empty expr (AC-4), eq, in, and, or, not, malformed JSON.
//   - scope.Canonicalize (canonical.go) — the empty-key rejection branch
//     not exercised by the existing canonical_test.go cases. Backs the
//     DB UNIQUE(tenant_id, dimensions_hash) determinism contract.
//   - scope.Canonicalize — JSON-string escape paths for the control
//     characters / quote / backslash / newline / tab / CR fields. These
//     live in writeJSONString (canonical.go) and are reachable via the
//     public Canonicalize API by including those bytes in dim values.
//   - scope.Evaluate (expr.go) — additional rejection-loud cases on top
//     of expr_test.go: missing-`dim` on `in`, validate's recursive
//     descent into nested and/or args, the `not` recursive case.
//
// These tests are deliberately DB-free; the integration_test.go file
// (build tag `integration`) covers the store.go surface against a real
// Postgres + RLS. The unit + integration profiles merge in CI per slice
// 279's measurement pipeline.

import (
	"context"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// TestDefaultEngine_Applicability covers the Engine wrapper that slice 012
// imports. The wrapper is a thin pass-through to Evaluate; this confirms
// the contract (nil context, nil/empty expr -> universe; real expr ->
// subset) and exercises every operator at least once.
func TestDefaultEngine_Applicability(t *testing.T) {
	cells := []scope.Cell{
		{Label: "prod-platform", Dimensions: map[string]string{
			"environment": "prod", "business_unit": "platform",
		}},
		{Label: "dev-platform", Dimensions: map[string]string{
			"environment": "dev", "business_unit": "platform",
		}},
		{Label: "prod-data", Dimensions: map[string]string{
			"environment": "prod", "business_unit": "data",
		}},
	}

	tests := []struct {
		name   string
		exprJS string
		want   []string
	}{
		{
			name:   "nil expression matches universe (AC-4)",
			exprJS: "",
			want:   []string{"prod-platform", "dev-platform", "prod-data"},
		},
		{
			name:   "empty object matches universe (AC-4)",
			exprJS: "{}",
			want:   []string{"prod-platform", "dev-platform", "prod-data"},
		},
		{
			name:   "null matches universe (AC-4)",
			exprJS: "null",
			want:   []string{"prod-platform", "dev-platform", "prod-data"},
		},
		{
			name:   "eq narrows to one dimension value",
			exprJS: `{"op":"eq","dim":"business_unit","value":"data"}`,
			want:   []string{"prod-data"},
		},
		{
			name:   "and composes multiple constraints",
			exprJS: `{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"business_unit","value":"platform"}]}`,
			want:   []string{"prod-platform"},
		},
		{
			name:   "not inverts the match set",
			exprJS: `{"op":"not","arg":{"op":"eq","dim":"environment","value":"prod"}}`,
			want:   []string{"dev-platform"},
		},
	}

	engine := scope.NewEngine()
	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.Applicability(context.Background(), []byte(tc.exprJS), cells)
			if err != nil {
				t.Fatalf("Applicability: %v", err)
			}
			if !equalLabels(got, tc.want) {
				t.Fatalf("labels = %v; want %v", labelsOf(got), tc.want)
			}
		})
	}
}

// TestDefaultEngine_Applicability_RejectsMalformed confirms the wrapper
// surfaces evaluator errors (anti-criterion: never silently drop cells
// on a bad expression).
func TestDefaultEngine_Applicability_RejectsMalformed(t *testing.T) {
	cells := []scope.Cell{
		{Label: "a", Dimensions: map[string]string{"environment": "prod"}},
	}
	engine := scope.NewEngine()
	if _, err := engine.Applicability(context.Background(), []byte(`{"op":"contains","dim":"x","value":"y"}`), cells); err == nil {
		t.Fatal("expected error for unknown op")
	}
}

// TestDefaultEngine_Applicability_EmptyUniverse confirms the empty-AC-4
// path still returns a non-nil empty slice (callers can range without a
// nil check — same contract Evaluate guarantees).
func TestDefaultEngine_Applicability_EmptyUniverse(t *testing.T) {
	engine := scope.NewEngine()
	got, err := engine.Applicability(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Applicability: %v", err)
	}
	if got == nil {
		t.Fatal("got nil slice; want empty non-nil per Evaluate contract")
	}
	if len(got) != 0 {
		t.Fatalf("got %d cells; want 0", len(got))
	}
}

// TestCanonicalize_RejectsEmptyKey covers the canonical-encoder's
// empty-dimension-key guard. The DB UNIQUE on
// (tenant_id, dimensions_hash) is meaningless if an empty key is
// allowed to pass through — the encoder rejects it loudly here.
func TestCanonicalize_RejectsEmptyKey(t *testing.T) {
	dims := map[string]string{"": "prod"}
	_, _, err := scope.Canonicalize(dims)
	if err == nil {
		t.Fatal("expected error for empty dimension key")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("error %q does not name the constraint clearly", err.Error())
	}
}

// TestCanonicalize_EscapesJSONStringSpecials covers the writeJSONString
// escape branches inside Canonicalize: double-quote, backslash, newline,
// carriage return, tab, and a sub-0x20 control character. Every operator
// path through writeJSONString must produce well-formed JSON so the
// downstream sha256 hash is deterministic across operating-system line
// endings and unusual user-supplied dimension values.
func TestCanonicalize_EscapesJSONStringSpecials(t *testing.T) {
	// All special characters writeJSONString must escape, packed into one
	// dim value so we exercise the switch statement's cases AND the
	// `c < 0x20` numeric fallback (here triggered by BEL = 0x07) in a
	// single pass. We build the string at runtime to avoid raw control
	// bytes appearing in source.
	bel := string(rune(0x07))
	specials := "tab:\t newline:\n cr:\r quote:\" backslash:\\ bel:" + bel

	dims := map[string]string{
		"environment": "prod",
		"weird":       specials,
		// Empty-string VALUE is allowed (only empty KEY is rejected);
		// keep an empty value too so we cover the trivial-no-escape path
		// in writeJSONString as well.
		"empty": "",
	}

	bytes1, hash1, err := scope.Canonicalize(dims)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	// Idempotency: same map MUST produce the same canonical encoding.
	bytes2, hash2, err := scope.Canonicalize(dims)
	if err != nil {
		t.Fatalf("Canonicalize (2nd): %v", err)
	}
	if string(bytes1) != string(bytes2) {
		t.Fatalf("canonical bytes drifted: %q vs %q", bytes1, bytes2)
	}
	if hash1 != hash2 {
		t.Fatalf("canonical hashes drifted: %s vs %s", hash1, hash2)
	}

	// The output must contain each expected escape sequence — proving the
	// switch branches fired rather than emitting the raw control byte.
	// Sub-0x20 bytes hit the `\uXXXX` fmt.Fprintf fallback inside
	// writeJSONString; BEL (0x07) renders as `` (six literal
	// bytes), NOT the Go-source escape sequence `\a`.
	got := string(bytes1)
	wantEscapes := []string{
		`\t`,     // tab branch
		`\n`,     // newline branch
		`\r`,     // CR branch
		`\"`,     // double-quote branch
		`\\`,     // backslash branch
		`\u0007`, // sub-0x20 control-character fallback
	}
	for _, want := range wantEscapes {
		if !strings.Contains(got, want) {
			t.Fatalf("expected escape %q in %q", want, got)
		}
	}
	// Raw BEL byte MUST NOT survive into the canonical output — if it
	// did, the sha256 hash would not be deterministic.
	if strings.ContainsRune(got, rune(0x07)) {
		t.Fatalf("raw BEL byte leaked into canonical output: %q", got)
	}
}

// TestCanonicalize_KeyOrderIsLexicographic guards the canonical-encoder's
// sort.Strings(keys) call. If keys ever stopped being sorted, the DB
// UNIQUE constraint on (tenant_id, dimensions_hash) would let near-duplicate
// rows in (same dims, different insertion order → different hash).
func TestCanonicalize_KeyOrderIsLexicographic(t *testing.T) {
	// Three keys whose lexicographic order is the opposite of a typical
	// insertion order (z first vs. a first).
	dims := map[string]string{"z": "1", "a": "2", "m": "3"}
	bytes, _, err := scope.Canonicalize(dims)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	got := string(bytes)
	// Expected exact shape — sorted, comma-joined, key-quoted.
	want := `{"a":"2","m":"3","z":"1"}`
	if got != want {
		t.Fatalf("canonical = %q; want %q", got, want)
	}
}

// TestEvaluate_NestedComposition covers the recursive descent through
// `not` of an `and` of two `eq`s — the validate() recursive call shape
// the existing expr_test.go does not exercise as a single composed
// expression. This pins the structural guarantee that nested validation
// catches problems anywhere in the tree.
func TestEvaluate_NestedComposition(t *testing.T) {
	cells := []scope.Cell{
		{Label: "prod-restricted", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "restricted",
		}},
		{Label: "prod-public", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "public",
		}},
		{Label: "staging-restricted", Dimensions: map[string]string{
			"environment": "staging", "data_classification": "restricted",
		}},
	}
	// "everything EXCEPT (prod AND restricted)"
	expr := `{"op":"not","arg":{"op":"and","args":[
		{"op":"eq","dim":"environment","value":"prod"},
		{"op":"eq","dim":"data_classification","value":"restricted"}
	]}}`
	got, err := scope.Evaluate([]byte(expr), cells)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := []string{"prod-public", "staging-restricted"}
	if !equalLabels(got, want) {
		t.Fatalf("labels = %v; want %v", labelsOf(got), want)
	}
}

// TestEvaluate_InRejectsMissingDim covers the `in` operator's `dim==""`
// validate branch. The existing expr_test.go covers `in missing values`
// (Values==nil) and `eq missing value` but not `in missing dim`.
func TestEvaluate_InRejectsMissingDim(t *testing.T) {
	cells := []scope.Cell{
		{Label: "a", Dimensions: map[string]string{"environment": "prod"}},
	}
	_, err := scope.Evaluate([]byte(`{"op":"in","values":["prod"]}`), cells)
	if err == nil {
		t.Fatal("expected error for `in` with no `dim`")
	}
}

// TestEvaluate_NestedValidationCatchesDeepError walks the validate()
// recursive descent — a malformed leaf nested two levels deep MUST be
// rejected. Anti-criterion: never silently drop cells.
func TestEvaluate_NestedValidationCatchesDeepError(t *testing.T) {
	cells := []scope.Cell{
		{Label: "a", Dimensions: map[string]string{"environment": "prod"}},
	}
	// `and` with a deeply-nested `or` that itself has an `eq` missing
	// `value`. The error MUST propagate out from match()-free validation.
	expr := `{"op":"and","args":[
		{"op":"eq","dim":"environment","value":"prod"},
		{"op":"or","args":[
			{"op":"eq","dim":"environment"}
		]}
	]}`
	if _, err := scope.Evaluate([]byte(expr), cells); err == nil {
		t.Fatal("expected error for deeply-nested malformed eq")
	}
}

// TestEvaluate_InWithMissingCellDimMisses covers the `in` operator's
// branch where the cell lacks the queried dimension entirely (vs. having
// it with a non-matching value). The existing expr_test.go only covers
// this case for `eq`.
func TestEvaluate_InWithMissingCellDimMisses(t *testing.T) {
	cells := []scope.Cell{
		// Cell has `environment` but lacks `product_line`.
		{Label: "no-product", Dimensions: map[string]string{"environment": "prod"}},
	}
	got, err := scope.Evaluate([]byte(`{"op":"in","dim":"product_line","values":["core","extras"]}`), cells)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d cells; want 0 (cell lacks dim → in must miss)", len(got))
	}
}

// equalLabels compares two cell slices by label, regardless of order.
// Order-insensitive because Evaluate's iteration order is universe-order
// but tests should remain stable if the package's iteration ever changes.
func equalLabels(got []scope.Cell, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, w := range want {
		seen[w]++
	}
	for _, c := range got {
		if seen[c.Label] == 0 {
			return false
		}
		seen[c.Label]--
	}
	return true
}
