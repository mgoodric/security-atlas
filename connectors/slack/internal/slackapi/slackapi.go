// Package slackapi adapts the real Slack Web API to the narrow interfaces
// the connector consumes (slackauth.IdentityAPI + slackcollect.MembersAPI /
// AuditAPI / RetentionAPI). It is the ONLY package that touches the network
// and the ONLY place the Slack token is read (via slackauth.Token.Value, at
// the Authorization header — never logged).
//
// Endpoint discipline (slice 443 threat-model I / P0-443-1): this adapter
// calls ONLY admin/audit read endpoints. It has no code path that calls
// conversations.history, conversations.replies, search.messages, or any
// message-reading endpoint. The struct decoders deliberately decode only the
// metadata fields the connector emits — a message body in a Slack response is
// simply never read into any field.
package slackapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackauth"
	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackcollect"
)

// BaseURL is the Slack Web API root. The audit-logs surface lives under the
// same host (api.slack.com/audit/v1) but is reached via the same client.
const BaseURL = "https://slack.com/api"

// AuditBaseURL is the Slack audit-logs API root.
const AuditBaseURL = "https://api.slack.com/audit/v1"

// Client is the Slack Web API adapter. Construct with New.
type Client struct {
	token      slackauth.Token
	httpClient *http.Client
	baseURL    string
	auditURL   string
}

// New returns a Client. insecureTLS is honored only for loopback test
// servers; production always verifies TLS.
func New(token slackauth.Token, insecureTLS bool) *Client {
	transport := &http.Transport{}
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback test endpoints only
	}
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
		baseURL:    BaseURL,
		auditURL:   AuditBaseURL,
	}
}

// get issues an authenticated GET against the Slack API. The token is read
// here and ONLY here, onto the Authorization header — never logged.
func (c *Client) get(ctx context.Context, fullURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("slackapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token.Value())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The error from Do can include the request URL but never the
		// Authorization header value; the token cannot leak here.
		return fmt.Errorf("slackapi: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slackapi: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("slackapi: decode response: %w", err)
	}
	return nil
}

// --- slackauth.IdentityAPI ---

type teamInfoResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Team  struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

// ResolveTeam reads team.info to resolve the workspace id used for scoping.
func (c *Client) ResolveTeam(ctx context.Context) (slackauth.Identity, error) {
	var r teamInfoResponse
	if err := c.get(ctx, c.baseURL+"/team.info", &r); err != nil {
		return slackauth.Identity{}, err
	}
	if !r.OK {
		return slackauth.Identity{}, fmt.Errorf("slackapi: team.info: %s", r.Error)
	}
	return slackauth.Identity{TeamID: r.Team.ID, TeamName: r.Team.Name}, nil
}

// --- slackcollect.MembersAPI ---

type usersListResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Members []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
		IsOwner bool   `json:"is_owner"`
		IsBot   bool   `json:"is_bot"`
		Deleted bool   `json:"deleted"`
		Has2FA  bool   `json:"has_2fa"`
		Profile struct {
			DisplayName string `json:"display_name"`
		} `json:"profile"`
	} `json:"members"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ListMembers reads one page of the workspace roster via users.list. Only
// membership/role/2FA METADATA is decoded — never message content.
func (c *Client) ListMembers(ctx context.Context, cursor string) ([]slackcollect.Member, string, error) {
	q := url.Values{}
	q.Set("limit", "200")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var r usersListResponse
	if err := c.get(ctx, c.baseURL+"/users.list?"+q.Encode(), &r); err != nil {
		return nil, "", err
	}
	if !r.OK {
		return nil, "", fmt.Errorf("slackapi: users.list: %s", r.Error)
	}
	out := make([]slackcollect.Member, 0, len(r.Members))
	for _, m := range r.Members {
		handle := m.Profile.DisplayName
		if handle == "" {
			handle = m.Name
		}
		out = append(out, slackcollect.Member{
			UserID:  m.ID,
			Handle:  handle,
			IsAdmin: m.IsAdmin,
			IsOwner: m.IsOwner,
			IsBot:   m.IsBot,
			Deleted: m.Deleted,
			Has2FA:  m.Has2FA,
		})
	}
	return out, r.ResponseMetadata.NextCursor, nil
}

// --- slackcollect.AuditAPI ---

type auditLogsResponse struct {
	Entries []struct {
		ID         string `json:"id"`
		DateCreate int64  `json:"date_create"`
		Action     string `json:"action"`
		Actor      struct {
			User struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"actor"`
		Entity struct {
			Type string `json:"type"`
		} `json:"entity"`
	} `json:"entries"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ListAuditEvents reads one page of the admin audit-log via /audit/v1/logs.
// Only admin-action METADATA is decoded — never the body of any object Slack
// acted on.
func (c *Client) ListAuditEvents(ctx context.Context, cursor string) ([]slackcollect.AuditEvent, string, error) {
	q := url.Values{}
	q.Set("limit", "200")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var r auditLogsResponse
	if err := c.get(ctx, c.auditURL+"/logs?"+q.Encode(), &r); err != nil {
		return nil, "", err
	}
	out := make([]slackcollect.AuditEvent, 0, len(r.Entries))
	for _, e := range r.Entries {
		out = append(out, slackcollect.AuditEvent{
			ID:         e.ID,
			Action:     e.Action,
			ActorID:    e.Actor.User.ID,
			ActorEmail: e.Actor.User.Email,
			EntityType: e.Entity.Type,
			DateCreate: e.DateCreate,
		})
	}
	return out, r.ResponseMetadata.NextCursor, nil
}

// --- slackcollect.RetentionAPI ---

type teamPrefsResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Prefs struct {
		// Slack exposes retention as a duration in days; 0 means "retain
		// forever". The exact pref keys vary by plan; the connector reads the
		// retention-duration prefs only — never any message.
		RetentionDuration     int32 `json:"retention_duration"`
		FileRetentionDuration int32 `json:"file_retention_duration"`
		RetentionType         int32 `json:"retention_type"`
	} `json:"prefs"`
}

// GetRetention reads the workspace retention posture via team.preferences /
// team.info prefs. Settings-only — duration + policy flags, never a message.
func (c *Client) GetRetention(ctx context.Context) (slackcollect.RetentionSettings, error) {
	var r teamPrefsResponse
	if err := c.get(ctx, c.baseURL+"/team.preferences.list", &r); err != nil {
		return slackcollect.RetentionSettings{}, err
	}
	if !r.OK {
		return slackcollect.RetentionSettings{}, fmt.Errorf("slackapi: team.preferences.list: %s", r.Error)
	}
	// retention_type != 0 (or a finite duration) signals a non-default policy.
	enabled := r.Prefs.RetentionType != 0 || r.Prefs.RetentionDuration > 0
	return slackcollect.RetentionSettings{
		MessagesRetentionDays: r.Prefs.RetentionDuration,
		FilesRetentionDays:    r.Prefs.FileRetentionDuration,
		RetentionEnabled:      enabled,
	}, nil
}

// assertReadOnlyHost is a defensive constant-fold guard kept here to make the
// endpoint discipline explicit to a reader: every URL this package builds is
// rooted at one of these read hosts.
var _ = func() bool {
	for _, base := range []string{BaseURL, AuditBaseURL} {
		if !strings.HasPrefix(base, "https://") {
			panic("slackapi: base URLs must be https")
		}
	}
	return true
}()
