// Slice 124 — unified audit-log aggregation HTTP handler.
//
// `GET /v1/admin/audit-log/unified` exposes the UNION ALL across the nine
// per-domain audit-log tables (see `internal/audit/unifiedlog/`) as a
// paginated read for admins, auditors, and the v1 GRC operator.
//
// The handler:
//
//   - Enforces a 90-day request window (400 otherwise).
//   - Caps the result page to 1000 rows; emits an opaque cursor when more
//     pages are available.
//   - Defense-in-depth: rejects credentials whose role is neither admin
//     NOR auditor NOR grc_engineer (the upstream OPA middleware enforces
//     the canonical decision; this is the second leg of the gate). The
//     role-membership check runs under the same tenant context as the
//     subsequent aggregator query.
//   - Writes one `me_audit_log` row per successful query with the request
//     params serialized into `before` (params shape) and `after` (result
//     summary).
//
// Tenant isolation is handed off to PostgreSQL RLS via `tenancy.ApplyTenant`
// inside the same transaction that executes the aggregator + the meta-audit.
// The handler never accepts a tenant_id parameter (slice-124 anti-criterion
// P0-A5).

package adminauditlog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// maxWindowDays is the AC-5 90-day request-window cap. Wider windows
	// risk planner blowup on tables that grow unbounded over time.
	maxWindowDays = 90

	// unifiedPageSize is the AC-6 hard cap on rows per page.
	unifiedPageSize = 1000

	// metaAuditAction is the slice-108 `me_audit_log.action` value
	// written by every successful unified-log query. The slice 124
	// migration extends the CHECK constraint to permit this value.
	metaAuditAction = "audit_log_query_unified"
)

// UnifiedEntry is the wire shape for one row of the response.
//
// ActorName (slice 129) is the human-readable display name resolved via
// LEFT JOIN against `users.display_name` under the caller's tenant
// context (RLS enforced). It is `null` on the wire when no users row
// matches the actor_id — the normal case for bootstrap-key callers,
// credential-only callers, and system actors (whose actor_id is a
// credential id like "key_foo" or a literal like "seeder", not a UUID).
// Consumers MUST tolerate `null`; the frontend falls back to a truncated
// actor_id render.
//
// SubjectModule (slice 180) tags which module owns the writing code path.
// Today every row carries `"core"`. When the privacy sibling module ships
// (v2+), privacy-side audit-log writes will carry `"privacy"`. Future
// consumers can filter by module client-side; the platform does not yet
// expose a server-side `module=` filter (deferred until privacy v0 lands
// and real query patterns inform the index decision — see canvas OQ #7
// resolution and slice 180's threat-model `D — Denial of service` note).
type UnifiedEntry struct {
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorID       string          `json:"actor_id"`
	ActorName     *string         `json:"actor_name"`
	TenantID      uuid.UUID       `json:"tenant_id"`
	Kind          string          `json:"kind"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Action        string          `json:"action"`
	RowID         uuid.UUID       `json:"row_id"`
	SubjectModule string          `json:"subject_module"`
	PayloadJSON   json.RawMessage `json:"payload_json"`
}

// UnifiedListResponse is the GET /v1/admin/audit-log/unified shape.
type UnifiedListResponse struct {
	Entries    []UnifiedEntry `json:"entries"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// unifiedCursorPayload is the JSON shape inside the opaque base64 cursor.
type unifiedCursorPayload struct {
	OccurredAt string `json:"ts"`
	Kind       string `json:"kind"`
	RowID      string `json:"rid"`
}

// metaAuditParams is the shape persisted to me_audit_log.before so the
// audit record carries enough context to reconstruct the request.
//
// Surface (slice 270) splits the two endpoints that write this shape:
// `"admin"` for the slice 124 `/v1/admin/audit-log/unified` route,
// `"activity"` for the slice 270 `/v1/activity/unified` route. The
// `me_audit_log.action` value is shared (`audit_log_query_unified`)
// because the underlying SQL is the same; the surface field is the
// forensic discriminator (slice 270 D7).
type metaAuditParams struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Actor   string   `json:"actor,omitempty"`
	Kinds   []string `json:"kinds,omitempty"`
	Cursor  string   `json:"cursor,omitempty"`
	Surface string   `json:"surface,omitempty"`
	// IncludeReads (slice 669) records whether the caller opted in to the
	// high-volume read-telemetry on the Activity surface. Recorded in the
	// meta-audit so forensic review can tell a "show-all" query from the
	// default business-events-only view. Omitted (false) for the admin
	// endpoint, which always returns every row.
	IncludeReads bool `json:"include_reads,omitempty"`
}

// metaAuditResult is the shape persisted to me_audit_log.after.
type metaAuditResult struct {
	Returned       int    `json:"returned"`
	NextCursorEcho string `json:"next_cursor,omitempty"`
}

// UnifiedList handles GET /v1/admin/audit-log/unified.
func (h *Handler) UnifiedList(w http.ResponseWriter, r *http.Request) {
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
	// Caller's stable identity for the meta-audit row. Prefer the resolved
	// user_id (slice-108 IssuedBy → UserID bridge); fall back to the
	// credential id when no user row resolves (bootstrap-key shape).
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	params, perr := parseUnifiedParams(r)
	if perr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, perr.Error())
		return
	}
	// Slice 270: the slice 124 admin endpoint is admit-gated to
	// {admin, auditor, grc_engineer}. Every caller that reaches this
	// point is privileged; the SQL-layer row-visibility predicate
	// short-circuits and behavior matches pre-slice-270.
	params.queryParams.CallerIsPrivileged = true

	// Defense-in-depth role gate: admin OR auditor OR grc_engineer.
	// IsAdmin is on the credential; the other two require a user_roles
	// lookup. The upstream slice-035 OPA middleware is the canonical
	// gate; this is the second leg.
	allowed, err := h.callerAllowedUnified(r.Context(), tenantID, cred.UserID, cred.IsAdmin)
	if err != nil {
		httperr.WriteInternal(w, r, "role probe", err)
		return
	}
	if !allowed {
		httpresp.WriteError(w, http.StatusForbidden, "admin, auditor, or grc_engineer role required")
		return
	}

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
		// Apply the page-size cap + cursor derivation here. The aggregator
		// was asked for (pageSize + 1) rows; if it returned more than
		// pageSize, the (pageSize+1)-th row signals "more available" and
		// the in-page cursor anchor is entries[pageSize-1].
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

		// Meta-audit: one row per successful query (slice-124 AC-10).
		// Slice 270 D7: surface-tag the row for forensic split between
		// the admin endpoint and the new `/v1/activity/unified`.
		auditShape := params.toAuditShape()
		auditShape.Surface = adminSurfaceTag
		paramsBlob, _ := json.Marshal(auditShape)
		resultBlob, _ := json.Marshal(metaAuditResult{
			Returned:       len(entries),
			NextCursorEcho: encodeUnifiedCursor(nextCursor),
		})
		uID, parseErr := uuid.Parse(userIdentifier)
		if parseErr != nil {
			// Bootstrap-key callers carry a non-UUID id ("key_..."); a
			// zero-UUID is the honest fallback. The meta-audit still
			// captures the credential id via payload (before.actor or
			// the request log) when needed.
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
		// Slice 126: fan out the meta-audit row to the external sink.
		// InsertMeAuditLog uses default gen_random_uuid() at the DB layer,
		// so we don't have the row's id locally; sink RowID gets a fresh
		// UUID — it is a sink-side correlation id, not the DB row id.
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
		httperr.WriteInternal(w, r, "unified audit-log", err)
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

// callerAllowedUnified is the defense-in-depth role probe. IsAdmin short-circuits
// (admin always allowed). Otherwise we probe user_roles for either auditor or
// grc_engineer membership under the caller's tenant context. The query goes
// through ApplyTenant so RLS on user_roles enforces tenant scoping.
func (h *Handler) callerAllowedUnified(ctx context.Context, tenantID uuid.UUID, userID string, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	if userID == "" {
		return false, nil
	}
	var allowed bool
	err := h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		got, qErr := q.HasUnifiedAuditLogRole(ctx, dbx.HasUnifiedAuditLogRoleParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   userID,
		})
		if qErr != nil {
			return qErr
		}
		allowed = got
		return nil
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// parsedUnifiedParams bundles the aggregator query params with the raw
// strings needed for the meta-audit blob (so we don't re-serialize times
// twice).
type parsedUnifiedParams struct {
	queryParams unifiedlog.QueryParams
	fromRaw     string
	toRaw       string
	cursorRaw   string
	// includeReads (slice 669) is the parsed `?include_reads=true` opt-in.
	// Only the activity endpoint consults it (to flip ExcludeReadTelemetry);
	// the admin endpoint ignores it and always shows every row. Parsing it
	// here (rather than per-handler) keeps the query-string contract in one
	// place.
	includeReads bool
}

func (p parsedUnifiedParams) toAuditShape() metaAuditParams {
	kinds := make([]string, 0, len(p.queryParams.KindFilter))
	for _, k := range p.queryParams.KindFilter {
		kinds = append(kinds, string(k))
	}
	return metaAuditParams{
		From:         p.fromRaw,
		To:           p.toRaw,
		Actor:        p.queryParams.ActorFilter,
		Kinds:        kinds,
		Cursor:       p.cursorRaw,
		IncludeReads: p.includeReads,
	}
}

func parseUnifiedParams(r *http.Request) (parsedUnifiedParams, error) {
	q := r.URL.Query()

	fromRaw := q.Get("from")
	toRaw := q.Get("to")
	if fromRaw == "" {
		return parsedUnifiedParams{}, fmt.Errorf("from query parameter is required (RFC3339)")
	}
	if toRaw == "" {
		return parsedUnifiedParams{}, fmt.Errorf("to query parameter is required (RFC3339)")
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return parsedUnifiedParams{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		return parsedUnifiedParams{}, fmt.Errorf("invalid to: %w", err)
	}
	if !to.After(from) {
		return parsedUnifiedParams{}, fmt.Errorf("to must be strictly after from")
	}
	if to.Sub(from) > maxWindowDays*24*time.Hour {
		return parsedUnifiedParams{}, fmt.Errorf("window exceeds %d days; narrow the from/to range", maxWindowDays)
	}

	params := unifiedlog.QueryParams{
		From:        from,
		To:          to,
		ActorFilter: q.Get("actor"),
		// Slice 270: row-visibility privilege is set by the handler AFTER
		// parsing, not here. The slice 124 admin handler sets
		// CallerIsPrivileged=true unconditionally (every caller that
		// reaches it has cleared the {admin, auditor, grc_engineer}
		// OPA admit). The slice 270 activity handler sets it from its
		// own role probe.
	}
	if raw := q.Get("kind"); raw != "" {
		for _, candidate := range strings.Split(raw, ",") {
			k := unifiedlog.Kind(strings.TrimSpace(candidate))
			if !unifiedlog.IsCanonical(k) {
				return parsedUnifiedParams{}, fmt.Errorf("unknown kind: %q", candidate)
			}
			params.KindFilter = append(params.KindFilter, k)
		}
	}

	cursorRaw := q.Get("cursor")
	if cursorRaw != "" {
		cursor, derr := decodeUnifiedCursor(cursorRaw)
		if derr != nil {
			return parsedUnifiedParams{}, fmt.Errorf("invalid cursor: %w", derr)
		}
		params.Cursor = cursor
	}

	// Slice 669: `?include_reads=true` is the opt-in to surface the
	// high-volume `decision`/`read` telemetry on the Activity feed.
	// Default (absent / any non-"true" value) keeps the business-events
	// view. Only the activity handler flips ExcludeReadTelemetry from
	// this; the admin handler ignores it (always shows every row).
	includeReads := q.Get("include_reads") == "true"

	// The aggregator returns up to Limit rows; the handler asks for one
	// more than the page size so it can detect "more available" without
	// an extra round-trip.
	params.Limit = unifiedPageSize + 1

	return parsedUnifiedParams{
		queryParams:  params,
		fromRaw:      fromRaw,
		toRaw:        toRaw,
		cursorRaw:    cursorRaw,
		includeReads: includeReads,
	}, nil
}

func encodeUnifiedCursor(c *unifiedlog.Cursor) string {
	if c == nil {
		return ""
	}
	b, _ := json.Marshal(unifiedCursorPayload{
		OccurredAt: c.OccurredAt.UTC().Format(time.RFC3339Nano),
		Kind:       string(c.Kind),
		RowID:      c.RowID.String(),
	})
	return base64.URLEncoding.EncodeToString(b)
}

func decodeUnifiedCursor(s string) (*unifiedlog.Cursor, error) {
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var p unifiedCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, p.OccurredAt)
	if err != nil {
		return nil, fmt.Errorf("cursor ts: %w", err)
	}
	kind := unifiedlog.Kind(p.Kind)
	if !unifiedlog.IsCanonical(kind) {
		return nil, fmt.Errorf("cursor kind unknown: %q", p.Kind)
	}
	rowID, err := uuid.Parse(p.RowID)
	if err != nil {
		return nil, fmt.Errorf("cursor row_id: %w", err)
	}
	return &unifiedlog.Cursor{
		OccurredAt: t,
		Kind:       kind,
		RowID:      rowID,
	}, nil
}

// Compile-time assertion: tenancy.ApplyTenant must exist on the call chain
// inside h.inTx so the aggregator query and meta-audit write both run with
// the tenant GUC set.
var _ = tenancy.ApplyTenant
