package pss

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// Client is a thin read-only HTTP client for the one Kubernetes endpoint the PSS
// collector reads: core namespaces (get/list — already held by the base
// ClusterRole). It delegates HTTP + pagination to the shared k8slist.Reader:
// the namespace list call follows the Kubernetes `metadata.continue` cursor to
// completion (slice 653), so a cluster with more than one page of namespaces is
// no longer silently truncated. It holds a short-lived bearer token (never
// logged) and issues only GET requests.
//
// CRITICAL (structural over-collection guard): the namespace object carries
// arbitrary labels AND annotations. This client extracts ONLY the
// pod-security.kubernetes.io/* label values into RawNamespace — every other
// label, and ALL annotations, are read into a transient map and then dropped on
// the floor (never copied into a record-bound field). The reduce() boundary is
// the single chokepoint; a test feeds a namespace WITH unrelated labels +
// annotations and proves none of them reach a RawNamespace / Admission.
type Client struct {
	r *k8slist.Reader
}

// NewClient builds a PSS client. token is a read-only ServiceAccount bearer
// token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{r: k8slist.NewReader(httpClient, baseURL, token)}
}

// APIError is re-exported from the shared reader so existing callers and tests
// keep referring to pss.APIError.
type APIError = k8slist.APIError

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

// ListNamespacePSS reads every namespace (the list call follows the continue
// cursor to completion — read-only) and reduces each to its PSS label
// configuration. A namespace with no PSS labels appears with every Level unset
// (unenforced — recorded honestly). Read-only: only a GET against core
// namespaces.
func (c *Client) ListNamespacePSS(ctx context.Context) ([]RawNamespace, error) {
	namespaces, err := k8slist.ListAll[apiNamespace](ctx, c.r, "/api/v1/namespaces")
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	out := make([]RawNamespace, 0, len(namespaces))
	for _, ns := range namespaces {
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
