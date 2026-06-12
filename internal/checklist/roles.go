// Package checklist is the slice-471 role-scoped control-implementation
// checklist generator v0 — cited, non-binding. For an in-scope control set it
// (1) DETERMINISTICALLY assigns each control to exactly one fixed v0 role from
// its owner_role (+ applicability_expr dimensions as a fallback), then (2) for
// each AI-assignable role calls the shared local-inference substrate
// (internal/llm, local Ollama) to turn the controls' text into 1..N cited,
// role-appropriate task statements. Every item is cited to a real tenant-owned
// control / SCF-anchor / policy id, validated BEFORE the operator sees the
// draft. The checklist is a DRAFT the operator approves one section (role) at a
// time; nothing is exported / marked authoritative without one-click approval.
//
// This surface is governed by the CLAUDE.md AI-assist boundary (hard):
//
//   - The which-control -> which-role SPLIT is DETERMINISTIC (this file), never
//     an LLM guess (P0-471-deterministic). The LLM only writes task TEXT for a
//     control already assigned to a role. This keeps the assignment auditable
//     and the LLM surface minimal.
//
//   - No fabricated coverage (AC-6, P0-471-4). A control with no evidence
//     backing is rendered as an explicit "no evidence yet" gap item, never as
//     satisfied. Every emitted item carries a citation that resolves to a real
//     tenant-owned row; a single unresolvable citation suppresses that role
//     section (citations.go).
//
//   - No cross-tenant bleed (AC-8, P0-471-3). Control reads, citation
//     resolution, and persistence all run under the requesting tenant's RLS
//     context (app.current_tenant). A tenant-B generation can never read or
//     cite a tenant-A row (invariant #6).
//
//   - One-click human approval (AC-10, P0-471-1). A section persists
//     ai_assisted=TRUE, human_approved=FALSE. Approval is a SEPARATE operator
//     action recording the approver; the shared ai_assist_human_approver_guard
//     CHECK makes human_approved=TRUE without an approver impossible (P0-471-6).
//
//   - Local Ollama only (P0-471-5). The generation rides internal/llm, whose v0
//     backend is local Ollama; no data leaves the deployment. No configurable
//     role taxonomy, no assignable-task (Jira/Linear) integration in v0.
package checklist

import "strings"

// Role is one of the FIXED v0 taxonomy roles. The taxonomy is deliberately not
// configurable in v0 (P0-471-5) — it is a tracer-bullet fixed set. RoleUnassigned
// is the explicit "matched none" bucket: a control that normalizes to no role is
// surfaced to the operator under it, never silently dropped (AC-1).
type Role string

const (
	RoleInfra       Role = "infra"
	RoleEngineering Role = "engineering"
	RoleSecurity    Role = "security"
	RoleUnassigned  Role = "unassigned"
)

// AIRoles is the ordered set of roles whose sections are AI-authored (the LLM
// writes task text for them). RoleUnassigned is excluded — it carries no AI
// tasks, only the honest "these controls matched no role" list. Stable order so
// the review view + the persisted sections render deterministically.
var AIRoles = []Role{RoleInfra, RoleEngineering, RoleSecurity}

// ValidRole reports whether r is a known v0 role (incl. unassigned). Mirrors the
// checklist_sections_role_chk DB CHECK so a typo surfaces as a Go error rather
// than a 23514 check_violation.
func ValidRole(r Role) bool {
	switch r {
	case RoleInfra, RoleEngineering, RoleSecurity, RoleUnassigned:
		return true
	default:
		return false
	}
}

// ownerRoleAliases maps a NORMALIZED owner_role token (lowercased, trimmed,
// punctuation/underscores/dashes collapsed to spaces) to a v0 role. This is the
// JUDGMENT surface of the slice (decisions log): the map is seeded from the
// roles present in the demo dataset (slice 205) + common GRC role names. It is
// deliberately exhaustive-by-enumeration rather than fuzzy: a deterministic,
// auditable assignment beats a clever-but-opaque one. Unknown tokens fall to
// the substring heuristic, then to RoleUnassigned.
//
// owner_role is free-text (slice 448 confirmed controls.owner_role is a
// read-only TEXT role string), so the map must tolerate many spellings of the
// same intent: "infrastructure", "infra", "platform", "devops", "sre", "cloud
// ops" all mean the infra team in this taxonomy.
var ownerRoleAliases = map[string]Role{
	// --- infra / platform / operations ---
	"infra":                        RoleInfra,
	"infrastructure":               RoleInfra,
	"infra team":                   RoleInfra,
	"infrastructure team":          RoleInfra,
	"platform":                     RoleInfra,
	"platform team":                RoleInfra,
	"platform engineering":         RoleInfra,
	"devops":                       RoleInfra,
	"dev ops":                      RoleInfra,
	"sre":                          RoleInfra,
	"site reliability":             RoleInfra,
	"site reliability engineering": RoleInfra,
	"operations":                   RoleInfra,
	"ops":                          RoleInfra,
	"cloud ops":                    RoleInfra,
	"cloud operations":             RoleInfra,
	"it ops":                       RoleInfra,
	"it operations":                RoleInfra,
	"networking":                   RoleInfra,
	"network":                      RoleInfra,
	"systems":                      RoleInfra,
	"sysadmin":                     RoleInfra,

	// --- engineering / development / application ---
	"engineering":          RoleEngineering,
	"engineer":             RoleEngineering,
	"engineering team":     RoleEngineering,
	"software engineering": RoleEngineering,
	"development":          RoleEngineering,
	"developer":            RoleEngineering,
	"dev":                  RoleEngineering,
	"dev team":             RoleEngineering,
	"application":          RoleEngineering,
	"app":                  RoleEngineering,
	"application team":     RoleEngineering,
	"product engineering":  RoleEngineering,
	"backend":              RoleEngineering,
	"frontend":             RoleEngineering,
	"full stack":           RoleEngineering,

	// --- security / GRC / compliance ---
	"security":                       RoleSecurity,
	"security team":                  RoleSecurity,
	"infosec":                        RoleSecurity,
	"information security":           RoleSecurity,
	"secops":                         RoleSecurity,
	"security operations":            RoleSecurity,
	"grc":                            RoleSecurity,
	"grc engineer":                   RoleSecurity,
	"governance risk and compliance": RoleSecurity,
	"compliance":                     RoleSecurity,
	"compliance team":                RoleSecurity,
	"ciso":                           RoleSecurity,
	"security engineering":           RoleSecurity,
	"appsec":                         RoleSecurity,
	"application security":           RoleSecurity,
	"risk":                           RoleSecurity,
	"audit":                          RoleSecurity,
}

// substringHints is the SECOND-pass heuristic for an owner_role that did not hit
// the alias table exactly. Each hint is a substring tested against the
// normalized owner_role; the FIRST match (in this fixed order) wins. Order is
// load-bearing: more-specific security/appsec terms are tested before the
// broader "ops"/"eng" terms so e.g. "security operations" maps to security, not
// infra. Deterministic + unit-tested.
var substringHints = []struct {
	needle string
	role   Role
}{
	// Security-flavored first (most specific intent).
	{"infosec", RoleSecurity},
	{"secops", RoleSecurity},
	{"appsec", RoleSecurity},
	{"security", RoleSecurity},
	{"compliance", RoleSecurity},
	{"grc", RoleSecurity},
	{"audit", RoleSecurity},
	{"risk", RoleSecurity},
	// Infra / platform / ops.
	{"infra", RoleInfra},
	{"platform", RoleInfra},
	{"devops", RoleInfra},
	{"sre", RoleInfra},
	{"network", RoleInfra},
	{"ops", RoleInfra},
	{"cloud", RoleInfra},
	{"system", RoleInfra},
	// Engineering / development / application (broadest; tested last).
	{"engineer", RoleEngineering},
	{"develop", RoleEngineering},
	{"application", RoleEngineering},
	{"backend", RoleEngineering},
	{"frontend", RoleEngineering},
	{"dev", RoleEngineering},
	{"eng", RoleEngineering},
}

// normalizeOwnerRole lowercases, trims, and collapses underscores/dashes/extra
// whitespace to single spaces so the alias table + substring hints see a
// canonical token. Pure function. "Infra_Team", "infra-team", "  INFRA  TEAM "
// all normalize to "infra team".
func normalizeOwnerRole(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Replace separators with spaces, then collapse runs of whitespace.
	s = strings.NewReplacer("_", " ", "-", " ", "/", " ", ".", " ", ",", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// AssignRole is the DETERMINISTIC control->role split (AC-1). It resolves a
// control's free-text owner_role to exactly one v0 role:
//
//  1. Exact alias hit on the normalized owner_role (the auditable common case).
//  2. Substring-hint heuristic in fixed precedence order.
//  3. applicabilityFallback over the control's applicability_expr dimensions,
//     for a control whose owner_role is blank or non-indicative.
//  4. RoleUnassigned — surfaced honestly to the operator, never dropped.
//
// This function is PURE (no IO, no LLM). It is the single source of truth for
// the assignment and is exhaustively unit-tested. The LLM is invoked ONLY after
// this returns, and only for an AI-assignable role's controls.
func AssignRole(ownerRole, applicabilityExpr string) Role {
	norm := normalizeOwnerRole(ownerRole)

	// 1. Exact alias.
	if norm != "" {
		if r, ok := ownerRoleAliases[norm]; ok {
			return r
		}
		// 2. Substring hints.
		for _, h := range substringHints {
			if strings.Contains(norm, h.needle) {
				return h.role
			}
		}
	}

	// 3. applicability_expr fallback (owner_role absent/non-indicative).
	if r := applicabilityFallback(applicabilityExpr); r != RoleUnassigned {
		return r
	}

	// 4. Explicit unassigned bucket.
	return RoleUnassigned
}

// applicabilityFallback derives a role hint from the control's
// applicability_expr dimensions when owner_role is unhelpful. The expr is a
// scope predicate over (BU x env x geo x cloud x data_class x product) — canvas
// §5. v0 reads only coarse, high-signal hints: an expr scoped to cloud/prod
// infrastructure leans infra; one mentioning a data-classification leans
// security; an app/product scope leans engineering. This is intentionally
// conservative — it only fires when a clear signal is present, otherwise it
// returns RoleUnassigned so the control surfaces under the honest bucket rather
// than being mis-assigned. Pure + unit-tested.
func applicabilityFallback(expr string) Role {
	e := strings.ToLower(expr)
	if e == "" || e == "true" {
		// The default "applies everywhere" predicate carries no role signal.
		return RoleUnassigned
	}
	// Security-flavored dimensions (data classification / regulated scope).
	for _, s := range []string{"data_class", "pci", "phi", "pii", "cde", "confidential", "regulated"} {
		if strings.Contains(e, s) {
			return RoleSecurity
		}
	}
	// Engineering-flavored dimensions (product / application / service). Tested
	// BEFORE infra so "product" is not swallowed by infra's "prod" substring.
	for _, s := range []string{"product", "application", "app", "service", "repo"} {
		if strings.Contains(e, s) {
			return RoleEngineering
		}
	}
	// Infra-flavored dimensions (cloud / environment / network).
	for _, s := range []string{"cloud", "environment", "env", "network", "region", "geo", "prod"} {
		if strings.Contains(e, s) {
			return RoleInfra
		}
	}
	return RoleUnassigned
}
