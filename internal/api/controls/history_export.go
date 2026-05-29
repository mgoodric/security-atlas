// Slice 175 — control bundle history export handler.
//
// `GET /v1/controls/history/export?format=<csv|json|xlsx>` exports the
// FULL lineage of every control bundle for the caller's tenant — every
// version, active + superseded — with the slice 137 15-column projection
// PLUS two new columns (`superseded_by`, `superseded_at`).
//
// Spillover provenance: slice 137 D2 explicitly rejected including the
// `superseded_by` / `superseded_at` columns in the active-only export
// because they would always be NULL for the slice 137 row set. For
// auditor period-freeze reconstruction ("what did this control look
// like at frozen_at T?") the superseded versions matter — this slice
// ships the lineage view as a SEPARATE endpoint, leaving slice 137's
// active-only export shape unchanged for its existing consumers
// (compliance gap analysis, auditor handoff index sheets).
//
// Constitutional posture:
//
//   - Invariant #6 (RLS): every read goes through the existing dbx
//     plumbing under `atlas_app` + `tenancy.ApplyTenant`. NO
//     `BYPASSRLS`. The cross-tenant isolation integration test in
//     `history_export_integration_test.go` is the merge-blocking
//     evidence.
//
//   - Invariant #1 (one control, N framework satisfactions): inherited
//     from slice 137 — the export carries `scf_anchor_id` on every row
//     for downstream UCF-graph reconstruction.
//
//   - Append-only ledger discipline (canvas §4.3): the `controls`
//     table itself is append-only with supersession markers
//     (`superseded_by`); the history export surfaces that ledger shape
//     directly. No row is ever deleted from the underlying table.
//
//   - Slice 145 concurrency cap inherited from slice 137. Refusal
//     returns 429 with `Retry-After: 30`. The release fn is sync.Once-
//     guarded so this is safe alongside any other explicit release call.
//
//   - Streaming-memory: each format's writer consumes the row iterator
//     pull-style. The `TestSlice175_StreamingMemoryUnder200MBFor50KRows`
//     integration test asserts heap delta stays under 200 MB.
//
// Reuses slice 137's package-local helpers:
//
//   - `controlsHasProgramRead` (role gate predicate)
//   - `controlsCountingWriter` (body byte-count for the meta-audit row)
//   - `parseControlsExportFormat` (format query-string resolution)
//   - `exportLimiter` (slice 145 limiter accessor — singleton fallback)
//
// Does NOT reuse slice 137's `controlExportRow` / `controlsToRowIter` /
// `controlsExportHeader` / `metaAuditActionControlsExport` because the
// history export's row shape (+2 columns) and meta-audit action
// (`controls_history_export` — different downstream consumer) diverge.

package controls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// defaultControlsHistoryExportRowCap is the slice 175 row-cap
	// default AND maximum. Same 500K ceiling as slice 137 — the
	// lineage view is the slice 137 active-only set plus all
	// superseded versions, so the row count scales by the average
	// supersession depth per bundle. Realistic large tenants run
	// O(10³–10⁴) active controls with O(1–3) versions each, well
	// below 500K; the cap leaves headroom against future re-uploads.
	defaultControlsHistoryExportRowCap = 500_000

	// metaAuditActionControlsHistoryExport is the slice 175 D5
	// meta-audit action value. Distinct from slice 137's
	// `controls_export` so a forensic query like
	//   WHERE action = 'controls_history_export'
	// cleanly enumerates lineage-export events separately from the
	// active-only catalog dumps. Migration
	// `20260522010000_controls_history_export_meta_audit.sql`
	// extends the `me_audit_log.action` CHECK to permit this value.
	metaAuditActionControlsHistoryExport = "controls_history_export"

	// controlsHistoryExportEntity is the slice 135 BuildFilename
	// entity identifier. Downloaded filenames look like
	// `controls_history_20260522.csv` / `.json` / `.xlsx`.
	controlsHistoryExportEntity = "controls_history"
)

// HistoryExportHandler owns the slice 175 control bundle history
// export endpoint. The pool is needed for the data read (RLS-scoped
// via `tenancy.ApplyTenant`) and the meta-audit write (a short
// transaction outside the export's iteration window so the meta-audit
// row commits even when the streaming write is in flight).
//
// Slice 145 hook: the optional `limiter` field overrides the
// process-wide singleton [export.DefaultLimiter] when set — used by
// integration tests to pin a small, deterministic concurrency cap.
type HistoryExportHandler struct {
	source  controlsHistoryExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// controlsHistoryExportSource is the minimal interface
// HistoryExportHandler needs to pull controls for export — kept local
// so tests can inject a deterministic data source without standing up
// the full RLS plumbing. Production code uses the inline implementation
// directly off the pool.
type controlsHistoryExportSource interface {
	listHistoryForExport(ctx context.Context, limit int) ([]controlHistoryExportRow, bool, error)
}

// controlHistoryExportRow is the minimal projection the history
// exporter needs. The first 15 fields mirror slice 137's
// `controlExportRow` exactly — same ordering, same types — so the
// downstream column layout stays bytewise identical for the first 15
// columns. The last two (`SupersededBy`, `SupersededAt`) are slice
// 175 additions.
//
// `SupersededAt` is synthesised at projection time from the row's
// `UpdatedAt` whenever `SupersededBy != uuid.Nil`. For active rows
// (no successor) it remains the zero value and the projection emits
// an empty cell.
type controlHistoryExportRow struct {
	ID                 uuid.UUID
	BundleID           string
	Version            int32
	SCFID              string
	SCFAnchorID        uuid.UUID
	Title              string
	ControlFamily      string
	ImplementationType string
	OwnerRole          string
	LifecycleState     string
	ApplicabilityExpr  string
	FreshnessClass     string
	BundleManifestHash string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	// Slice 175 additions:
	SupersededBy uuid.UUID // uuid.Nil for active rows
	SupersededAt time.Time // zero for active rows
}

// NewHistoryExportHandler constructs the slice 175 history-export
// handler. The pool is the same `atlas_app` (NOSUPERUSER NOBYPASSRLS)
// pool the rest of the API uses; the meta-audit writes go through it
// under the caller's tenant context.
func NewHistoryExportHandler(pool *pgxpool.Pool) *HistoryExportHandler {
	return &HistoryExportHandler{
		source: nil,
		pool:   pool,
	}
}

// WithSource installs a controlsHistoryExportSource for testing.
// Production callers leave it nil — the handler falls back to the
// inline pool-backed adapter via [HistoryExportHandler.listHistoryDirect].
func (h *HistoryExportHandler) WithSource(s controlsHistoryExportSource) *HistoryExportHandler {
	h.source = s
	return h
}

// WithLimiter installs a Limiter into the handler — used by
// integration tests to pin a small, deterministic cap without setting
// the env var across the whole test process. Production callers MUST
// NOT use this — the default singleton is the only correct shape for
// a process-wide cap.
func (h *HistoryExportHandler) WithLimiter(l *export.Limiter) *HistoryExportHandler {
	h.limiter = l
	return h
}

// ExportControlsHistory handles `GET /v1/controls/history/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitised
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4 inherited).
func (h *HistoryExportHandler) ExportControlsHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Role gate (defense-in-depth) — same pattern slice 137 uses.
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
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

	// Parse + validate the request shape. Bad-format → 400 with a
	// meta-audit row recording the denial reason. We reuse the slice
	// 137 parser since the format selector is identical (csv|json|xlsx).
	format, formatErr := parseControlsExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		httpresp.WriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	// Defense-in-depth role gate. The history view carries the same
	// tenant-private `applicability_expr` cells slice 137 protects —
	// PLUS the supersession metadata, which is internal program-build
	// state. Same gate, same predicate.
	if !controlsHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant controls/program-read access",
		})
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant controls/program-read access")
		return
	}

	// Slice 145 — per-(tenant, user) concurrency cap inherited from
	// slice 137. Acquired AFTER auth + role gate but BEFORE encoder
	// resolve / DB work. Same shape, same Retry-After semantics.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format: string(format),
			Result: "denied:concurrency_cap_exceeded",
			Reason: capErr.Error(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf(
				"export concurrency cap (%d) reached for this (tenant, user); "+
					"retry in 30s or narrow concurrent forensics work",
				limiter.Cap()),
			"retry_after_seconds": 30,
			"cap":                 limiter.Cap(),
		})
		return
	}
	defer release()

	// Resolve encoder for the requested format.
	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Pull rows from the store. Ask for one more than the row cap so
	// the 413 path triggers without an extra round-trip.
	rowCap := defaultControlsHistoryExportRowCap
	var (
		rows         []controlHistoryExportRow
		exceededCap  bool
		queryErr     string
		statusToSend int
		bodyBytes    int64
	)

	rows, exceededCap, err = h.listHistoryForExport(ctx, rowCap+1)
	if err != nil {
		queryErr = err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: queryErr,
		})
		httpresp.WriteError(w, http.StatusInternalServerError, "list controls history for export: "+queryErr)
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d controls; "+
				"contact the maintainer if your lineage legitimately exceeds this ceiling",
				rowCap))

		return
	}

	// Streaming write. Headers go BEFORE the body. Encoder pipes
	// per-row into the response via countingWriter so the meta-audit
	// records the body's byte count.
	statusToSend = http.StatusOK
	header := controlsHistoryExportHeader()
	filename := export.BuildFilename(controlsHistoryExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusToSend)

	cw := &controlsCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, controlsHistoryToRowIter(rows)); err != nil {
		queryErr = "encoder: " + err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    queryErr,
			RowCount:  len(rows),
			ByteCount: cw.n,
		})
		// Body already started; cannot change status now.
		return
	}
	bodyBytes = cw.n

	// Success path — write the meta-audit row.
	h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsHistoryExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: bodyBytes,
	})
}

// ===== Row iteration =====

// controlsHistoryExportHeader is the canonical 17-column list for the
// slice 175 history export — slice 137's 15 columns IN THE SAME ORDER
// followed by the two slice-175 additions (`superseded_by`,
// `superseded_at`).
//
// Stable: changing this list is a breaking change for downstream
// consumers. The slice 175 P0-A-175-1 anti-criterion locks the
// invariant: "the prior 15 columns from slice 137 stay in the same
// positions for downstream-tool reuse." The unit-suite test
// `TestSlice175_HistoryHeader_LockedShape` is the merge gate.
func controlsHistoryExportHeader() []string {
	return []string{
		// slice 137 canonical 15 columns — DO NOT REORDER
		"id",
		"bundle_id",
		"version",
		"title",
		"control_family",
		"scf_id",
		"scf_anchor_id",
		"implementation_type",
		"owner_role",
		"lifecycle_state",
		"applicability_expr",
		"freshness_class",
		"bundle_manifest_hash",
		"created_at",
		"updated_at",
		// slice 175 supersession columns
		"superseded_by",
		"superseded_at",
	}
}

// controlsHistoryToRowIter projects a slice of controlHistoryExportRow
// into an iter.Seq[[]string] in the canonical column order. Pure
// projection — no DB I/O.
//
// `superseded_by` and `superseded_at` render as empty strings for
// active rows (where SupersededBy == uuid.Nil) so downstream CSV
// consumers see consistent empty cells rather than the canonical
// `00000000-0000-0000-0000-000000000000` UUID literal.
func controlsHistoryToRowIter(rows []controlHistoryExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			supersededBy := ""
			supersededAt := ""
			if r.SupersededBy != uuid.Nil {
				supersededBy = r.SupersededBy.String()
				supersededAt = r.SupersededAt.UTC().Format(time.RFC3339)
			}
			row := []string{
				r.ID.String(),
				r.BundleID,
				fmt.Sprintf("%d", r.Version),
				r.Title,
				r.ControlFamily,
				r.SCFID,
				r.SCFAnchorID.String(),
				r.ImplementationType,
				r.OwnerRole,
				r.LifecycleState,
				r.ApplicabilityExpr,
				r.FreshnessClass,
				r.BundleManifestHash,
				r.CreatedAt.UTC().Format(time.RFC3339),
				r.UpdatedAt.UTC().Format(time.RFC3339),
				supersededBy,
				supersededAt,
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== Store adapter =====

// listHistoryForExport runs the RLS-scoped list query. Returns up to
// `limit` rows; the boolean is true when the underlying store
// returned exactly `limit` rows AND the caller asked for one more
// than the cap, signalling cap-exceeded.
func (h *HistoryExportHandler) listHistoryForExport(ctx context.Context, limit int) ([]controlHistoryExportRow, bool, error) {
	if h.source != nil {
		return h.source.listHistoryForExport(ctx, limit)
	}
	return h.listHistoryDirect(ctx, limit)
}

// listHistoryDirect is the production data-access path. Opens a tx,
// sets the tenant GUC, runs the slice 175
// ListControlsHistoryForExport query (which drops the
// `superseded_by IS NULL` filter slice 137's active-only query
// carries), projects each row into a controlHistoryExportRow.
func (h *HistoryExportHandler) listHistoryDirect(ctx context.Context, limit int) ([]controlHistoryExportRow, bool, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("tenant context: %w", err)
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, false, fmt.Errorf("parse tenant id: %w", err)
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, false, err
	}
	q := dbx.New(tx)
	dbRows, err := q.ListControlsHistoryForExport(ctx, dbx.ListControlsHistoryForExportParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, false, fmt.Errorf("list controls history for export: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	// Cap detection: the caller passed (rowCap + 1). If the store
	// returned at least that many rows, we've exceeded the cap.
	exceeded := len(dbRows) >= limit
	if exceeded {
		dbRows = dbRows[:limit]
	}

	out := make([]controlHistoryExportRow, len(dbRows))
	for i, r := range dbRows {
		scfCode := ""
		if r.ScfID != nil {
			scfCode = *r.ScfID
		}
		freshness := ""
		if r.FreshnessClass != nil {
			freshness = *r.FreshnessClass
		}
		row := controlHistoryExportRow{
			ID:                 uuid.UUID(r.ID.Bytes),
			BundleID:           r.BundleID,
			Version:            r.Version,
			SCFID:              scfCode,
			SCFAnchorID:        uuid.UUID(r.ScfAnchorID.Bytes),
			Title:              r.Title,
			ControlFamily:      r.ControlFamily,
			ImplementationType: string(r.ImplementationType),
			OwnerRole:          r.OwnerRole,
			LifecycleState:     string(r.LifecycleState),
			ApplicabilityExpr:  r.ApplicabilityExpr,
			FreshnessClass:     freshness,
			BundleManifestHash: r.BundleManifestHash,
		}
		if r.CreatedAt.Valid {
			row.CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			row.UpdatedAt = r.UpdatedAt.Time
		}
		// Slice 175 supersession synthesis. The SQL projection
		// returns `superseded_by` (a possibly-null UUID); we treat a
		// non-NULL value as the supersession signal and use the row's
		// `updated_at` as the synthesised `superseded_at`. See the
		// query doc-comment for the supersession-transaction
		// invariant that makes this safe.
		if r.SupersededBy.Valid {
			row.SupersededBy = uuid.UUID(r.SupersededBy.Bytes)
			if r.UpdatedAt.Valid {
				row.SupersededAt = r.UpdatedAt.Time
			}
		}
		out[i] = row
	}
	return out, exceeded, nil
}

// ===== Meta-audit =====

// controlsHistoryExportMetaAudit is the JSON shape persisted to
// me_audit_log.after on every history-export attempt. Outcome buckets
// mirror slice 137's `controlsExportMetaAudit` (success / denied:* /
// error:*) so a single forensic query can correlate the two export
// surfaces side-by-side.
type controlsHistoryExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

// writeMetaAudit writes ONE me_audit_log row with
// action='controls_history_export'. Slice 135 P0-A4 (inherited) —
// EVERY terminal outcome path writes a row. Failure of the meta-audit
// write itself is intentionally non-fatal to the caller.
func (h *HistoryExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta controlsHistoryExportMetaAudit) {
	paramsBlob, _ := json.Marshal(map[string]any{
		"format": meta.Format,
	})
	resultBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		uID = uuid.Nil
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
		Action:   metaAuditActionControlsHistoryExport,
		Before:   paramsBlob,
		After:    resultBlob,
	}); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ===== Limiter accessor =====

// exportLimiter returns the per-(tenant, user) concurrency limiter
// used by this handler. Default returns the process-wide singleton;
// tests override via [HistoryExportHandler.WithLimiter].
func (h *HistoryExportHandler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// Compile-time assertion that controlsCountingWriter from export.go
// satisfies io.Writer — the slice 175 handler uses the same type for
// body byte-count tracking. Documented here so a future contributor
// understands the cross-file reuse.
var _ io.Writer = (*controlsCountingWriter)(nil)
