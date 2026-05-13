package authz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditWriter persists Decision rows to decision_audit_log. The writer
// expects the calling context to carry the tenant id (via
// tenancy.WithTenant) so RLS allows the INSERT.
type AuditWriter struct {
	pool *pgxpool.Pool
}

// NewAuditWriter constructs an AuditWriter against pool.
func NewAuditWriter(pool *pgxpool.Pool) *AuditWriter {
	return &AuditWriter{pool: pool}
}

// AuditRecord captures the fields written for one decision.
type AuditRecord struct {
	TenantID      string
	UserID        string
	UserRoles     []Role
	Action        string
	ResourceType  string
	ResourceID    string
	Result        string // "allow" | "deny"
	Reason        string
	PolicyHits    []string
	RequestPath   string
	RequestMethod string
}

// Write persists rec to decision_audit_log under the tenant context in
// ctx. Returns the generated decision_id.
//
// Error semantics: callers MUST inspect the error. The middleware
// elevates a Write failure to HTTP 500 on the allow path (an unaudited
// allow violates anti-criterion P0). On the deny path the middleware
// still returns 403 to the user, but logs the Write error at ERROR.
func (w *AuditWriter) Write(ctx context.Context, rec AuditRecord) (uuid.UUID, error) {
	if w == nil || w.pool == nil {
		return uuid.Nil, fmt.Errorf("authz: audit writer not configured")
	}
	if rec.TenantID == "" {
		return uuid.Nil, fmt.Errorf("authz: audit record missing tenant_id")
	}
	tenantUUID, err := uuid.Parse(rec.TenantID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("authz: audit tenant_id parse: %w", err)
	}
	if rec.UserID == "" {
		// CHECK constraint forbids empty user_id. Substitute a
		// stable sentinel so a credential-less request (which should
		// have been blocked upstream) still leaves an audit trail.
		rec.UserID = "anonymous"
	}
	if rec.Action == "" {
		rec.Action = "unknown"
	}
	if rec.ResourceType == "" {
		rec.ResourceType = "unknown"
	}
	if rec.Result != "allow" && rec.Result != "deny" {
		return uuid.Nil, fmt.Errorf("authz: audit result must be allow|deny, got %q", rec.Result)
	}

	roleStrings := make([]string, len(rec.UserRoles))
	for i, r := range rec.UserRoles {
		roleStrings[i] = string(r)
	}

	id := uuid.New()
	const stmt = `
		INSERT INTO decision_audit_log
			(decision_id, tenant_id, user_id, user_roles,
			 action, resource_type, resource_id,
			 result, reason, policy_hits,
			 request_path, request_method)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err = w.pool.Exec(ctx, stmt,
		id, tenantUUID, rec.UserID, roleStrings,
		rec.Action, rec.ResourceType, rec.ResourceID,
		rec.Result, rec.Reason, rec.PolicyHits,
		rec.RequestPath, rec.RequestMethod,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("authz: insert decision_audit_log: %w", err)
	}
	return id, nil
}
