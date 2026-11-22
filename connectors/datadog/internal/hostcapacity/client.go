package hostcapacity

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

// Client is a thin read-only HTTP client for Datadog's metrics query API
// (GET /api/v1/query, documented to require the timeseries_query
// permission). It holds the API + Application keys and never logs them.
// It mirrors the other Datadog surfaces' thin-HTTP pattern rather than the
// Datadog Go SDK.
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.datadoghq.com
	apiKey  string
	appKey  string
}

// NewClient builds a metrics query client.
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

// maxBody bounds one response. 5,000 hosts x 20 points is far below it.
const maxBody = 32 << 20

// apiResponse is the documented /api/v1/query response, the fields read.
// series[].pointlist is "[[ms, value], ...]" with a null value for a gap.
type apiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Series []struct {
		Scope     string       `json:"scope"`
		Pointlist [][]*float64 `json:"pointlist"`
	} `json:"series"`
}

// Query runs one metrics query over [from, to].
func (c *Client) Query(ctx context.Context, from, to time.Time, query string) ([]Series, error) {
	v := url.Values{}
	v.Set("from", strconv.FormatInt(from.Unix(), 10))
	v.Set("to", strconv.FormatInt(to.Unix(), 10))
	v.Set("query", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/query?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("DD-API-KEY", c.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", c.appKey)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<13))
		return nil, &APIError{Status: res.StatusCode, Body: string(b)}
	}
	var body apiResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode /api/v1/query: %w", err)
	}
	if body.Status != "" && body.Status != "ok" {
		return nil, fmt.Errorf("datadog query status %q: %s", body.Status, body.Error)
	}
	out := make([]Series, 0, len(body.Series))
	for _, s := range body.Series {
		ser := Series{Tags: parseScope(s.Scope)}
		for _, p := range s.Pointlist {
			if len(p) != 2 || p[0] == nil {
				continue
			}
			ser.Points = append(ser.Points, Point{
				At:    time.UnixMilli(int64(*p[0])).UTC(),
				Value: p[1],
			})
		}
		out = append(out, ser)
	}
	return out, nil
}

// APIError carries a non-200 response. Datadog error bodies do not echo the
// request keys.
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
