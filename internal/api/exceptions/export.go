// Slice 138 — exceptions data-export handler.
//
// `GET /v1/admin/exceptions/export?format=<csv|json|xlsx>` reuses the
// slice 135 data-export library + slice 145 concurrency cap, mirroring
// the slice 137 controls export shape.
//
// Per slice 138 threat-model addendum: exception justification +
// reviewer notes are sensitive but in-scope — operators need them for
// audit prep. RLS enforcement is the only mitigation. Owner +
// duration + justification are INCLUDED per the slice doc.

package exceptions

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
	defaultExceptionsExportRowCap   = 50_000
	metaAuditActionExceptionsExport = "exceptions_export"
	exceptionsExportEntity          = "exceptions"
)

// ExportHandler owns the slice 138 exceptions export endpoint.
type ExportHandler struct {
	source  exceptionsExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

type exceptionsExportSource interface {
	listForExport(ctx context.Context, limit int) ([]exceptionExportRow, bool, error)
}

type exceptionExportRow struct {
	ID                   uuid.UUID
	ControlID            uuid.UUID
	Status               string
	Justification        string
	CompensatingControls string // joined "|"
	ScopeCellPredicate   string // canonical-json text
	RequestedBy          string
	RequestedAt          time.Time
	ApprovedBy           string // empty when null
	ApprovedAt           string // RFC3339 when set
	DeniedBy             string
	DeniedAt             string
	ActivatedBy          string
	ActivatedAt          string
	EffectiveFrom        string
	ExpiresAt            time.Time
	ExpiredAt            string
	DurationDays         int64 // expires_at − requested_at, in days
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{pool: pool}
}

func (h *ExportHandler) WithSource(s exceptionsExportSource) *ExportHandler {
	h.source = s
	return h
}

func (h *ExportHandler) WithLimiter(l *export.Limiter) *ExportHandler {
	h.limiter = l
	return h
}

func (h *ExportHandler) ExportExceptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		exportWriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		exportWriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	userIdentifier := cred.UserID
	if userIdentifier == "" {
		userIdentifier = cred.ID
	}

	format, formatErr := parseExceptionsExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	if !exceptionsHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant exceptions/program-read access",
		})
		exportWriteError(w, http.StatusForbidden, "role does not grant exceptions/program-read access")
		return
	}

	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
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
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rowCap := defaultExceptionsExportRowCap
	rows, exceededCap, err := h.listExceptionsForExport(ctx, rowCap+1)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusInternalServerError, "list exceptions for export: "+err.Error())
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		exportWriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d exceptions; "+
				"contact the maintainer if your exception register legitimately exceeds this ceiling",
				rowCap))
		return
	}

	header := exceptionsExportHeader()
	filename := export.BuildFilename(exceptionsExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &exceptionsCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, exceptionsToRowIter(rows)); err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    "encoder: " + err.Error(),
			RowCount:  len(rows),
			ByteCount: cw.n,
		})
		return
	}

	h.writeMetaAudit(ctx, tenantID, userIdentifier, exceptionsExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parseExceptionsExportFormat(r *http.Request) (export.Format, error) {
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

// exceptionsExportHeader is the canonical column set. INCLUDES owner
// (requested_by), duration (computed expires_at − requested_at), and
// justification per slice doc.
func exceptionsExportHeader() []string {
	return []string{
		"id",
		"control_id",
		"status",
		"justification",
		"compensating_controls",
		"scope_cell_predicate",
		"requested_by",
		"requested_at",
		"approved_by",
		"approved_at",
		"denied_by",
		"denied_at",
		"activated_by",
		"activated_at",
		"effective_from",
		"expires_at",
		"expired_at",
		"duration_days",
		"created_at",
		"updated_at",
	}
}

func exceptionsToRowIter(rows []exceptionExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			row := []string{
				r.ID.String(),
				r.ControlID.String(),
				r.Status,
				r.Justification,
				r.CompensatingControls,
				r.ScopeCellPredicate,
				r.RequestedBy,
				r.RequestedAt.UTC().Format(time.RFC3339),
				r.ApprovedBy,
				r.ApprovedAt,
				r.DeniedBy,
				r.DeniedAt,
				r.ActivatedBy,
				r.ActivatedAt,
				r.EffectiveFrom,
				r.ExpiresAt.UTC().Format(time.RFC3339),
				r.ExpiredAt,
				fmt.Sprintf("%d", r.DurationDays),
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

func (h *ExportHandler) listExceptionsForExport(ctx context.Context, limit int) ([]exceptionExportRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listExceptionsDirect(ctx, limit)
}

func (h *ExportHandler) listExceptionsDirect(ctx context.Context, limit int) ([]exceptionExportRow, bool, error) {
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

	rs, err := tx.Query(ctx, `
		SELECT
			id,
			control_id,
			status,
			justification,
			compensating_controls,
			scope_cell_predicate::text,
			requested_by,
			requested_at,
			approved_by,
			approved_at,
			denied_by,
			denied_at,
			activated_by,
			activated_at,
			effective_from,
			expires_at,
			expired_at,
			created_at,
			updated_at
		FROM exceptions
		WHERE tenant_id = $1
		ORDER BY requested_at DESC, id ASC
		LIMIT $2
	`, tenantID, int32(limit))
	if err != nil {
		return nil, false, fmt.Errorf("query exceptions: %w", err)
	}
	defer rs.Close()

	var out []exceptionExportRow
	for rs.Next() {
		var (
			id            uuid.UUID
			controlID     uuid.UUID
			status        string
			justification string
			compensating  []string
			scopeCellPred string
			requestedBy   string
			requestedAt   time.Time
			approvedBy    pgtype.Text
			approvedAt    pgtype.Timestamptz
			deniedBy      pgtype.Text
			deniedAt      pgtype.Timestamptz
			activatedBy   pgtype.Text
			activatedAt   pgtype.Timestamptz
			effectiveFrom pgtype.Timestamptz
			expiresAt     time.Time
			expiredAt     pgtype.Timestamptz
			createdAt     time.Time
			updatedAt     time.Time
		)
		if err := rs.Scan(
			&id, &controlID, &status, &justification,
			&compensating, &scopeCellPred,
			&requestedBy, &requestedAt,
			&approvedBy, &approvedAt,
			&deniedBy, &deniedAt,
			&activatedBy, &activatedAt,
			&effectiveFrom, &expiresAt, &expiredAt,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan exception row: %w", err)
		}
		row := exceptionExportRow{
			ID:                   id,
			ControlID:            controlID,
			Status:               status,
			Justification:        justification,
			CompensatingControls: strings.Join(compensating, "|"),
			ScopeCellPredicate:   scopeCellPred,
			RequestedBy:          requestedBy,
			RequestedAt:          requestedAt,
			ExpiresAt:            expiresAt,
			CreatedAt:            createdAt,
			UpdatedAt:            updatedAt,
		}
		// Duration in days (truncated to whole days). Always >= 0
		// because the DB CHECK constraint enforces
		// expires_at <= requested_at + 365d AND expires_at NOT NULL.
		row.DurationDays = int64(expiresAt.Sub(requestedAt) / (24 * time.Hour))
		if approvedBy.Valid {
			row.ApprovedBy = approvedBy.String
		}
		if approvedAt.Valid {
			row.ApprovedAt = approvedAt.Time.UTC().Format(time.RFC3339)
		}
		if deniedBy.Valid {
			row.DeniedBy = deniedBy.String
		}
		if deniedAt.Valid {
			row.DeniedAt = deniedAt.Time.UTC().Format(time.RFC3339)
		}
		if activatedBy.Valid {
			row.ActivatedBy = activatedBy.String
		}
		if activatedAt.Valid {
			row.ActivatedAt = activatedAt.Time.UTC().Format(time.RFC3339)
		}
		if effectiveFrom.Valid {
			row.EffectiveFrom = effectiveFrom.Time.UTC().Format(time.RFC3339)
		}
		if expiredAt.Valid {
			row.ExpiredAt = expiredAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	if rs.Err() != nil {
		return nil, false, fmt.Errorf("iterate exception rows: %w", rs.Err())
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

type exceptionsExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta exceptionsExportMetaAudit) {
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
		Action:   metaAuditActionExceptionsExport,
		Before:   paramsBlob,
		After:    resultBlob,
	}); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// ===== Limiter accessor =====

func (h *ExportHandler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// ===== Role gate =====

func exceptionsHasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== Counting writer / shared helpers =====

type exceptionsCountingWriter struct {
	w io.Writer
	n int64
}

func (c *exceptionsCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func exportWriteError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
