// Package jiratickets pulls Jira issues via the REST v3 search endpoint
// and surfaces them as canonical Ticket records the cmd layer can turn
// into jira.ticket_evidence.v1 evidence.
//
// The package is built around a narrow API interface so tests can swap
// in an httptest.NewServer-backed transport without pulling in the
// google/go-jira SDK (which we deliberately avoid to keep the connector's
// go.sum lean).
//
// Slice 048 is read-only: no transition, comment, or update calls live in
// this package. Anti-criterion P0: the connector never mutates Jira.
package jiratickets

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

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
)

// Ticket is the canonical record one Jira issue turns into. Fields map
// 1:1 to jira.ticket_evidence.v1 schema (additionalProperties: false).
type Ticket struct {
	TicketKey  string
	ProjectKey string
	Summary    string
	Status     string
	Resolution string
	Assignee   string
	URL        string
	ObservedAt time.Time
}

// API is the narrow surface needed by List; the concrete Client
// satisfies it.
type API interface {
	Search(ctx context.Context, jql string, startAt, maxResults int) (*SearchResponse, error)
	BrowseURL(ticketKey string) string
}

// ListOpts steers the pull. JQL is required and bounds which issues
// land on the ledger (operators target change-management or
// incident-response projects per the canvas).
type ListOpts struct {
	JQL  string
	Now  func() time.Time
	Page int // first page; defaults to 0
	// MaxResults caps page size. Defaults to 100 (Jira max). Tests can
	// shrink this to exercise pagination.
	MaxResults int
}

// List pulls every issue matching JQL and returns canonical Tickets.
// Pagination is followed transparently up to a hard cap of 10 pages
// (1,000 tickets per run) to bound a malicious / runaway JQL.
func List(ctx context.Context, api API, opts ListOpts) ([]Ticket, error) {
	if api == nil {
		return nil, errors.New("jiratickets: API is nil")
	}
	if strings.TrimSpace(opts.JQL) == "" {
		return nil, errors.New("jiratickets: JQL is required")
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.MaxResults == 0 {
		opts.MaxResults = 100
	}
	const maxPages = 10
	out := make([]Ticket, 0, opts.MaxResults)
	page := opts.Page
	for i := 0; i < maxPages; i++ {
		resp, err := api.Search(ctx, opts.JQL, page*opts.MaxResults, opts.MaxResults)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
		for _, raw := range resp.Issues {
			t := Ticket{
				TicketKey:  raw.Key,
				ProjectKey: raw.Fields.Project.Key,
				Summary:    raw.Fields.Summary,
				URL:        api.BrowseURL(raw.Key),
				ObservedAt: opts.Now(),
			}
			if raw.Fields.Status != nil {
				t.Status = raw.Fields.Status.Name
			}
			if raw.Fields.Resolution != nil {
				t.Resolution = raw.Fields.Resolution.Name
			}
			if raw.Fields.Assignee != nil {
				t.Assignee = raw.Fields.Assignee.DisplayName
			}
			out = append(out, t)
		}
		fetched := (page + 1) * opts.MaxResults
		if resp.Total <= fetched || len(resp.Issues) == 0 {
			break
		}
		page++
	}
	return out, nil
}

// ---- REST client ----

// SearchResponse is the subset of /rest/api/3/search we decode.
type SearchResponse struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Issues     []searchHit `json:"issues"`
}

type searchHit struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  *struct {
			Name string `json:"name"`
		} `json:"status,omitempty"`
		Resolution *struct {
			Name string `json:"name"`
		} `json:"resolution,omitempty"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee,omitempty"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"fields"`
}

// Client is a thin wrapper around http.Client carrying Jira auth +
// JSON decoding. Tests construct one against an httptest server.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   jiraauth.Credential
}

// NewClient builds a Client against a Jira Cloud REST endpoint. baseURL
// is the tenant base (e.g. "https://acme.atlassian.net"), without the
// /rest/api/3 prefix — Client appends it.
func NewClient(httpClient *http.Client, baseURL string, creds jiraauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// BrowseURL returns the operator-facing URL for a ticket key. Jira
// conventionally serves "<base>/browse/<KEY>".
func (c *Client) BrowseURL(ticketKey string) string {
	return c.BaseURL + "/browse/" + url.PathEscape(ticketKey)
}

// Search calls /rest/api/3/search. v1 uses POST (Jira recommends POST
// for JQL bodies > 1 KiB; GET works under the limit but the spec is
// converging on POST). Returns the parsed envelope.
func (c *Client) Search(ctx context.Context, jql string, startAt, maxResults int) (*SearchResponse, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("startAt", fmt.Sprintf("%d", startAt))
	q.Set("maxResults", fmt.Sprintf("%d", maxResults))
	q.Set("fields", "summary,status,resolution,assignee,project")
	u := c.BaseURL + "/rest/api/3/search?" + q.Encode()
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
	var sr SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return &sr, nil
}

// APIError carries Jira REST error context. The Body is bounded.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("jira: HTTP %d", e.Status)
	}
	return fmt.Sprintf("jira: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13 // 8 KiB cap on captured error bodies
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
