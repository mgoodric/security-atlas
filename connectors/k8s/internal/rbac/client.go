package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the Kubernetes RBAC endpoints the
// connector reads. It holds a short-lived bearer token (never logged) and issues
// only GET requests. v0 reads the first bounded page of each kind.
//
// It deliberately does NOT depend on k8s.io/client-go — the connector mirrors
// the slice-486 thin-HTTP pattern to keep the dependency tree small. The four
// endpoints below are stable Kubernetes API surfaces.
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://kube-api:6443
	token   string
}

// NewClient builds an RBAC client. token is a read-only ServiceAccount bearer
// token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// --- minimal Kubernetes API JSON shapes (read-only fields only) ---

type apiSubject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type apiRoleRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type apiPolicyRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

type apiRole struct {
	Metadata apiMeta         `json:"metadata"`
	Rules    []apiPolicyRule `json:"rules"`
}

type apiBinding struct {
	Metadata apiMeta      `json:"metadata"`
	RoleRef  apiRoleRef   `json:"roleRef"`
	Subjects []apiSubject `json:"subjects"`
}

type apiMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type roleList struct {
	Items []apiRole `json:"items"`
}

type bindingList struct {
	Items []apiBinding `json:"items"`
}

const pageLimit = 500

// ListBindings reads cluster + namespaced bindings and resolves each one's role
// rules from the role / clusterrole lists. Read-only: only GET requests, only
// the RBAC API group.
func (c *Client) ListBindings(ctx context.Context) ([]RawBinding, error) {
	// Resolve role rules first so each binding can attach them.
	clusterRoles, err := c.listRoles(ctx, "/apis/rbac.authorization.k8s.io/v1/clusterroles")
	if err != nil {
		return nil, fmt.Errorf("list clusterroles: %w", err)
	}
	roles, err := c.listRoles(ctx, "/apis/rbac.authorization.k8s.io/v1/roles")
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}

	out := make([]RawBinding, 0)

	crb, err := c.listBindings(ctx, "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings")
	if err != nil {
		return nil, fmt.Errorf("list clusterrolebindings: %w", err)
	}
	for _, b := range crb {
		out = append(out, toRaw(b, ScopeCluster, lookupRules(b.RoleRef, b.Metadata.Namespace, clusterRoles, roles)))
	}

	rb, err := c.listBindings(ctx, "/apis/rbac.authorization.k8s.io/v1/rolebindings")
	if err != nil {
		return nil, fmt.Errorf("list rolebindings: %w", err)
	}
	for _, b := range rb {
		out = append(out, toRaw(b, ScopeNamespace, lookupRules(b.RoleRef, b.Metadata.Namespace, clusterRoles, roles)))
	}

	return out, nil
}

func toRaw(b apiBinding, scope string, rules []Rule) RawBinding {
	subjects := make([]Subject, 0, len(b.Subjects))
	for _, s := range b.Subjects {
		subjects = append(subjects, Subject(s))
	}
	return RawBinding{
		Name:      b.Metadata.Name,
		Scope:     scope,
		Namespace: b.Metadata.Namespace,
		RoleKind:  b.RoleRef.Kind,
		RoleName:  b.RoleRef.Name,
		Subjects:  subjects,
		Rules:     rules,
	}
}

// lookupRules resolves a roleRef to its policy rules from the pre-fetched lists.
func lookupRules(ref apiRoleRef, ns string, clusterRoles, roles map[string][]Rule) []Rule {
	if ref.Kind == RoleKindClusterRole {
		return clusterRoles[ref.Name]
	}
	return roles[ns+"/"+ref.Name]
}

func (c *Client) listRoles(ctx context.Context, path string) (map[string][]Rule, error) {
	var list roleList
	if err := c.getJSON(ctx, path, &list); err != nil {
		return nil, err
	}
	out := make(map[string][]Rule, len(list.Items))
	for _, r := range list.Items {
		rules := make([]Rule, 0, len(r.Rules))
		for _, pr := range r.Rules {
			rules = append(rules, Rule(pr))
		}
		key := r.Metadata.Name
		if r.Metadata.Namespace != "" {
			key = r.Metadata.Namespace + "/" + r.Metadata.Name
		}
		out[key] = rules
	}
	return out, nil
}

func (c *Client) listBindings(ctx context.Context, path string) ([]apiBinding, error) {
	var list bindingList
	if err := c.getJSON(ctx, path, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	u := fmt.Sprintf("%s%s?limit=%d", c.BaseURL, path, pageLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
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

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
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
