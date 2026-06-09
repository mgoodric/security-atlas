package siemrules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the Datadog Security-Monitoring
// rules API. It holds the API + Application keys (never logged) and issues only
// GET requests against /api/v2/security_monitoring/rules (requires the
// security_monitoring_rules_read scope). It deliberately does NOT depend on the
// Datadog Go SDK — the connector mirrors the slice-488 thin-HTTP pattern.
//
// The client decodes ONLY the secret-free fields (id, name, type/detection
// class, enabled, severity, and the per-case notification handles). The
// detection query, matched signals, log samples, matched-event payloads, the
// secret webhook URLs behind notification targets, and any telemetry are never
// decoded, so they cannot leak into a record (P0-533).
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.datadoghq.com
	apiKey  string
	appKey  string
}

// NewClient builds a Datadog security-monitoring rules client. apiKey + appKey
// are the read-scoped Datadog keys (from datadogauth.Credential); baseURL is
// the site-derived API base.
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

const (
	// pageSize is the per-request page size the cursor loop requests.
	pageSize = 100
	// maxPages is the hard per-run page cap (DoS / over-collection guard,
	// threat-model D). At pageSize=100 this bounds a run to 5,000 rules; beyond
	// that the run stops and reports honestly rather than reading unbounded.
	maxPages = 50
	// runTimeout bounds the whole multi-page read regardless of the caller's
	// context, so a slow/adversarial source cannot hang the connector run.
	runTimeout = 60 * time.Second
)

// ErrRuleCapExceeded is returned when the rule set exceeds maxPages*pageSize.
// The connector stops and reports honestly rather than reading unbounded.
var ErrRuleCapExceeded = fmt.Errorf("datadog security-monitoring rule set exceeds the per-run cap (%d rules); narrow the source or raise the cap deliberately", maxPages*pageSize)

// apiRule is the minimal Datadog security-monitoring rule JSON shape —
// secret-free fields only. The query / queries / cases-with-conditions /
// filters / options and every telemetry field are intentionally absent:
// json.Decode discards JSON keys with no matching struct field, so they never
// enter memory as connector data.
type apiRule struct {
	ID         string `json:"id"`
	Attributes struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		IsEnabled bool   `json:"isEnabled"`
		// Cases carry only the per-case severity (status) and the per-case
		// notification handles. The case CONDITION / query is NOT decoded.
		Cases []struct {
			Status        string   `json:"status"`
			Notifications []string `json:"notifications"`
		} `json:"cases"`
	} `json:"attributes"`
}

// apiPage is the Datadog v2 list envelope: data + cursor-based pagination meta.
type apiPage struct {
	Data []apiRule `json:"data"`
	Meta struct {
		Page struct {
			After string `json:"after"`
		} `json:"page"`
	} `json:"meta"`
}

// severityRank orders Datadog case statuses so we can report the rule's HIGHEST
// case severity (the audit-relevant "how serious can this rule's signals get").
var severityRank = map[string]int{
	"info": 0, "low": 1, "medium": 2, "high": 3, "critical": 4,
}

// ListRules reads every detection rule via a bounded cursor loop. Read-only:
// only GETs against /api/v2/security_monitoring/rules. Stops at maxPages with
// ErrRuleCapExceeded if the source still reports more (DoS guard).
func (c *Client) ListRules(ctx context.Context) ([]RawRule, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var out []RawRule
	after := ""
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, ErrRuleCapExceeded
		}
		q := url.Values{}
		q.Set("page[size]", strconv.Itoa(pageSize))
		if after != "" {
			q.Set("page[cursor]", after)
		}
		var pg apiPage
		if err := c.getJSON(ctx, "/api/v2/security_monitoring/rules?"+q.Encode(), &pg); err != nil {
			return nil, err
		}
		for _, r := range pg.Data {
			id := strings.TrimSpace(r.ID)
			if id == "" {
				continue
			}
			out = append(out, RawRule{
				ID:             id,
				Name:           r.Attributes.Name,
				DetectionClass: r.Attributes.Type,
				Enabled:        r.Attributes.IsEnabled,
				Severity:       highestSeverity(r),
				Handles:        caseHandles(r),
			})
		}
		after = strings.TrimSpace(pg.Meta.Page.After)
		if after == "" {
			break
		}
	}
	return out, nil
}

// highestSeverity returns the highest case status across the rule's cases.
func highestSeverity(r apiRule) string {
	best := ""
	bestRank := -1
	for _, ca := range r.Attributes.Cases {
		s := strings.ToLower(strings.TrimSpace(ca.Status))
		if rank, ok := severityRank[s]; ok && rank > bestRank {
			bestRank = rank
			best = s
		}
	}
	return best
}

// caseHandles flattens the per-case notification mentions into one handle list.
// The raw mentions are passed up; parseTargets does the PII drop + classify.
func caseHandles(r apiRule) []string {
	var out []string
	for _, ca := range r.Attributes.Cases {
		out = append(out, ca.Notifications...)
	}
	return out
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
