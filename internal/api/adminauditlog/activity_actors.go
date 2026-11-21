package adminauditlog

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// ActivityActorsResponse is the GET /v1/activity/actors wire shape.
type ActivityActorsResponse struct {
	// Machine and system actor ids (`system`, `system:<process>`,
	// `oauth_client:<id>`), sorted. Never a person or API key: those
	// are listed from /v1/people.
	Actors []string `json:"actors"`
	// Truncated is true when the window held more than machineActorCap
	// actors the caller may see (#2627). Actors then holds the first
	// machineActorCap of them in sort order; a narrower window lists
	// them all.
	Truncated bool `json:"truncated"`
}

// machineActorCap bounds the actors one request lists.
// ListUnifiedAuditMachineActors returns one row more than this, so a
// 1001st row marks the response truncated.
const machineActorCap = 1000

// ActivityActors handles GET /v1/activity/actors (#2414): the machine and
// system actors in the caller's activity for a `from`/`to` window, so the
// Activity page's actor picker can offer them by name.
//
// It reads what GET /v1/activity/unified would show the same caller: the
// same window bounds, the slice 270 privilege predicate, the slice 669
// read-telemetry default (`?include_reads=true` opts back in) and the
// #2173 module filter, which the query applies before the cap (#2718).
// RLS scopes every source to the caller's tenant. It lists identifiers,
// not rows, so it writes no meta-audit row.
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
		moduleaccess.WriteError(w, r, "activity module access", err)
		return
	}

	arg := dbx.ListUnifiedAuditMachineActorsParams{
		FromTs:               pgtype.Timestamptz{Time: params.queryParams.From, Valid: true},
		ToTs:                 pgtype.Timestamptz{Time: params.queryParams.To, Valid: true},
		CallerIsPrivileged:   privileged,
		ExcludeReadTelemetry: !params.includeReads,
	}
	moduleRouteFilter(&arg, modules)
	var actors []string
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		var qErr error
		actors, qErr = q.ListUnifiedAuditMachineActors(ctx, arg)
		return qErr
	})
	if err != nil {
		httperr.WriteInternal(w, r, "activity actors", err)
		return
	}
	truncated := len(actors) > machineActorCap
	if truncated {
		actors = actors[:machineActorCap]
	}
	if actors == nil {
		actors = []string{}
	}
	httpresp.WriteJSON(w, http.StatusOK, ActivityActorsResponse{
		Actors:    actors,
		Truncated: truncated,
	})
}

// moduleRouteFilter sets the arguments that carry the #2173 module filter
// (entrySeen) into ListUnifiedAuditMachineActors: the kinds whose module
// set hides, and every registry route with its owner and whether set
// shows that owner, so the query can place a decision's route as
// featureflag.Resolve does. The slices are never nil: a NULL array would
// make `kind = ANY(...)` NULL and drop every row.
func moduleRouteFilter(arg *dbx.ListUnifiedAuditMachineActorsParams, set moduleaccess.ModuleSet) {
	arg.HiddenKinds = []string{}
	for kind, route := range kindModuleRoute {
		if !set.Sees(route) {
			arg.HiddenKinds = append(arg.HiddenKinds, string(kind))
		}
	}
	arg.RoutePatterns, arg.RouteOwners, arg.RouteVisible = []string{}, []string{}, []bool{}
	add := func(o featureflag.Owner, owner string, patterns []string) {
		for _, p := range patterns {
			arg.RoutePatterns = append(arg.RoutePatterns, p)
			arg.RouteOwners = append(arg.RouteOwners, owner)
			arg.RouteVisible = append(arg.RouteVisible, set.SeesOwner(o))
		}
	}
	for _, m := range featureflag.Modules {
		add(featureflag.Owner{Module: m}, m.Key, m.Routes)
		for j := range m.Features {
			f := &m.Features[j]
			add(featureflag.Owner{Module: m, Feature: f}, m.Key+"/"+f.Key, f.Routes)
		}
	}
}
