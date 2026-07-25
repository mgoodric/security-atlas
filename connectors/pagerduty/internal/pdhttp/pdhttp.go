// Package pdhttp is a thin read-only HTTP transport for the PagerDuty REST API,
// shared by the oncall + incidents collectors. It holds the read-only API
// token (never logged) and issues only GET requests with the PagerDuty
// "Authorization: Token token=<token>" header. It deliberately does NOT depend
// on a PagerDuty Go SDK — the connector mirrors the slice 486/487/488 thin-HTTP
// pattern to keep the dependency tree small.
package pdhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Transport issues read-only GETs against the PagerDuty REST API.
type Transport struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// New builds a transport. token is the read-only PagerDuty token (from
// pagerdutyauth.Credential); baseURL is the REST API base.
func New(httpClient *http.Client, baseURL, token string) *Transport {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Transport{
		HTTP:    httpClient,
		BaseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// GetJSON issues a single GET against path and decodes the JSON body into
// `into`. path must begin with "/".
func (t *Transport) GetJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL+path, nil)
	if err != nil {
		return err
	}
	t.applyAuth(req)
	res, err := t.HTTP.Do(req)
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

func (t *Transport) applyAuth(req *http.Request) {
	if t.token != "" {
		// PagerDuty REST auth header form.
		req.Header.Set("Authorization", "Token token="+t.token)
	}
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
}

// APIError carries PagerDuty REST error context. The body is bounded; PagerDuty
// error bodies do not echo the request credentials.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "pagerduty: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("pagerduty: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
