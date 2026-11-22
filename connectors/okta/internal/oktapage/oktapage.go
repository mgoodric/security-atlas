// Package oktapage reads every page of an Okta list endpoint.
//
// Okta paginates list operations with cursor links: each response carries
// `Link: <url>; rel="self"` and, when more results exist, a second
// `Link: <url>; rel="next"` header whose URL holds the `after` cursor
// (developer.okta.com/docs/api/#pagination). The next URL is opaque and is
// followed as given. A list read that stops at the first page under-reports
// the org (#2915), so every Okta list call in the connector goes through
// List.
//
// A next link must stay on the client's base scheme and host, so the SSWS
// token is never sent elsewhere, and the chain is capped at MaxPages. Both
// rules match the GitHub connector's getPages (#2928).
package oktapage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// MaxPages bounds how many rel="next" pages one listing follows. At
// Okta's common limit of 200 that is 200,000 items.
const MaxPages = 1000

// APIError carries Okta REST error context. The Body is bounded.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("okta: HTTP %d", e.Status)
	}
	return fmt.Sprintf("okta: HTTP %d: %s", e.Status, e.Body)
}

// List GETs baseURL+pathAndQuery and every page its rel="next" chain
// names, decoding each 200 body as a JSON array of T and concatenating
// them in order. A non-200 page returns an *APIError.
func List[T any](ctx context.Context, hc *http.Client, baseURL string, creds oktaauth.Credential, pathAndQuery string) ([]T, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("oktapage: base url: %w", err)
	}
	u := baseURL + pathAndQuery
	var out []T
	for page := 0; u != ""; page++ {
		if page >= MaxPages {
			return nil, fmt.Errorf("oktapage: more than %d pages at %s", MaxPages, u)
		}
		items, next, err := get[T](ctx, hc, creds, u)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" {
			break
		}
		nu, err := url.Parse(next)
		if err != nil {
			return nil, fmt.Errorf("oktapage: next link %q: %w", next, err)
		}
		if nu.Scheme != base.Scheme || nu.Host != base.Host {
			return nil, fmt.Errorf("oktapage: next link %q leaves %s", next, baseURL)
		}
		u = nu.String()
	}
	return out, nil
}

func get[T any](ctx context.Context, hc *http.Client, creds oktaauth.Credential, u string) ([]T, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	creds.Apply(req)
	res, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", &APIError{Status: res.StatusCode, Body: Drain(res.Body)}
	}
	var items []T
	if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}
	return items, NextLink(res.Header.Values("Link")), nil
}

// NextLink returns the rel="next" target among RFC 8288 Link header
// values, or "". Okta sends self and next as separate Link headers; a
// single comma-joined value is parsed the same way.
func NextLink(headers []string) string {
	for _, h := range headers {
		for _, part := range strings.Split(h, ",") {
			segs := strings.Split(part, ";")
			target := strings.TrimSpace(segs[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for _, p := range segs[1:] {
				k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(k), "rel") {
					continue
				}
				for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(v), `"`)) {
					if rel == "next" {
						return target[1 : len(target)-1]
					}
				}
			}
		}
	}
	return ""
}

// Drain reads at most 8 KiB of r for an error body.
func Drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
