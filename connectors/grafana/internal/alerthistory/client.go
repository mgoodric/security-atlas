package alerthistory

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

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

// Client is a thin read-only HTTP client for the Grafana alerting state-history
// API. It holds a Viewer-role service-account token (never logged) and issues
// only GET requests against /api/v1/rules/history. It deliberately does NOT
// depend on a Grafana Go SDK — mirrors the slice-488/534 thin-HTTP pattern.
//
// The state-history API returns a Loki-style frame whose lines carry the rule
// uid, the current/previous state, the rule's labels (which can embed the
// triggering metric VALUES and recipient labels), and free-text annotations
// (the alert message). The client decodes ONLY the rule uid, the state, the
// timestamp, and the contact-point label — NEVER the annotations, the metric
// VALUES, the secret contact-point settings, or recipient PII (P0-535).
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a Grafana state-history client.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// maxTransitions is the hard per-run cap (DoS / over-collection guard): the
// state-history frame is truncated to this many transitions; beyond it the run
// stops with ErrTransitionCapExceeded rather than building an unbounded record
// set.
const maxTransitions = 5000

// runTimeout bounds the whole read regardless of the caller's context.
const runTimeout = 60 * time.Second

// ErrTransitionCapExceeded is returned when the state-history frame holds more
// than maxTransitions transitions in the look-back window. The operator narrows
// the window or raises the cap deliberately.
var ErrTransitionCapExceeded = fmt.Errorf("grafana state-history frame exceeds the per-run cap (%d transitions); narrow the look-back window or raise the cap deliberately", maxTransitions)

// historyFrame is the minimal Grafana state-history envelope. The Loki frame's
// `values` is a column array: values[0] is the timestamp column (epoch millis),
// values[1] is the line column (each line a JSON object). We decode the frame
// envelope and then each line's body-free fields only.
type historyFrame struct {
	Data struct {
		Values []json.RawMessage `json:"values"`
	} `json:"data"`
}

// lineFields is the per-transition body-free view. The Grafana state-history
// line also carries `values` (the triggering metric numbers), `error`, and
// free-text `annotations` — none of which are declared here, so json.Decode
// discards them and they never enter memory (P0-535).
type lineFields struct {
	// Current is the state being entered (e.g. "Alerting", "Normal", "Pending",
	// "NoData"). Mapped to a firing state by firing.NormalizeState.
	Current string `json:"current"`
	// RuleUID is the rule that transitioned.
	RuleUID string `json:"ruleUID"`
	// Labels carry the alert instance labels. We read ONLY the contact-point /
	// receiver label for the routing handle; everything else (including any
	// metric-value or recipient label) is ignored.
	Labels map[string]string `json:"labels"`
}

// ListStateHistory reads the state-history frame and maps each firing
// transition to a firing.RawFiring. Read-only: a single GET against
// /api/v1/rules/history with a from/to window. Drops non-firing-relevant
// transitions to Normal only when they cannot be tied to a fired_at — every
// transition with a timestamp + rule uid is emitted with its state so a
// resolve transition is captured too.
func (c *Client) ListStateHistory(ctx context.Context, since time.Time) ([]firing.RawFiring, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("from", strconv.FormatInt(since.UTC().Unix(), 10))
	q.Set("to", strconv.FormatInt(time.Now().UTC().Unix(), 10))
	q.Set("limit", strconv.Itoa(maxTransitions))

	var frame historyFrame
	if err := c.getJSON(ctx, "/api/v1/rules/history?"+q.Encode(), &frame); err != nil {
		return nil, err
	}
	if len(frame.Data.Values) < 2 {
		return nil, nil
	}

	var times []int64
	if err := json.Unmarshal(frame.Data.Values[0], &times); err != nil {
		return nil, fmt.Errorf("decode state-history timestamps: %w", err)
	}
	var lines []json.RawMessage
	if err := json.Unmarshal(frame.Data.Values[1], &lines); err != nil {
		return nil, fmt.Errorf("decode state-history lines: %w", err)
	}
	n := len(times)
	if len(lines) < n {
		n = len(lines)
	}
	if n > maxTransitions {
		return nil, ErrTransitionCapExceeded
	}

	out := make([]firing.RawFiring, 0, n)
	for i := 0; i < n; i++ {
		var lf lineFields
		if err := json.Unmarshal(lines[i], &lf); err != nil {
			continue // a malformed line carries no evidence value
		}
		uid := strings.TrimSpace(lf.RuleUID)
		if uid == "" || times[i] <= 0 {
			continue
		}
		handle, kind := receiverFromLabels(lf.Labels)
		out = append(out, firing.RawFiring{
			RuleID:       uid,
			State:        lf.Current,
			FiredAt:      time.UnixMilli(times[i]).UTC(),
			TargetHandle: handle,
			TargetKind:   kind,
		})
	}
	return out, nil
}

// receiverFromLabels returns the routing handle + kind from the alert instance
// labels, reading ONLY the contact-point / receiver label. It never returns a
// label that looks like a metric value or a recipient email (firing.Collect
// drops an email-shaped handle as a final guard).
func receiverFromLabels(labels map[string]string) (string, string) {
	if labels == nil {
		return "", ""
	}
	for _, key := range []string{"__contact_point__", "contact_point", "receiver"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v, "contact_point"
		}
	}
	return "", ""
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
