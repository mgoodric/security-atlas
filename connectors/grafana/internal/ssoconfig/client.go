package ssoconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxItems is the hard per-run cap on how many providers / assignments the
// client will decode from any single endpoint (threat-model D / DoS guard).
// Grafana installs have a handful of SSO providers and bounded RBAC
// assignments; this cap stops a malformed / hostile source from forcing an
// unbounded decode.
const maxItems = 5000

// Client is a thin read-only HTTP client for the Grafana SSO-settings +
// access-control API. It holds a service-account token (never logged) and
// issues only GET requests. It mirrors the slice-488 alertrules thin-HTTP
// pattern to keep the dependency tree small.
//
// The client decodes ONLY the secret-free / identity-free fields. The
// sso-settings `settings` blob holds the SAML private key / OAuth client secret
// / signing certificate / LDAP bind password; those keys are NEVER decoded into
// a struct field, so they cannot leak into an evidence record (P0-534).
// Likewise the access-control responses' assigned-principal identities (user
// login / email / name) are NEVER decoded — only the assignment is counted.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a Grafana SSO/RBAC read-only client.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// --- minimal Grafana JSON shapes (read-only, secret-free, identity-free) ---

// apiSSOSetting is the narrow view of one GET /api/v1/sso-settings entry. The
// `settings` object in the real response carries the SAML private_key, the
// OAuth client_secret, certificates, and the LDAP bind password — NONE of those
// keys are declared here, so json.Decode silently discards them. Only the
// provider id, the enabled flag, and the role-mapping rule strings are decoded.
type apiSSOSetting struct {
	Provider string `json:"provider"`
	Settings struct {
		Enabled bool `json:"enabled"`
		// roleValuesEditor / roleValuesAdmin / roleValuesGrafanaAdmin are
		// Grafana's org-role mapping RULE strings (role names / attribute
		// expressions). Role names only — never an identity, never a secret.
		RoleValuesEditor       []string `json:"role_values_editor"`
		RoleValuesAdmin        []string `json:"role_values_admin"`
		RoleValuesGrafanaAdmin []string `json:"role_values_grafana_admin"`
		RoleValuesViewer       []string `json:"role_values_viewer"`
		// Every secret-bearing key (private_key, client_secret, certificate,
		// bind_password, signing_cert, ...) is intentionally ABSENT: an absent
		// struct field is never populated by json.Decode, so the secret never
		// enters connector memory as data (P0-534).
	} `json:"settings"`
}

// apiTeamSearch is the narrow view of GET /api/teams/search. memberCount is a
// per-team COUNT; the team members themselves are never requested or decoded.
type apiTeamSearch struct {
	TotalCount int `json:"totalCount"`
	Teams      []struct {
		MemberCount int `json:"memberCount"`
		// No member identity fields are declared — counts only.
	} `json:"teams"`
}

// apiRoleAssignment is the narrow view of one GET /api/access-control/assignments
// entry. Only the assignment SCOPE is decoded (whether it targets a user, a
// team, or a built-in role) — never the assigned principal's id / login / email.
type apiRoleAssignment struct {
	// Exactly one of these is non-empty in a Grafana assignment row; the client
	// counts which scope it is, never the value's identity.
	UserID      json.RawMessage `json:"userId"`
	TeamID      json.RawMessage `json:"teamId"`
	BuiltinRole string          `json:"builtinRole"`
}

// ListSSOProviders reads SSO settings (secret-free fields only).
func (c *Client) ListSSOProviders(ctx context.Context) ([]RawProvider, error) {
	var list []apiSSOSetting
	if err := c.getJSON(ctx, "/api/v1/sso-settings", &list); err != nil {
		return nil, err
	}
	if len(list) > maxItems {
		list = list[:maxItems]
	}
	out := make([]RawProvider, 0, len(list))
	for _, s := range list {
		provider := strings.TrimSpace(s.Provider)
		if provider == "" {
			continue
		}
		mappings := make([]string, 0)
		mappings = append(mappings, s.Settings.RoleValuesViewer...)
		mappings = append(mappings, s.Settings.RoleValuesEditor...)
		mappings = append(mappings, s.Settings.RoleValuesAdmin...)
		mappings = append(mappings, s.Settings.RoleValuesGrafanaAdmin...)
		out = append(out, RawProvider{
			Type:         provider,
			Enabled:      s.Settings.Enabled,
			RoleMappings: mappings,
		})
	}
	return out, nil
}

// TeamStats reads aggregate team counts (team count + total membership count).
func (c *Client) TeamStats(ctx context.Context) (RawTeamStats, error) {
	var resp apiTeamSearch
	if err := c.getJSON(ctx, "/api/teams/search?perpage=5000&page=1", &resp); err != nil {
		return RawTeamStats{}, err
	}
	teams := resp.Teams
	if len(teams) > maxItems {
		teams = teams[:maxItems]
	}
	total := 0
	for _, t := range teams {
		if t.MemberCount > 0 {
			total += t.MemberCount
		}
	}
	count := resp.TotalCount
	if count <= 0 {
		count = len(teams)
	}
	return RawTeamStats{TeamCount: count, TotalMemberships: total}, nil
}

// RoleAssignmentStats reads RBAC role assignments and rolls them up by scope.
func (c *Client) RoleAssignmentStats(ctx context.Context) (RawRoleStats, error) {
	var list []apiRoleAssignment
	if err := c.getJSON(ctx, "/api/access-control/assignments", &list); err != nil {
		return RawRoleStats{}, err
	}
	if len(list) > maxItems {
		list = list[:maxItems]
	}
	var stats RawRoleStats
	for _, a := range list {
		switch {
		case len(a.UserID) > 0 && string(a.UserID) != "null":
			stats.UserAssignments++
		case len(a.TeamID) > 0 && string(a.TeamID) != "null":
			stats.TeamAssignments++
		case strings.TrimSpace(a.BuiltinRole) != "":
			stats.BuiltinAssignments++
		}
	}
	return stats, nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	// Bound the body read so a hostile/huge response cannot exhaust memory.
	const maxBody = 1 << 24 // 16 MiB
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries Grafana REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "grafana: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("grafana: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
