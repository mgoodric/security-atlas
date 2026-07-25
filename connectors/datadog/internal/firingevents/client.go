package firingevents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

// Client is a thin read-only HTTP client for the Datadog Events API. It holds
// the API + Application keys (never logged) and issues only GET requests against
// /api/v1/events (requires the events_read scope), filtered to monitor-alert
// events. It deliberately does NOT depend on the Datadog Go SDK — mirrors the
// slice-488/533/636 thin-HTTP pattern.
//
// The client decodes ONLY the body-free fields (the monitor id parsed from the
// event's monitor reference, the alert state, the firing timestamp, and the
// opaque notification-target handle parsed from the event's "@handle"
// mentions). The event TEXT/body, the triggering metric VALUES, the secret
// WEBHOOK URL, and recipient PII are never decoded into a record, so they cannot
// leak (P0-535).
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.datadoghq.com
	apiKey  string
	appKey  string
}

// NewClient builds a Datadog Events client. apiKey + appKey are the read-scoped
// Datadog keys (from datadogauth.Credential); baseURL is the site-derived API
// base.
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
	// pageLimit is the per-request event page size the loop requests.
	pageLimit = 100
	// maxPages is the hard per-run page cap (DoS / over-collection guard). At
	// pageLimit=100 this bounds a run to 5,000 firing events; beyond that the run
	// stops and reports honestly rather than reading unbounded.
	maxPages = 50
	// runTimeout bounds the whole multi-page read regardless of the caller's
	// context, so a slow/adversarial source cannot hang the connector run.
	runTimeout = 60 * time.Second
)

// ErrEventCapExceeded is returned when the monitor-event set exceeds
// maxPages*pageLimit in the look-back window. The connector stops and reports
// honestly rather than reading unbounded; the operator narrows the window or
// raises the cap.
var ErrEventCapExceeded = fmt.Errorf("datadog monitor-event set exceeds the per-run cap (%d events); narrow the look-back window or raise the cap deliberately", maxPages*pageLimit)

// handleRe matches Datadog "@handle" notification mentions in an event title.
// A handle is the run of handle characters after the @. Email recipients
// ("@user@example.com") are filtered out by firing.Collect's PII drop.
var handleRe = regexp.MustCompile(`@([A-Za-z0-9._\-]+(?:@[A-Za-z0-9._\-]+)?)`)

// apiEvent is the minimal Datadog Events API JSON shape — body-free fields
// only. The event `text` body, the metric `values`, custom attributes, and tags
// are intentionally absent: json.Decode discards JSON keys with no matching
// struct field, so they never enter memory as connector data.
//
// Datadog encodes a monitor's alert/recovery transition as an event with
// alert_type (error|warning|success|info...) and a monitor reference. We decode
// only those.
type apiEvent struct {
	// AlertType is the transition class: "error"/"warning" = firing,
	// "success"/"recovery" = resolved. Mapped to a firing state.
	AlertType string `json:"alert_type"`
	// MonitorID is the firing monitor's numeric id (the rule_id). Datadog also
	// echoes it inside `monitor_id`; we read that field directly.
	MonitorID int64 `json:"monitor_id"`
	// DateHappened is the firing instant in epoch SECONDS.
	DateHappened int64 `json:"date_happened"`
	// Title is the event title; used ONLY to parse "@handle" notification
	// mentions for the routing target. The title text itself is not emitted.
	Title string `json:"title"`
}

// apiPage is the Datadog v1 events list envelope.
type apiPage struct {
	Events []apiEvent `json:"events"`
}

// ListMonitorEvents reads every monitor-alert event in [since, now] via a
// bounded page loop. Read-only: only GETs against /api/v1/events filtered to
// sources=monitor_alert. Stops at maxPages with ErrEventCapExceeded if the
// source still reports a full page (DoS guard).
func (c *Client) ListMonitorEvents(ctx context.Context, since time.Time) ([]firing.RawFiring, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var out []firing.RawFiring
	// The Events API paginates by time: each subsequent page reads strictly
	// before the oldest event already seen. We walk backwards from now to since.
	end := time.Now().UTC()
	if end.Before(since) {
		end = since.Add(time.Hour)
	}
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, ErrEventCapExceeded
		}
		q := url.Values{}
		q.Set("start", strconv.FormatInt(since.UTC().Unix(), 10))
		q.Set("end", strconv.FormatInt(end.UTC().Unix(), 10))
		q.Set("sources", "monitor_alert")
		var pg apiPage
		if err := c.getJSON(ctx, "/api/v1/events?"+q.Encode(), &pg); err != nil {
			return nil, err
		}
		oldest := end
		for _, e := range pg.Events {
			if e.MonitorID <= 0 || e.DateHappened <= 0 {
				continue
			}
			ts := time.Unix(e.DateHappened, 0).UTC()
			if ts.Before(oldest) {
				oldest = ts
			}
			out = append(out, firing.RawFiring{
				RuleID:       strconv.FormatInt(e.MonitorID, 10),
				State:        e.AlertType,
				FiredAt:      ts,
				TargetHandle: firstHandle(e.Title),
				TargetKind:   "",
			})
		}
		// Stop when the page is not full (no more history) or we've walked back
		// past the look-back window.
		if len(pg.Events) < pageLimit || !oldest.After(since) {
			break
		}
		// Next page ends one second before the oldest event seen.
		end = oldest.Add(-time.Second)
		if !end.After(since) {
			break
		}
	}
	return out, nil
}

// firstHandle returns the first non-email "@handle" notification mention in the
// event title, or "" if there is none. Email-shaped handles are left for
// firing.Collect to drop (PII); this just picks the first candidate token.
func firstHandle(title string) string {
	if title == "" {
		return ""
	}
	for _, m := range handleRe.FindAllStringSubmatch(title, -1) {
		h := m[1]
		if strings.Contains(h, "@") {
			continue // email recipient: PII, dropped downstream too
		}
		return h
	}
	return ""
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
