// Package graph reads policy documents from one SharePoint Online
// document library through Microsoft Graph (#2837): every item in the
// library, with its column values and, for a file, its content.
//
// The read is all or nothing. Documents returns either every document of
// the library or an error, never a partial set, because the platform
// reads a document missing from a sync as one that left the source
// (#2853).
//
// Read-only: apart from the token request, the package only issues GET
// requests, and only through documented Graph v1.0 endpoints:
//
//	POST https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token   (client credentials)
//	GET  /sites/{hostname}:/{server-relative-path}                      (the site)
//	GET  /sites/{site-id}/lists                                         (the library)
//	GET  /sites/{site-id}/lists/{list-id}/items?expand=fields            (items and column values)
//	GET  /sites/{site-id}/lists/{list-id}/items/{item-id}/driveItem      (the file)
//
// and the file's @microsoft.graph.downloadUrl, which Graph documents as
// needing no authentication, so it is fetched without the token.
//
// Credentials and hosts: the client secret (an Entra app registration's)
// stays in this process. The token request goes only to
// login.microsoftonline.com and the bearer token only to
// graph.microsoft.com; a next link or download URL on any other host is
// refused, so a response can never redirect a credential.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Hosts the connector talks to. They are fixed: national clouds are not
// supported (see the decisions log).
const (
	LoginHost = "login.microsoftonline.com"
	GraphHost = "graph.microsoft.com"
	// graphBase is the Graph v1.0 endpoint.
	graphBase = "https://" + GraphHost + "/v1.0"
	// graphScope asks for the app's granted application permissions.
	graphScope = "https://graph.microsoft.com/.default"
	// SharePointHostSuffix is the host suffix of every SharePoint Online
	// site and download URL the connector accepts.
	SharePointHostSuffix = ".sharepoint.com"
)

// Env vars for the credential. The secret is read from the environment
// only, so it never appears in shell history or a process listing.
const (
	EnvTenantID     = "SHAREPOINT_TENANT_ID"
	EnvClientID     = "SHAREPOINT_CLIENT_ID"
	EnvClientSecret = "SHAREPOINT_CLIENT_SECRET"
)

// Credential is an Entra app registration's client credentials.
type Credential struct {
	tenant   string
	clientID string
	secret   string // never logged
}

// String never reveals the secret.
func (c Credential) String() string {
	return fmt.Sprintf("graph.Credential{tenant: %q, client_id: %q, secret: <redacted>}", c.tenant, c.clientID)
}

// tenantPattern is a directory (tenant) id or a verified domain: it goes
// into the token URL's path, so nothing else is accepted.
var tenantPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

// ResolveCredential builds the credential from the given tenant and client
// id, falling back to SHAREPOINT_TENANT_ID and SHAREPOINT_CLIENT_ID, and
// reads SHAREPOINT_CLIENT_SECRET.
func ResolveCredential(tenant, clientID string) (Credential, error) {
	tenant = strings.TrimSpace(firstNonEmpty(tenant, os.Getenv(EnvTenantID)))
	clientID = strings.TrimSpace(firstNonEmpty(clientID, os.Getenv(EnvClientID)))
	secret := strings.TrimSpace(os.Getenv(EnvClientSecret))
	switch {
	case tenant == "":
		return Credential{}, fmt.Errorf("tenant id required (set %s or pass --tenant-id)", EnvTenantID)
	case !tenantPattern.MatchString(tenant):
		return Credential{}, errors.New("tenant id must be a directory id or a domain name")
	case clientID == "":
		return Credential{}, fmt.Errorf("client id required (set %s or pass --client-id)", EnvClientID)
	case secret == "":
		return Credential{}, fmt.Errorf("client secret required (set %s)", EnvClientSecret)
	}
	return Credential{tenant: tenant, clientID: clientID, secret: secret}, nil
}

// CheckSiteURL parses a SharePoint Online site URL,
// https://<tenant>.sharepoint.com/<site path>. The site is never contacted
// directly: its host and path name the site in a Graph request.
func CheckSiteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("--site must be an absolute URL, got %q", raw)
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("--site must be https://<tenant>.sharepoint.com/<site path>, with no port, user info, query or fragment")
	}
	if !IsSharePointHost(u.Hostname()) {
		return nil, fmt.Errorf("--site host must end in %s, got %q", SharePointHostSuffix, u.Hostname())
	}
	if strings.Trim(u.Path, "/") == "" {
		return nil, errors.New("--site must name a site path, e.g. https://contoso.sharepoint.com/sites/Policies")
	}
	return u, nil
}

// IsSharePointHost reports whether host is a SharePoint Online host:
// <name>.sharepoint.com, not sharepoint.com itself.
func IsSharePointHost(host string) bool {
	host = strings.ToLower(host)
	return strings.HasSuffix(host, SharePointHostSuffix) && len(host) > len(SharePointHostSuffix)
}

// Document is one file in the library.
type Document struct {
	// ID is the list item id, which identifies the document across runs
	// (its name and URL can change).
	ID string
	// Name is the file name, e.g. "Access Control Policy.docx".
	Name string
	// WebURL is the document's web URL, the policy's source link.
	WebURL string
	// Fields are the item's column values, keyed as Graph returns them.
	Fields map[string]any
	// Content is the file's bytes, for a .docx only, read up to
	// MaxContentBytes+1 so the converter can refuse an oversized file.
	Content []byte
}

// IsDocx reports whether the document is a Word document by its name.
func (d Document) IsDocx() bool {
	return strings.HasSuffix(strings.ToLower(d.Name), ".docx")
}

// Defaults for Client.
const (
	// DefaultMaxDocuments is the most documents one sync may carry: the
	// sync endpoint refuses a push of more than 1000.
	DefaultMaxDocuments = 1000
	// maxListItems bounds the items read, folders included.
	maxListItems = 10000
	// MaxContentBytes is the largest file read in full; a larger one is
	// cut at MaxContentBytes+1 bytes for the converter to refuse.
	MaxContentBytes = 16 << 20
	// maxResponseBytes bounds a Graph JSON response.
	maxResponseBytes = 32 << 20
)

// ErrTooManyDocuments is returned when the library holds more documents
// than one sync may carry. Nothing is returned with it: a truncated set
// would read as documents going missing.
var ErrTooManyDocuments = errors.New("graph: too many documents for one sync")

// Client reads one library.
type Client struct {
	// HTTP sends the token and Graph requests. Its CheckRedirect should
	// refuse a change of host (policysync.CheckRedirect).
	HTTP *http.Client
	Cred Credential
	// MaxDocuments defaults to DefaultMaxDocuments.
	MaxDocuments int

	token string
}

// Library is the library a run read.
type Library struct {
	SiteID string
	ListID string
	Name   string
}

// Documents returns every document (file) in the library named library
// on the site at site. Folders are left out. Any failed request, an
// unreadable response, a library that is not found or is ambiguous, or a
// set larger than MaxDocuments is an error, and no documents are returned
// with it.
func (c *Client) Documents(ctx context.Context, site *url.URL, library string) (Library, []Document, error) {
	if c.HTTP == nil || site == nil {
		return Library{}, nil, errors.New("graph: client needs HTTP and a site")
	}
	library = strings.TrimSpace(library)
	if library == "" {
		return Library{}, nil, errors.New("graph: library is required")
	}
	if err := c.authenticate(ctx); err != nil {
		return Library{}, nil, err
	}
	siteID, err := c.siteID(ctx, site)
	if err != nil {
		return Library{}, nil, err
	}
	lib, err := c.library(ctx, siteID, library)
	if err != nil {
		return Library{}, nil, err
	}
	items, err := c.items(ctx, lib)
	if err != nil {
		return Library{}, nil, err
	}
	maxDocs := c.MaxDocuments
	if maxDocs <= 0 {
		maxDocs = DefaultMaxDocuments
	}
	var docs []Document
	for _, it := range items {
		d, isFile, err := c.document(ctx, lib, it)
		if err != nil {
			return Library{}, nil, err
		}
		if !isFile {
			continue
		}
		docs = append(docs, d)
		if len(docs) > maxDocs {
			return Library{}, nil, fmt.Errorf("%w: more than %d", ErrTooManyDocuments, maxDocs)
		}
	}
	return lib, docs, nil
}

// authenticate gets an app-only token with the client credentials grant.
// Its expiry is not tracked: a run is bounded by its --timeout (10 minutes
// by default), inside the token's lifetime (expires_in; Microsoft's
// example shows 3599 seconds).
func (c *Client) authenticate(ctx context.Context) error {
	form := url.Values{
		"client_id":     {c.Cred.clientID},
		"client_secret": {c.Cred.secret},
		"scope":         {graphScope},
		"grant_type":    {"client_credentials"},
	}
	u := "https://" + LoginHost + "/" + url.PathEscape(c.Cred.tenant) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("graph: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := c.do(req, "token", &tok); err != nil {
		return err
	}
	if tok.AccessToken == "" || !strings.EqualFold(tok.TokenType, "bearer") {
		return errors.New("graph: token response carried no bearer access token")
	}
	c.token = tok.AccessToken
	return nil
}

// siteID resolves the site by host name and server-relative path.
func (c *Client) siteID(ctx context.Context, site *url.URL) (string, error) {
	segs := strings.Split(strings.Trim(site.Path, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.get(ctx, graphBase+"/sites/"+url.PathEscape(strings.ToLower(site.Hostname()))+":/"+strings.Join(segs, "/"), "site", &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("graph: site response carried no id")
	}
	return out.ID, nil
}

// listsPage is the subset of GET /sites/{id}/lists used.
type listsPage struct {
	Value []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		List        *struct {
			Template string `json:"template"`
		} `json:"list"`
	} `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// library finds the document library whose display name is name,
// ignoring case. None, or more than one, is an error.
func (c *Client) library(ctx context.Context, siteID, name string) (Library, error) {
	var found []Library
	next := graphBase + "/sites/" + url.PathEscape(siteID) + "/lists"
	for req := 0; next != ""; req++ {
		if req > 100 {
			return Library{}, errors.New("graph: list of lists did not finish (next link loops)")
		}
		var page listsPage
		if err := c.get(ctx, next, "lists", &page); err != nil {
			return Library{}, err
		}
		for _, l := range page.Value {
			if !strings.EqualFold(strings.TrimSpace(l.DisplayName), name) {
				continue
			}
			if l.List != nil && l.List.Template != "" && l.List.Template != "documentLibrary" {
				continue
			}
			found = append(found, Library{SiteID: siteID, ListID: l.ID, Name: l.DisplayName})
		}
		next = page.NextLink
	}
	switch len(found) {
	case 0:
		return Library{}, fmt.Errorf("graph: no document library named %q on the site", name)
	case 1:
		if found[0].ListID == "" {
			return Library{}, errors.New("graph: library carried no id")
		}
		return found[0], nil
	default:
		return Library{}, fmt.Errorf("graph: %d document libraries named %q on the site", len(found), name)
	}
}

// item is one list item with its column values.
type item struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

type itemsPage struct {
	Value    []item `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// items reads every item of the list, following next links.
func (c *Client) items(ctx context.Context, lib Library) ([]item, error) {
	var out []item
	seen := map[string]bool{}
	next := c.listURL(lib) + "/items?expand=fields"
	for req := 0; next != ""; req++ {
		if req > maxListItems {
			return nil, errors.New("graph: item list did not finish (next link loops)")
		}
		var page itemsPage
		if err := c.get(ctx, next, "items", &page); err != nil {
			return nil, err
		}
		fresh := 0
		for _, it := range page.Value {
			if it.ID == "" || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			fresh++
			out = append(out, it)
			if len(out) > maxListItems {
				return nil, fmt.Errorf("%w: more than %d items", ErrTooManyDocuments, maxListItems)
			}
		}
		if page.NextLink != "" && fresh == 0 {
			return nil, errors.New("graph: item list returned a next link with no new items")
		}
		next = page.NextLink
	}
	return out, nil
}

func (c *Client) listURL(lib Library) string {
	return graphBase + "/sites/" + url.PathEscape(lib.SiteID) + "/lists/" + url.PathEscape(lib.ListID)
}

// driveItem is the subset of the item's driveItem used.
type driveItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	WebURL      string    `json:"webUrl"`
	File        *struct{} `json:"file"`
	Folder      *struct{} `json:"folder"`
	DownloadURL string    `json:"@microsoft.graph.downloadUrl"`
}

// document reads an item's file and, for a .docx, its content. isFile is
// false for a folder or anything else that is not a file.
func (c *Client) document(ctx context.Context, lib Library, it item) (Document, bool, error) {
	var di driveItem
	if err := c.get(ctx, c.listURL(lib)+"/items/"+url.PathEscape(it.ID)+"/driveItem", "item "+it.ID, &di); err != nil {
		return Document{}, false, err
	}
	if di.Folder != nil || di.File == nil {
		return Document{}, false, nil
	}
	d := Document{ID: it.ID, Name: di.Name, WebURL: di.WebURL, Fields: it.Fields}
	if !d.IsDocx() {
		return d, true, nil
	}
	content, err := c.download(ctx, it.ID, di.DownloadURL)
	if err != nil {
		return Document{}, false, err
	}
	d.Content = content
	return d, true, nil
}

// download fetches a file from its pre-authenticated download URL,
// without the bearer token. The URL must be https on a SharePoint Online
// host.
func (c *Client) download(ctx context.Context, id, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return nil, fmt.Errorf("graph: item %s: no usable download URL", id)
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || !IsSharePointHost(u.Hostname()) {
		return nil, fmt.Errorf("graph: item %s: download URL is not https on a %s host with no port", id, SharePointHostSuffix)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("graph: item %s: build download: %w", id, scrub(err, raw, u.RawQuery))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph: item %s: download: %w", id, scrub(err, raw, u.RawQuery))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph: item %s: download: HTTP %d", id, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxContentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("graph: item %s: download: %w", id, scrub(err, raw, u.RawQuery))
	}
	return b, nil
}

// scrub keeps a download URL out of an error. The URL is pre-authenticated:
// its query (tempauth) reads the file for about an hour, and the error
// reaches stderr and the scheduler's logs. Go's *url.Error quotes the
// whole URL, so it is unwrapped; any remaining trace of the URL or its
// query in the text is redacted.
func scrub(err error, secrets ...string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	redacted := msg
	for _, s := range secrets {
		if s != "" {
			redacted = strings.ReplaceAll(redacted, s, "<redacted>")
		}
	}
	if redacted != msg {
		return errors.New(redacted)
	}
	return err
}

// get issues an authenticated Graph GET. The URL must be https on
// graph.microsoft.com: a next link pointing anywhere else is refused, so
// the token is never sent off Graph.
func (c *Client) get(ctx context.Context, raw, what string, out any) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("graph: %s: unreadable URL: %w", what, err)
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, GraphHost) || u.User != nil {
		return fmt.Errorf("graph: %s: refused a URL off https://%s", what, GraphHost)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("graph: %s: build request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return c.do(req, what, out)
}

// maxErrorBody is how much of an error response is quoted in the error.
const maxErrorBody = 512

// do sends req and decodes a 200 JSON response into out. An error
// response is quoted, but never the request, which may carry the secret.
func (c *Client) do(req *http.Request, what string, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("graph: %s: %w", what, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("graph: %s: HTTP %d: %s", what, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("graph: %s: decode response: %w", what, err)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
