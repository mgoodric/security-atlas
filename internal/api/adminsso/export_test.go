package adminsso

import (
	"context"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// WithAuditInsert returns a copy of h whose audit write is fn, so a test can
// make it fail and prove the SSO change rolls back with it (#2609).
func (h *Handler) WithAuditInsert(fn func(context.Context, *dbx.Queries, dbx.InsertMeAuditLogParams) error) *Handler {
	c := *h
	c.insertAudit = fn
	return &c
}
