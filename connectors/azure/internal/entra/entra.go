// Package entra pulls Microsoft Entra ID directory-role / RBAC assignments and
// the service-principal / app-registration inventory needed to interpret them,
// producing one Assignment per (principal, role, scope).
//
// Source: read-only Microsoft Graph (Directory.Read.All + Application.Read.All).
// The connector emits configuration + assignment metadata only — NEVER mailbox /
// profile PII beyond the display name needed to name the assignment.
//
// Output is descriptive: the platform evaluator (slice 015) interprets which
// assignment pattern passes/fails per (control, scope). The connector emits
// Result_INCONCLUSIVE so we don't bake policy into the pipe.
package entra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PrincipalType enumerates the kinds of principal a role can be assigned to.
const (
	PrincipalUser             = "user"
	PrincipalServicePrincipal = "servicePrincipal"
	PrincipalGroup            = "group"
	PrincipalUnknown          = "unknown"
)

// Assignment is one record the cmd layer turns into an evidence record. Field
// names map 1:1 to azure.entra_role_assignment.v1 schema.
type Assignment struct {
	AssignmentID         string
	PrincipalID          string
	PrincipalType        string
	PrincipalDisplayName string
	RoleDefinitionID     string
	RoleDisplayName      string
	DirectoryScopeID     string
	IsPrivileged         bool
	TenantID             string
	ObservedAt           time.Time
}

// RawAssignment is the narrow view the API surface returns for one role
// assignment. The concrete Graph client maps the SDK response into this shape;
// tests construct it directly. No secret, no mailbox/profile PII.
type RawAssignment struct {
	ID                   string
	PrincipalID          string
	PrincipalType        string
	PrincipalDisplayName string
	RoleDefinitionID     string
	RoleDisplayName      string
	DirectoryScopeID     string
}

// API is the narrow surface Pull depends on. The concrete implementation wraps
// the Microsoft Graph SDK; tests pass a fake. v0 lists the first bounded page;
// cursor pagination is a documented follow-on (threat-model D: bounded page +
// run timeout cap a large tenant).
type API interface {
	ListRoleAssignments(ctx context.Context) ([]RawAssignment, error)
}

// privilegedRoles is the connector-side heuristic set of known high-privilege
// Entra directory roles. Used only to set the descriptive is_privileged flag;
// the evaluator makes the actual policy call.
var privilegedRoles = map[string]bool{
	"global administrator":            true,
	"privileged role administrator":   true,
	"privileged authentication admin": true,
	"security administrator":          true,
	"user administrator":              true,
	"application administrator":       true,
	"cloud application administrator": true,
}

// Pull lists every visible role assignment and normalizes it. tenantID scopes
// every record. now is injectable for deterministic tests (nil → time.Now UTC).
func Pull(ctx context.Context, api API, tenantID string, now func() time.Time) ([]Assignment, error) {
	if api == nil {
		return nil, errors.New("entra: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListRoleAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list role assignments: %w", err)
	}
	observedAt := now()
	out := make([]Assignment, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.PrincipalID == "" || r.RoleDefinitionID == "" {
			// Schema requires these; skip rather than emit invalid records.
			continue
		}
		out = append(out, Assignment{
			AssignmentID:         r.ID,
			PrincipalID:          r.PrincipalID,
			PrincipalType:        normalizePrincipalType(r.PrincipalType),
			PrincipalDisplayName: r.PrincipalDisplayName,
			RoleDefinitionID:     r.RoleDefinitionID,
			RoleDisplayName:      r.RoleDisplayName,
			DirectoryScopeID:     defaultScope(r.DirectoryScopeID),
			IsPrivileged:         privilegedRoles[strings.ToLower(strings.TrimSpace(r.RoleDisplayName))],
			TenantID:             tenantID,
			ObservedAt:           observedAt,
		})
	}
	return out, nil
}

func normalizePrincipalType(s string) string {
	switch strings.TrimSpace(s) {
	case PrincipalUser, PrincipalServicePrincipal, PrincipalGroup:
		return s
	default:
		return PrincipalUnknown
	}
}

func defaultScope(s string) string {
	if strings.TrimSpace(s) == "" {
		return "/"
	}
	return s
}
