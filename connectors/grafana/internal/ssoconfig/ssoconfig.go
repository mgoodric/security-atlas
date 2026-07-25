// Package ssoconfig pulls Grafana authentication / authorization CONFIGURATION
// evidence via the read-only Grafana API: SSO-settings (GET /api/v1/sso-settings)
// and RBAC role assignments (GET /api/access-control/...). It is the slice-534
// sibling of the slice-488 alertrules collector and emits a SEPARATE evidence
// kind (grafana.access_config.v1) because this is an IAM surface, not a
// monitoring surface — slice 488 deferred exactly this authn/authz surface
// (P0-488-7).
//
// The load-bearing guard (P0-534 / threat-model I — DOMINANT): Grafana's SSO
// settings payload EMBEDS secrets — the SAML private key, the OAuth client
// secret, the LDAP bind password, signing certificates — and the access-control
// payload embeds user identities (names/emails). The collector's record structs
// (Provider, AccessConfig) are STRUCTURALLY INCAPABLE of holding any of them:
// there is no field for a key, a secret, a certificate, a user name, or a user
// email. The collector captures only:
//   - per-provider: the provider TYPE (saml / oauth / oidc / ldap / ...), the
//     ENABLED boolean, and the org-role MAPPING RULE strings (role NAMES only,
//     never an identity),
//   - aggregate COUNTS: team count + total team-membership count (counts, never
//     member identities/PII), and the RBAC role-assignment count rolled up by
//     scope (user / team / builtin) — counts, never the assigned principals.
//
// If the API returns a secret/PII key in the same JSON object, the decode struct
// in client.go simply omits that key and the record struct has no slot — the
// type system is the over-collection guard. A reflection test pins the field set
// and a decode test feeds a fake response CONTAINING a SAML private key + a user
// email and proves neither reaches a record.
package ssoconfig

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Grafana API GETs; tests pass a fake. The implementation
// reads a bounded set of endpoints with a hard per-run cap (threat-model D).
type API interface {
	// ListSSOProviders returns the configured SSO providers (secret-free fields
	// only: type, enabled, org-role mapping rules).
	ListSSOProviders(ctx context.Context) ([]RawProvider, error)
	// TeamStats returns aggregate team counts (team count + total membership
	// count) — counts only, never member identities.
	TeamStats(ctx context.Context) (RawTeamStats, error)
	// RoleAssignmentStats returns the RBAC role-assignment counts rolled up by
	// assignment scope (user / team / builtin) — counts only, never principals.
	RoleAssignmentStats(ctx context.Context) (RawRoleStats, error)
}

// RawProvider is the narrow, secret-free view the Grafana client returns for one
// SSO provider. The HTTP client maps the sso-settings response into this shape,
// discarding the SAML private key / OAuth client secret / signing certificate /
// LDAP bind password at the decode boundary.
//
// There is intentionally NO field here capable of holding a secret, a key, a
// certificate, or a user identity — the type system is the first line of the
// over-collection defence (P0-534).
type RawProvider struct {
	// Type is the provider type: "saml", "oauth", "oidc", "ldap",
	// "github", "google", etc. Descriptive string.
	Type string
	// Enabled is whether this SSO provider is currently enabled.
	Enabled bool
	// RoleMappings are the org-role mapping RULES (e.g. "Editor", "Viewer",
	// "GrafanaAdmin", or an attribute->role expression NAME). Role names /
	// rule strings only — never an identity, never a secret.
	RoleMappings []string
}

// RawTeamStats is the aggregate team rollup — COUNTS only. There is no field for
// a team member's name, email, or user id (P0-534).
type RawTeamStats struct {
	// TeamCount is the number of teams configured.
	TeamCount int
	// TotalMemberships is the sum of team memberships across all teams. A COUNT,
	// never the member identities.
	TotalMemberships int
}

// RawRoleStats is the aggregate RBAC role-assignment rollup — COUNTS only,
// keyed by assignment scope. There is no field for an assigned principal's
// identity (P0-534).
type RawRoleStats struct {
	// UserAssignments is the count of role assignments scoped to individual users.
	UserAssignments int
	// TeamAssignments is the count of role assignments scoped to teams.
	TeamAssignments int
	// BuiltinAssignments is the count of role assignments scoped to built-in roles.
	BuiltinAssignments int
}

// Provider is the normalized per-provider record fragment. Like RawProvider it
// has no field that could carry a secret, a key, a certificate, or an identity.
type Provider struct {
	Type         string
	Enabled      bool
	RoleMappings []string
}

// AccessConfig is the normalized, secret-free access-configuration record the
// cmd layer turns into one grafana.access_config.v1 evidence record. Field set
// is pinned by the structural over-collection guard test.
type AccessConfig struct {
	// SSOEnabled is true if ANY SSO provider is enabled (the CC6.1 "is SSO
	// enforced" headline).
	SSOEnabled bool
	// Providers is the per-provider config (type + enabled + role-mapping rules).
	Providers []Provider
	// TeamCount + TotalTeamMemberships are aggregate counts (never identities).
	TeamCount            int
	TotalTeamMemberships int
	// RBAC role-assignment counts rolled up by scope (never principals).
	UserRoleAssignments    int
	TeamRoleAssignments    int
	BuiltinRoleAssignments int
	// ObservedAt is the UTC-hour-truncated collection time.
	ObservedAt time.Time
}

// Collect reads the SSO + RBAC configuration and returns ONE normalized,
// secret-free AccessConfig. now is injectable for deterministic tests
// (nil -> time.Now UTC); the observed-at is truncated to the UTC hour so
// same-hour re-runs collapse to one ledger row.
func Collect(ctx context.Context, api API, now func() time.Time) (AccessConfig, error) {
	if api == nil {
		return AccessConfig{}, errors.New("ssoconfig: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)

	rawProviders, err := api.ListSSOProviders(ctx)
	if err != nil {
		return AccessConfig{}, fmt.Errorf("list grafana sso providers: %w", err)
	}
	teamStats, err := api.TeamStats(ctx)
	if err != nil {
		return AccessConfig{}, fmt.Errorf("grafana team stats: %w", err)
	}
	roleStats, err := api.RoleAssignmentStats(ctx)
	if err != nil {
		return AccessConfig{}, fmt.Errorf("grafana role-assignment stats: %w", err)
	}

	providers := make([]Provider, 0, len(rawProviders))
	anyEnabled := false
	for _, rp := range rawProviders {
		typ := normalizeType(rp.Type)
		if typ == "" {
			continue
		}
		if rp.Enabled {
			anyEnabled = true
		}
		providers = append(providers, Provider{
			Type:         typ,
			Enabled:      rp.Enabled,
			RoleMappings: sanitizeMappings(rp.RoleMappings),
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Type < providers[j].Type })

	return AccessConfig{
		SSOEnabled:             anyEnabled,
		Providers:              providers,
		TeamCount:              nonNeg(teamStats.TeamCount),
		TotalTeamMemberships:   nonNeg(teamStats.TotalMemberships),
		UserRoleAssignments:    nonNeg(roleStats.UserAssignments),
		TeamRoleAssignments:    nonNeg(roleStats.TeamAssignments),
		BuiltinRoleAssignments: nonNeg(roleStats.BuiltinAssignments),
		ObservedAt:             observedAt,
	}, nil
}

// normalizeType lower-cases + trims the provider type. An empty type is dropped
// by the caller (a provider with no type carries no evidence value).
func normalizeType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// sanitizeMappings trims + de-duplicates + sorts the org-role mapping rule
// strings for a deterministic record. It does NOT inspect the value for secrets:
// the mapping strings are role NAMES / rule expressions by construction (the
// client never populates a secret or an identity here); this is the
// belt-and-braces shaping pass.
func sanitizeMappings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, m := range in {
		v := strings.TrimSpace(m)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// nonNeg clamps a count to >= 0 so a malformed source can never push a negative
// count into a record.
func nonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
