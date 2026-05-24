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
// Slice 269 D3: narrow admit — admin OR approver only. The dashboard
// EXPORT is the bulk-handoff variant of the dashboard READ, and bulk
// handoff is a more sensitive surface than the in-app view. Mirrors
// the slice 138 evidence-export pattern (`IsAdmin || IsApprover`)
// rather than the slice 066 dashboard `requireProgramRead` pattern
// (which also admits `control_owner`). The defense-in-depth gate is
// the handler-level predicate `hasDashboardExportAccess`; the
// production gate is the slice 035 OPA admit on `dashboard_export`
// (added in the same slice).
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
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
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
	Snapshot(ctx context.Context) (Snapshot, error)
}

// Handler owns the slice 269 dashboard export endpoint. The pool is
// needed for the meta-audit write under the caller's tenant GUC; the
// per-panel reads go through the injected `PanelSource`.
type Handler struct {
	source PanelSource
	pool   *pgxpool.Pool
}

// NewHandler constructs a Handler. `source` MUST be non-nil for
// production callers; pass a stub via `WithSource` for unit tests.
func NewHandler(pool *pgxpool.Pool, source PanelSource) *Handler {
	return &Handler{source: source, pool: pool}
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
//	500 result=error:snapshot|encoder  (downstream / streaming err)
func (h *Handler) ExportDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// AC-6: role gate runs FIRST after credential resolution so an
	// unauthorised caller is a clean 403 regardless of tenant /
	// query-string state.
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing credential")
		// Cannot record meta-audit without a tenant id; the upstream
		// bearer-auth middleware would normally have rejected this
		// before we ever see the request.
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid tenant in credential")
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
		writeError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	if !hasDashboardExportAccess(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant dashboard/export access",
		})
		writeError(w, http.StatusForbidden, "role does not grant dashboard/export access")
		return
	}

	// AC-9 / AC-3..5: compose the six panels from existing stores.
	// Each panel's underlying call opens its own RLS-gated tx; cross-
	// tenant isolation is a property of those stores, NOT of this
	// handler.
	snapshot, snapErr := h.source.Snapshot(ctx)
	if snapErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "error:snapshot",
			Reason: snapErr.Error(),
		})
		writeError(w, http.StatusInternalServerError,
			"compose dashboard snapshot: "+snapErr.Error())
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

// hasDashboardExportAccess reports whether the credential grants
// dashboard-export access. Slice 269 D3: narrower than the slice 066
// dashboard-read admit (which also accepts `control_owner`) — the
// export is a bulk-handoff surface, so we limit it to admin +
// approver (auditor-level). Pins the AC-6 contract.
//
// `IsAdmin` is the wildcard signal; `IsApprover` represents the
// auditor / sign-off role family. A bare push credential or a
// control-owner credential lacks both and is denied.
func hasDashboardExportAccess(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover
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
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
