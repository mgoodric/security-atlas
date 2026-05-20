// Slice 137 — controls UCF graph data-export handler.
//
// `GET /v1/controls/export?format=<csv|json|xlsx>` reuses the slice 135
// data-export library (`internal/export/`) and the slice 145
// per-(tenant, user) concurrency cap. Closest precedent is slice 136's
// risk-register export (`internal/api/risks/export.go`); this handler
// mirrors the same shape with three slice-specific differences:
//
//   - Row cap lifted to 500,000 (slice 137 P0-A-UCF-1 / D3). UCF graphs
//     can be large at multi-product orgs; 50K from slice 136 is too
//     conservative.
//   - Streaming-memory budget asserted at 200 MB at 500K rows (slice
//     137 P0-A-UCF-3). The integration test runs the encoder against
//     `discardWriter` and reads `runtime.MemStats` deltas.
//   - Column projection is flat (D1) — one row per active tenant
//     control, with `scf_id` + `scf_anchor_id` as foreign-key columns
//     for downstream UCF-graph reconstruction. Anchor metadata is NOT
//     fanned inline (see decisions log D1 rejected alternatives).
//
// Constitutional posture:
//
//   - Invariant #6 (RLS): every read goes through the existing dbx
//     plumbing under `atlas_app` + `tenancy.ApplyTenant`. NO `BYPASSRLS`.
//     The cross-tenant isolation integration test in
//     `export_integration_test.go` is the merge-blocking evidence.
//
//   - Invariant #1 (one control, N framework satisfactions): the
//     export preserves the graph shape by carrying `scf_anchor_id`
//     (the join key into `fw_to_scf_edges`) on every row. The
//     control-as-row shape is structurally compatible with the
//     graph — clients can reconstruct framework satisfactions
//     downstream without the export embedding them.
//
//   - Slice 137 P0-A4 (slice 145 concurrency cap): every successful
//     acquire defers a release; refusal returns 429 with
//     `Retry-After: 30`. The release fn is sync.Once-guarded so
//     this is safe alongside any other explicit release call.
//
//   - Slice 137 P0-A5 (500K row cap): the default IS the maximum;
//     no operator override at v1. Cap is enforced by asking the
//     store for one more row than the cap and detecting the
//     overflow before encoding.
//
//   - Slice 137 P0-A6 (streaming-memory): the encoder consumes the
//     row iterator pull-style; per-row allocation is bounded. The
//     integration test asserts heap stays under 200 MB at 500K rows.
//
//   - Slice 137 P0-A9 (no library modifications): every change to
//     `internal/export/` is rejected. The handler consumes the
//     library through its existing exported surface only.

package controls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// defaultControlsExportRowCap is the slice 137 row-cap default,
	// AND the maximum — there is no operator override knob at v1.
	// The slice doc lifts the cap to 500K (5x slice 136's 50K) because
	// the UCF graph spans ~1,400 SCF anchors × per-tenant control
	// bundles × applicability_expr scope cells; realistic large
	// tenants run O(10³–10⁴) controls, leaving ~50x headroom against
	// the 500K ceiling.
	defaultControlsExportRowCap = 500_000

	// metaAuditActionControlsExport is the slice 137 meta-audit action
	// value. The migration `20260520000000_controls_export_meta_audit.sql`
	// extends the `me_audit_log.action` CHECK to permit this value.
	// Plural `controls_` matches the slice 139 convention
	// (`audit_periods_export`, `vendors_export`); the slice 136 risk
	// export's singular `risk_export` is the outlier (one register).
	metaAuditActionControlsExport = "controls_export"

	// controlsExportEntity is the slice 135 BuildFilename entity
	// identifier for controls exports. The downloaded filename will
	// look like `controls_20260519.csv`.
	controlsExportEntity = "controls"
)

// ExportHandler owns the slice 137 controls UCF graph export endpoint.
// The pool is needed for both the data read (RLS-scoped via
// `tenancy.ApplyTenant`) and the meta-audit write (a short transaction
// outside the export's iteration window so the meta-audit row commits
// even when the export body has been partially written).
//
// Slice 145 hook: the optional `limiter` field overrides the
// process-wide singleton [export.DefaultLimiter] when set — used by
// integration tests to pin a small, deterministic concurrency cap.
// Production callers leave it nil; the handler resolves the singleton
// lazily on every request.
type ExportHandler struct {
	source  controlsExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// controlsExportSource is the minimal interface ExportHandler needs to
// pull controls for export — kept local so tests can inject a
// deterministic data source without standing up the full RLS plumbing.
// Production code uses the inline implementation directly off the pool.
type controlsExportSource interface {
	listForExport(ctx context.Context, limit int) ([]controlExportRow, bool, error)
}

// controlExportRow is the minimal projection the exporter needs. Kept
// narrow (not the full dbx row type) so the export handler doesn't
// have to re-export every field the controls table carries — the
// canonical column set in `controlsExportHeader` is the single source
// of truth.
type controlExportRow struct {
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
}

// NewExportHandler constructs the slice 137 export handler. The pool
// is the same `atlas_app` (NOSUPERUSER NOBYPASSRLS) pool the rest of
// the API uses; the meta-audit writes go through it under the
// caller's tenant context.
func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{
		source: nil,
		pool:   pool,
	}
}

// WithSource installs a controlsExportSource for testing. Production
// callers leave it nil — the handler falls back to the inline
// pool-backed adapter via [ExportHandler.listControlsForExport].
func (h *ExportHandler) WithSource(s controlsExportSource) *ExportHandler {
	h.source = s
	return h
}

// WithLimiter installs a Limiter into the handler — used by
// integration tests to pin a small, deterministic cap without setting
// the env var across the whole test process.
//
// Production callers MUST NOT use this — the default singleton is the
// only correct shape for a process-wide cap.
func (h *ExportHandler) WithLimiter(l *export.Limiter) *ExportHandler {
	h.limiter = l
	return h
}

// ExportControls handles `GET /v1/controls/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitized
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4 inherited).
func (h *ExportHandler) ExportControls(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Role gate (defense-in-depth) — same pattern slice 136 uses. The
	// upstream slice 035 OPA middleware is the canonical authz gate;
	// this is the second leg. A missing credential is a 401; a denied
	// role is a 403 with no body access.
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing credential")
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

	// Parse + validate the request shape. Bad-format → 400 with a
	// meta-audit row recording the denial reason.
	format, formatErr := parseControlsExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		writeError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	// Defense-in-depth role gate. The control catalog includes the
	// tenant-private `applicability_expr` cells, so the same
	// program-read role set the slice 067 risk read endpoints check
	// is the right gate here. A bare push credential (no admin /
	// approver / owner flag) cannot read the catalog.
	if !controlsHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant controls/program-read access",
		})
		writeError(w, http.StatusForbidden, "role does not grant controls/program-read access")
		return
	}

	// Slice 145 — per-(tenant, user) concurrency cap. ACQUIRED HERE,
	// AFTER auth + role gate but BEFORE encoder resolve / DB work:
	//   * Anonymous + bad-auth requests never count against the cap.
	//   * The slot is held for the duration of the streaming write
	//     (release is deferred), so an attacker firing N concurrent
	//     requests saturates at cap=N regardless of latency.
	//   * Refusal returns 429 with `Retry-After: 30` and a JSON body
	//     explaining the limit (slice 145 P0-HARDEN-3 + P0-A10).
	//   * Meta-audit fires on the 429 path too — operators reading
	//     `me_audit_log WHERE action = 'controls_export'` see the
	//     attempt with result=denied:concurrency_cap_exceeded.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
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
	// Slice 145 P0-A9 — release on EVERY exit path (panic / error /
	// 413 / 500 / success). The release fn is sync.Once-guarded so
	// this is safe alongside any other explicit release call.
	defer release()

	// Resolve encoder for the requested format.
	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Pull rows from the store. Ask for one more than the row cap so
	// the 413 path triggers without an extra round-trip.
	rowCap := defaultControlsExportRowCap
	var (
		rows         []controlExportRow
		exceededCap  bool
		queryErr     string
		statusToSend int
		bodyBytes    int64
	)

	rows, exceededCap, err = h.listControlsForExport(ctx, rowCap+1)
	if err != nil {
		queryErr = err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: queryErr,
		})
		writeError(w, http.StatusInternalServerError, "list controls for export: "+queryErr)
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d controls; "+
				"contact the maintainer if your catalog legitimately exceeds this ceiling",
				rowCap))
		return
	}

	// Streaming write. Headers go BEFORE the body. Encoder pipes
	// per-row into the response via countingWriter so the meta-audit
	// records the body's byte count.
	statusToSend = http.StatusOK
	header := controlsExportHeader()
	filename := export.BuildFilename(controlsExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusToSend)

	cw := &controlsCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, controlsToRowIter(rows)); err != nil {
		queryErr = "encoder: " + err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
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
	h.writeMetaAudit(ctx, tenantID, userIdentifier, controlsExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: bodyBytes,
	})
}

// ===== Parsing =====

// parseControlsExportFormat resolves the `?format=` query param.
// Default is CSV (matches slice 135 / 136 / 139 behavior). Anything
// other than csv|json|xlsx is a 400.
func parseControlsExportFormat(r *http.Request) (export.Format, error) {
	raw := r.URL.Query().Get("format")
	if raw == "" {
		raw = string(export.FormatCSV)
	}
	format := export.Format(strings.ToLower(raw))
	if !export.IsValid(format) {
		return format, fmt.Errorf("unsupported format %q (want csv|json|xlsx)", raw)
	}
	return format, nil
}

// ===== Row iteration =====

// controlsExportHeader is the canonical column list for the slice 137
// controls export — see decisions log D2 for ordering rationale.
//
// Stable: changing this list is a breaking change for downstream
// consumers (compliance gap-analysis scripts, auditor handoff index
// sheets, Excel pivots keyed off column position). The CSV / JSON /
// XLSX encoders all emit the same column order; the XLSX writer
// renders the same header as the first row.
//
// Slice 137 D2 ordering rationale (mirrored at the call site for
// future contributors who skim the header without the decisions log):
//
//	identity:    id, bundle_id, version, title, control_family
//	topology:    scf_id, scf_anchor_id  (UCF graph join keys)
//	posture:     implementation_type, owner_role, lifecycle_state
//	tenant data: applicability_expr     (slice 017 DSL; RLS guards)
//	integrity:   freshness_class, bundle_manifest_hash
//	audit:       created_at, updated_at
func controlsExportHeader() []string {
	return []string{
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
	}
}

// controlsToRowIter projects a slice of controlExportRow into an
// iter.Seq[[]string] in the canonical column order. Pure projection —
// no DB I/O.
//
// Date fields render as RFC3339 (timestamps). The UUID columns render
// as canonical 8-4-4-4-12 hex (UUID.String()). The nullable string
// columns (`scf_id`, `freshness_class`) render as the empty string
// when absent.
func controlsToRowIter(rows []controlExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
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
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== Store adapter =====

// listControlsForExport runs the RLS-scoped list query. Returns up to
// `limit` rows; the boolean is true when the underlying store returned
// exactly `limit` rows AND the caller asked for one more than the cap,
// signalling cap-exceeded.
//
// The store-interface indirection is so integration tests can inject
// a deterministic data source without standing up the full controls
// store + RLS context — the production path uses the inline
// implementation directly off the pool.
func (h *ExportHandler) listControlsForExport(ctx context.Context, limit int) ([]controlExportRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listControlsDirect(ctx, limit)
}

// listControlsDirect is the production data-access path. Opens a tx,
// sets the tenant GUC, runs the slice 137 ListActiveControlsForExport
// query (which selects the canonical column projection at the dbx
// layer), projects each row into a controlExportRow.
func (h *ExportHandler) listControlsDirect(ctx context.Context, limit int) ([]controlExportRow, bool, error) {
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
	dbRows, err := q.ListActiveControlsForExport(ctx, dbx.ListActiveControlsForExportParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, false, fmt.Errorf("list active controls for export: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	// Cap detection: the caller passed (rowCap + 1). If the store
	// returned at least that many rows, we've exceeded the cap.
	exceeded := len(dbRows) >= limit
	if exceeded {
		// Trim to one-over for the meta-audit row count; the
		// streaming write never actually happens on this path.
		dbRows = dbRows[:limit]
	}

	out := make([]controlExportRow, len(dbRows))
	for i, r := range dbRows {
		scfCode := ""
		if r.ScfID != nil {
			scfCode = *r.ScfID
		}
		freshness := ""
		if r.FreshnessClass != nil {
			freshness = *r.FreshnessClass
		}
		out[i] = controlExportRow{
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
			out[i].CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			out[i].UpdatedAt = r.UpdatedAt.Time
		}
	}
	return out, exceeded, nil
}

// ===== Meta-audit =====

// controlsExportMetaAudit is the JSON shape persisted to
// me_audit_log.after on every export attempt. Outcome buckets mirror
// slice 135's exportMetaAudit (success / denied:* / error:*) so a
// single forensic query can correlate across all per-entity export
// actions.
type controlsExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

// writeMetaAudit writes ONE me_audit_log row with
// action='controls_export'. Slice 135 P0-A4 (inherited) — EVERY
// terminal outcome path writes a row. Uses a fresh tx so the
// meta-audit row commits even when the streaming write is in flight.
//
// Failure of the meta-audit write itself is intentionally non-fatal
// to the caller: the export body is the authoritative artifact, and
// failing the caller to surface an audit-write failure would be a
// bad trade-off. Production deployments wire the slice 126 external
// sink to mirror these rows tamper-evidently.
func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta controlsExportMetaAudit) {
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
		Action:   metaAuditActionControlsExport,
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
// tests override via [ExportHandler.WithLimiter].
func (h *ExportHandler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// ===== Role gate =====

// controlsHasProgramRead reports whether the credential carries an
// explicit program-read role signal. Mirrors `hasProgramRead` from
// `internal/api/risks/authz.go`; the controls catalog includes the
// same class of tenant-private data (applicability_expr) and demands
// the same gate.
//
// Deliberately stricter than authz.derivedRolesFor — a bare push
// credential (no flags) has no business reading the control catalog.
func controlsHasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== Counting writer =====

// controlsCountingWriter wraps an io.Writer and counts bytes written.
// Used to record body byte-count for the meta-audit row.
type controlsCountingWriter struct {
	w io.Writer
	n int64
}

func (c *controlsCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
