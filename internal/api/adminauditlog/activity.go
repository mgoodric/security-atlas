// Slice 270 — non-admin activity-ledger HTTP handler.
//
// `GET /v1/activity/unified` is the non-admin / any-tenant-member surface
// over the slice-124 unified audit-log aggregator. It exposes the same
// shape as `/v1/admin/audit-log/unified` (see [Handler.UnifiedList]) but
// with two behavioural differences enforced at the SQL layer:
//
//   - For privileged callers (admin / auditor / grc_engineer — the same
//     three-role set slice 124 admits), the result set is identical to
//     `/v1/admin/audit-log/unified` (the row-visibility WHERE predicate
//     short-circuits when `caller_is_privileged = true`).
//   - For non-privileged callers (viewer / control_owner — any other
//     authenticated tenant member), the result set is restricted to:
//     (a) tenant-public kinds (decision, evidence, exception, sample,
//     audit_period, aggregation_rule, walkthrough — i.e. everything
//     except feature_flag, which is admin-only program configuration),
//     AND (b) me-rows whose `actor_id` equals the caller's user_id
//     (the caller's own self-audit trail; admins / other operators'
//     personal actions are hidden).
//
// The discriminator lives in the WHERE predicate, NOT in the URL filter
// the caller controls — slice 270 P0-A5 (filter-combination authz
// independence). A non-privileged caller passing `?actor=<admin-uuid>`
// gets zero rows on the me-row branch because the SQL's
// `actor_id = caller_user_id` predicate conjoins with the URL's
// `actor_filter`.
//
// The route shares the slice 124 aggregator (`unifiedlog.Query`), the
// slice 124 meta-audit pattern (one `me_audit_log` row per query with
// `action = 'audit_log_query_unified'`), and the slice 124 sink emit
// pattern. The differentiator is in `metaAuditParams.Surface` —
// `"activity"` here vs `"admin"` on slice 124's endpoint — so forensic
// review can split queries by surface without an additional action
// value (no `me_audit_log.action` CHECK extension needed; honors
// slice 270 P0-A3).
//
// OPA admit-set: the route is admitted to {admin, auditor, grc_engineer,
// viewer, control_owner} via `resource.type = "activity"` — the same
// resource type slice 156 added for the dashboard's `/v1/activity`
// activity-feed panel. Admins and grc_engineers admit via wildcard
// reads; viewer / control_owner / auditor admit via the existing
// `"activity"` enumeration. Slice 270 adds NO new OPA resource-type
// symbol — the new route mounts under the existing admit (slice 270 D1).
// Pin: `internal/authz/slice270_test.go::TestSlice270_ActivityUnifiedOPAAdmit`.
//
// Tenant isolation is identical to slice 124: handed off to PostgreSQL
// RLS via `tenancy.ApplyTenant` inside the same transaction that
// executes the aggregator + the meta-audit. The handler never accepts
// a tenant_id parameter.

package adminauditlog

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// activitySurfaceTag is the value written into `metaAuditParams.Surface`
// by the slice 270 endpoint, distinguishing it from the slice 124 admin
// endpoint's `"admin"` value. Reused in the slice 270 meta-audit row +
// the sink emit so forensic review can split queries by surface.
const activitySurfaceTag = "activity"

// adminSurfaceTag is the corresponding value the slice 124 endpoint
// emits. Exported package-internal so the slice 124 handler can pick
// the right tag without duplicating the string literal.
const adminSurfaceTag = "admin"

// ActivityList handles GET /v1/activity/unified.
//
// Behavioural delta vs UnifiedList:
//
//   - No defense-in-depth role gate — the OPA middleware at the route
//     layer is the authoritative admit (slice 270 widens the admit-set
//     to {admin, auditor, grc_engineer, viewer, control_owner} via the
//     `activity-unified` resource type).
//   - The role probe runs to derive `CallerIsPrivileged`, NOT to gate
//     the request. Privileged callers (admin / auditor / grc_engineer)
//     see the full slice-124-shape result; non-privileged callers
//     (viewer / control_owner) see the restricted shape.
//   - The meta-audit row + sink emit carry `surface="activity"` instead
//     of the slice 124's `surface="admin"`.
func (h *Handler) ActivityList(w http.ResponseWriter, r *http.Request) {
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
	// Caller's stable identity for both the SQL-layer row-visibility
	// predicate AND the meta-audit row. Prefer the resolved user_id
	// (slice-108 IssuedBy → UserID bridge); fall back to the credential
	// id when no user row resolves (bootstrap-key shape).
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	params, perr := parseUnifiedParams(r)
	if perr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, perr.Error())
		return
	}

	// Slice 270 D1: derive CallerIsPrivileged from the same role probe
	// slice 124's defense-in-depth gate uses. The probe runs to
	// CLASSIFY the caller, not to deny — non-privileged callers reach
	// this handler legitimately via the widened OPA admit. Setting
	// CallerIsPrivileged=true short-circuits the SQL row-visibility
	// predicate; CallerIsPrivileged=false enables the predicate
	// (hides feature_flag rows + restricts me-rows to the caller).
	privileged, err := h.callerAllowedUnified(r.Context(), tenantID, cred.UserID, cred.IsAdmin)
	if err != nil {
		httperr.WriteInternal(w, r, "role probe", err)
		return
	}
	params.queryParams.CallerIsPrivileged = privileged
	// CallerUserID is consumed by the SQL predicate ONLY when
	// CallerIsPrivileged=false. Setting it unconditionally keeps the
	// parameter shape uniform and protects against a future code path
	// that flips the bool without updating this assignment.
	params.queryParams.CallerUserID = userIdentifier

	// One transaction for both the aggregator read AND the meta-audit
	// write. Same `tenancy.ApplyTenant` context guards both. RLS on the
	// nine audit-log tables filters reads; the me_audit_log INSERT
	// policy filters the audit write.
	var (
		entries    []unifiedlog.Entry
		nextCursor *unifiedlog.Cursor
	)
	err = h.inTx(r.Context(), func(ctx context.Context, q *dbx.Queries) error {
		got, _, qErr := unifiedlog.Query(ctx, q, params.queryParams)
		if qErr != nil {
			return qErr
		}
		// Apply the page-size cap + cursor derivation here (mirrors
		// UnifiedList — the aggregator returns up to Limit rows, and
		// the handler asks for one more than the page size to detect
		// "more available" without an extra round-trip).
		if len(got) > unifiedPageSize {
			anchor := got[unifiedPageSize-1]
			nextCursor = &unifiedlog.Cursor{
				OccurredAt: anchor.OccurredAt,
				Kind:       anchor.Kind,
				RowID:      anchor.RowID,
			}
			got = got[:unifiedPageSize]
		}
		entries = got

		// Meta-audit: one row per successful query, surface-tagged so
		// forensic review can split slice 124 vs slice 270 traffic.
		auditShape := params.toAuditShape()
		auditShape.Surface = activitySurfaceTag
		paramsBlob, _ := json.Marshal(auditShape)
		resultBlob, _ := json.Marshal(metaAuditResult{
			Returned:       len(entries),
			NextCursorEcho: encodeUnifiedCursor(nextCursor),
		})
		uID, parseErr := uuid.Parse(userIdentifier)
		if parseErr != nil {
			// Bootstrap-key / credential-only callers carry a non-UUID
			// id; zero-UUID is the honest fallback (matches slice 124).
			uID = uuid.Nil
		}
		if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: uID, Valid: true},
			Action:   metaAuditAction,
			Before:   paramsBlob,
			After:    resultBlob,
		}); err != nil {
			return err
		}
		// Sink emit (slice 126 parity): InsertMeAuditLog uses the DB-
		// side gen_random_uuid() default, so we don't have the row's
		// id locally; sink RowID gets a fresh UUID — it is a sink-side
		// correlation id, not the DB row id.
		sinkPayload, _ := json.Marshal(map[string]any{
			"before": json.RawMessage(paramsBlob),
			"after":  json.RawMessage(resultBlob),
		})
		sink.EmitDefault(ctx, unifiedlog.Entry{
			OccurredAt:    time.Now().UTC(),
			ActorID:       userIdentifier,
			TenantID:      tenantID,
			Kind:          unifiedlog.KindMe,
			TargetType:    "user",
			TargetID:      uID.String(),
			Action:        metaAuditAction,
			RowID:         uuid.New(),
			SubjectModule: unifiedlog.SubjectModuleCore,
			PayloadJSON:   sinkPayload,
		})
		return nil
	})
	if err != nil {
		httperr.WriteInternal(w, r, "activity", err)
		return
	}

	out := make([]UnifiedEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, UnifiedEntry{
			OccurredAt:    e.OccurredAt,
			ActorID:       e.ActorID,
			ActorName:     e.ActorName,
			TenantID:      e.TenantID,
			Kind:          string(e.Kind),
			TargetType:    e.TargetType,
			TargetID:      e.TargetID,
			Action:        e.Action,
			RowID:         e.RowID,
			SubjectModule: e.SubjectModule,
			PayloadJSON:   e.PayloadJSON,
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, UnifiedListResponse{
		Entries:    out,
		NextCursor: encodeUnifiedCursor(nextCursor),
	})

}
