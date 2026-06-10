// Package k8slist is the shared read-only paginating HTTP reader the
// atlas-k8s collectors (rbac, workload, netpol) use to list Kubernetes
// resources. It is the ONE place the connector follows the Kubernetes list
// cursor (`metadata.continue`): every collector's list call walks the cursor to
// completion instead of consuming only the first bounded page (slice 621).
//
// Kubernetes list pagination works like this: a list GET with `?limit=N` returns
// at most N items plus a `metadata.continue` token when more remain. The next
// request repeats the SAME list URL adding `&continue=<token>`; the server
// returns the next window and a fresh continue token, empty on the last page.
// (https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks)
//
// This package is read-only: it issues GET requests only and adds no new verb,
// resource, or ClusterRole grant — pagination is a query-parameter change
// (`limit` + `continue`), nothing more (AC-4 / P0-621). It holds a short-lived
// bearer token that it never logs.
//
// It is a pure-Go HTTP helper with unit tests (httptest); it touches neither
// Postgres nor app.current_tenant, so it ships no integration_test.go and is
// not enrolled in scripts/integration-shards.txt (slice 621 decisions note).
package k8slist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PageLimit is the per-request `?limit=N` window. Identical to the value the
// three collectors used before slice 621 (each hard-coded `pageLimit = 500`);
// centralising it keeps every collector's page size uniform.
const PageLimit = 500

// MaxListPages is the deterministic loop-termination backstop: a single
// ListAll walk follows at most this many `metadata.continue` cursors. It exists
// so a server that always returns a non-empty continue token (a buggy or
// hostile API server) cannot drive an unbounded read loop — the walk stops at
// the cap rather than looping forever (AC-3 / P0-621). At PageLimit=500 items
// per page this bounds one logical list at 500 * 1000 = 500,000 items, far
// beyond any real cluster's count of a single resource kind; the run timeout
// (the caller's context deadline) is the other backstop. Hitting the cap is not
// an error — the reader returns the items it gathered. Mirrors the Azure
// connector's maxRoleAssignmentPages (slice 623) / maxRuleCollectionGroupPages
// (slice 634) page caps.
const MaxListPages = 1000

// Reader is a thin read-only paginating HTTP client for the Kubernetes API
// server. It issues only GET requests and never mutates the cluster.
type Reader struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://kube-api:6443
	token   string
}

// NewReader builds a Reader. token is a read-only ServiceAccount bearer token
// (from k8sauth.Credential.Token); baseURL is the API server URL. A nil
// httpClient gets a 20s-timeout default, matching the collectors' prior default.
func NewReader(httpClient *http.Client, baseURL, token string) *Reader {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Reader{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// listPage is the slice of a Kubernetes list response this reader needs: the
// items (decoded into the caller's item type T) and the continue cursor. Any
// other top-level keys (apiVersion, kind, the rest of metadata) are discarded by
// the json decoder, exactly as the collectors' prior decode targets discarded
// unmodeled keys.
type listPage[T any] struct {
	Items    []T `json:"items"`
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
}

// ListAll issues a paginated GET against path (e.g.
// "/apis/networking.k8s.io/v1/networkpolicies") and follows the
// `metadata.continue` cursor until the server returns an empty continue token,
// accumulating the decoded items across every page (AC-1 / AC-2). The walk is
// bounded by MaxListPages (AC-3) and by ctx's deadline (the run timeout).
//
// Read-only: every request — first page and every continue follow-up — is a GET
// against the SAME path; pagination only adds the `limit` and `continue` query
// parameters (AC-4). On any non-200 response or decode failure it returns an
// error and the items gathered so far are discarded (a partial list is never
// reported as complete).
func ListAll[T any](ctx context.Context, r *Reader, path string) ([]T, error) {
	out := make([]T, 0)
	cont := ""
	for page := 0; page < MaxListPages; page++ {
		var pg listPage[T]
		if err := r.getPage(ctx, path, cont, &pg); err != nil {
			return nil, err
		}
		out = append(out, pg.Items...)
		next := strings.TrimSpace(pg.Metadata.Continue)
		if next == "" {
			// Empty continue token => this was the last page.
			return out, nil
		}
		cont = next
	}
	// Cap reached with a still-non-empty cursor: terminate deterministically
	// rather than loop. Return what we gathered (the run under-reports honestly
	// at the cap, the same contract the Azure page caps use).
	return out, nil
}

// Probe issues a read-only GET against path (an API-discovery path such as
// "/apis/cilium.io/v2") and returns the HTTP status code, discarding the body.
// It is the presence-check primitive for CNI-CRD discovery (slice 622): a 200
// means the API group/version is served (the CRD is installed), a 404 means it
// is absent. It adds no verb beyond GET and no new ClusterRole grant — discovery
// is the same read-only plane the list calls use. The response body is drained
// and closed so the connection can be reused; it is never decoded.
func (r *Reader) Probe(ctx context.Context, path string) (int, error) {
	u := r.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	req.Header.Set("Accept", "application/json")
	res, err := r.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<13))
	return res.StatusCode, nil
}

// getPage GETs one list page. cont is the `metadata.continue` token from the
// previous page ("" on the first request). The URL carries `limit` always and
// `continue` only when cont is non-empty.
func (r *Reader) getPage(ctx context.Context, path, cont string, into any) error {
	u := fmt.Sprintf("%s%s?limit=%d", r.BaseURL, path, PageLimit)
	if cont != "" {
		u += "&continue=" + url.QueryEscape(cont)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	req.Header.Set("Accept", "application/json")
	res, err := r.HTTP.Do(req)
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

// APIError carries Kubernetes REST error context. Shared so the three
// collectors surface a uniform error shape.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("k8s: HTTP %d", e.Status)
	}
	return fmt.Sprintf("k8s: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
