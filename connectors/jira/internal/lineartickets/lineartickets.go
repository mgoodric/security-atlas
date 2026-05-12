// Package lineartickets pulls Linear issues via the GraphQL `issues`
// query and surfaces them as canonical Ticket records the cmd layer
// can turn into jira.ticket_evidence.v1 evidence (Jira and Linear
// share the schema; the source field on the EvidenceRecord envelope
// distinguishes per-record).
//
// Slice 048 is read-only: no mutation queries (issueUpdate / issueCreate
// / commentCreate) appear in this package. Anti-criterion P0.
package lineartickets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
)

// Ticket is the canonical record one Linear issue turns into. Fields
// map 1:1 to jira.ticket_evidence.v1 schema. Linear has no concept of
// "resolution" distinct from state, so Resolution stays empty.
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

// Filter narrows the Linear query. v1 supports team-key filtering;
// further filters (state, label) land in slice 049+.
type Filter struct {
	TeamKey string
}

// ListOpts steers the pull. Filter.TeamKey is required so the operator
// scopes per team (mirrors Jira's JQL project constraint).
type ListOpts struct {
	Filter Filter
	Now    func() time.Time
}

// API is the narrow surface needed by List.
type API interface {
	Issues(ctx context.Context, filter Filter, after string) (*IssuesResponse, error)
}

// List pulls every issue matching filter (paginating transparently)
// and returns canonical Tickets. Hard cap of 10 pages — protects
// against runaway filter values.
func List(ctx context.Context, api API, opts ListOpts) ([]Ticket, error) {
	if api == nil {
		return nil, errors.New("lineartickets: API is nil")
	}
	if strings.TrimSpace(opts.Filter.TeamKey) == "" {
		return nil, errors.New("lineartickets: Filter.TeamKey is required")
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	const maxPages = 10
	cursor := ""
	out := []Ticket{}
	for i := 0; i < maxPages; i++ {
		resp, err := api.Issues(ctx, opts.Filter, cursor)
		if err != nil {
			return nil, fmt.Errorf("issues: %w", err)
		}
		for _, n := range resp.Data.Issues.Nodes {
			t := Ticket{
				TicketKey:  n.Identifier,
				ProjectKey: n.Team.Key,
				Summary:    n.Title,
				URL:        n.URL,
				ObservedAt: opts.Now(),
			}
			if n.State != nil {
				t.Status = n.State.Name
			}
			if n.Assignee != nil {
				t.Assignee = n.Assignee.Name
			}
			out = append(out, t)
		}
		if !resp.Data.Issues.PageInfo.HasNextPage || resp.Data.Issues.PageInfo.EndCursor == "" {
			break
		}
		cursor = resp.Data.Issues.PageInfo.EndCursor
	}
	return out, nil
}

// ---- GraphQL client ----

// IssuesResponse mirrors the relevant GraphQL response shape.
type IssuesResponse struct {
	Data struct {
		Issues struct {
			Nodes    []issueNode `json:"nodes"`
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"issues"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

type issueNode struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      *struct {
		Name string `json:"name"`
	} `json:"state,omitempty"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee,omitempty"`
	Team struct {
		Key string `json:"key"`
	} `json:"team"`
}

// Client is the GraphQL client.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   jiraauth.Credential
}

// NewClient builds a Client. baseURL defaults to "https://api.linear.app"
// when empty; tests override.
func NewClient(httpClient *http.Client, baseURL string, creds jiraauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.linear.app"
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

const issuesQuery = `query Issues($filter: IssueFilter, $after: String) {
  issues(filter: $filter, first: 100, after: $after) {
    nodes {
      identifier
      title
      url
      state { name }
      assignee { name }
      team { key }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// Issues runs the canonical GraphQL query, optionally continuing from
// `after`.
func (c *Client) Issues(ctx context.Context, filter Filter, after string) (*IssuesResponse, error) {
	vars := map[string]any{
		"filter": map[string]any{
			"team": map[string]any{"key": map[string]any{"eq": filter.TeamKey}},
		},
	}
	if after != "" {
		vars["after"] = after
	}
	body, _ := json.Marshal(map[string]any{
		"query":     issuesQuery,
		"variables": vars,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/graphql", bytes.NewReader(body))
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
	var ir IssuesResponse
	if err := json.NewDecoder(res.Body).Decode(&ir); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(ir.Errors) > 0 {
		// Surface GraphQL errors as a real error so the cmd layer fails
		// fast. v1 only reports the first message.
		return nil, fmt.Errorf("linear graphql error: %s", ir.Errors[0].Message)
	}
	return &ir, nil
}

// APIError carries Linear HTTP error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("linear: HTTP %d", e.Status)
	}
	return fmt.Sprintf("linear: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
