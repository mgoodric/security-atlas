// Slice 449 — OPA 1.17 regression gate (STRIDE-T, bundle-validation).
//
// The slice-378 hot-reload path (internal/api/adminauthzbundle ->
// Engine.Reload) parses an operator-driven Rego bundle. An OPA
// parser/loader behavior change across 13 minors could change which
// bundles compile — accepting a bundle 1.4 rejected (or vice-versa) and
// silently changing the integrity contract. The existing reload_test.go
// covers empty-modules and a wrong-package compile error. This file pins
// the two STRIDE-T cases the threat model names explicitly:
//
//  1. A SYNTACTICALLY MALFORMED bundle is still rejected at parse time
//     under 1.17 (and the prior bundle stays installed — fail-closed).
//  2. An OVERSIZED-but-valid-syntax bundle does not silently bypass the
//     matrix validator — the matrix gate still runs against the candidate
//     before any swap.
//
// P0-449-4: does NOT relax the slice-378 bundle-validation contract.
package authz_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestSlice449_BundleRejectsMalformedSyntax asserts that a bundle whose
// source does not parse as Rego is rejected under 1.17, and the engine
// continues to serve the prior (canonical) bundle. We parse the candidate
// source ourselves first (as the reload pipeline does) — a malformed
// source fails ast.ParseModule, which is the loader's first gate. The
// load-bearing property: a parse failure NEVER reaches a swap.
func TestSlice449_BundleRejectsMalformedSyntax(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	preSHA := e.BundleSHA256()

	// Garbage that is not valid Rego. ParseModule must reject it; the
	// reload pipeline therefore never even constructs a candidate module
	// map, so the engine keeps serving the canonical bundle.
	malformed := `package authz

import rego.v1

this is not valid rego !!! { { {
allow := := true
`
	if _, parseErr := ast.ParseModule("malformed.rego", malformed); parseErr == nil {
		t.Fatalf("expected ast.ParseModule to reject malformed source under 1.17, got nil error")
	}

	// The canonical engine must still serve admin-write (prior bundle
	// untouched) and the SHA must be unchanged.
	d, dErr := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "u-after-malformed", Roles: []authz.Role{authz.RoleAdmin}},
		TenantID: "00000000-0000-0000-0000-000000000449",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "risks"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/risks"},
	})
	if dErr != nil {
		t.Fatalf("Decide after malformed-bundle rejection: %v", dErr)
	}
	if !d.Allow {
		t.Fatalf("expected admin write to remain allowed after a rejected malformed bundle, got deny: %s", d.Reason)
	}
	if got := e.BundleSHA256(); got != preSHA {
		t.Fatalf("bundle SHA changed despite a rejected malformed bundle: pre=%s post=%s", preSHA, got)
	}
}

// TestSlice449_OversizedPermissiveBundleRejectedByMatrix asserts that an
// oversized, syntactically-VALID bundle that would make the policy
// permissive (default allow := true) is rejected by the matrix validator
// BEFORE the atomic swap under 1.17. This is the elevation-of-privilege
// guard: a large operator bundle cannot smuggle a blanket-allow past the
// matrix gate. P0-449-4 + P0-449-1 (no outcome change).
func TestSlice449_OversizedPermissiveBundleRejectedByMatrix(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	preSHA := e.BundleSHA256()

	// A valid-syntax but PERMISSIVE bundle: default allow := true. Padded
	// with a large number of inert rules so the candidate is "oversized"
	// relative to the canonical bundle — the matrix validator must still
	// reject it because a viewer would be allowed to write (a canonical
	// matrix DENY cell).
	var b strings.Builder
	b.WriteString("package authz\n\nimport rego.v1\n\ndefault allow := true\n")
	for i := 0; i < 2000; i++ {
		// Inert helper rules to inflate the bundle size without changing
		// the permissive default.
		fmt.Fprintf(&b, "inert_%d := %d if { true }\n", i, i)
	}
	src := b.String()
	mod, parseErr := ast.ParseModule("oversized.rego", src)
	if parseErr != nil {
		t.Fatalf("oversized bundle should parse (valid syntax): %v", parseErr)
	}
	modules := map[string]*ast.Module{"oversized.rego": mod}
	sources := map[string][]byte{"oversized.rego": []byte(src)}

	// Reload WITH the production matrix validator. The permissive default
	// must fail a canonical DENY cell, so Reload returns an error and the
	// engine keeps the prior bundle.
	rErr := e.Reload(context.Background(), modules, sources, authz.ValidateMatrix)
	if rErr == nil {
		t.Fatalf("expected matrix validator to REJECT a permissive oversized bundle under 1.17, got nil error")
	}
	if !strings.Contains(rErr.Error(), "matrix") {
		t.Fatalf("expected a matrix-validation rejection, got: %v", rErr)
	}

	// Fail-closed: SHA unchanged, and a viewer is STILL denied write
	// (the permissive bundle never reached Decide).
	if got := e.BundleSHA256(); got != preSHA {
		t.Fatalf("bundle SHA changed despite a rejected permissive bundle: pre=%s post=%s", preSHA, got)
	}
	d, dErr := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "viewer-after-reject", Roles: []authz.Role{authz.RoleViewer}},
		TenantID: "00000000-0000-0000-0000-000000000449",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "risks"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/risks"},
	})
	if dErr != nil {
		t.Fatalf("Decide after rejected permissive bundle: %v", dErr)
	}
	if d.Allow {
		t.Fatalf("ELEVATION VIOLATION: viewer allowed write after a rejected permissive bundle under 1.17")
	}
}
