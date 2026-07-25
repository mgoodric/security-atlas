// Package rbac pulls Kubernetes RBAC roles + bindings (who-can-do-what),
// producing one Binding per (RoleBinding|ClusterRoleBinding) grant.
//
// Source: read-only Kubernetes API (get/list on rbac.authorization.k8s.io
// roles / clusterroles / rolebindings / clusterrolebindings). The connector
// emits authorization CONFIGURATION only — NEVER Secret values, ConfigMap
// values, container env, or logs.
//
// Output is descriptive: the platform evaluator interprets which binding pattern
// passes / fails per (control, scope). The connector emits a connector-side
// grants_wildcard heuristic but leaves the policy call to the evaluator
// (Result_INCONCLUSIVE).
package rbac

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Subject kinds.
const (
	SubjectUser           = "User"
	SubjectGroup          = "Group"
	SubjectServiceAccount = "ServiceAccount"
)

// Role kinds.
const (
	RoleKindRole        = "Role"
	RoleKindClusterRole = "ClusterRole"
)

// Binding scopes.
const (
	ScopeCluster   = "cluster"
	ScopeNamespace = "namespace"
)

// Subject is one identity a binding grants the role to. Identity reference only
// — never a credential.
type Subject struct {
	Kind      string
	Name      string
	Namespace string
}

// Rule is one policy rule a role grants. Authorization configuration only.
type Rule struct {
	APIGroups []string
	Resources []string
	Verbs     []string
}

// Binding is one record the cmd layer turns into an evidence record. Field
// names map 1:1 to k8s.rbac_binding.v1 schema.
type Binding struct {
	BindingName    string
	BindingScope   string
	Namespace      string
	RoleKind       string
	RoleName       string
	Subjects       []Subject
	Rules          []Rule
	GrantsWildcard bool
	ObservedAt     time.Time
}

// RawBinding is the narrow view the API surface returns for one binding plus the
// rules of its referenced role (resolved by the client). Tests construct it
// directly. No secret, no payload data.
type RawBinding struct {
	Name      string
	Scope     string // "cluster" | "namespace"
	Namespace string
	RoleKind  string
	RoleName  string
	Subjects  []Subject
	Rules     []Rule
}

// API is the narrow surface Pull depends on. The concrete implementation issues
// read-only Kubernetes API calls; tests pass a fake. v0 lists bindings + their
// referenced-role rules; cursor pagination is a documented follow-on
// (threat-model D: bounded page + run timeout cap a large cluster).
type API interface {
	ListBindings(ctx context.Context) ([]RawBinding, error)
}

// Pull lists every visible RBAC binding and normalizes it. now is injectable for
// deterministic tests (nil → time.Now UTC).
func Pull(ctx context.Context, api API, now func() time.Time) ([]Binding, error) {
	if api == nil {
		return nil, errors.New("rbac: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rbac bindings: %w", err)
	}
	observedAt := now()
	out := make([]Binding, 0, len(raw))
	for _, r := range raw {
		if r.Name == "" || r.RoleName == "" {
			// Schema requires these; skip rather than emit invalid records.
			continue
		}
		out = append(out, Binding{
			BindingName:    r.Name,
			BindingScope:   normalizeScope(r.Scope),
			Namespace:      r.Namespace,
			RoleKind:       normalizeRoleKind(r.RoleKind),
			RoleName:       r.RoleName,
			Subjects:       r.Subjects,
			Rules:          r.Rules,
			GrantsWildcard: rulesGrantWildcard(r.Rules),
			ObservedAt:     observedAt,
		})
	}
	return out, nil
}

// rulesGrantWildcard is the connector-side heuristic: a rule that grants a
// wildcard verb, resource, or apiGroup is cluster-admin-grade reach. Descriptive
// only — the evaluator owns the policy call.
func rulesGrantWildcard(rules []Rule) bool {
	for _, r := range rules {
		if containsWildcard(r.Verbs) || containsWildcard(r.Resources) || containsWildcard(r.APIGroups) {
			return true
		}
	}
	return false
}

func containsWildcard(ss []string) bool {
	for _, s := range ss {
		if s == "*" {
			return true
		}
	}
	return false
}

func normalizeScope(s string) string {
	switch s {
	case ScopeCluster, ScopeNamespace:
		return s
	default:
		return ScopeNamespace
	}
}

func normalizeRoleKind(s string) string {
	switch s {
	case RoleKindRole, RoleKindClusterRole:
		return s
	default:
		return RoleKindClusterRole
	}
}
