package authz

import "context"

// AttrsResolver is an optional hook that hydrates per-user ABAC
// attributes at decision time. The OPA `auditor` policy reads
// `input.user.attrs.audit_period_ids` to gate period-scoped reads and
// audit-note writes (slice 025); the resolver is what populates that
// attribute from the auditor_assignments table when the request reaches
// the authz middleware.
//
// Implementations are consulted by Engine.Decide only when the request
// carries the auditor role AND no `audit_period_ids` is already present
// in the input's user.attrs map. Both conditions matter:
//
//   - Auditor-only: the resolver hits the DB once per matching request,
//     so non-auditor traffic (the 99% case) skips it entirely. The
//     check is `RoleAuditor in user.roles` -- one slice scan.
//
//   - Attrs-absent: tests can pre-populate attrs directly (the
//     slice-035 matrix tests do exactly this), and the resolver MUST
//     NOT overwrite them. This is the no-op opt-in pattern.
//
// AttrsFor returns the attributes to merge into the request's user.attrs
// map. The only key v1 cares about is `audit_period_ids`; future ABAC
// dimensions can extend the contract additively.
//
// Implementations MUST be safe to call concurrently -- the authz
// middleware runs in HTTP request handlers and each request gets its
// own goroutine.
type AttrsResolver interface {
	AttrsFor(ctx context.Context, tenantID, userID string, roles []Role) (map[string]interface{}, error)
}

// NoopAttrsResolver returns no attributes. Used in tests and in
// deployments where no resolver is required. Engine.Decide treats nil
// the same as NoopAttrsResolver -- the absence of attrs leaves the
// existing input untouched.
type NoopAttrsResolver struct{}

// AttrsFor implements AttrsResolver. Always returns nil, nil.
func (NoopAttrsResolver) AttrsFor(_ context.Context, _, _ string, _ []Role) (map[string]interface{}, error) {
	return nil, nil
}

// hasAuditorRole reports whether the role list contains the auditor
// role. Used by Engine.Decide to skip the resolver call entirely for
// non-auditor traffic.
func hasAuditorRole(roles []Role) bool {
	for _, r := range roles {
		if r == RoleAuditor {
			return true
		}
	}
	return false
}
