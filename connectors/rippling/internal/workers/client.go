package workers

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

// Client is a thin read-only HTTP client for the Rippling employee-directory
// API. It issues only GET requests against the worker-directory endpoint with an
// explicit minimal `fields` selector so the API returns ONLY the
// worker-lifecycle fields — never compensation, SSN, bank, address, benefits, or
// performance data. The API token is never logged. It deliberately does NOT
// depend on a Rippling Go SDK — the connector mirrors the
// slice-486/487/488/490 thin-HTTP pattern to keep the dependency tree small.
//
// The `fields` query is the request-side over-collection guard (P0-491-3): the
// connector asks Rippling for only the lifecycle field set, so the sensitive PII
// is never returned over the wire, and apiEmployee has no field to decode it
// into even if it were.
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	apiToken string
}

// NewClient builds a Rippling employee-directory client. apiToken is the
// read-scoped Rippling API token (from ripplingauth.Credential); baseURL is the
// API base.
func NewClient(httpClient *http.Client, baseURL, apiToken string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		HTTP:     httpClient,
		BaseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
	}
}

const pageLimit = 200

// LifecycleFields is the EXACT set of employee fields the connector requests
// from Rippling. Compensation, SSN, home address, bank details, benefits, and
// performance fields are deliberately absent (P0-491-3 / threat-model I): the
// connector never asks for them, so the API never returns them.
var LifecycleFields = []string{
	"id", "employmentStatus", "startDate", "endDate", "title", "department", "manager", "workEmail",
}

// apiEmployeePage is the minimal Rippling employee-directory JSON shape —
// worker-lifecycle fields only. Every field not listed here (compensation, SSN,
// home address, bank, benefits, performance, DOB, personal phone) is absent:
// json.Decode discards JSON keys with no matching struct field, so they never
// enter memory as connector data even if a misconfigured token returned them.
type apiEmployeePage struct {
	Results []apiEmployee `json:"results"`
}

type apiEmployee struct {
	ID               string `json:"id"`
	EmploymentStatus string `json:"employmentStatus"`
	StartDate        string `json:"startDate"`
	EndDate          string `json:"endDate"`
	Title            string `json:"title"`
	Department       string `json:"department"`
	// Manager is the OPAQUE manager worker id only.
	Manager string `json:"manager"`
	// WorkEmail is the work email only — the access-review join key. NEVER a
	// personal email.
	WorkEmail string `json:"workEmail"`
}

// ListWorkers reads the first bounded page of the employee directory. Read-only:
// a single GET against the worker-directory endpoint requesting only the minimal
// lifecycle `fields`.
func (c *Client) ListWorkers(ctx context.Context) ([]RawWorker, error) {
	q := url.Values{}
	q.Set("fields", strings.Join(LifecycleFields, ","))
	q.Set("limit", strconv.Itoa(pageLimit))
	var page apiEmployeePage
	if err := c.getJSON(ctx, "/platform/api/employees?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawWorker, 0, len(page.Results))
	for _, e := range page.Results {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			continue
		}
		out = append(out, RawWorker{
			ID:                  id,
			EmploymentStatus:    e.EmploymentStatus,
			StartDate:           e.StartDate,
			EndDate:             e.EndDate,
			Title:               e.Title,
			Department:          e.Department,
			ManagerAssignmentID: e.Manager,
			WorkEmail:           e.WorkEmail,
		})
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Accept", "application/json")
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

// APIError carries Rippling REST error context. The body is bounded; Rippling
// error bodies do not echo the request credential.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "rippling: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("rippling: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
