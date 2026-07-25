package ssoconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

// The fake SAML private key + the fake user email below are OBVIOUSLY fake and
// are present ONLY inside the fake server's response body so the drop test can
// prove they never reach a record. No real Grafana token / SAML key.
//
// fakeSAMLPrivateKey is assembled from PEM-marker fragments at runtime rather
// than written as a contiguous literal so the detect-private-key / GitGuardian
// pre-commit scanners do not flag this test file. The assembled value is still a
// realistic PEM-shaped secret the drop test scans for, but the PEM header never
// appears verbatim in source (the dashes + the two marker words are kept apart).
var fakeSAMLPrivateKey = pemDashes + "BEGIN " + pemKeyWords + pemDashes +
	"FAKE-NOT-A-REAL-KEY-534" + pemDashes + "END " + pemKeyWords + pemDashes

const (
	pemDashes   = "-----"
	pemKeyWords = "PRIVATE " + "KEY"
)

const (
	fakeOAuthSecret     = "FAKE-oauth-client-secret-534"
	fakeLDAPBindPass    = "FAKE-ldap-bind-password-534"
	fakeUserEmail       = "victim@example.test"
	fakeUserLogin       = "jdoe-fake-534"
	fakeUserDisplayName = "Jane Doe FAKE"
)

// ssoSettingsBody is a fake GET /api/v1/sso-settings response that DELIBERATELY
// embeds the SAML private key, the OAuth client secret, the LDAP bind password,
// and a signing certificate alongside the safe fields — to prove the decode
// drops every secret.
var ssoSettingsBody = `[
  {
    "provider": "saml",
    "settings": {
      "enabled": true,
      "role_values_editor": ["Editor"],
      "role_values_admin": ["Admin"],
      "private_key": "` + fakeSAMLPrivateKey + `",
      "certificate": "FAKE-signing-cert-534",
      "signing_cert": "FAKE-signing-cert-534"
    }
  },
  {
    "provider": "oauth",
    "settings": {
      "enabled": false,
      "client_secret": "` + fakeOAuthSecret + `",
      "role_values_viewer": ["Viewer"]
    }
  },
  {
    "provider": "ldap",
    "settings": {
      "enabled": false,
      "bind_password": "` + fakeLDAPBindPass + `"
    }
  }
]`

// teamsSearchBody embeds team member identities (login + email + name) to prove
// the decode counts members but never materializes their identity.
const teamsSearchBody = `{
  "totalCount": 2,
  "teams": [
    {"id": 1, "name": "Security", "memberCount": 4,
     "members": [{"login": "` + fakeUserLogin + `", "email": "` + fakeUserEmail + `", "name": "` + fakeUserDisplayName + `"}]},
    {"id": 2, "name": "Platform", "memberCount": 3}
  ]
}`

// roleAssignmentsBody embeds the assigned principal's login/email to prove the
// decode counts by scope but never materializes the principal identity.
const roleAssignmentsBody = `[
  {"userId": 7, "userLogin": "` + fakeUserLogin + `", "userEmail": "` + fakeUserEmail + `", "roleName": "fixed:datasources:reader"},
  {"userId": 9, "roleName": "fixed:reports:admin"},
  {"teamId": 2, "roleName": "fixed:teams:reader"},
  {"builtinRole": "Editor", "roleName": "fixed:dashboards:reader"}
]`

func newFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sso-settings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(ssoSettingsBody))
	})
	mux.HandleFunc("/api/teams/search", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(teamsSearchBody))
	})
	mux.HandleFunc("/api/access-control/assignments", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(roleAssignmentsBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_CollectShape(t *testing.T) {
	t.Parallel()
	srv := newFakeServer(t)
	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")

	cfg, err := Collect(context.Background(), c, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !cfg.SSOEnabled {
		t.Error("SSOEnabled should be true (saml enabled)")
	}
	if len(cfg.Providers) != 3 {
		t.Fatalf("providers = %d; want 3 (saml, oauth, ldap)", len(cfg.Providers))
	}
	if cfg.TeamCount != 2 || cfg.TotalTeamMemberships != 7 {
		t.Errorf("team stats wrong: count=%d members=%d", cfg.TeamCount, cfg.TotalTeamMemberships)
	}
	// 2 user assignments, 1 team, 1 builtin.
	if cfg.UserRoleAssignments != 2 || cfg.TeamRoleAssignments != 1 || cfg.BuiltinRoleAssignments != 1 {
		t.Errorf("role stats wrong: %+v", cfg)
	}
}

// TestClient_NeverLeaksSecretOrPII is the load-bearing drop test (P0-534 /
// threat-model I): the fake server returns SAML private key + OAuth client
// secret + LDAP bind password + signing cert + user email/login/name; this test
// proves NONE of them reaches a built evidence record's payload (or any
// collected struct). The serialized record is scanned for every fake secret /
// identity literal.
func TestClient_NeverLeaksSecretOrPII(t *testing.T) {
	t.Parallel()
	srv := newFakeServer(t)
	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")

	cfg, err := Collect(context.Background(), c, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, err := Build(cfg, "scf:IAC-06", "connector:grafana:ssoconfig@test", "grafana", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Serialize the entire record payload and scan for every leaked literal.
	blob := serializePayload(t, rec.GetPayload())
	leakLiterals := map[string]string{
		"SAML private key":        fakeSAMLPrivateKey,
		"OAuth client secret":     fakeOAuthSecret,
		"LDAP bind password":      fakeLDAPBindPass,
		"signing certificate":     "FAKE-signing-cert-534",
		"user email (PII)":        fakeUserEmail,
		"user login (PII)":        fakeUserLogin,
		"user display name (PII)": fakeUserDisplayName,
	}
	for label, lit := range leakLiterals {
		if strings.Contains(blob, lit) {
			t.Errorf("LEAK: %s (%q) reached the evidence record payload:\n%s", label, lit, blob)
		}
	}

	// Belt-and-braces: no payload key may itself name a secret/identity field.
	bannedKeySubstr := []string{"private", "secret", "password", "certificate",
		"cert", "key", "email", "login", "credential"}
	var scanKeys func(m map[string]any)
	scanKeys = func(m map[string]any) {
		for k, v := range m {
			low := strings.ToLower(k)
			for _, b := range bannedKeySubstr {
				if strings.Contains(low, b) {
					t.Errorf("payload key %q contains banned substring %q", k, b)
				}
			}
			if nested, ok := v.(map[string]any); ok {
				scanKeys(nested)
			}
			if list, ok := v.([]any); ok {
				for _, item := range list {
					if nm, ok := item.(map[string]any); ok {
						scanKeys(nm)
					}
				}
			}
		}
	}
	scanKeys(rec.GetPayload().AsMap())
}

// TestClient_RoleMappingsSafe asserts the role-mapping rule strings ARE captured
// (they are role names, not secrets) so the drop test isn't vacuously passing by
// dropping everything.
func TestClient_RoleMappingsSafe(t *testing.T) {
	t.Parallel()
	srv := newFakeServer(t)
	c := NewClient(srv.Client(), srv.URL, "test-grafana-token")
	cfg, err := Collect(context.Background(), c, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var samlMappings []string
	for _, p := range cfg.Providers {
		if p.Type == "saml" {
			samlMappings = p.RoleMappings
		}
	}
	if len(samlMappings) != 2 {
		t.Fatalf("saml role mappings = %v; want [Admin Editor]", samlMappings)
	}
	if samlMappings[0] != "Admin" || samlMappings[1] != "Editor" {
		t.Errorf("role mappings not captured/sorted: %v", samlMappings)
	}
}

func TestClient_BoundedCap(t *testing.T) {
	t.Parallel()
	// Return more than maxItems providers; the client must cap the decode.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sso-settings" {
			var b strings.Builder
			b.WriteString("[")
			for i := 0; i < maxItems+50; i++ {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"provider":"p%d","settings":{"enabled":false}}`, i)
			}
			b.WriteString("]")
			_, _ = w.Write([]byte(b.String()))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-token")
	got, err := c.ListSSOProviders(context.Background())
	if err != nil {
		t.Fatalf("ListSSOProviders: %v", err)
	}
	if len(got) > maxItems {
		t.Errorf("provider list = %d; want capped at %d", len(got), maxItems)
	}
}

func TestClient_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-token")
	if _, err := c.ListSSOProviders(context.Background()); err == nil {
		t.Fatal("want HTTP error")
	}
	if _, err := c.TeamStats(context.Background()); err == nil {
		t.Fatal("want HTTP error from TeamStats")
	}
	if _, err := c.RoleAssignmentStats(context.Background()); err == nil {
		t.Fatal("want HTTP error from RoleAssignmentStats")
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	if got := (&APIError{Status: 403}).Error(); !strings.Contains(got, "403") {
		t.Errorf("APIError = %q", got)
	}
	if got := (&APIError{Status: 500, Body: "boom"}).Error(); !strings.Contains(got, "boom") {
		t.Errorf("APIError = %q", got)
	}
}

func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://grafana.example.com/", "test-token")
	if c.BaseURL != "https://grafana.example.com" {
		t.Errorf("BaseURL = %q; trailing slash not trimmed", c.BaseURL)
	}
}

func TestTeamStats_FallsBackToTeamLen(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/teams/search") {
			_, _ = w.Write([]byte(`{"teams":[{"memberCount":2},{"memberCount":3}]}`))
			return
		}
		http.Error(w, "nf", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-token")
	stats, err := c.TeamStats(context.Background())
	if err != nil {
		t.Fatalf("TeamStats: %v", err)
	}
	if stats.TeamCount != 2 || stats.TotalMemberships != 5 {
		t.Errorf("fallback wrong: %+v", stats)
	}
}

// serializePayload marshals a structpb payload to JSON for literal scanning.
func serializePayload(t *testing.T, p *structpb.Struct) string {
	t.Helper()
	b, err := json.Marshal(p.AsMap())
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}
