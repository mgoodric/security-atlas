// Package featureflag implements slice 059 -- per-tenant feature flags +
// capability toggles.
//
// The package exposes:
//
//   - Seed (the 12 canonical capability flags shipped with v1 defaults).
//   - SpineForbiddenPrefixes (the namespaces that MUST NOT be toggleable).
//   - Store (DB-backed CRUD + audit-log writes; tenant-scoped under RLS).
//   - Enabled(ctx, key) (package-level helper with in-request memoization).
//   - Gate(key) (chi middleware that 404s a route when the flag is off).
//
// Constitutional invariants honored:
//
//   - Invariant 6 (RLS): every DB read / write happens inside a tenant tx
//     with `app.current_tenant` set. The four-policy RLS pattern on
//     feature_flags + the append-only two-policy pattern on
//     feature_flag_audit_log are the enforcer.
//   - Spine non-toggleability: the Seed list excludes every spine-forbidden
//     prefix; a unit test enforces this at compile-pass time. The Store's
//     Set path also rejects any flag_key that matches a spine prefix before
//     writing, so even an admin caller cannot create a spine flag through
//     the API.
//   - Fail-open on DB unreach: Enabled() returns the seed default + logs a
//     warning when the DB call errors. RLS is the security boundary, not
//     the feature flag.
//   - No cross-request cache: memoization is keyed off context.Context so
//     it dies when the request ends. There is no package-level cache.
package featureflag

import "strings"

// Default is the v1-shipped state for a single flag. The Seed list below
// is the authoritative source. Operator toggles persist into
// feature_flags(tenant_id, flag_key); a row's `enabled` overrides the
// seed default for that tenant.
type Default struct {
	Key         string
	Enabled     bool
	Description string
	Category    string
}

// Seed is the canonical v1 capability-flag inventory. Each entry maps to
// the issue's "Seed flag inventory" table; categories match the
// feature_flags.category CHECK constraint enum. Adding a new flag here is
// a no-op for existing tenants until they explicitly toggle it -- the
// Store returns the default until a row exists.
//
// IMPORTANT: every key here MUST NOT match a SpineForbiddenPrefix. A unit
// test asserts this. Spine namespaces (tenancy, rls, auth, schema.registry,
// scope, evidence.ledger, framework.crosswalk) are non-toggleable by
// construction -- an admin cannot disable RLS via a flag because no flag
// in this Seed gates RLS, and the Set path refuses to create one.
var Seed = []Default{
	{
		Key:         "risk.enabled",
		Enabled:     true,
		Description: "Risk register module (canvas §6). Disabling hides /v1/risks/*.",
		Category:    "risk",
	},
	{
		Key:         "risk.themes",
		Enabled:     true,
		Description: "Risk theme catalog and per-risk theme tagging.",
		Category:    "risk",
	},
	{
		Key:         "risk.hierarchy",
		Enabled:     true,
		Description: "Organisational unit hierarchy under risks (slice 014).",
		Category:    "risk",
	},
	{
		Key:         "vendor.enabled",
		Enabled:     true,
		Description: "Vendor lite module (canvas §10.1). Disabling hides /v1/vendors/*.",
		Category:    "vendor",
	},
	{
		Key:         "policy.enabled",
		Enabled:     true,
		Description: "Policy library module (canvas §2.6). Disabling hides /v1/policies/*.",
		Category:    "policy",
	},
	{
		Key:         "policy.acknowledgments",
		Enabled:     true,
		Description: "Policy acknowledgment workflow (slice 023).",
		Category:    "policy",
	},
	{
		Key:         "controls.bundles",
		Enabled:     true,
		Description: "Control-as-code bundle upload + execution (slice 009).",
		Category:    "controls",
	},
	{
		Key:         "exceptions.enabled",
		Enabled:     true,
		Description: "Exception / waiver workflow (slice 021).",
		Category:    "controls",
	},
	{
		Key:         "audit.workflow",
		Enabled:     true,
		Description: "Sample-pull primitives for audit workflow (slice 026).",
		Category:    "audit",
	},
	{
		Key:         "oscal.export",
		Enabled:     false,
		Description: "OSCAL SSP / AP / AR / POA&M export (future slice 030). Default off pending GA.",
		Category:    "integrations",
	},
	{
		Key:         "board.reporting",
		Enabled:     false,
		Description: "Board reporting module (future slices 031/032). Default off pending GA.",
		Category:    "board",
	},
	{
		Key:         "decisions.log",
		Enabled:     true,
		Description: "Risk decision log (canvas §6.7, slice 055).",
		Category:    "risk",
	},
}

// SpineForbiddenPrefixes enumerates the flag-key namespaces that MUST
// remain non-toggleable per the constitutional invariants in CLAUDE.md.
// The Seed list above is checked against these at unit-test time. The
// Store's Set path also rejects matching keys at write time so a caller
// who somehow circumvents the API surface still cannot create a spine
// flag.
//
// IsSpineForbidden matches a key against each entry two ways: exact
// equality (`key == "rls"`) and dotted-namespace prefix
// (`strings.HasPrefix(key, "rls.")`). So the bare entry `"rls"` catches
// both `"rls"` and `"rls.policies"` -- there is no need for separate
// trailing-dot entries.
var SpineForbiddenPrefixes = []string{
	// RLS itself -- the security boundary. Disabling this would
	// break every tenant-scoped query.
	"rls",
	// Tenancy plumbing -- the request-context tenant id.
	"tenancy",
	// Authentication -- bearer auth, OIDC, local users.
	"auth",
	// Schema registry -- evidence_kind validation.
	"schema.registry",
	// Scope dimensions + applicability expressions.
	"scope.dimensions",
	"scope.cells",
	"scope.applicability",
	// Evidence ledger -- the append-only record of truth.
	"evidence.ledger",
	"evidence.ingest",
	// Framework crosswalk -- the UCF graph through SCF anchors.
	"framework.crosswalk",
	"framework.requirements",
	"framework.scope",
	// Core control machinery -- canvas §3.5 "SCF is the canonical
	// control catalog". `controls.bundles` is a capability flag (and
	// in the Seed list); `controls.core` / `controls.spine` are not.
	"controls.core",
	"controls.spine",
}

// IsSpineForbidden reports whether the given flag_key falls under any
// reserved spine prefix. Used by the Seed unit test (no entry may match)
// and by Store.Set (rejects matching keys with ErrSpineForbidden).
//
// Matches a key when it equals a prefix exactly OR begins with a prefix
// followed by ".". So `"rls"` matches `"rls"` and `"rls.policies"` but
// NOT `"rlsfoo"` -- the dot boundary prevents false-positive matches
// against capability names that happen to share a substring.
func IsSpineForbidden(key string) bool {
	for _, prefix := range SpineForbiddenPrefixes {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			return true
		}
	}
	return false
}

// DefaultByKey looks up the Seed default for a flag_key. Returns the
// Default and true when present; zero value and false otherwise.
func DefaultByKey(key string) (Default, bool) {
	for _, d := range Seed {
		if d.Key == key {
			return d, true
		}
	}
	return Default{}, false
}
