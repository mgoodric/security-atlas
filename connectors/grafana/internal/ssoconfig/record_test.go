package ssoconfig

import (
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

func sampleConfig() AccessConfig {
	return AccessConfig{
		SSOEnabled: true,
		Providers: []Provider{
			{Type: "saml", Enabled: true, RoleMappings: []string{"Admin", "Editor"}},
			{Type: "oauth", Enabled: false},
		},
		TeamCount:              3,
		TotalTeamMemberships:   17,
		UserRoleAssignments:    12,
		TeamRoleAssignments:    4,
		BuiltinRoleAssignments: 2,
		ObservedAt:             time.Date(2026, 6, 8, 12, 30, 0, 0, time.UTC),
	}
}

func scopeValue(dims []*evidencev1.ScopeDimension, key string) string {
	for _, d := range dims {
		if d.GetKey() == key && len(d.GetValues()) > 0 {
			return d.GetValues()[0]
		}
	}
	return ""
}

func TestBuild_Shape(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleConfig(), "scf:IAC-06", "connector:grafana:ssoconfig@dev", "grafana", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.EvidenceKind != EvidenceKind || EvidenceKind != "grafana.access_config.v1" {
		t.Errorf("kind = %q", rec.EvidenceKind)
	}
	if rec.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %q", rec.SchemaVersion)
	}
	if rec.Result != evidencev1.Result_RESULT_INCONCLUSIVE {
		t.Errorf("result = %v; want INCONCLUSIVE", rec.Result)
	}
	if rec.IdempotencyKey == "" {
		t.Error("empty idempotency key")
	}
	if rec.GetSourceAttribution().GetActorId() != "connector:grafana:ssoconfig@dev" {
		t.Errorf("actor_id = %q", rec.GetSourceAttribution().GetActorId())
	}
	if scopeValue(rec.GetScope(), "service") != "grafana" || scopeValue(rec.GetScope(), "environment") != "prod" {
		t.Errorf("scope wrong: %v", rec.GetScope())
	}
	pl := rec.GetPayload().AsMap()
	for _, k := range []string{
		"sso_enabled", "team_count", "total_team_memberships",
		"user_role_assignments", "team_role_assignments", "builtin_role_assignments",
		"sso_providers",
	} {
		if _, ok := pl[k]; !ok {
			t.Errorf("payload missing %q; got %v", k, pl)
		}
	}
	if pl["sso_enabled"] != true {
		t.Errorf("sso_enabled = %v; want true", pl["sso_enabled"])
	}
}

func TestBuild_OmitsEmptyProviders(t *testing.T) {
	t.Parallel()
	cfg := sampleConfig()
	cfg.Providers = nil
	rec, _ := Build(cfg, "scf:IAC-06", "a", "grafana", "prod")
	if _, ok := rec.GetPayload().AsMap()["sso_providers"]; ok {
		t.Error("empty sso_providers should be omitted")
	}
}

func TestBuild_OmitsEmptyRoleMappings(t *testing.T) {
	t.Parallel()
	cfg := sampleConfig()
	cfg.Providers = []Provider{{Type: "saml", Enabled: true}} // no mappings
	rec, _ := Build(cfg, "scf:IAC-06", "a", "grafana", "prod")
	providers, _ := rec.GetPayload().AsMap()["sso_providers"].([]any)
	if len(providers) != 1 {
		t.Fatalf("providers = %d; want 1", len(providers))
	}
	m, _ := providers[0].(map[string]any)
	if _, ok := m["role_mappings"]; ok {
		t.Error("empty role_mappings should be omitted")
	}
}

// TestBuild_PayloadIsConfigOnly pins P0-534 at the builder boundary: only the
// allow-listed config / count keys may appear, and no key may contain a
// secret/identity-flavoured substring.
func TestBuild_PayloadIsConfigOnly(t *testing.T) {
	t.Parallel()
	allowedTop := map[string]bool{
		"sso_enabled": true, "team_count": true, "total_team_memberships": true,
		"user_role_assignments": true, "team_role_assignments": true,
		"builtin_role_assignments": true, "sso_providers": true,
	}
	allowedProvider := map[string]bool{
		"provider_type": true, "enabled": true, "role_mappings": true,
	}
	banned := []string{
		"private", "secret", "password", "passwd", "certificate", "cert",
		"key", "email", "login", "username", "credential", "principal", "bind",
	}
	rec, _ := Build(sampleConfig(), "scf:IAC-06", "a", "grafana", "prod")
	pl := rec.GetPayload().AsMap()
	for k := range pl {
		if !allowedTop[k] {
			t.Errorf("non-allow-listed top-level payload key %q", k)
		}
		assertNoBanned(t, k, banned)
	}
	providers, _ := pl["sso_providers"].([]any)
	for _, pi := range providers {
		m, _ := pi.(map[string]any)
		for k := range m {
			if !allowedProvider[k] {
				t.Errorf("non-allow-listed provider key %q", k)
			}
			assertNoBanned(t, k, banned)
		}
	}
}

func assertNoBanned(t *testing.T, key string, banned []string) {
	t.Helper()
	low := strings.ToLower(key)
	for _, b := range banned {
		if strings.Contains(low, b) {
			t.Errorf("payload key %q contains banned substring %q", key, b)
		}
	}
}

func TestBuild_DedupKeyStableWithinHour(t *testing.T) {
	t.Parallel()
	c1 := sampleConfig()
	c2 := sampleConfig()
	c2.ObservedAt = c2.ObservedAt.Add(20 * time.Minute) // same hour
	rec1, _ := Build(c1, "c", "a", "grafana", "prod")
	rec2, _ := Build(c2, "c", "a", "grafana", "prod")
	if rec1.IdempotencyKey != rec2.IdempotencyKey {
		t.Error("same environment in same hour should share idempotency key")
	}
}

func TestBuild_DedupKeyDiffersAcrossEnvironments(t *testing.T) {
	t.Parallel()
	rec1, _ := Build(sampleConfig(), "c", "a", "grafana", "prod")
	rec2, _ := Build(sampleConfig(), "c", "a", "grafana", "staging")
	if rec1.IdempotencyKey == rec2.IdempotencyKey {
		t.Error("different environments should not share idempotency key")
	}
}
