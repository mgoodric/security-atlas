// Package confluenceapi reads policy pages from Confluence Cloud: every
// page carrying one label within one space, with its storage-format body
// (#2856).
//
// The read is all or nothing. LabelledPages returns either every page of
// the set or an error, never a partial set, because the platform reads a
// page missing from a sync as a page that lost its label (#2853).
//
// Read-only: the package only issues GET requests. The credential (an
// Atlassian account email and API token, HTTP Basic) stays in this
// process and is sent only to the configured site.
package confluenceapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Env vars for the Confluence credential. Env is preferred to flags so
// the token never appears in shell history or a process listing.
const (
	EnvEmail = "CONFLUENCE_EMAIL"
	EnvToken = "CONFLUENCE_API_TOKEN"
)

// Credential is an Atlassian email and API token, sent as HTTP Basic.
type Credential struct {
	secret   string // base64("email:token"); never logged
	emailLen int
}

// String never reveals the email or the token.
func (c Credential) String() string {
	return fmt.Sprintf("confluenceapi.Credential{email: <redacted %d bytes>, token: <redacted>}", c.emailLen)
}

// ResolveCredential builds the credential from the given values, falling
// back to CONFLUENCE_EMAIL and CONFLUENCE_API_TOKEN.
func ResolveCredential(email, token string) (Credential, error) {
	email = strings.TrimSpace(firstNonEmpty(email, os.Getenv(EnvEmail)))
	token = strings.TrimSpace(firstNonEmpty(token, os.Getenv(EnvToken)))
	if email == "" {
		return Credential{}, fmt.Errorf("confluence email required (set %s or pass --confluence-email)", EnvEmail)
	}
	if token == "" {
		return Credential{}, fmt.Errorf("confluence API token required (set %s or pass --confluence-token)", EnvToken)
	}
	return Credential{secret: base64.StdEncoding.EncodeToString([]byte(email + ":" + token)), emailLen: len(email)}, nil
}

// CheckBaseURL parses a site or platform base URL. It must be https, or
// http on a loopback host (a local platform, or a test server), so a
// credential is never sent in the clear across a network.
func CheckBaseURL(flag, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL, got %q", flag, raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("%s must not carry a query, fragment or user info", flag)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("%s must be https (http is accepted for loopback hosts only)", flag)
		}
	default:
		return nil, fmt.Errorf("%s must be https, got scheme %q", flag, u.Scheme)
	}
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Page is one labelled page.
type Page struct {
	ID    string
	Title string
	// URL is the page's web URL, the policy's source link.
	URL string
	// Storage is the body in storage format.
	Storage string
}

// Defaults for Client.
const (
	// DefaultPageSize is the number of results asked for per request.
	DefaultPageSize = 50
	// DefaultMaxPages is the most pages one sync may carry: the sync
	// endpoint refuses a push of more than 1000.
	DefaultMaxPages = 1000
)

// Client reads one site's pages.
type Client struct {
	HTTP *http.Client
	// Site is the site's base URL, e.g. https://acme.atlassian.net.
	Site *url.URL
	Cred Credential
	// PageSize and MaxPages default to DefaultPageSize and
	// DefaultMaxPages.
	PageSize int
	MaxPages int
}

// ErrTooManyPages is returned when the label selects more pages than one
// sync may carry. Nothing is returned with it: a truncated set would
// read as pages going missing.
var ErrTooManyPages = errors.New("confluenceapi: too many labelled pages for one sync")

// searchResponse is the subset of the CQL content search response used.
type searchResponse struct {
	Results []struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		Title string `json:"title"`
		Body  struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	} `json:"results"`
	Links struct {
		Base    string `json:"base"`
		Context string `json:"context"`
		Next    string `json:"next"`
	} `json:"_links"`
}

// CQL builds the query selecting the pages labelled label in space.
func CQL(space, label string) (string, error) {
	for name, v := range map[string]string{"space": space, "label": label} {
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		if strings.ContainsAny(v, "\"\\\r\n") {
			return "", fmt.Errorf("%s must not contain quotes, backslashes or newlines", name)
		}
	}
	return fmt.Sprintf(`type = page AND space = "%s" AND label = "%s" ORDER BY id`,
		strings.TrimSpace(space), strings.TrimSpace(label)), nil
}

// LabelledPages returns every page labelled label in space, following the
// search's next links until there are none. Any failed request, an
// unreadable response or a set larger than MaxPages is an error, and no
// pages are returned with it.
func (c *Client) LabelledPages(ctx context.Context, space, label string) ([]Page, error) {
	if c.HTTP == nil || c.Site == nil {
		return nil, errors.New("confluenceapi: client needs HTTP and Site")
	}
	cql, err := CQL(space, label)
	if err != nil {
		return nil, err
	}
	pageSize, maxPages := c.PageSize, c.MaxPages
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	q := url.Values{}
	q.Set("cql", cql)
	q.Set("expand", "body.storage")
	q.Set("limit", strconv.Itoa(pageSize))
	next := c.Site.JoinPath("wiki", "rest", "api", "content", "search")
	next.RawQuery = q.Encode()

	var out []Page
	seen := map[string]bool{}
	// Each request returns at least one new page or ends the loop, so
	// maxPages+1 requests is a bound that a looping next link cannot pass.
	for req := 0; next != nil; req++ {
		if req > maxPages {
			return nil, errors.New("confluenceapi: search did not finish (next link loops)")
		}
		resp, err := c.get(ctx, next)
		if err != nil {
			return nil, err
		}
		fresh := 0
		for _, r := range resp.Results {
			if r.Type != "" && r.Type != "page" {
				continue
			}
			if r.ID == "" || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			fresh++
			out = append(out, Page{ID: r.ID, Title: r.Title, URL: c.pageURL(resp.Links.Base, r.Links.WebUI, r.ID), Storage: r.Body.Storage.Value})
			if len(out) > maxPages {
				return nil, fmt.Errorf("%w: more than %d", ErrTooManyPages, maxPages)
			}
		}
		if resp.Links.Next == "" {
			break
		}
		if fresh == 0 {
			return nil, errors.New("confluenceapi: search returned a next link with no new pages")
		}
		if next, err = c.resolveNext(resp.Links.Context, resp.Links.Next); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// resolveNext turns a next link into a URL on the configured site. Only
// its path and query are used, so a response can never redirect the
// credential to another host.
func (c *Client) resolveNext(ctxPath, next string) (*url.URL, error) {
	n, err := url.Parse(next)
	if err != nil {
		return nil, fmt.Errorf("confluenceapi: unreadable next link: %w", err)
	}
	if n.Host != "" && !strings.EqualFold(n.Host, c.Site.Host) {
		return nil, fmt.Errorf("confluenceapi: next link points at another host %q", n.Host)
	}
	path := n.Path
	if ctxPath == "" {
		ctxPath = "/wiki"
	}
	if n.Host == "" && !strings.HasPrefix(path, ctxPath+"/") {
		path = ctxPath + path
	}
	u := *c.Site
	u.Path = strings.TrimRight(c.Site.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = n.RawQuery
	return &u, nil
}

// pageURL is the page's web URL: the response's base plus the result's
// webui link when both are on the site, else the site's viewpage URL.
func (c *Client) pageURL(base, webui, id string) string {
	if b, err := url.Parse(base); err == nil && base != "" && webui != "" &&
		strings.EqualFold(b.Host, c.Site.Host) && b.Scheme == c.Site.Scheme && strings.HasPrefix(webui, "/") {
		return strings.TrimRight(base, "/") + webui
	}
	u := c.Site.JoinPath("wiki", "pages", "viewpage.action")
	u.RawQuery = url.Values{"pageId": {id}}.Encode()
	return u.String()
}

// maxErrorBody is how much of an error response is quoted in the error.
const maxErrorBody = 512

func (c *Client) get(ctx context.Context, u *url.URL) (*searchResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("confluenceapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+c.Cred.secret)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("confluenceapi: search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("confluenceapi: search: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("confluenceapi: decode search response: %w", err)
	}
	return &out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
