package metrics

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdhttp"
)

// Client is the read-only PagerDuty incident-timing client. It issues only GETs
// (requires a read-only token) over a bounded since/until window and decodes
// ONLY each incident's service id, created timestamp, acknowledgment
// timestamps, and resolved timestamp. The acknowledgment's `acknowledger` (the
// responder identity), the assignee, the incident title/body/notes, and every
// other free-text or responder-identity field is NEVER decoded (P0-539): the
// decode struct has no field for it, and json.Decode discards JSON keys with no
// matching struct field, so the responder identity never enters memory as
// connector data.
type Client struct {
	t *pdhttp.Transport
}

// NewClient builds an incident-timing client. token is the read-only PagerDuty
// token; baseURL is the REST API base. No new credential scope beyond the
// slice-489 read-only token is required — incident timings are readable with
// the same read-only REST API key.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{t: pdhttp.New(httpClient, baseURL, token)}
}

// pageLimit bounds a single page; the metrics run cap (MaxServices distinct
// services) and maxPages bound the total work.
const pageLimit = 100

// maxPages bounds the page loop so a pathological / paginated source cannot make
// the run unbounded (threat-model D).
const maxPages = 50

// apiIncidentTimings is the minimal PagerDuty incident list shape — TIMING
// fields only. The incident `title` / `description` / `body` / notes, and —
// load-bearingly — each acknowledgment's `acknowledger` and the incident's
// `assignments` / `assignees` (the responder identities) are intentionally
// ABSENT from this struct: json.Decode discards JSON keys with no matching
// struct field, so they never enter memory as connector data (P0-539). Only the
// acknowledgment `at` timestamp is read, never WHO acknowledged.
type apiIncidentTimings struct {
	Incidents []struct {
		ID         string `json:"id"`
		CreatedAt  string `json:"created_at"`
		ResolvedAt string `json:"resolved_at"`
		Service    struct {
			ID string `json:"id"`
		} `json:"service"`
		// Acknowledgments carry ONLY the `at` timestamp here. The
		// `acknowledger` (responder identity) is deliberately not in the struct.
		Acknowledgments []struct {
			At string `json:"at"`
		} `json:"acknowledgments"`
	} `json:"incidents"`
	More   bool `json:"more"`
	Limit  int  `json:"limit"`
	Offset int  `json:"offset"`
}

// ListIncidentTimings reads bounded pages of incident timings in [since, until],
// stopping at maxPages. Read-only: GETs against /incidents only. Returns one
// RawTiming per incident, identity-free.
func (c *Client) ListIncidentTimings(ctx context.Context, since, until time.Time) ([]RawTiming, error) {
	out := make([]RawTiming, 0, pageLimit)
	offset := 0
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("since", since.UTC().Format(time.RFC3339))
		q.Set("until", until.UTC().Format(time.RFC3339))
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))
		// statuses[] omitted: collect all lifecycle states so resolved + open
		// both contribute (open incidents still contribute to MTTA).

		var resp apiIncidentTimings
		if err := c.t.GetJSON(ctx, "/incidents?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		for _, in := range resp.Incidents {
			if strings.TrimSpace(in.ID) == "" {
				continue
			}
			acks := make([]RawAck, 0, len(in.Acknowledgments))
			for _, a := range in.Acknowledgments {
				acks = append(acks, RawAck{At: parseTime(a.At)})
			}
			out = append(out, RawTiming{
				ServiceID:  in.Service.ID,
				CreatedAt:  parseTime(in.CreatedAt),
				Acks:       acks,
				ResolvedAt: parseTime(in.ResolvedAt),
			})
		}
		if !resp.More || len(resp.Incidents) == 0 {
			break
		}
		offset += pageLimit
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
