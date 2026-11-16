package auditnotes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxPeriodPeekBytes bounds how much of a POST /v1/audit-notes body the
// authz hydrator reads to find audit_period_id. A body longer than this is
// still served in full to the handler; the hydrator just finds no period in
// the prefix it read, and the write is denied for an auditor.
const maxPeriodPeekBytes = 1 << 20

// periodGetter is the one period.Store method the hydrator needs: a
// tenant-scoped lookup that returns period.ErrNotFound for an absent or
// cross-tenant id.
type periodGetter interface {
	Get(ctx context.Context, id uuid.UUID) (period.Period, error)
}

// PeriodResourceAttrs returns the authz resource-attrs hydrator for
// POST /v1/audit-notes (#2193). It sets input.resource.attrs.audit_period_id
// from the request body, so the auditor.rego write rule can compare it with
// the auditor's assigned periods. The id is set only when it names a period
// in the caller's tenant (tenancymw put the tenant on the request context,
// and periods.Get reads under RLS); a malformed body, a non-UUID id, or an
// unknown or other-tenant period leaves the attribute unset, and the rule
// denies. Every other request gets no attributes.
//
// The body is read up to maxPeriodPeekBytes and then put back in front of
// the unread remainder, so the handler decodes the same bytes.
func PeriodResourceAttrs(periods periodGetter) func(*http.Request) (map[string]any, error) {
	return func(r *http.Request) (map[string]any, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audit-notes" || r.Body == nil {
			return nil, nil
		}
		// No tenant means no credential: the decision default-denies, and
		// there is no tenant to look the period up in.
		if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
			return nil, nil
		}
		peek, err := io.ReadAll(io.LimitReader(r.Body, maxPeriodPeekBytes))
		r.Body = readCloser{io.MultiReader(bytes.NewReader(peek), r.Body), r.Body}
		if err != nil {
			return nil, fmt.Errorf("audit-notes period attrs: read body: %w", err)
		}

		var req struct {
			AuditPeriodID string `json:"audit_period_id"`
		}
		if json.Unmarshal(peek, &req) != nil {
			return nil, nil
		}
		id, err := uuid.Parse(req.AuditPeriodID)
		if err != nil {
			return nil, nil
		}
		if _, err := periods.Get(r.Context(), id); err != nil {
			if errors.Is(err, period.ErrNotFound) {
				return nil, nil
			}
			return nil, fmt.Errorf("audit-notes period attrs: get period: %w", err)
		}
		return map[string]any{"audit_period_id": id.String()}, nil
	}
}

// readCloser reads from the replayed body and closes the original one.
type readCloser struct {
	io.Reader
	io.Closer
}
