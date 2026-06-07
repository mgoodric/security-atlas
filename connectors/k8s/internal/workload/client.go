package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the Kubernetes workload endpoints
// the connector reads. It holds a short-lived bearer token (never logged) and
// issues only GET requests against the apps API group.
//
// CRITICAL (P0-487-3): the JSON decode targets below deliberately model ONLY the
// security-context + host-namespace fields of the pod template. Container env,
// envFrom, volumes, Secret refs, and ConfigMap refs are NOT decoded — they never
// enter a RawWorkload and therefore can never reach an evidence record.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	token   string
}

// NewClient builds a workload client. token is a read-only ServiceAccount bearer
// token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

const pageLimit = 500

// --- minimal Kubernetes API JSON shapes (security-context fields ONLY) ---
//
// We intentionally do NOT model env / envFrom / volumes / volumeMounts. Go's
// json decoder discards unmodeled keys, so the Secret / ConfigMap / env payloads
// in the API response are never materialized into Go memory here.

type apiSecurityContext struct {
	RunAsNonRoot             *bool `json:"runAsNonRoot"`
	Privileged               *bool `json:"privileged"`
	ReadOnlyRootFilesystem   *bool `json:"readOnlyRootFilesystem"`
	AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation"`
}

type apiContainer struct {
	SecurityContext *apiSecurityContext `json:"securityContext"`
}

type apiPodSpec struct {
	HostNetwork     bool                `json:"hostNetwork"`
	HostPID         bool                `json:"hostPID"`
	HostIPC         bool                `json:"hostIPC"`
	SecurityContext *apiSecurityContext `json:"securityContext"`
	Containers      []apiContainer      `json:"containers"`
}

type apiPodTemplate struct {
	Spec apiPodSpec `json:"spec"`
}

type apiWorkloadSpec struct {
	Template apiPodTemplate `json:"template"`
}

type apiWorkload struct {
	Metadata apiMeta         `json:"metadata"`
	Spec     apiWorkloadSpec `json:"spec"`
}

type apiMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type workloadList struct {
	Items []apiWorkload `json:"items"`
}

// ListWorkloads reads deployments, daemonsets, and statefulsets across all
// namespaces and reduces each one's pod template to its effective security
// context. Read-only: only GET requests, only the apps API group.
func (c *Client) ListWorkloads(ctx context.Context) ([]RawWorkload, error) {
	out := make([]RawWorkload, 0)
	specs := []struct {
		path string
		kind string
	}{
		{"/apis/apps/v1/deployments", KindDeployment},
		{"/apis/apps/v1/daemonsets", KindDaemonSet},
		{"/apis/apps/v1/statefulsets", KindStatefulSet},
	}
	for _, s := range specs {
		var list workloadList
		if err := c.getJSON(ctx, s.path, &list); err != nil {
			return nil, fmt.Errorf("list %s: %w", s.kind, err)
		}
		for _, w := range list.Items {
			out = append(out, reduce(w, s.kind))
		}
	}
	return out, nil
}

// reduce collapses a workload's pod template into the aggregate security flags
// the evidence kind carries. Pod-level securityContext is the inheritance
// default; a container-level setting overrides it.
func reduce(w apiWorkload, kind string) RawWorkload {
	spec := w.Spec.Template.Spec
	r := RawWorkload{
		Kind:           kind,
		Name:           w.Metadata.Name,
		Namespace:      w.Metadata.Namespace,
		HostNetwork:    spec.HostNetwork,
		HostPID:        spec.HostPID,
		HostIPC:        spec.HostIPC,
		ContainerCount: len(spec.Containers),
	}

	podRunAsNonRoot := boolVal(scField(spec.SecurityContext, func(s *apiSecurityContext) *bool { return s.RunAsNonRoot }), false)

	// Aggregate across containers: the workload is "good" on a flag only when
	// EVERY container is good; "bad" if ANY container is bad.
	runAsNonRoot := len(spec.Containers) > 0 || podRunAsNonRoot
	readOnlyFS := len(spec.Containers) > 0
	privileged := false
	allowEsc := false

	for _, ctr := range spec.Containers {
		sc := ctr.SecurityContext
		// runAsNonRoot: container value, else pod value.
		cNonRoot := boolVal(scField(sc, func(s *apiSecurityContext) *bool { return s.RunAsNonRoot }), podRunAsNonRoot)
		if !cNonRoot {
			runAsNonRoot = false
		}
		// readOnlyRootFilesystem: container-only field; default false.
		cReadOnly := boolVal(scField(sc, func(s *apiSecurityContext) *bool { return s.ReadOnlyRootFilesystem }), false)
		if !cReadOnly {
			readOnlyFS = false
		}
		// privileged: any true -> true.
		if boolVal(scField(sc, func(s *apiSecurityContext) *bool { return s.Privileged }), false) {
			privileged = true
		}
		// allowPrivilegeEscalation: defaults to true when unset; any escalating
		// container -> true.
		if boolVal(scField(sc, func(s *apiSecurityContext) *bool { return s.AllowPrivilegeEscalation }), true) {
			allowEsc = true
		}
	}
	if len(spec.Containers) == 0 {
		// No containers modeled: cannot assert container-level hardening.
		readOnlyFS = false
		allowEsc = true
	}

	r.RunAsNonRoot = runAsNonRoot
	r.ReadOnlyRootFilesystem = readOnlyFS
	r.Privileged = privileged
	r.AllowPrivilegeEscalation = allowEsc
	return r
}

func scField(sc *apiSecurityContext, get func(*apiSecurityContext) *bool) *bool {
	if sc == nil {
		return nil
	}
	return get(sc)
}

func boolVal(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
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
