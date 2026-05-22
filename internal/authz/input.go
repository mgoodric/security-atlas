package authz

import (
	"context"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// Input is the canonical decision input passed to OPA. The schema is
// documented in policies/authz/helpers.rego and must stay in sync with
// it.
type Input struct {
	User     UserInput     `json:"user"`
	TenantID string        `json:"tenant_id"`
	Action   string        `json:"action"`
	Resource ResourceInput `json:"resource"`
	Request  RequestInput  `json:"request"`
}

// UserInput is the user half of Input.
type UserInput struct {
	ID    string                 `json:"id"`
	Roles []Role                 `json:"roles"`
	Attrs map[string]interface{} `json:"attrs"`
}

// ResourceInput is the resource half of Input.
type ResourceInput struct {
	Type  string                 `json:"type"`
	ID    string                 `json:"id"`
	Attrs map[string]interface{} `json:"attrs"`
}

// RequestInput surfaces the HTTP request metadata for audit logging
// and for Rego rules that want to inspect the raw method/path.
type RequestInput struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// transitionActions enumerates the path-terminal segments that the
// middleware promotes from action="write" to a more specific action
// (e.g. action="approve"). Matches the policies/authz/helpers.rego
// transition_actions set.
var transitionActions = map[string]bool{
	"submit":        true,
	"approve":       true,
	"activate":      true,
	"publish":       true,
	"deny":          true,
	"rotate":        true,
	"revoke":        true,
	"aggregate":     true,
	"upload-bundle": true,
}

// BuildInput constructs the canonical Input from an http.Request and
// the authenticated credential carried on the request context. It is
// the single source of input-shape consistency across the codebase --
// Rego rules can rely on every decision arriving with the same fields.
func BuildInput(r *http.Request, attrs map[string]interface{}) Input {
	cred, _ := authctx.CredentialFromContext(r.Context())
	roles := derivedRolesFor(cred)

	action := actionFromMethodAndPath(r.Method, r.URL.Path)
	resourceType, resourceID := resourceFromPath(r.URL.Path)

	// is_machine_actor: true for non-human callers. Recognised prefixes:
	//   - empty UserID         — slice-034 api_keys without a bound user
	//   - "key_..."            — slice-014/034 api_keys (legacy bearer)
	//   - "oauth_client:..."   — slice 188 OAuth client_credentials JWTs
	//                            (jwtmw synthesises the credstore.Credential
	//                            with UserID = claims.Subject which the
	//                            slice 188 token handler sets to
	//                            MachineSubjectPrefix + client_id).
	// The slice-196 bootstrap migration uses the third form to land an
	// OAuth-client identity that the slice-035 system.rego carve-out can
	// match for the controls:upload-bundle path.
	userAttrs := map[string]interface{}{
		"is_machine_actor": cred.UserID == "" ||
			strings.HasPrefix(cred.UserID, "key_") ||
			strings.HasPrefix(cred.UserID, "oauth_client:"),
		// is_super_admin: slice 142. Surfaces the platform-global
		// super_admin bit from the verified JWT's `atlas:super_admin`
		// claim. The policies/authz/super_admin.rego rule keys on this
		// attribute. Falls back to false when no JWT is on the context
		// (legacy bearer path; super_admin is JWT-only at v1).
		"is_super_admin": jwtmw.FromContext(r.Context()) != nil &&
			jwtmw.FromContext(r.Context()).SuperAdmin,
	}
	for k, v := range attrs {
		userAttrs[k] = v
	}

	return Input{
		User: UserInput{
			ID:    cred.UserID,
			Roles: roles,
			Attrs: userAttrs,
		},
		TenantID: cred.TenantID,
		Action:   action,
		Resource: ResourceInput{
			Type:  resourceType,
			ID:    resourceID,
			Attrs: map[string]interface{}{},
		},
		Request: RequestInput{
			Method: r.Method,
			Path:   r.URL.Path,
		},
	}
}

// derivedRolesFor maps a slice-014/011/018 credstore.Credential to the
// canonical role set. v1 backwards-compat path: in-memory credentials
// don't have user_roles rows yet, so the role is derived from the
// IsAdmin / IsApprover / OwnerRoles flags. When a user_roles row
// exists (looked up by RolesResolver below), it takes precedence.
//
// Slice 035 graduates these flags to the OPA-driven RBAC model
// described in canvas §9.5. The bridge keeps slices 014 / 011 / 018
// behaviour working without needing a forklift migration of the
// in-memory credstore.
func derivedRolesFor(cred credstore.Credential) []Role {
	switch {
	case cred.IsAdmin:
		return []Role{RoleAdmin}
	case cred.IsApprover:
		return []Role{RoleGRCEngineer}
	case len(cred.OwnerRoles) > 0:
		return []Role{RoleControlOwner}
	case cred.TenantID == "":
		// No credential in context -- exempt path. Return empty so
		// authz.Decide hits default-deny.
		return nil
	default:
		// Standard tenant credential with no special flags. For v1
		// connectors (push credentials) this is grc_engineer (write
		// to evidence). Pure viewer credentials don't exist yet at
		// the credstore layer; user_roles assignments via the future
		// admin UI will populate them.
		return []Role{RoleGRCEngineer}
	}
}

// actionFromMethodAndPath derives the canonical action name. GET is
// "read"; other methods are "write" UNLESS the path terminates in a
// transition segment (submit / approve / publish / etc.), in which
// case the segment IS the action.
func actionFromMethodAndPath(method, path string) string {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return "read"
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) > 0 {
		last := segments[len(segments)-1]
		// Strip a sub-resource collection name like "annotations".
		if transitionActions[last] {
			return last
		}
		// Handle ":push" / ":upload" colon-suffix on the last segment
		// (slice 013 / 036 pattern).
		if i := strings.LastIndex(last, ":"); i >= 0 && i+1 < len(last) {
			suffix := last[i+1:]
			if transitionActions[suffix] {
				return suffix
			}
		}
	}
	return "write"
}

// resourceFromPath extracts (resource_type, resource_id) from a /v1/...
// path. The first segment after /v1/ is the type; if the second
// segment is a UUID-shaped token it's the id. Sub-resources fold into
// the same type (POST /v1/risks/{id}/themes is still type="risks").
//
// Non-/v1 paths (/auth/*, /health) return empty type -- those should
// not reach this function because the middleware skips them.
func resourceFromPath(path string) (string, string) {
	// Strip leading slash.
	p := strings.TrimPrefix(path, "/")
	segments := strings.Split(p, "/")
	if len(segments) < 2 {
		return "", ""
	}
	if segments[0] != "v1" && segments[0] != "auth" {
		return segments[0], ""
	}
	if segments[0] == "auth" {
		return "auth", ""
	}
	resource := segments[1]
	// Strip ":push" / ":upload" suffix from the resource name.
	if i := strings.Index(resource, ":"); i >= 0 {
		resource = resource[:i]
	}
	resourceID := ""
	if len(segments) >= 3 {
		resourceID = segments[2]
	}
	return resource, resourceID
}

// RolesResolver is an optional interface for DB-backed role lookup.
// When set on the Engine via NewEngine, BuildInput is followed by a
// pass through this resolver to merge user_roles table rows. v1
// connector tests can pass NoopRolesResolver to skip the DB hop.
type RolesResolver interface {
	RolesFor(ctx context.Context, tenantID, userID string) ([]Role, error)
}

// NoopRolesResolver returns no DB-side roles. Used in unit tests and
// in deployments where every credential's role is fully derived from
// its flags.
type NoopRolesResolver struct{}

// RolesFor implements RolesResolver. Always returns nil, nil.
func (NoopRolesResolver) RolesFor(_ context.Context, _ /*tenantID*/, _ /*userID*/ string) ([]Role, error) {
	return nil, nil
}
