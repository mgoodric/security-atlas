package siemsignals

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

// Client is a thin read-only HTTP client for the Datadog security-signals
// search API. It holds the API + Application keys (never logged) and issues only
// GET requests against /api/v2/security_monitoring/signals (requires the
// security_monitoring_signals_read scope). It deliberately does NOT depend on
// the Datadog Go SDK — mirrors the slice-488/533 thin-HTTP pattern.
//
// The client decodes ONLY the body-free fields (signal id, firing rule id +
// name, severity, triage status, the timeline timestamps, and the opaque
// triager handle). The signal MESSAGE body, the matched log/event SAMPLES, the
// detection QUERY, the signal-body tags/facets, and any PII are never decoded,
// so they cannot leak into a record (P0-636).
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.datadoghq.com
	apiKey  string
	appKey  string
}

// NewClient builds a Datadog security-signals client. apiKey + appKey are the
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

const (
	// pageSize is the per-request page size the cursor loop requests.
	pageSize = 100
	// maxPages is the hard per-run page cap (DoS / over-collection guard). At
	// pageSize=100 this bounds a run to 5,000 signals; beyond that the run stops
	// and reports honestly rather than reading unbounded.
	maxPages = 50
	// runTimeout bounds the whole multi-page read regardless of the caller's
	// context, so a slow/adversarial source cannot hang the connector run.
	runTimeout = 60 * time.Second
)

// ErrSignalCapExceeded is returned when the signal set exceeds maxPages*pageSize
// in the look-back window. The connector stops and reports honestly rather than
// reading unbounded; the operator narrows the window or raises the cap.
var ErrSignalCapExceeded = fmt.Errorf("datadog security-signal set exceeds the per-run cap (%d signals); narrow the look-back window or raise the cap deliberately", maxPages*pageSize)

// apiSignal is the minimal Datadog security-signal JSON shape — body-free fields
// only. The message / samples / custom attributes / tags / the rule query are
// intentionally absent: json.Decode discards JSON keys with no matching struct
// field, so they never enter memory as connector data.
type apiSignal struct {
	ID         string `json:"id"`
	Attributes struct {
		// Timestamp is the signal first-seen instant (RFC3339 string or epoch
		// millis — both handled by parseTime).
		Timestamp string `json:"timestamp"`
		// Status is the signal severity label (info/low/medium/high/critical).
		// Datadog names the severity field "status" on the signal attributes.
		Status string `json:"status"`
		// Workflow carries the triage state + actor + triage timestamp. ONLY the
		// triage metadata is decoded — never the signal message or samples.
		Workflow struct {
			TriageState string `json:"triage_state"`
			ArchivedBy  string `json:"archived_by"`
			AssigneeID  string `json:"assignee_id"`
			UpdatedAt   string `json:"triage_state_updated_at"`
		} `json:"workflow"`
		// Custom carries ONLY the firing rule's id + name. The rest of the
		// custom block (the matched event payload, query, tags) is NOT decoded.
		Custom struct {
			RuleID   string `json:"rule_id"`
			RuleName string `json:"rule_name"`
		} `json:"custom"`
	} `json:"attributes"`
}

// apiPage is the Datadog v2 list envelope: data + cursor-based pagination meta.
type apiPage struct {
	Data []apiSignal `json:"data"`
	Meta struct {
		Page struct {
			After string `json:"after"`
		} `json:"page"`
	} `json:"meta"`
}

// ListSignals reads every signal in [since, now] via a bounded cursor loop.
// Read-only: only GETs against /api/v2/security_monitoring/signals. Stops at
// maxPages with ErrSignalCapExceeded if the source still reports more (DoS
// guard).
func (c *Client) ListSignals(ctx context.Context, since time.Time) ([]RawSignal, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var out []RawSignal
	after := ""
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, ErrSignalCapExceeded
		}
		q := url.Values{}
		q.Set("page[limit]", strconv.Itoa(pageSize))
		q.Set("filter[from]", since.UTC().Format(time.RFC3339))
		if after != "" {
			q.Set("page[cursor]", after)
		}
		var pg apiPage
		if err := c.getJSON(ctx, "/api/v2/security_monitoring/signals?"+q.Encode(), &pg); err != nil {
			return nil, err
		}
		for _, s := range pg.Data {
			id := strings.TrimSpace(s.ID)
			if id == "" {
				continue
			}
			out = append(out, RawSignal{
				ID:            id,
				RuleID:        strings.TrimSpace(s.Attributes.Custom.RuleID),
				RuleName:      strings.TrimSpace(s.Attributes.Custom.RuleName),
				Severity:      s.Attributes.Status,
				Status:        s.Attributes.Workflow.TriageState,
				FirstSeen:     parseTime(s.Attributes.Timestamp),
				Triaged:       parseTime(s.Attributes.Workflow.UpdatedAt),
				LastUpdated:   parseTime(s.Attributes.Workflow.UpdatedAt),
				TriagerHandle: triager(s),
			})
		}
		after = strings.TrimSpace(pg.Meta.Page.After)
		if after == "" {
			break
		}
	}
	return out, nil
}

// triager returns the opaque triager id, preferring the archiver, then the
// assignee. An email-shaped value is left to Collect's sanitizeHandle to drop;
// the client passes the opaque id up untouched.
func triager(s apiSignal) string {
	if v := strings.TrimSpace(s.Attributes.Workflow.ArchivedBy); v != "" {
		return v
	}
	return strings.TrimSpace(s.Attributes.Workflow.AssigneeID)
}

// parseTime parses the RFC3339 (or epoch-millis) timestamp Datadog returns.
// Returns the zero time on an unparseable/empty value (the caller omits a zero
// timestamp from the record).
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
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
