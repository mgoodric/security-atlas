// Package oktaapps pulls Okta applications and their group assignments,
// producing one Assignment per application.
//
// Endpoints:
//   - GET /api/v1/apps                            (list applications)
//   - GET /api/v1/apps/{id}/groups                (groups assigned to app)
//
// Output is descriptive: the evaluator (slice 015) interprets which app-
// assignment pattern passes/fails per (control, scope). The connector
// emits Result_INCONCLUSIVE so we don't bake policy into the pipe.
package oktaapps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// Assignment is one record the cmd layer turns into an evidence record.
// Field names map 1:1 to okta.app_assignment.v1 schema.
type Assignment struct {
	AppID              string
	AppName            string
	SignOnMode         string
	Status             string
	AssignedGroupIDs   []string
	AssignedGroupCount int
	ObservedAt         time.Time
}

// API is the narrow surface Pull depends on.
type API interface {
	ListApps(ctx context.Context) ([]rawApp, error)
	ListAppGroups(ctx context.Context, appID string) ([]rawGroup, error)
}

// Pull lists every app and reconciles its group assignments. Errors on a
// single app's group listing are recorded as the app having an empty
// assignment set with no fatal abort — partial inventory beats no
// inventory.
func Pull(ctx context.Context, api API, now func() time.Time) ([]Assignment, error) {
	if api == nil {
		return nil, errors.New("oktaapps: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	apps, err := api.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	out := make([]Assignment, 0, len(apps))
	for _, a := range apps {
		if a.ID == "" {
			continue
		}
		name := firstNonEmpty(a.Label, a.Name)
		if name == "" {
			// Schema requires app_name; skip rather than emit invalid records.
			continue
		}
		groups, err := api.ListAppGroups(ctx, a.ID)
		if err != nil {
			// Recorded as zero-group assignment so the evaluator can decide.
			groups = nil
		}
		ids := make([]string, 0, len(groups))
		for _, g := range groups {
			if g.ID != "" {
				ids = append(ids, g.ID)
			}
		}
		out = append(out, Assignment{
			AppID:              a.ID,
			AppName:            name,
			SignOnMode:         a.SignOnMode,
			Status:             normalizeStatus(a.Status),
			AssignedGroupIDs:   ids,
			AssignedGroupCount: len(ids),
			ObservedAt:         now(),
		})
	}
	return out, nil
}

// normalizeStatus collapses Okta's known status strings to the
// schema-enum subset. Unknown statuses pass through verbatim; the
// schema's enum will reject them at ingest validation time, surfacing the
// drift loudly.
func normalizeStatus(s string) string {
	up := strings.ToUpper(strings.TrimSpace(s))
	if up == "" {
		return "INACTIVE"
	}
	return up
}

// ---- REST client ----

type rawApp struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	SignOnMode string `json:"signOnMode"`
}

type rawGroup struct {
	ID string `json:"id"`
}

// Client is a thin HTTP client for the apps endpoint family.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   oktaauth.Credential
}

// NewClient builds a Client.
func NewClient(httpClient *http.Client, baseURL string, creds oktaauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListApps fetches the first page of apps. v1 pulls only the first 200;
// pagination lands when org sizes demand it.
func (c *Client) ListApps(ctx context.Context) ([]rawApp, error) {
	u := c.BaseURL + "/api/v1/apps?limit=200"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawApp
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode apps: %w", err)
	}
	return out, nil
}

// ListAppGroups fetches the groups assigned to one app.
func (c *Client) ListAppGroups(ctx context.Context, appID string) ([]rawGroup, error) {
	if appID == "" {
		return nil, errors.New("oktaapps: appID required")
	}
	u := fmt.Sprintf("%s/api/v1/apps/%s/groups", c.BaseURL, url.PathEscape(appID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawGroup
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode app groups: %w", err)
	}
	return out, nil
}

// APIError carries Okta REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("okta: HTTP %d", e.Status)
	}
	return fmt.Sprintf("okta: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
