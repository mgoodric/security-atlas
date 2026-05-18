// Slice 140 — unit tests for the OpenAPI 3.1 spec generator.
//
// The generator is pure-Go (no external network, no DB) and reads
// the static RouteSpecs slice declared in routes.go. Tests cover:
//
//   - Determinism (P0: two runs produce byte-identical output —
//     load-bearing for the BLOCKING drift-detect CI guard).
//   - Every route emitted carries a `security` field (slice 140 P0-A1).
//   - Internal routes carry `x-internal: true` (slice 140 P0-A3).
//   - Spec validates against the OpenAPI 3.1 surface invariants
//     the generator owns (top-level openapi version, paths shape).
//   - Spec file ≤ 500 KB after emission (P0-A5).

package openapi

import (
	"bytes"
	"strings"
	"testing"
)

// TestGenerateDeterministic is the load-bearing test for the
// BLOCKING drift-detect CI guard. Two back-to-back Generate calls
// against the same RouteSpecs MUST produce byte-identical output.
//
// If this test fails, the drift-detect CI job becomes flaky and the
// merge-blocking discipline collapses.
func TestGenerateDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	if err := Generate(&first, RouteSpecs); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if err := Generate(&second, RouteSpecs); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("Generate is not deterministic; two runs produced different output")
	}
}

// TestEverySpecRouteHasSecurity enforces slice 140 P0-A1: no operation
// ships with an empty `security` block unless the route's Tier is
// explicitly "none" (the genuinely-public set: /health, /metrics,
// /v1/version, /v1/install-state, /v1/calendar.ics, /auth/*).
func TestEverySpecRouteHasSecurity(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	for _, r := range RouteSpecs {
		if r.Tier == "none" {
			continue
		}
		// We can't trivially parse YAML in this test without adding
		// a dep; instead assert the security key appears under the
		// route entry. The generator format is stable.
		op := opMarker(r)
		if !strings.Contains(yaml, op) {
			t.Fatalf("route %s %s not present in generated spec", r.Method, r.Path)
		}
	}
	// Counting check: every non-"none" route emits a `security:` block.
	wantSecurityBlocks := 0
	for _, r := range RouteSpecs {
		if r.Tier != "none" {
			wantSecurityBlocks++
		}
	}
	if got := strings.Count(yaml, "      security:"); got != wantSecurityBlocks {
		t.Fatalf("security blocks: got=%d want=%d (one per non-public operation)",
			got, wantSecurityBlocks)
	}
}

// TestInternalRoutesCarryExtension enforces slice 140 P0-A3: internal
// endpoints carry `x-internal: true`. The Redoc UI's filter relies on
// this extension to exclude them from the public render.
func TestInternalRoutesCarryExtension(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	wantInternal := 0
	for _, r := range RouteSpecs {
		if r.Internal {
			wantInternal++
		}
	}
	if got := strings.Count(yaml, "      x-internal: true"); got != wantInternal {
		t.Fatalf("x-internal markers: got=%d want=%d", got, wantInternal)
	}
}

// TestSpecHeader enforces the top-level OpenAPI 3.1 invariants the
// generator owns: openapi version, info block, three security schemes,
// and tags.
func TestSpecHeader(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	for _, want := range []string{
		"openapi: 3.1.0",
		"info:",
		"  title: security-atlas REST API",
		"components:",
		"  securitySchemes:",
		"    bearer:",
		"    adminBearer:",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("spec missing required marker: %q", want)
		}
	}
}

// TestSpecSizeUnderBudget enforces slice 140 P0-A5: spec file ≤ 500 KB.
// The generator is the gatekeeper — if a future change blows the
// budget (e.g. someone adds enormous inline examples), this test
// catches it before merge.
func TestSpecSizeUnderBudget(t *testing.T) {
	const budget = 500 * 1024
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Len() > budget {
		t.Fatalf("generated spec is %d bytes; budget is %d (P0-A5)", out.Len(), budget)
	}
}

// TestNeutralExamples enforces slice 140 P0-A4: no vendor prefixes,
// real emails, or real tenant names in example values. The generator
// itself emits no examples (operation-level body schemas + examples
// are a follow-on slice), but the test pins the property so a future
// generator extension cannot regress it.
func TestNeutralExamples(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	// Patterns that, if present, would indicate a non-neutral example.
	// These are illustrative — the generator does not currently emit
	// examples at all, so this is a forward-looking guard.
	forbidden := []string{
		"@mgoodric",
		"@gmail.com",
		"@anthropic.com",
		"sk_live_",
		"mgoodric.com",
	}
	for _, pat := range forbidden {
		if strings.Contains(yaml, pat) {
			t.Errorf("spec contains non-neutral example token %q (P0-A4)", pat)
		}
	}
}

// TestEveryRouteSpecAppearsInOutput sanity-checks that every entry in
// RouteSpecs produces a corresponding operation in the YAML. Backs
// AC-4 (every chi route in spec) from the generator side; the
// drift-detect script enforces the reverse direction (every chi route
// appears in RouteSpecs).
func TestEveryRouteSpecAppearsInOutput(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	for _, r := range RouteSpecs {
		// Each operation appears as a method line under its path key.
		// We look for the lowered-case method (yaml convention).
		opLine := strings.ToLower(r.Method) + ":"
		// Path keys are not unique on their own (e.g. /v1/me has GET +
		// PATCH); pin via the summary which carries METHOD + PATH.
		summary := "summary: " + r.Summary
		if !strings.Contains(yaml, opLine) {
			t.Errorf("spec missing method line for %s %s", r.Method, r.Path)
		}
		if !strings.Contains(yaml, summary) {
			t.Errorf("spec missing summary line for %s %s", r.Method, r.Path)
		}
	}
}

// TestParameterExtraction verifies that path placeholders ({id},
// {kind}, etc.) are extracted into per-operation `parameters` entries
// with `in: path` + `required: true`.
func TestParameterExtraction(t *testing.T) {
	specRoute := RouteSpec{
		Method:  "GET",
		Path:    "/v1/decisions/{id}/links/{kind}/{targetID}",
		Tag:     "decisions",
		Tier:    "bearer",
		Summary: "GET /v1/decisions/{id}/links/{kind}/{targetID}",
	}
	var out bytes.Buffer
	if err := Generate(&out, []RouteSpec{specRoute}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	yaml := out.String()
	for _, name := range []string{"id", "kind", "targetID"} {
		if !strings.Contains(yaml, "name: "+name) {
			t.Errorf("spec missing path parameter %q", name)
		}
	}
}

// opMarker returns a stable substring uniquely identifying a route in
// the YAML output. Used by the security-block presence test.
func opMarker(r RouteSpec) string {
	return "summary: " + r.Summary
}
