package incidents

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdhttp"
)

// Client is the read-only PagerDuty incidents client. It issues only a GET
// against /incidents (requires a read-only token) over a bounded since/until
// window and decodes ONLY each incident's id/number/status/urgency, the
// affected service's id/summary, and the created/resolved timestamps. The
// incident title/body/notes/postmortem free-text is NEVER decoded (P0-489-3).
type Client struct {
	t *pdhttp.Transport
}

// NewClient builds an incidents client. token is the read-only PagerDuty
// token; baseURL is the REST API base.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{t: pdhttp.New(httpClient, baseURL, token)}
}

const pageLimit = 100

// apiIncidents is the minimal PagerDuty incident list shape — summary fields
// only. The incident `title` / `description` / `body` / notes and every other
// free-text field are intentionally absent: json.Decode discards JSON keys with
// no matching struct field, so they never enter memory as connector data.
type apiIncidents struct {
	Incidents []struct {
		ID             string `json:"id"`
		IncidentNumber int    `json:"incident_number"`
		Status         string `json:"status"`
		Urgency        string `json:"urgency"`
		CreatedAt      string `json:"created_at"`
		ResolvedAt     string `json:"resolved_at"`
		Service        struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"service"`
	} `json:"incidents"`
}

// ListIncidents reads the first bounded page of incidents in [since, until].
// Read-only: a single GET against /incidents.
func (c *Client) ListIncidents(ctx context.Context, since, until time.Time) ([]RawIncident, error) {
	q := url.Values{}
	q.Set("since", since.UTC().Format(time.RFC3339))
	q.Set("until", until.UTC().Format(time.RFC3339))
	q.Set("limit", strconv.Itoa(pageLimit))
	q.Set("offset", "0")
	// statuses[] omitted: collect all lifecycle states so resolved + open both appear.

	var resp apiIncidents
	if err := c.t.GetJSON(ctx, "/incidents?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	out := make([]RawIncident, 0, len(resp.Incidents))
	for _, in := range resp.Incidents {
		if strings.TrimSpace(in.ID) == "" {
			continue
		}
		out = append(out, RawIncident{
			ID:          in.ID,
			Number:      in.IncidentNumber,
			Status:      in.Status,
			Urgency:     in.Urgency,
			ServiceID:   in.Service.ID,
			ServiceName: in.Service.Summary,
			CreatedAt:   parseTime(in.CreatedAt),
			ResolvedAt:  parseTime(in.ResolvedAt),
		})
	}
	return out, nil
}

// parseTime parses an RFC3339 timestamp, returning the zero time on empty/bad
// input (e.g. resolved_at is absent for an open incident).
func parseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
