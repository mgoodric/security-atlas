package authz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
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
// The INSERT runs inside a transaction that first applies the
// `app.current_tenant` GUC via tenancy.ApplyTenant. This is mandatory,
// not optional: decision_audit_log carries FORCE ROW LEVEL SECURITY with
// a `tenant_write` policy whose `WITH CHECK (current_tenant_matches(
// tenant_id))` is evaluated against that GUC. tenancy.ApplyTenant only
// works on a pgx.Tx — outside a transaction `SET LOCAL` is silently
// inert and the GUC reads empty, so RLS rejects every row (see
// internal/tenancy/apply.go). Slice 065 bug #1 fixed exactly this: the
// writer previously called pool.Exec OUTSIDE a transaction, so every
// authenticated request 500'd on the post-decision audit write. The
// explicit Begin + defer Rollback + ApplyTenant + Exec + Commit shape
// matches the per-store `inTx` helpers used elsewhere in the codebase.
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

	// user_roles and policy_hits are both `TEXT[] NOT NULL` columns. The
	// INSERT names them explicitly, so the column DEFAULT '{}' never
	// applies — a nil Go slice would be sent as SQL NULL and trip the
	// NOT NULL constraint. roleStrings is non-nil by construction
	// (make() returns a non-nil empty slice); policyHits is normalised
	// the same way so a record with no policy hits writes an empty array.
	roleStrings := make([]string, len(rec.UserRoles))
	for i, r := range rec.UserRoles {
		roleStrings[i] = string(r)
	}
	policyHits := rec.PolicyHits
	if policyHits == nil {
		policyHits = []string{}
	}

	id := uuid.New()
	// Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
	// the DB layer; explicit-is-clearer per AC-5).
	const stmt = `
		INSERT INTO decision_audit_log
			(decision_id, tenant_id, user_id, user_roles,
			 action, resource_type, resource_id,
			 result, reason, policy_hits,
			 request_path, request_method, subject_module)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'core')
	`

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("authz: begin audit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Apply the `app.current_tenant` GUC on THIS transaction so the
	// decision_audit_log.tenant_write RLS WITH CHECK can match tenant_id.
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return uuid.Nil, fmt.Errorf("authz: apply tenant to audit tx: %w", err)
	}

	if _, err := tx.Exec(ctx, stmt,
		id, tenantUUID, rec.UserID, roleStrings,
		rec.Action, rec.ResourceType, rec.ResourceID,
		rec.Result, rec.Reason, policyHits,
		rec.RequestPath, rec.RequestMethod,
	); err != nil {
		return uuid.Nil, fmt.Errorf("authz: insert decision_audit_log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("authz: commit audit tx: %w", err)
	}
	return id, nil
}
