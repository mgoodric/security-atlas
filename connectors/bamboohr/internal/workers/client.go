package workers

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
)

// Client is a thin read-only HTTP client for the BambooHR custom-report API. It
// issues only GET requests against the custom-report endpoint with an explicit
// minimal `fields` selector so the API returns ONLY the worker-lifecycle fields
// — never compensation, SSN, bank, home address, benefits, or performance data.
// It deliberately does NOT use the /employees/directory endpoint (whose field
// set is fixed and cannot be scoped down) nor the full-employee endpoint; the
// custom report with an explicit field list is the request-side over-collection
// guard (P0-491-3). The API key is sent as the HTTP Basic username and is never
// logged. No BambooHR Go SDK dependency — mirrors the slice-486/487/488/490
// thin-HTTP pattern.
type Client struct {
	HTTP          *http.Client
	BaseURL       string
	companyDomain string
	apiKey        string
}

// NewClient builds a BambooHR custom-report client. apiKey is the read-scoped
// BambooHR API key (from bamboohrauth.Credential); companyDomain is the company
// subdomain; baseURL is the API base.
func NewClient(httpClient *http.Client, baseURL, companyDomain, apiKey string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		HTTP:          httpClient,
		BaseURL:       strings.TrimRight(baseURL, "/"),
		companyDomain: companyDomain,
		apiKey:        apiKey,
	}
}

// LifecycleFields is the EXACT set of BambooHR report fields the connector
// requests. Compensation ("payRate", "payType"), SSN ("ssn"), home address
// ("homeEmail", "address1", "city"), bank, benefits, and performance fields are
// deliberately absent (P0-491-3 / threat-model I): the connector never asks for
// them, so the report never returns them.
//
// BambooHR field ids: id, status (Active/Inactive), hireDate, terminationDate,
// jobTitle, department, supervisorEid (manager's employee id), workEmail.
var LifecycleFields = []string{
	"id", "status", "hireDate", "terminationDate", "jobTitle", "department", "supervisorEid", "workEmail",
}

// apiReport is the minimal BambooHR custom-report JSON shape — worker-lifecycle
// fields only. Every field not listed here (payRate, ssn, address, etc.) is
// absent: json.Decode discards JSON keys with no matching struct field, so they
// never enter memory as connector data even if a misconfigured key returned them.
type apiReport struct {
	Employees []apiEmployee `json:"employees"`
}

type apiEmployee struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	HireDate        string `json:"hireDate"`
	TerminationDate string `json:"terminationDate"`
	JobTitle        string `json:"jobTitle"`
	Department      string `json:"department"`
	// SupervisorEid is the OPAQUE employee id of the worker's manager only.
	SupervisorEid string `json:"supervisorEid"`
	// WorkEmail is the work email only — the access-review join key. NEVER a
	// personal / home email.
	WorkEmail string `json:"workEmail"`
}

// ListWorkers reads the worker directory via a custom report scoped to the
// minimal lifecycle `fields`. Read-only: a single GET.
func (c *Client) ListWorkers(ctx context.Context) ([]RawWorker, error) {
	q := url.Values{}
	q.Set("format", "JSON")
	q.Set("fields", strings.Join(LifecycleFields, ","))
	q.Set("onlyCurrent", "false") // include terminated workers — the leaver signal
	path := "/api/gateway.php/" + url.PathEscape(c.companyDomain) + "/v1/reports/custom?" + q.Encode()
	var report apiReport
	if err := c.getJSON(ctx, path, &report); err != nil {
		return nil, err
	}
	out := make([]RawWorker, 0, len(report.Employees))
	for _, e := range report.Employees {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			continue
		}
		out = append(out, RawWorker{
			ID:                  id,
			Status:              e.Status,
			HireDate:            e.HireDate,
			TerminationDate:     e.TerminationDate,
			JobTitle:            e.JobTitle,
			Department:          e.Department,
			ManagerAssignmentID: e.SupervisorEid,
			WorkEmail:           e.WorkEmail,
		})
	}
	return out, nil
}

// GetWorker re-reads ONE worker's minimal lifecycle fields by id, for the
// event-driven (subscribe) profile (slice 573). It uses the per-employee
// endpoint (GET /v1/employees/{id}?fields=...) with the SAME minimal `fields`
// over-collection guard as the custom report: only the lifecycle fields are
// requested, so the excluded PII never returns. ok=false on 404 (unknown id).
//
// The single-employee endpoint returns flat string fields keyed by the same
// field ids; it does not wrap them in an "employees" array.
func (c *Client) GetWorker(ctx context.Context, workerID string) (RawWorker, bool, error) {
	id := strings.TrimSpace(workerID)
	if id == "" {
		return RawWorker{}, false, nil
	}
	q := url.Values{}
	q.Set("fields", strings.Join(LifecycleFields, ","))
	path := "/api/gateway.php/" + url.PathEscape(c.companyDomain) + "/v1/employees/" + url.PathEscape(id) + "?" + q.Encode()
	var e apiEmployee
	if err := c.getJSON(ctx, path, &e); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return RawWorker{}, false, nil
		}
		return RawWorker{}, false, err
	}
	if strings.TrimSpace(e.ID) == "" {
		// BambooHR's per-employee endpoint omits "id" from the field response;
		// fall back to the requested id so the record carries a stable key.
		e.ID = id
	}
	return RawWorker{
		ID:                  e.ID,
		Status:              e.Status,
		HireDate:            e.HireDate,
		TerminationDate:     e.TerminationDate,
		JobTitle:            e.JobTitle,
		Department:          e.Department,
		ManagerAssignmentID: e.SupervisorEid,
		WorkEmail:           e.WorkEmail,
	}, true, nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	// BambooHR auth: API key as the HTTP Basic username, any non-empty password.
	req.SetBasicAuth(c.apiKey, "x")
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

// APIError carries BambooHR REST error context. The body is bounded; BambooHR
// error bodies do not echo the request credential.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("bamboohr: HTTP %d", e.Status)
	}
	return fmt.Sprintf("bamboohr: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
