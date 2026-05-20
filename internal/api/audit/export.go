// Slice 138 — samples data-export handler.
//
// `GET /v1/admin/samples/export?format=<csv|json|xlsx>` reuses the
// slice 135 data-export library + slice 145 concurrency cap, mirroring
// the slice 137 controls export shape.
//
// Per slice 138 threat-model addendum: row cap is **250,000** (between
// the slice 135 default of 50K and slice 137's 500K) because sample
// populations at multi-product orgs can be voluminous. INCLUDES
// audit_period_id link (via populations.audit_period_id — slice 028's
// freezing fk) so downstream consumers can correlate samples to a
// specific frozen audit period.

package audit

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
	// defaultSamplesExportRowCap is the slice 138 row cap for the
	// samples export. Slice doc D-locked at 250,000 — between the
	// slice 135 default (50K) and slice 137's lift to 500K. Samples
	// at multi-product orgs span many audit periods × many populations
	// × N records each; 50K is too tight; 500K is overkill (samples
	// rows themselves are narrow — population_id, n, seed, created_*).
	defaultSamplesExportRowCap = 250_000

	// metaAuditActionSamplesExport is the slice 138 meta-audit action
	// value. The migration extends me_audit_log.action CHECK to
	// permit this value.
	metaAuditActionSamplesExport = "samples_export"

	samplesExportEntity = "samples"
)

// SamplesExportHandler owns the slice 138 samples export endpoint.
// Distinct type name to avoid collision with the existing audit
// package's Handler (the slice 026 sample-draw + annotate handler).
type SamplesExportHandler struct {
	source  samplesExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

type samplesExportSource interface {
	listForExport(ctx context.Context, limit int) ([]sampleExportRow, bool, error)
}

type sampleExportRow struct {
	ID                 uuid.UUID
	PopulationID       uuid.UUID
	AuditPeriodID      string // empty when populations.audit_period_id is NULL
	ControlID          uuid.UUID
	N                  int32
	Seed               string
	CreatedBy          string
	CreatedAt          time.Time
	WindowStart        time.Time
	WindowEnd          time.Time
	PopulationFrozenAt string // RFC3339 when populations.frozen_at is set
	PopulationRowCount int64
}

func NewSamplesExportHandler(pool *pgxpool.Pool) *SamplesExportHandler {
	return &SamplesExportHandler{pool: pool}
}

func (h *SamplesExportHandler) WithSource(s samplesExportSource) *SamplesExportHandler {
	h.source = s
	return h
}

func (h *SamplesExportHandler) WithLimiter(l *export.Limiter) *SamplesExportHandler {
	h.limiter = l
	return h
}

// ExportSamples handles `GET /v1/admin/samples/export?format=...`.
func (h *SamplesExportHandler) ExportSamples(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		samplesExportWriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		samplesExportWriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	format, formatErr := parseSamplesExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		samplesExportWriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	if !samplesHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant samples/program-read access",
		})
		samplesExportWriteError(w, http.StatusForbidden, "role does not grant samples/program-read access")
		return
	}

	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
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

	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		samplesExportWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rowCap := defaultSamplesExportRowCap
	rows, exceededCap, err := h.listSamplesForExport(ctx, rowCap+1)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		samplesExportWriteError(w, http.StatusInternalServerError, "list samples for export: "+err.Error())
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		samplesExportWriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d samples; "+
				"contact the maintainer if your sample register legitimately exceeds this ceiling",
				rowCap))
		return
	}

	header := samplesExportHeader()
	filename := export.BuildFilename(samplesExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &samplesCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, samplesToRowIter(rows)); err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    "encoder: " + err.Error(),
			RowCount:  len(rows),
			ByteCount: cw.n,
		})
		return
	}

	h.writeMetaAudit(ctx, tenantID, userIdentifier, samplesExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parseSamplesExportFormat(r *http.Request) (export.Format, error) {
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

// samplesExportHeader is the canonical column set. INCLUDES
// audit_period_id (joined from populations) per slice doc.
func samplesExportHeader() []string {
	return []string{
		"id",
		"population_id",
		"audit_period_id",
		"control_id",
		"n",
		"seed",
		"created_by",
		"created_at",
		"window_start",
		"window_end",
		"population_frozen_at",
		"population_row_count",
	}
}

func samplesToRowIter(rows []sampleExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			row := []string{
				r.ID.String(),
				r.PopulationID.String(),
				r.AuditPeriodID,
				r.ControlID.String(),
				fmt.Sprintf("%d", r.N),
				r.Seed,
				r.CreatedBy,
				r.CreatedAt.UTC().Format(time.RFC3339),
				r.WindowStart.UTC().Format(time.RFC3339),
				r.WindowEnd.UTC().Format(time.RFC3339),
				r.PopulationFrozenAt,
				fmt.Sprintf("%d", r.PopulationRowCount),
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== Store adapter =====

func (h *SamplesExportHandler) listSamplesForExport(ctx context.Context, limit int) ([]sampleExportRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listSamplesDirect(ctx, limit)
}

func (h *SamplesExportHandler) listSamplesDirect(ctx context.Context, limit int) ([]sampleExportRow, bool, error) {
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

	// JOIN samples → populations on (tenant_id, population_id) — the
	// composite FK guarantees the row is the caller's tenant under
	// RLS. The join surfaces audit_period_id (slice 028's freezing
	// fk), control_id (so consumers don't need a second round-trip),
	// window_start/end, frozen_at, and row_count.
	rs, err := tx.Query(ctx, `
		SELECT
			s.id,
			s.population_id,
			p.audit_period_id,
			p.control_id,
			s.n,
			s.seed,
			s.created_by,
			s.created_at,
			p.time_window_start,
			p.time_window_end,
			p.frozen_at,
			p.row_count
		FROM samples s
		JOIN populations p
			ON p.tenant_id = s.tenant_id
		   AND p.id = s.population_id
		WHERE s.tenant_id = $1
		ORDER BY s.created_at DESC, s.id ASC
		LIMIT $2
	`, tenantID, int32(limit))
	if err != nil {
		return nil, false, fmt.Errorf("query samples: %w", err)
	}
	defer rs.Close()

	var out []sampleExportRow
	for rs.Next() {
		var (
			id           uuid.UUID
			populationID uuid.UUID
			auditPeriod  pgtype.UUID
			controlID    uuid.UUID
			n            int32
			seed         string
			createdBy    string
			createdAt    time.Time
			windowStart  time.Time
			windowEnd    time.Time
			frozenAt     pgtype.Timestamptz
			rowCount     int64
		)
		if err := rs.Scan(
			&id, &populationID, &auditPeriod, &controlID,
			&n, &seed, &createdBy, &createdAt,
			&windowStart, &windowEnd, &frozenAt, &rowCount,
		); err != nil {
			return nil, false, fmt.Errorf("scan sample row: %w", err)
		}
		row := sampleExportRow{
			ID:                 id,
			PopulationID:       populationID,
			ControlID:          controlID,
			N:                  n,
			Seed:               seed,
			CreatedBy:          createdBy,
			CreatedAt:          createdAt,
			WindowStart:        windowStart,
			WindowEnd:          windowEnd,
			PopulationRowCount: rowCount,
		}
		if auditPeriod.Valid {
			row.AuditPeriodID = uuid.UUID(auditPeriod.Bytes).String()
		}
		if frozenAt.Valid {
			row.PopulationFrozenAt = frozenAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	if rs.Err() != nil {
		return nil, false, fmt.Errorf("iterate sample rows: %w", rs.Err())
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	exceeded := len(out) >= limit
	if exceeded {
		out = out[:limit]
	}
	return out, exceeded, nil
}

// ===== Meta-audit =====

type samplesExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *SamplesExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta samplesExportMetaAudit) {
	paramsBlob, _ := json.Marshal(map[string]any{"format": meta.Format})
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
		Action:   metaAuditActionSamplesExport,
		Before:   paramsBlob,
		After:    resultBlob,
	}); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ===== Limiter accessor =====

func (h *SamplesExportHandler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// ===== Role gate =====

func samplesHasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== Counting writer / shared helpers =====

type samplesCountingWriter struct {
	w io.Writer
	n int64
}

func (c *samplesCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// samplesExportWriteError mirrors the controls package's exportWriteError
// pattern. The audit package's pre-existing writeError is shared with
// the slice 026 sample-draw handler; we use a slice-138-named helper to
// avoid coupling export error-shape changes to the slice 026 handler.
func samplesExportWriteError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
