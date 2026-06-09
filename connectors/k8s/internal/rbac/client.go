package rbac

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// Client is a thin read-only HTTP client for the Kubernetes RBAC endpoints the
// connector reads. It delegates the HTTP + pagination to the shared
// k8slist.Reader: every list call follows the Kubernetes `metadata.continue`
// cursor to completion (slice 621), so a cluster with more than one page of
// roles / clusterroles / bindings is no longer silently truncated.
//
// It deliberately does NOT depend on k8s.io/client-go — the connector mirrors
// the slice-486 thin-HTTP pattern to keep the dependency tree small. The four
// endpoints below are stable Kubernetes API surfaces. Read-only: only GET
// requests, only the RBAC API group.
type Client struct {
	r *k8slist.Reader
}

// NewClient builds an RBAC client. token is a read-only ServiceAccount bearer
// token (from k8sauth.Credential.Token). baseURL is the API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{r: k8slist.NewReader(httpClient, baseURL, token)}
}

// APIError is re-exported from the shared reader so existing callers and tests
// keep referring to rbac.APIError.
type APIError = k8slist.APIError

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

// ListBindings reads cluster + namespaced bindings and resolves each one's role
// rules from the role / clusterrole lists. Each underlying list call follows the
// continue cursor to completion. Read-only: only GET requests, only the RBAC API
// group.
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

	crb, err := k8slist.ListAll[apiBinding](ctx, c.r, "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings")
	if err != nil {
		return nil, fmt.Errorf("list clusterrolebindings: %w", err)
	}
	for _, b := range crb {
		out = append(out, toRaw(b, ScopeCluster, lookupRules(b.RoleRef, b.Metadata.Namespace, clusterRoles, roles)))
	}

	rb, err := k8slist.ListAll[apiBinding](ctx, c.r, "/apis/rbac.authorization.k8s.io/v1/rolebindings")
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
	items, err := k8slist.ListAll[apiRole](ctx, c.r, path)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]Rule, len(items))
	for _, r := range items {
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
