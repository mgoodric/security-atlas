package pss

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the one Kubernetes endpoint the PSS
// collector reads: core namespaces (get/list — already held by the base
// ClusterRole). It holds a short-lived bearer token (never logged) and issues
// only GET requests.
//
// CRITICAL (structural over-collection guard): the namespace object carries
// arbitrary labels AND annotations. This client extracts ONLY the
// pod-security.kubernetes.io/* label values into RawNamespace — every other
// label, and ALL annotations, are read into a transient map and then dropped on
// the floor (never copied into a record-bound field). The reduce() boundary is
// the single chokepoint; a test feeds a namespace WITH unrelated labels +
// annotations and proves none of them reach a RawNamespace / Admission.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a PSS client. token is a read-only ServiceAccount bearer
// token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// pageLimit bounds the single namespace list call (read-only). A cluster with
// more than this many namespaces is paginated server-side; v0 reads the first
// bounded page (a documented follow-on, mirrors netpol).
const pageLimit = 500

// labelPrefix is the only label namespace the connector reads off a Namespace
// object. Every other label, and every annotation, is discarded.
const labelPrefix = "pod-security.kubernetes.io/"

// PSS label suffixes (mode + optional pinned version).
const (
	labelEnforce        = labelPrefix + "enforce"
	labelEnforceVersion = labelPrefix + "enforce-version"
	labelAudit          = labelPrefix + "audit"
	labelAuditVersion   = labelPrefix + "audit-version"
	labelWarn           = labelPrefix + "warn"
	labelWarnVersion    = labelPrefix + "warn-version"
)

// --- minimal Kubernetes API JSON shapes (namespace LABELS only) ---
//
// We model only metadata.name + metadata.labels. metadata.annotations and
// every other field on the namespace object (spec, status, ownerReferences,
// managedFields, etc.) are NOT modeled, so Go's json decoder discards them and
// they never materialize. Even the labels map we decode is then filtered down to
// the pod-security.kubernetes.io/* keys in reduce().

type apiMeta struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type apiNamespace struct {
	Metadata apiMeta `json:"metadata"`
}

type namespaceList struct {
	Items []apiNamespace `json:"items"`
}

// ListNamespacePSS reads every namespace (one bounded list call — read-only) and
// reduces each to its PSS label configuration. A namespace with no PSS labels
// appears with every Level unset (unenforced — recorded honestly). Read-only:
// only a GET against core namespaces.
func (c *Client) ListNamespacePSS(ctx context.Context) ([]RawNamespace, error) {
	var nsList namespaceList
	if err := c.getJSON(ctx, "/api/v1/namespaces", &nsList); err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	out := make([]RawNamespace, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		if ns.Metadata.Name == "" {
			continue
		}
		out = append(out, reduce(ns))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// reduce collapses one Namespace object into the PSS-label-only RawNamespace the
// evidence kind carries. This is THE over-collection chokepoint: it reads ONLY
// the six pod-security.kubernetes.io/* labels off metadata.labels and ignores
// every other label key (and never touches annotations — they are not even
// decoded). A label whose value is not a valid PSS level is dropped by
// normalizeLevel downstream.
func reduce(ns apiNamespace) RawNamespace {
	labels := ns.Metadata.Labels // arbitrary cluster labels — read but NOT copied wholesale
	return RawNamespace{
		Name:           ns.Metadata.Name,
		EnforceLevel:   Level(labels[labelEnforce]),
		EnforceVersion: labels[labelEnforceVersion],
		AuditLevel:     Level(labels[labelAudit]),
		AuditVersion:   labels[labelAuditVersion],
		WarnLevel:      Level(labels[labelWarn]),
		WarnVersion:    labels[labelWarnVersion],
	}
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	u := fmt.Sprintf("%s%s?limit=%d", c.BaseURL, path, pageLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
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

// APIError carries Kubernetes REST error context.
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
