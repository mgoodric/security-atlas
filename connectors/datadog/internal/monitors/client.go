package monitors

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

// Client is a thin read-only HTTP client for the Datadog monitor API. It holds
// the API + Application keys (never logged) and issues only GET requests against
// /api/v1/monitor (requires the monitors_read scope). It deliberately does NOT
// depend on the Datadog Go SDK — the connector mirrors the slice-486/487
// thin-HTTP pattern to keep the dependency tree small.
//
// The client decodes ONLY the secret-free fields (id, name, type, message,
// enabled). The monitor query, options blob, and any telemetry are never
// decoded, so they cannot leak into an evidence record (P0-488-3).
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.datadoghq.com
	apiKey  string
	appKey  string
}

// NewClient builds a Datadog monitors client. apiKey + appKey are the
// read-scoped Datadog keys (from datadogauth.Credential); baseURL is the
// site-derived API base.
func NewClient(httpClient *http.Client, baseURL, apiKey, appKey string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		HTTP:    httpClient,
		BaseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		appKey:  appKey,
	}
}

const pageLimit = 1000

// apiMonitor is the minimal Datadog monitor JSON shape — secret-free fields
// only. The query / options / restricted_roles / creator and every telemetry
// field are intentionally absent: json.Decode discards JSON keys with no
// matching struct field, so they never enter memory as connector data.
type apiMonitor struct {
	ID      json.Number `json:"id"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	Message string      `json:"message"`
	Options struct {
		// EnableLogsSample etc. are NOT decoded. Only the silenced map tells us
		// whether the monitor is effectively muted; an empty/absent silenced map
		// means enabled.
		Silenced map[string]interface{} `json:"silenced"`
	} `json:"options"`
}

// ListMonitors reads the first bounded page of monitors. Read-only: a single
// GET against /api/v1/monitor.
func (c *Client) ListMonitors(ctx context.Context) ([]RawMonitor, error) {
	var list []apiMonitor
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v1/monitor?page_size=%d&page=0", pageLimit), &list); err != nil {
		return nil, err
	}
	out := make([]RawMonitor, 0, len(list))
	for _, m := range list {
		id := strings.TrimSpace(m.ID.String())
		if id == "" || id == "0" {
			continue
		}
		out = append(out, RawMonitor{
			ID:      id,
			Name:    m.Name,
			Type:    m.Type,
			Enabled: len(m.Options.Silenced) == 0,
			Message: m.Message,
		})
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
	if c.apiKey != "" {
		req.Header.Set("DD-API-KEY", c.apiKey)
	}
	if c.appKey != "" {
		req.Header.Set("DD-APPLICATION-KEY", c.appKey)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries Datadog REST error context. The body is bounded; Datadog
// error bodies do not echo the request credentials.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "datadog: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("datadog: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
