// Slice 140 AC-8 — spec validation. Asserts the generated
// docs/openapi.yaml passes the shape validator with zero violations.
// Backs the BLOCKING discipline: a future generator change that
// produced a syntactically valid but structurally broken spec would
// fail this test even before the drift-detect CI guard runs.

package openapi

import (
	"bytes"
	"strings"
	"testing"
)

// TestSpecValidatesAgainstShape — the canonical generator output
// passes the shape validator with zero violations. AC-8.
func TestSpecValidatesAgainstShape(t *testing.T) {
	var out bytes.Buffer
	if err := Generate(&out, RouteSpecs); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	violations, err := Validate(out.Bytes())
	if err != nil {
		t.Fatalf("Validate hard error: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("spec has %d shape violations:\n  - %s",
			len(violations), strings.Join(violations, "\n  - "))
	}
}

// TestValidatorCatchesUndeclaredSecurityScheme — the validator
// rejects a spec that references a security scheme not declared
// under components.securitySchemes. Pin the contract: AC-5's
// "every operation carries a security field" implies the referenced
// schemes must exist.
func TestValidatorCatchesUndeclaredSecurityScheme(t *testing.T) {
	bad := []byte(`openapi: 3.1.0
info:
  title: x
  version: v1
paths:
  /v1/foo:
    get:
      summary: GET /v1/foo
      tags: [foo]
      operationId: get-v1-foo
      security:
        - missingScheme: []
      responses:
        default:
          description: ok
components:
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
`)
	violations, err := Validate(bad)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v, "missingScheme") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("validator did not catch undeclared scheme; violations: %v", violations)
	}
}

// TestValidatorCatchesMissingResponses — the validator rejects any
// operation that is missing the `responses` block.
func TestValidatorCatchesMissingResponses(t *testing.T) {
	bad := []byte(`openapi: 3.1.0
info:
  title: x
  version: v1
paths:
  /v1/foo:
    get:
      summary: GET /v1/foo
      tags: [foo]
      operationId: get-v1-foo
components: {}
`)
	violations, err := Validate(bad)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v, "responses") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("validator did not catch missing responses; violations: %v", violations)
	}
}

// TestValidatorHardErrorOnInvalidYAML — malformed YAML returns the
// hard error (not a violation list).
func TestValidatorHardErrorOnInvalidYAML(t *testing.T) {
	bad := []byte("not yaml: : :::: -")
	_, err := Validate(bad)
	if err == nil {
		t.Fatalf("Validate should have returned a hard error on malformed YAML")
	}
}
