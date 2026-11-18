package adminauditlog

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// ActivityActorsResponse is the GET /v1/activity/actors wire shape.
type ActivityActorsResponse struct {
	// Machine and system actor ids (`system`, `system:<process>`,
	// `oauth_client:<id>`), sorted. Never a person or API key: those
	// are listed from /v1/people.
	Actors []string `json:"actors"`
}

// ActivityActors handles GET /v1/activity/actors (#2414): the machine and
// system actors in the caller's activity for a `from`/`to` window, so the
// Activity page's actor picker can offer them by name.
//
// It reads what GET /v1/activity/unified would show the same caller: the
// same window bounds, the slice 270 privilege predicate, the slice 669
// read-telemetry default (`?include_reads=true` opts back in) and the
// #2173 module filter. RLS scopes every source to the caller's tenant.
// It lists identifiers, not rows, so it writes no meta-audit row.
func (h *Handler) ActivityActors(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	params, perr := parseUnifiedParams(r)
	if perr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, perr.Error())
		return
	}
	privileged, err := h.callerAllowedUnified(r.Context(), tenantID, cred.UserID, authz.IsTenantAdmin(cred))
	if err != nil {
		httperr.WriteInternal(w, r, "role probe", err)
		return
	}
	viewer, err := h.views.For(r)
	var modules moduleaccess.ModuleSet
	if err == nil {
		modules, err = viewer.Modules()
	}
	if err != nil {
		httperr.WriteInternal(w, r, "activity module access", err)
		return
	}

	var rows []dbx.ListUnifiedAuditMachineActorsRow
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		var qErr error
		rows, qErr = q.ListUnifiedAuditMachineActors(ctx, dbx.ListUnifiedAuditMachineActorsParams{
			FromTs:               pgtype.Timestamptz{Time: params.queryParams.From, Valid: true},
			ToTs:                 pgtype.Timestamptz{Time: params.queryParams.To, Valid: true},
			CallerIsPrivileged:   privileged,
			ExcludeReadTelemetry: !params.includeReads,
		})
		return qErr
	})
	if err != nil {
		httperr.WriteInternal(w, r, "activity actors", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, ActivityActorsResponse{Actors: visibleActors(rows, modules)})
}

// visibleActors keeps each actor seen in at least one row the module
// filter lets through, deduplicated and sorted.
func visibleActors(rows []dbx.ListUnifiedAuditMachineActorsRow, modules moduleaccess.ModuleSet) []string {
	entries := make([]unifiedlog.Entry, 0, len(rows))
	for _, row := range rows {
		e := unifiedlog.Entry{
			ActorID:    row.ActorID,
			Kind:       unifiedlog.Kind(row.Kind),
			TargetType: row.TargetType,
		}
		if row.RequestPath != "" {
			e.PayloadJSON, _ = json.Marshal(map[string]string{"request_path": row.RequestPath})
		}
		entries = append(entries, e)
	}
	seen := map[string]bool{}
	actors := []string{}
	for _, e := range visibleEntries(entries, modules) {
		if !seen[e.ActorID] {
			seen[e.ActorID] = true
			actors = append(actors, e.ActorID)
		}
	}
	sort.Strings(actors)
	return actors
}
