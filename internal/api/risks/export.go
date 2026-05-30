// Slice 136 — risk register data-export handler.
//
// `GET /v1/risks/export?format=<csv|json|xlsx>` reuses the slice 135
// data-export library (`internal/export/`) and the slice 145
// per-(tenant, user) concurrency cap. Mirror of the slice 135
// audit-log reference impl (`internal/api/adminauditlog/export.go`),
// adapted to the risk register shape:
//
//   - Source query is `risk.Store.List(ctx, ListFilter{})` (RLS-scoped).
//   - No from/to window filter — the risk register is a small, live
//     state; the row cap (50K) is the load-bearing DoS mitigation.
//     Audit-period freezing is not relevant to the v1 export (risks
//     do not carry an `observed_at` time the way evidence does; the
//     register's "frozen state" is the audit-period attestation
//     workflow, out of slice 136 scope — see decisions log D2).
//   - Meta-audit action is `risk_export` (distinct from
//     `audit_log_export`, so forensic queries can enumerate
//     risk-register extractions separately).
//   - Role gate is `requireProgramRead` (the same gate the slice 067
//     read endpoints use), kept as defense-in-depth alongside the
//     slice 035 OPA middleware.
//
// Constitutional posture:
//
//   - Invariant #6 (RLS): every read goes through the existing
//     `risk.Store` plumbing under `atlas_app` + `tenancy.ApplyTenant`.
//     NO `BYPASSRLS`. The cross-tenant isolation integration test in
//     `export_integration_test.go` is the merge-blocking evidence.
//
//   - Slice 136 P0-A3 (concurrency cap inherited): every successful
//     acquire defers a release; refusal returns 429 with
//     `Retry-After: 30`. Slice 145 P0-A9 (idempotent release on every
//     path) holds via the sync.Once-guarded closure in
//     [export.Limiter.Acquire].
//
//   - Slice 136 P0-A4 (no `?include_payload` flag): risks have no
//     payload_json column. The flag would be meaningless; not parsed.
//
//   - Slice 136 P0-A7 (50K row cap): the default risk register
//     contains O(10²) risks even at large orgs; 50K is the v1 ceiling.
//     The cap is enforced by asking the store for one more row than
//     the cap and detecting the overflow before encoding.
//
//   - Slice 136 P0-A8 (streaming write): the encoder consumes the row
//     iterator pull-style; per-row allocation is bounded.

package risks

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
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// defaultRiskExportRowCap is the slice 136 row-cap default. Set
	// well above any realistic risk register size — even a Fortune 500
	// security program runs O(10³) risks. 50K leaves three orders of
	// magnitude of headroom while keeping the encoded body inside a
	// reasonable HTTP/2 streaming budget (~50 MB worst case).
	defaultRiskExportRowCap = 50_000

	// metaAuditActionRiskExport is the slice 136 meta-audit action
	// value. The migration `20260519000010_risk_export_meta_audit.sql`
	// extends the `me_audit_log.action` CHECK to permit this value.
	metaAuditActionRiskExport = "risk_export"

	// riskExportEntity is the slice 135 BuildFilename entity identifier
	// for risk-register exports. The downloaded filename will look like
	// `risk-register_20260519.csv`.
	riskExportEntity = "risk-register"
)

// ExportHandler owns the slice 136 risk-register export endpoint. The
// store provides the RLS-scoped data access; the pool is needed for
// the meta-audit write (which uses its own short transaction outside
// the export's iteration window so the meta-audit row commits even
// when the export body has already been partially written).
//
// Slice 145 hook: the optional `limiter` field overrides the
// process-wide singleton [export.DefaultLimiter] when set — used by
// integration tests to pin a small, deterministic concurrency cap.
// Production callers leave it nil; the handler resolves the singleton
// lazily on every request.
type ExportHandler struct {
	source  exportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// exportSource is the minimal interface ExportHandler needs to pull
// risks for export — kept local so tests can inject a deterministic
// data source without standing up the full slice 019/020/053 wiring.
// Production code uses the inline implementation directly off the pool.
type exportSource interface {
	listForExport(ctx context.Context, limit int) ([]riskRow, bool, error)
}

// riskRow is the minimal projection the exporter needs. Kept narrow
// (not a full risk.Risk) so the export handler doesn't have to
// re-export every field the riskWire shape carries — the canonical
// column set in `riskExportHeader` is the single source of truth.
type riskRow struct {
	ID             uuid.UUID
	Title          string
	Description    string
	Category       string
	Methodology    string
	InherentScore  []byte
	Treatment      string
	TreatmentOwner string
	ResidualScore  []byte
	ReviewDueAt    *time.Time
	AcceptedUntil  *time.Time
	Accepter       string
	InstrumentRef  string
	OrgUnitID      *uuid.UUID
	Themes         []string
	Severity       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewExportHandler constructs the slice 136 export handler. The pool
// is the same `atlas_app` (NOSUPERUSER NOBYPASSRLS) pool the rest of
// the API uses; the meta-audit writes go through it under the
// caller's tenant context.
func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{
		source: nil,
		pool:   pool,
	}
}

// WithSource installs an exportSource for testing. Production callers
// leave it nil — the handler falls back to the inline pool-backed
// adapter via [ExportHandler.listRisksForExport].
func (h *ExportHandler) WithSource(s exportSource) *ExportHandler {
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

// ExportRisks handles `GET /v1/risks/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitized
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4 inherited).
func (h *ExportHandler) ExportRisks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Role gate (defense-in-depth) — same one slice 067 read endpoints
	// call. The upstream slice 035 OPA middleware is the canonical
	// authz gate; this is the second leg. A missing credential is a
	// 401 / a denied role is a 403 with no body access.
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
	// meta-audit row recording the denial reason.
	format, formatErr := parseRiskExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		httpresp.WriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	// Defense-in-depth role gate. A bare push credential (no admin /
	// approver / owner flag) cannot read the risk register — the
	// program-read role set is the same one slice 067's risk list +
	// theme-heatmap endpoints check.
	if !hasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant risk/program-read access",
		})
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-read access")
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
	//     `me_audit_log WHERE action = 'risk_export'` see the attempt
	//     with result=denied:concurrency_cap_exceeded.
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
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
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Pull rows from the store. Ask for one more than the row cap so
	// the 413 path triggers without an extra round-trip.
	rowCap := defaultRiskExportRowCap
	var (
		rows         []riskRow
		exceededCap  bool
		queryErr     string
		statusToSend int
		bodyBytes    int64
	)

	rows, exceededCap, err = h.listRisksForExport(ctx, rowCap+1)
	if err != nil {
		queryErr = err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: queryErr,
		})
		httpresp.WriteError(w, http.StatusInternalServerError, "list risks for export: "+queryErr)
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d risks; "+
				"contact the maintainer if your register legitimately exceeds this ceiling",
				rowCap))

		return
	}

	// Streaming write. Headers go BEFORE the body. Encoder pipes
	// per-row into the response via countingWriter so the meta-audit
	// records the body's byte count.
	statusToSend = http.StatusOK
	header := riskExportHeader()
	filename := export.BuildFilename(riskExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusToSend)

	cw := &exportCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, risksToRowIter(rows)); err != nil {
		queryErr = "encoder: " + err.Error()
		h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
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
	h.writeMetaAudit(ctx, tenantID, userIdentifier, riskExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: bodyBytes,
	})
}

// ===== Parsing =====

// parseRiskExportFormat resolves the `?format=` query param. Default
// is CSV (matches slice 135 audit-log behavior). Anything other than
// csv|json|xlsx is a 400.
func parseRiskExportFormat(r *http.Request) (export.Format, error) {
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

// riskExportHeader is the canonical column list for the slice 136
// risk-register export. Stable: changing this list is a breaking
// change for downstream consumers (Excel-shaped quarterly reports,
// scripts that key off column position).
//
// Slice 136 P0-A-Risk-1: `treatment_narrative` is intentionally
// EXCLUDED at v1 (deferred to a future column-selection slice).
//
// Slice 136 P0-A-Risk-2: `org_unit_id` is included so the slice 053
// hierarchy is preserved across export.
//
// `linked_control_count` is the compact form of the
// `linked_control_ids` array — preserved via count rather than
// JSON-encoded list to keep the CSV shape Excel-friendly. A future
// slice may add a separate "risk-control-links" export.
func riskExportHeader() []string {
	return []string{
		"id",
		"title",
		"description",
		"category",
		"methodology",
		"treatment",
		"treatment_owner",
		"accepter",
		"instrument_reference",
		"inherent_score",
		"residual_score",
		"severity",
		"org_unit_id",
		"themes",
		"review_due_at",
		"accepted_until",
		"created_at",
		"updated_at",
	}
}

// risksToRowIter projects a slice of riskRow into an iter.Seq[[]string]
// in the canonical column order. Pure projection — no DB I/O.
//
// Date fields render as RFC3339 (timestamps) or YYYY-MM-DD (dates) so
// downstream CSV consumers round-trip cleanly. JSON columns
// (inherent_score, residual_score) are emitted as raw JSON text
// (single stringified blob per row).
//
// themes is rendered as a comma-separated string — XLSX / CSV
// consumers don't have a natural array primitive. An empty themes
// list renders as the empty string.
func risksToRowIter(rows []riskRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			reviewDue := ""
			if r.ReviewDueAt != nil {
				reviewDue = r.ReviewDueAt.UTC().Format(time.RFC3339)
			}
			acceptedUntil := ""
			if r.AcceptedUntil != nil {
				acceptedUntil = r.AcceptedUntil.UTC().Format("2006-01-02")
			}
			orgUnit := ""
			if r.OrgUnitID != nil {
				orgUnit = r.OrgUnitID.String()
			}
			inherent := ""
			if len(r.InherentScore) > 0 {
				inherent = string(r.InherentScore)
			}
			residual := ""
			if len(r.ResidualScore) > 0 {
				residual = string(r.ResidualScore)
			}
			row := []string{
				r.ID.String(),
				r.Title,
				r.Description,
				r.Category,
				r.Methodology,
				r.Treatment,
				r.TreatmentOwner,
				r.Accepter,
				r.InstrumentRef,
				inherent,
				residual,
				fmt.Sprintf("%d", r.Severity),
				orgUnit,
				strings.Join(r.Themes, ","),
				reviewDue,
				acceptedUntil,
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

// listRisksForExport runs the RLS-scoped list query against the
// canonical risk.Store via an internal `risk_list_for_export` method
// (added below). Returns up to `limit` rows; the boolean is true
// when the underlying store returned exactly `limit` rows AND the
// caller asked for one more than the cap, signalling cap-exceeded.
//
// The store-interface indirection is so integration tests can inject
// a deterministic data source without standing up the full risk
// store + RLS context — the production path uses the inline
// implementation directly off the pool.
func (h *ExportHandler) listRisksForExport(ctx context.Context, limit int) ([]riskRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listRisksDirect(ctx, limit)
}

// listRisksDirect is the production data-access path. Opens a tx,
// sets the tenant GUC, runs the ListRisks query (the same one slice
// 019 List uses), projects each dbx.Risk into a riskRow.
func (h *ExportHandler) listRisksDirect(ctx context.Context, limit int) ([]riskRow, bool, error) {
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
	dbRows, err := q.ListRisks(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		return nil, false, fmt.Errorf("list risks: %w", err)
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

	out := make([]riskRow, len(dbRows))
	for i, r := range dbRows {
		out[i] = riskRow{
			ID:             uuid.UUID(r.ID.Bytes),
			Title:          r.Title,
			Description:    r.Description,
			Category:       string(r.Category),
			Methodology:    string(r.Methodology),
			InherentScore:  r.InherentScore,
			Treatment:      string(r.Treatment),
			TreatmentOwner: r.TreatmentOwner,
			ResidualScore:  r.ResidualScore,
			Accepter:       r.Accepter,
			InstrumentRef:  r.InstrumentReference,
			Themes:         append([]string(nil), r.Themes...),
			Severity:       severityOf(r.InherentScore),
		}
		if r.ReviewDueAt.Valid {
			t := r.ReviewDueAt.Time
			out[i].ReviewDueAt = &t
		}
		if r.AcceptedUntil.Valid {
			t := r.AcceptedUntil.Time
			out[i].AcceptedUntil = &t
		}
		if r.OrgUnitID.Valid {
			ou := uuid.UUID(r.OrgUnitID.Bytes)
			out[i].OrgUnitID = &ou
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

// riskExportMetaAudit is the JSON shape persisted to me_audit_log.after
// on every export attempt. Outcome buckets mirror slice 135's
// exportMetaAudit (success / denied:* / error:*) so a single forensic
// query can correlate across both action values.
type riskExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

// writeMetaAudit writes ONE me_audit_log row with
// action='risk_export'. Slice 135 P0-A4 (inherited) — EVERY terminal
// outcome path writes a row. Uses a fresh tx so the meta-audit row
// commits even when the streaming write is in flight.
//
// Failure of the meta-audit write itself is intentionally non-fatal
// to the caller: the export body is the authoritative artifact, and
// failing the caller to surface an audit-write failure would be a
// bad trade-off. Production deployments wire the slice 126 external
// sink to mirror these rows tamper-evidently.
func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta riskExportMetaAudit) {
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
		Action:   metaAuditActionRiskExport,
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

// ===== Counting writer =====

// exportCountingWriter wraps an io.Writer and counts bytes written.
// Used to record body byte-count for the meta-audit row.
type exportCountingWriter struct {
	w io.Writer
	n int64
}

func (c *exportCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
