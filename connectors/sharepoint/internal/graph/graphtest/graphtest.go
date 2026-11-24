// Package graphtest replays Microsoft Graph for the SharePoint connector's
// tests (#2837): one httptest server answers the token endpoint, the
// Graph v1.0 requests the connector makes, the files' download URLs and,
// optionally, the platform's sync endpoint.
//
// The connector only talks to fixed hosts (login.microsoftonline.com,
// graph.microsoft.com, *.sharepoint.com), so Transport sends a request
// for any of them to the test server instead, keeping its Host, and
// records every host and request so a test can assert where each
// credential went. The response shapes follow Microsoft's documented
// examples for each endpoint (see the decisions log for what has not been
// checked against a live tenant).
package graphtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Fixed names the fake serves.
const (
	Tenant       = "contoso.onmicrosoft.com"
	ClientID     = "00001111-aaaa-2222-bbbb-3333cccc4444"
	ClientSecret = "test-client-secret"
	Token        = "test-access-token"
	SiteHost     = "contoso.sharepoint.com"
	SitePath     = "/sites/Policies"
	SiteID       = "contoso.sharepoint.com,2C712604-1370-44E7-A1F5-426573FDA80A,2D2244C3-251A-49EA-93A8-39E1C3A060FE"
	ListID       = "b57af081-936c-4803-a120-d94887b03864"
	// TempAuth is the download URLs' pre-authenticated query value, which
	// must never reach an error or a log.
	TempAuth = "v1.eyJTRUNSRVQtVEVNUEFVVEgifQ"
	Library  = "Policies"
)

// Item is one list item: its column values and the file (or folder)
// behind it.
type Item struct {
	ID     string
	Fields map[string]any
	// Name is the file name; Folder marks a folder.
	Name   string
	Folder bool
	// Content is served at the item's download URL.
	Content []byte
	// DownloadURL overrides the item's download URL.
	DownloadURL string
}

// Request is one request the server saw.
type Request struct {
	Host          string
	Method        string
	Path          string
	Query         string
	Authorization string
	Form          url.Values
}

// Server is the fake. Set its fields before the first request.
type Server struct {
	*httptest.Server
	Items []Item
	// ItemsPerPage splits the item list into pages joined by next links
	// (default: all on one page).
	ItemsPerPage int
	// Fail answers a request whose path contains the key with the value
	// as its status.
	Fail map[string]int
	// NextLink overrides the first items page's @odata.nextLink.
	NextLink string
	// Redirect answers a request whose path contains the key with a 302
	// to the value.
	Redirect map[string]string
	// Sync answers POST /v1/policies:sync; nil answers 404.
	Sync http.HandlerFunc
	// FailDownloads makes every request to a *.sharepoint.com host fail
	// in the transport, as a timeout or a TLS error would.
	FailDownloads bool

	mu   sync.Mutex
	seen []Request
}

// New starts a fake with items.
func New(t *testing.T, items ...Item) *Server {
	t.Helper()
	s := &Server{Items: items}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Requests returns every request seen, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.seen...)
}

// Client returns an http.Client whose transport sends the connector's
// fixed hosts to the fake.
func (s *Server) Client() *http.Client {
	target, _ := url.Parse(s.URL)
	return &http.Client{Transport: &transport{target: target, s: s}}
}

type transport struct {
	target *url.URL
	s      *Server
}

// ErrInjected is the transport error FailDownloads returns.
var ErrInjected = errors.New("injected transport failure")

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := strings.ToLower(r.URL.Hostname())
	if t.s.FailDownloads && strings.HasSuffix(host, ".sharepoint.com") {
		return nil, ErrInjected
	}
	if host == "login.microsoftonline.com" || host == "graph.microsoft.com" || strings.HasSuffix(host, ".sharepoint.com") || host == "evil.example" {
		r2 := r.Clone(r.Context())
		r2.URL.Scheme = t.target.Scheme
		r2.URL.Host = t.target.Host
		r2.Host = r.URL.Host
		return http.DefaultTransport.RoundTrip(r2)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (s *Server) record(r *http.Request) {
	rec := Request{Host: r.Host, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Authorization: r.Header.Get("Authorization")}
	if r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		_ = r.ParseForm()
		rec.Form = r.PostForm
	}
	s.mu.Lock()
	s.seen = append(s.seen, rec)
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	for k, status := range s.Fail {
		if strings.Contains(r.URL.Path, k) {
			http.Error(w, `{"error":{"code":"generalException","message":"injected"}}`, status)
			return
		}
	}
	for k, to := range s.Redirect {
		if strings.Contains(r.URL.Path, k) {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
	}
	host := strings.ToLower(r.Host)
	switch {
	case host == "login.microsoftonline.com":
		s.token(w, r)
	case host == "graph.microsoft.com":
		if r.Header.Get("Authorization") != "Bearer "+Token {
			http.Error(w, `{"error":{"code":"InvalidAuthenticationToken"}}`, http.StatusUnauthorized)
			return
		}
		s.graph(w, r)
	case strings.HasSuffix(host, ".sharepoint.com"):
		s.download(w, r)
	case r.URL.Path == "/v1/policies:sync" && s.Sync != nil:
		s.Sync(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/"+Tenant+"/oauth2/v2.0/token" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("client_secret") != ClientSecret ||
		r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != "https://graph.microsoft.com/.default" {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	writeJSON(w, map[string]any{"token_type": "Bearer", "expires_in": 3599, "access_token": Token})
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	siteBase := "/v1.0/sites/" + url.PathEscape(SiteID)
	listBase := siteBase + "/lists/" + ListID
	switch {
	case p == "/v1.0/sites/"+SiteHost+":"+SitePath:
		writeJSON(w, map[string]any{"id": SiteID, "name": "Policies", "webUrl": "https://" + SiteHost + SitePath})
	case p == siteBase+"/lists" && r.URL.Query().Get("$skiptoken") == "":
		writeJSON(w, map[string]any{
			"value": []any{
				map[string]any{"id": "l-docs", "displayName": "Documents", "list": map[string]any{"template": "documentLibrary"}},
				map[string]any{"id": "l-tasks", "displayName": "Policies", "list": map[string]any{"template": "genericList"}},
			},
			"@odata.nextLink": "https://graph.microsoft.com" + siteBase + "/lists?$skiptoken=2",
		})
	case p == siteBase+"/lists":
		writeJSON(w, map[string]any{"value": []any{
			map[string]any{"id": ListID, "displayName": Library, "list": map[string]any{"template": "documentLibrary"}},
		}})
	case p == listBase+"/items":
		s.items(w, r)
	case strings.HasPrefix(p, listBase+"/items/") && strings.HasSuffix(p, "/driveItem"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, listBase+"/items/"), "/driveItem")
		s.driveItem(w, r, id)
	default:
		http.Error(w, fmt.Sprintf(`{"error":{"code":"itemNotFound","message":%q}}`, p), http.StatusNotFound)
	}
}

func (s *Server) items(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("expand") != "fields" {
		http.Error(w, "expand=fields expected", http.StatusBadRequest)
		return
	}
	per := s.ItemsPerPage
	if per <= 0 {
		per = len(s.Items) + 1
	}
	start := 0
	if tok := r.URL.Query().Get("$skiptoken"); tok != "" {
		_, _ = fmt.Sscanf(tok, "%d", &start)
	}
	end := min(start+per, len(s.Items))
	var value []any
	for _, it := range s.Items[start:end] {
		value = append(value, map[string]any{
			"id":          it.ID,
			"@odata.etag": `"` + it.ID + `,1"`,
			"fields":      it.Fields,
		})
	}
	page := map[string]any{"value": value}
	if end < len(s.Items) {
		next := fmt.Sprintf("https://graph.microsoft.com%s?expand=fields&$skiptoken=%d", r.URL.EscapedPath(), end)
		if start == 0 && s.NextLink != "" {
			next = s.NextLink
		}
		page["@odata.nextLink"] = next
	}
	writeJSON(w, page)
}

func (s *Server) driveItem(w http.ResponseWriter, r *http.Request, id string) {
	for _, it := range s.Items {
		if it.ID != id {
			continue
		}
		di := map[string]any{
			"id":     "01BYE5RZ" + it.ID,
			"name":   it.Name,
			"webUrl": "https://" + SiteHost + SitePath + "/" + Library + "/" + url.PathEscape(it.Name),
		}
		if it.Folder {
			di["folder"] = map[string]any{"childCount": 0}
		} else {
			di["file"] = map[string]any{"mimeType": "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}
			dl := it.DownloadURL
			if dl == "" {
				dl = "https://" + SiteHost + SitePath + "/_layouts/15/download.aspx?UniqueId=" + it.ID + "&tempauth=" + TempAuth
			}
			di["@microsoft.graph.downloadUrl"] = dl
		}
		writeJSON(w, di)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("UniqueId")
	for _, it := range s.Items {
		if it.ID == id {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(it.Content)
			return
		}
	}
	http.NotFound(w, r)
}

// ReadBody decodes a JSON request body into v.
func ReadBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Errorf("decode body: %v (%s)", err, b)
	}
}
