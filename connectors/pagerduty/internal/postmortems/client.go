package postmortems

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdhttp"
)

// Client is the read-only PagerDuty postmortems client. It issues only GETs
// (requires a read-only token) over a bounded since/until window and decodes
// ONLY each postmortem's id, the linked incident id, the review status, the
// created / published timestamps, and — for each corrective-action item — its
// completion state. The postmortem's narrative body / timeline / root-cause
// prose and every action-item title/description is NEVER decoded (P0-538): the
// decode struct has no field for it, and json.Decode discards JSON keys with no
// matching struct field, so the narrative never enters memory as connector
// data.
type Client struct {
	t *pdhttp.Transport
}

// NewClient builds a postmortems client. token is the read-only PagerDuty
// token; baseURL is the REST API base. No new credential scope beyond the
// slice-489 read-only token is required — postmortems are readable with the
// same read-only REST API key.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{t: pdhttp.New(httpClient, baseURL, token)}
}

// pageLimit bounds a single page; MaxRecords (the run cap) bounds total pages.
const pageLimit = 100

// maxPages bounds the page loop so a pathological / paginated source cannot make
// the run unbounded (threat-model D). pageLimit*maxPages comfortably exceeds the
// MaxRecords run cap, which is the real stop condition.
const maxPages = 25

// apiPostmortems is the minimal PagerDuty postmortem list shape — METADATA
// fields only. The postmortem `body` / `narrative` / `timeline` /
// `root_cause` / `notes` and each action item's `title` / `description` are
// intentionally ABSENT from this struct: json.Decode discards JSON keys with no
// matching struct field, so they never enter memory as connector data (P0-538).
type apiPostmortems struct {
	Postmortems []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		// PublishedAt is present once the review is published.
		PublishedAt string `json:"published_at"`
		// Incident links the review to its incident; only the opaque id is read.
		Incident struct {
			ID string `json:"id"`
		} `json:"incident"`
		// ActionItems carry ONLY a completion-state field here. The title /
		// description (operator free-text) is deliberately not in the struct.
		ActionItems []struct {
			Status string `json:"status"`
		} `json:"action_items"`
	} `json:"postmortems"`
	More   bool `json:"more"`
	Limit  int  `json:"limit"`
	Offset int  `json:"offset"`
}

// ListPostmortems reads bounded pages of postmortem metadata in [since, until],
// stopping at the MaxRecords run cap or maxPages, whichever comes first.
// Read-only: GETs against /postmortems only.
func (c *Client) ListPostmortems(ctx context.Context, since, until time.Time) ([]RawPostmortem, error) {
	out := make([]RawPostmortem, 0, pageLimit)
	offset := 0
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("since", since.UTC().Format(time.RFC3339))
		q.Set("until", until.UTC().Format(time.RFC3339))
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))

		var resp apiPostmortems
		if err := c.t.GetJSON(ctx, "/postmortems?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		for _, in := range resp.Postmortems {
			if strings.TrimSpace(in.ID) == "" {
				continue
			}
			items := make([]RawActionItem, 0, len(in.ActionItems))
			for _, ai := range in.ActionItems {
				items = append(items, RawActionItem{Completed: isActionItemDone(ai.Status)})
			}
			out = append(out, RawPostmortem{
				ID:          in.ID,
				IncidentID:  in.Incident.ID,
				Status:      in.Status,
				CreatedAt:   parseTime(in.CreatedAt),
				PublishedAt: parseTime(in.PublishedAt),
				ActionItems: items,
			})
			if len(out) >= MaxRecords {
				return out, nil
			}
		}
		if !resp.More || len(resp.Postmortems) == 0 {
			break
		}
		offset += pageLimit
	}
	return out, nil
}

// isActionItemDone collapses a PagerDuty action-item status into a completion
// boolean. Only the completion FACT is read; the item title/description is never
// decoded.
func isActionItemDone(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "done", "resolved", "closed":
		return true
	default:
		return false
	}
}

// parseTime parses an RFC3339 timestamp, returning the zero time on empty/bad
// input (e.g. published_at is absent for an unpublished review).
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
