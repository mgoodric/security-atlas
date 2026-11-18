// Package dashboardexport serves the slice 269 dashboard snapshot export
// endpoint — `GET /v1/dashboard/export?format=json|csv|xlsx`.
//
// The handler exports a point-in-time snapshot of the six dashboard
// panels (framework_posture, risks, freshness, drift, upcoming,
// activity) in three formats:
//
//	json  — single document `{snapshot_at, panels: {...}}`
//	csv   — zip with one CSV per panel
//	xlsx  — workbook with one sheet per panel
//
// "Snapshot" here means "the same view the dashboard renders RIGHT
// NOW". This is NOT a historical / point-in-time snapshot (that is
// slice 071's audit-period freezing surface, a different concern).
// Slice 269 P0-A3 makes this explicit: the endpoint MUST NOT accept
// any historical / at-time parameter.
//
// # Design — composing existing reads
//
// Slice 269 P0-A1 anti-criterion: the export MUST NOT add new
// dashboard panels — only export the existing six. Slice 269 P0-A2:
// MUST NOT bypass per-panel RLS. Both are honoured here by composing
// the existing per-panel stores:
//
//   - framework_posture, upcoming, activity → `internal/api/dashboard`
//     (slice 066)
//   - freshness, drift → `internal/freshness` + `internal/drift`
//     (slice 016)
//   - risks → `internal/risk` (slice 019 List, slice 066 AC-3
//     sort=residual,age — same shape `/v1/risks?treatment=mitigate
//     &sort=residual,age` BFF uses for the risks panel)
//
// Each underlying store opens its own short-lived transaction with
// `tenancy.ApplyTenant` so RLS is enforced at the DB layer. No new
// queries; no schema migration touches a data table. The only schema
// change in this slice is extending `me_audit_log.action` CHECK to
// permit `dashboard_export` (migration
// `20260524000000_dashboard_export_meta_audit.sql`).
//
// # Format generators
//
// The slice 135 export library (`internal/export/`) ships single-table
// CSV / JSON / XLSX encoders. The dashboard export is multi-panel by
// definition, so the format generators live HERE rather than in the
// shared library. They are intentionally minimal:
//
//   - JSON: a single bytes.Buffer encode of the snapshot struct.
//     The total payload is small (six aggregated panels, bounded by
//     v1 dashboard rendering caps); buffered encode is the simplest
//     correct shape and stays well under the 200 MB AC-10 ceiling.
//
//   - CSV: archive/zip streamed to the response, one `<panel>.csv`
//     per panel. Each panel's rows are written with the OWASP
//     cell-injection mitigation copied from `internal/export/`.
//
//   - XLSX: handcrafted minimal Office Open XML (one workbook, one
//     sheet per panel, inline-string cells). Same construction
//     posture as `internal/export/xlsx.go` — text-only, no charts,
//     no named ranges, no VBA (slice 269 inherits slice 135 P0-A6).
//
// # Role gate
//
// #2350 (Matt, 2026-11-15): the export is gated by OPA, on read of
// the `dashboard` resource, the decision the slice 035 middleware
// makes for the route. admin.rego, grc_engineer.rego and auditor.rego
// admit it; control_owner and viewer are refused. The handler asks
// the engine itself (ExportDashboard, mayExport) so the gate holds
// where the middleware is not mounted, and keeps the defense-in-depth
// twin `hasDashboardExportAccess` over the same three roles. Slice 269
// D3's admin-OR-approver gate refused an auditor, whom OPA admits,
// and, before #2428, a tenant admin signed in by browser. Each
// panel's content stays filtered per role: the upcoming panel by
// calendar.ReadableTypes (#2342), hidden modules by moduleaccess.
//
// # Meta-audit
//
// Every terminal outcome (200, 400, 403, 500) writes one
// `me_audit_log` row with `action='dashboard_export'`. The `before`
// blob captures the request params (just `format` for v1); the
// `after` blob captures the outcome (`result`, `reason`, `row_count`
// per panel, `byte_count`). Failure to write the meta-audit row is
// intentionally non-fatal to the caller — same posture as slice 137
// / 138 / 175.
//
// # Constitutional invariants honoured
//
//   - **#6 RLS / tenancy**: each per-panel read runs through its
//     existing RLS-gated store; the export NEVER bypasses RLS.
//     `TestSlice269_CrossTenantIsolation` is the merge-blocking
//     evidence (AC-9).
//   - **Append-only ledger discipline (canvas §4.3)**: the only
//     write is the `me_audit_log` insert. No production data table
//     is touched (P0-A4).
//   - **AI-assist boundary**: n/a — pure data export; no LLM in
//     the loop.
package dashboardexport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// metaAuditActionDashboardExport is the slice 269 meta-audit
	// action value. Migration
	// `20260524000000_dashboard_export_meta_audit.sql` extends the
	// `me_audit_log.action` CHECK to permit this value. Spelling is
	// pinned by `TestSlice269_MetaAuditActionConstant`; a typo here
	// would surface as a CHECK violation on every export attempt.
	metaAuditActionDashboardExport = "dashboard_export"

	// dashboardExportEntity is the BuildFilename-style entity
	// identifier. Downloaded filenames look like
	// `dashboard_20260524.json` / `.zip` / `.xlsx`.
	dashboardExportEntity = "dashboard"
)

// Format is the wire-format string used in the URL query parameter.
// Slice 269 supports the three slice 135 canonical formats; PDF is
// explicitly out of scope (P0-A5).
type Format string

const (
	FormatJSON Format = "json"
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
)

// defaultFormat is the assumed value when the caller omits the
// `format` query parameter. AC-2 makes this `json`.
const defaultFormat = FormatJSON

// validFormats is the canonical set of accepted values for AC-2's
// 400-on-unknown branch. Order is documentation-only.
var validFormats = map[Format]bool{
	FormatJSON: true,
	FormatCSV:  true,
	FormatXLSX: true,
}

// PanelSource is the minimal seam the handler reads from. The
// production wiring (`NewHandler`) builds a panelSource over the
// per-domain stores; unit tests inject a stub via `WithSource`.
//
// Returning typed values per panel keeps each panel's wire shape
// type-safe at the boundary — the encoders work off a `Snapshot`
// value, not a free-form map.
type PanelSource interface {
	Snapshot(ctx context.Context, upcoming UpcomingTypes) (Snapshot, error)
}

// UpcomingTypes is the calendar type filter the upcoming panel reads with:
// the caller's readable event types from calendar.ReadableTypes, the same
// OPA narrowing GET /v1/calendar applies (#2234). Filter is a normalized
// CSV ("" is every type); None means the caller may read no type, so the
// panel is empty.
//
// HideRisks leaves the risks panel empty: the caller may not see the Risk
// module, because it is switched off or outside their module access
// (#2173). It rides on the same value so the source keeps one seam.
type UpcomingTypes struct {
	Filter    string
	None      bool
	HideRisks bool
}

// risksRoute is a route of the module that owns the risks panel's rows.
const risksRoute = "/v1/risks"

// withModules narrows u to the modules the viewer may see (#2173): the
// upcoming panel drops event types from hidden modules, as the calendar
// does, and the risks panel is hidden with the Risk module.
func withModules(u UpcomingTypes, v moduleaccess.Viewer) (UpcomingTypes, error) {
	if !u.None {
		filter, left, err := calendar.VisibleTypes(v, u.Filter)
		if err != nil {
			return u, err
		}
		u.Filter, u.None = filter, !left
	}
	seesRisks, err := v.Sees(risksRoute)
	if err != nil {
		return u, err
	}
	u.HideRisks = !seesRisks
	return u, nil
}

// UpcomingTypesFor is the upcoming panel's type filter for the caller of
// r: calendar.ReadableTypes over every type. With no engine every type
// stays, as on the calendar.
func UpcomingTypesFor(r *http.Request, engine *authz.Engine) UpcomingTypes {
	filter, anyLeft := calendar.ReadableTypes(r, engine, "")
	return UpcomingTypes{Filter: filter, None: !anyLeft}
}

// Handler owns the slice 269 dashboard export endpoint. The pool is
// needed for the meta-audit write under the caller's tenant GUC; the
// per-panel reads go through the injected `PanelSource`.
type Handler struct {
	source PanelSource
	pool   *pgxpool.Pool
	engine *authz.Engine
	views  *moduleaccess.Views
}

// WithModules wires the module views (#2173) that leave a hidden module's
// rows out of the export. With none every module is shown.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// NewHandler constructs a Handler. `source` MUST be non-nil for
// production callers; pass a stub via `WithSource` for unit tests.
func NewHandler(pool *pgxpool.Pool, source PanelSource) *Handler {
	return &Handler{source: source, pool: pool}
}

// WithAuthz wires the OPA engine the upcoming panel's type filter asks, as
// the calendar does (#2234). With no engine every type stays, as on the
// calendar's unit-test servers.
func (h *Handler) WithAuthz(engine *authz.Engine) *Handler {
	h.engine = engine
	return h
}

// WithSource overrides the PanelSource — used by unit tests so the
// handler can be exercised without standing up the full per-domain
// store graph. Returns the receiver so calls can chain.
func (h *Handler) WithSource(s PanelSource) *Handler {
	h.source = s
	return h
}

// ExportDashboard handles `GET /v1/dashboard/export?format=...`. See
// the package doc for the contract.
//
// Outcome buckets (each writes one `me_audit_log` row):
//
//	200 result=success
//	400 result=denied:bad_request   (unknown format)
//	401 result=denied:unauthenticated   (no credential / bad tenant)
//	403 result=denied:forbidden      (role lacks export admit)
//	500 result=error:authz|snapshot|encoder  (OPA / downstream / streaming err)
func (h *Handler) ExportDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// AC-6: role gate runs FIRST after credential resolution so an
	// unauthorised caller is a clean 403 regardless of tenant /
	// query-string state.
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		// Cannot record meta-audit without a tenant id; the upstream
		// bearer-auth middleware would normally have rejected this
		// before we ever see the request.
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	format, formatErr := parseFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		httpresp.WriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	admitted, authzErr := h.mayExport(r, cred)
	if authzErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "error:authz",
			Reason: authzErr.Error(),
		})
		httperr.WriteInternal(w, r, "authorize dashboard export", authzErr)
		return
	}
	if !admitted {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant dashboard/export access",
		})
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant dashboard/export access")
		return
	}

	// AC-9 / AC-3..5: compose the six panels from existing stores.
	// Each panel's underlying call opens its own RLS-gated tx; cross-
	// tenant isolation is a property of those stores, NOT of this
	// handler.
	// #2234: the upcoming panel shows the event types the caller may read
	// on the calendar, from the same function, not the flag gate above.
	// #2173: and only the modules the caller may see.
	viewer, snapErr := h.views.For(r)
	var filter UpcomingTypes
	if snapErr == nil {
		filter, snapErr = withModules(UpcomingTypesFor(r, h.engine), viewer)
	}
	var snapshot Snapshot
	if snapErr == nil {
		snapshot, snapErr = h.source.Snapshot(ctx, filter)
	}
	if snapErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "error:snapshot",
			Reason: snapErr.Error(),
		})
		httperr.WriteInternal(w, r, "compose dashboard snapshot", snapErr)
		return
	}

	// Build the filename + content-type and stream the encoder. The
	// snapshot's `SnapshotAt` is also stamped onto the meta-audit's
	// after-blob for forensic correlation.
	contentType, fileExt := contentMetaFor(format)
	filename := buildFilename(dashboardExportEntity, fileExt)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &countingWriter{w: w}
	if encErr := encodeSnapshot(cw, format, snapshot); encErr != nil {
		// Body already started; cannot change status now. Record the
		// failure in the meta-audit row so a forensic query can still
		// distinguish "completed" from "encoder failed mid-stream".
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    "encoder: " + encErr.Error(),
			RowCount:  panelRowCount(snapshot),
			ByteCount: cw.n,
		})
		return
	}

	// AC-7: success row.
	h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  panelRowCount(snapshot),
		ByteCount: cw.n,
	})
}

// ===== Format parsing =====

// parseFormat resolves the ?format= query parameter. Empty defaults
// to JSON (AC-2). Unknown values trip a 400 with an explanatory
// error.
func parseFormat(r *http.Request) (Format, error) {
	raw := r.URL.Query().Get("format")
	if raw == "" {
		return defaultFormat, nil
	}
	f := Format(strings.ToLower(raw))
	if !validFormats[f] {
		return f, fmt.Errorf("unsupported format %q (want json|csv|xlsx)", raw)
	}
	return f, nil
}

// contentMetaFor returns the (Content-Type, file extension) pair for
// a given format. The CSV format uses a ZIP envelope (one CSV per
// panel) so the Content-Type + extension reflect that.
func contentMetaFor(f Format) (contentType, ext string) {
	switch f {
	case FormatJSON:
		return "application/json", "json"
	case FormatCSV:
		return "application/zip", "zip"
	case FormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"
	default:
		// Unreachable in production — parseFormat already validated.
		return "application/octet-stream", "bin"
	}
}

// buildFilename produces a sanitised attachment filename of the
// shape `<entity>_<YYYYMMDD>.<ext>`. ASCII alphanum + `_` only;
// matches the slice 135 BuildFilename posture but inlined to avoid a
// cross-package dependency for one call site.
func buildFilename(entity, ext string) string {
	date := time.Now().UTC().Format("20060102")
	return entity + "_" + date + "." + ext
}

// ===== Role gate =====

// dashboardResource is the OPA resource type the export is read under:
// BuildInput derives it from the leading /v1/dashboard/ segment, and
// auditor.rego admits it by that name.
const dashboardResource = "dashboard"

// mayExport reports whether the caller of r may export the dashboard
// (#2350): the defense-in-depth twin admits it and, when an engine is
// wired, OPA allows read of the dashboard resource. A Decide error is
// returned for a 500, never read as an admit.
func (h *Handler) mayExport(r *http.Request, cred credstore.Credential) (bool, error) {
	if !hasDashboardExportAccess(cred) {
		return false, nil
	}
	if h.engine == nil {
		return true, nil
	}
	in := authz.BuildInput(r, nil)
	in.Resource.Type = dashboardResource
	in.Resource.ID = ""
	in.Action = "read"
	d, err := h.engine.Decide(r.Context(), in)
	if err != nil {
		return false, fmt.Errorf("decide dashboard export: %w", err)
	}
	return d.Allow, nil
}

// hasDashboardExportAccess is the handler-level twin of OPA's admit on
// the dashboard resource (#2350), authz.IsTenantAuditReader: the admin
// and approver flags, or the live admin, grc_engineer or auditor role.
// control_owner, viewer, a bare push credential and a session's role
// claim are refused. It matters where no engine is wired (unit and
// integration test servers), and keeps out a bare push key, for which
// OPA derives grc_engineer.
func hasDashboardExportAccess(c credstore.Credential) bool {
	return authz.IsTenantAuditReader(c)
}

// ===== Meta-audit =====

// exportMetaAudit is the JSON shape persisted to me_audit_log.after
// on every export attempt. Outcome buckets mirror the slice 137 /
// 138 / 175 shape (success / denied:* / error:*) so a single
// forensic query can correlate the dashboard export with the
// per-domain exports side-by-side.
type exportMetaAudit struct {
	Format    string         `json:"format"`
	Result    string         `json:"result"`
	Reason    string         `json:"reason,omitempty"`
	RowCount  map[string]int `json:"row_count,omitempty"`
	ByteCount int64          `json:"byte_count"`
}

// panelRowCount returns the per-panel row count for the success-
// path meta-audit row. Helps forensic queries reason about export
// size without scraping the body.
func panelRowCount(s Snapshot) map[string]int {
	return map[string]int{
		"framework_posture": len(s.Panels.FrameworkPosture),
		"risks":             len(s.Panels.Risks),
		"freshness":         len(s.Panels.Freshness.Buckets),
		"drift":             len(s.Panels.Drift.FlippedOut),
		"upcoming":          len(s.Panels.Upcoming),
		"activity":          len(s.Panels.Activity),
	}
}

// writeMetaAudit writes ONE me_audit_log row with
// action='dashboard_export'. Slice 135 P0-A4 inherited — EVERY
// terminal outcome path writes a row. Failure of the meta-audit
// write itself is intentionally non-fatal to the caller; the
// dashboard export must still succeed even if the audit-log table
// has a transient lock.
func (h *Handler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta exportMetaAudit) {
	paramsBlob, _ := json.Marshal(map[string]any{
		"format": meta.Format,
	})
	resultBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		uID = uuid.Nil
	}

	if h.pool == nil {
		// Unit-test path: no pool wired. The handler unit tests run
		// without a DB, so we skip the meta-audit write rather than
		// panic. The integration tests cover the persisted shape.
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return
	}
	q := dbx.New(tx)
	if err := q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: uID, Valid: true},
		Action:   metaAuditActionDashboardExport,
		Before:   paramsBlob,
		After:    resultBlob,
	}); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ===== HTTP helpers =====

// countingWriter wraps an io.Writer to track bytes written through
// it — used to populate the meta-audit row's `byte_count`.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// writeError emits a small JSON error envelope. Local to this
// package (the slice 137/175 controls package uses an equivalent
// helper; we avoid a cross-package dependency for one call site).
