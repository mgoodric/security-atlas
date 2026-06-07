package alertrules

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

// Client is a thin read-only HTTP client for the Grafana provisioning API. It
// holds a Viewer-role service-account token (never logged) and issues only GET
// requests. It deliberately does NOT depend on a Grafana Go SDK — the connector
// mirrors the slice-486/487 thin-HTTP pattern to keep the dependency tree small.
//
// The client decodes ONLY the secret-free fields. The contact-point `settings`
// blob (which holds the secret webhook URL / token / recipient address) is
// NEVER decoded into a struct field, so it cannot leak into an evidence record
// (P0-488-3).
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a Grafana provisioning client.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// --- minimal Grafana provisioning JSON shapes (read-only, secret-free) ---

type apiAlertRule struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	// NotificationSettings carries the receiver name ONLY (the contact point the
	// rule routes to). Grafana's full notification_settings has more fields; we
	// decode just the receiver.
	NotificationSettings struct {
		Receiver string `json:"receiver"`
	} `json:"notification_settings"`
	IsPaused  bool   `json:"isPaused"`
	FolderUID string `json:"folderUID"`
	// Condition tells us whether the rule is a Grafana-managed or
	// datasource-managed rule type heuristically; if Grafana returns a "type"
	// field in future, it maps here. data / query fields are NOT decoded.
	Type string `json:"type"`
}

type apiContactPoint struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// settings is intentionally NOT a field: the secret webhook URL / token /
	// recipient address lives there and must never be decoded.
}

// ListAlertRules reads the alert rules from the provisioning API.
func (c *Client) ListAlertRules(ctx context.Context) ([]RawRule, error) {
	var list []apiAlertRule
	if err := c.getJSON(ctx, "/api/v1/provisioning/alert-rules", &list); err != nil {
		return nil, err
	}
	out := make([]RawRule, 0, len(list))
	for _, r := range list {
		uid := strings.TrimSpace(r.UID)
		if uid == "" {
			continue
		}
		out = append(out, RawRule{
			UID:          uid,
			Title:        r.Title,
			RuleType:     r.Type,
			Paused:       r.IsPaused,
			FolderUID:    r.FolderUID,
			ReceiverName: r.NotificationSettings.Receiver,
		})
	}
	return out, nil
}

// ListContactPoints reads contact-point NAMES + TYPES from the provisioning
// API. The settings blob is never decoded.
func (c *Client) ListContactPoints(ctx context.Context) ([]ContactPoint, error) {
	var list []apiContactPoint
	if err := c.getJSON(ctx, "/api/v1/provisioning/contact-points", &list); err != nil {
		return nil, err
	}
	out := make([]ContactPoint, 0, len(list))
	for _, cp := range list {
		name := strings.TrimSpace(cp.Name)
		if name == "" {
			continue
		}
		out = append(out, ContactPoint{Name: name, Kind: cp.Type})
	}
	return out, nil
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
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
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
