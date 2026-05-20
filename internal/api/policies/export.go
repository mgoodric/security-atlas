// Slice 138 — policies data-export handler.
//
// `GET /v1/admin/policies/export?format=<csv|json|xlsx>` reuses the
// slice 135 data-export library + slice 145 concurrency cap, mirroring
// the slice 137 controls export shape. The full policy row set is
// emitted: id, title, version, effective_date, owner, approver,
// status, acknowledgment_required_role (joined), body_md,
// next_review_at, created_at, updated_at.
//
// Per slice 138 threat-model addendum: policy body text is large but
// is in-scope — operators need it for audit prep. RLS enforcement is
// the only mitigation here (no column omission). The slice 135 P0-A5
// cross-tenant test exercises the RLS guard.

package policies

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
	defaultPoliciesExportRowCap   = 50_000
	metaAuditActionPoliciesExport = "policies_export"
	policiesExportEntity          = "policies"
)

// ExportHandler owns the slice 138 policies data export endpoint.
type ExportHandler struct {
	source  policiesExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

type policiesExportSource interface {
	listForExport(ctx context.Context, limit int) ([]policyExportRow, bool, error)
}

type policyExportRow struct {
	ID                          uuid.UUID
	Title                       string
	Version                     int32
	EffectiveDate               string // empty when null; YYYY-MM-DD when set
	Owner                       string
	Approver                    string
	Status                      string
	BodyMD                      string
	AcknowledgmentRequiredRoles string // joined "," — null-safe
	NextReviewAt                string // RFC3339 when set; empty when null
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

// NewExportHandler constructs the slice 138 policies export handler.
func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{pool: pool}
}

func (h *ExportHandler) WithSource(s policiesExportSource) *ExportHandler {
	h.source = s
	return h
}

func (h *ExportHandler) WithLimiter(l *export.Limiter) *ExportHandler {
	h.limiter = l
	return h
}

// ExportPolicies handles `GET /v1/admin/policies/export?format=...`.
func (h *ExportHandler) ExportPolicies(w http.ResponseWriter, r *http.Request) {
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

	format, formatErr := parsePoliciesExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	if !policiesHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant policies/program-read access",
		})
		exportWriteError(w, http.StatusForbidden, "role does not grant policies/program-read access")
		return
	}

	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
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
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rowCap := defaultPoliciesExportRowCap
	rows, exceededCap, err := h.listPoliciesForExport(ctx, rowCap+1)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusInternalServerError, "list policies for export: "+err.Error())
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		exportWriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d policies; "+
				"contact the maintainer if your policy library legitimately exceeds this ceiling",
				rowCap))
		return
	}

	header := policiesExportHeader()
	filename := export.BuildFilename(policiesExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &policiesCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, policiesToRowIter(rows)); err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    "encoder: " + err.Error(),
			RowCount:  len(rows),
			ByteCount: cw.n,
		})
		return
	}

	h.writeMetaAudit(ctx, tenantID, userIdentifier, policiesExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parsePoliciesExportFormat(r *http.Request) (export.Format, error) {
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

func policiesExportHeader() []string {
	return []string{
		"id",
		"title",
		"version",
		"status",
		"effective_date",
		"owner",
		"approver",
		"acknowledgment_required_role",
		"next_review_at",
		"body_md",
		"created_at",
		"updated_at",
	}
}

func policiesToRowIter(rows []policyExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			row := []string{
				r.ID.String(),
				r.Title,
				fmt.Sprintf("%d", r.Version),
				r.Status,
				r.EffectiveDate,
				r.Owner,
				r.Approver,
				r.AcknowledgmentRequiredRoles,
				r.NextReviewAt,
				r.BodyMD,
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

func (h *ExportHandler) listPoliciesForExport(ctx context.Context, limit int) ([]policyExportRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listPoliciesDirect(ctx, limit)
}

func (h *ExportHandler) listPoliciesDirect(ctx context.Context, limit int) ([]policyExportRow, bool, error) {
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
			title,
			version,
			status::text,
			effective_date,
			owner,
			approver,
			acknowledgment_required_role,
			next_review_at,
			body_md,
			created_at,
			updated_at
		FROM policies
		WHERE tenant_id = $1
		ORDER BY title ASC, version DESC
		LIMIT $2
	`, tenantID, int32(limit))
	if err != nil {
		return nil, false, fmt.Errorf("query policies: %w", err)
	}
	defer rs.Close()

	var out []policyExportRow
	for rs.Next() {
		var (
			id           uuid.UUID
			title        string
			version      int32
			status       string
			effective    pgtype.Date
			owner        string
			approver     string
			roles        []string
			nextReviewAt pgtype.Timestamptz
			bodyMD       string
			createdAt    time.Time
			updatedAt    time.Time
		)
		if err := rs.Scan(
			&id, &title, &version, &status, &effective,
			&owner, &approver, &roles, &nextReviewAt,
			&bodyMD, &createdAt, &updatedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan policy row: %w", err)
		}
		row := policyExportRow{
			ID:                          id,
			Title:                       title,
			Version:                     version,
			Status:                      status,
			Owner:                       owner,
			Approver:                    approver,
			BodyMD:                      bodyMD,
			AcknowledgmentRequiredRoles: strings.Join(roles, ","),
			CreatedAt:                   createdAt,
			UpdatedAt:                   updatedAt,
		}
		if effective.Valid {
			row.EffectiveDate = effective.Time.UTC().Format("2006-01-02")
		}
		if nextReviewAt.Valid {
			row.NextReviewAt = nextReviewAt.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	if rs.Err() != nil {
		return nil, false, fmt.Errorf("iterate policy rows: %w", rs.Err())
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

type policiesExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta policiesExportMetaAudit) {
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
		Action:   metaAuditActionPoliciesExport,
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

func policiesHasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== Counting writer =====

type policiesCountingWriter struct {
	w io.Writer
	n int64
}

func (c *policiesCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// exportWriteError is a JSON error helper local to the export handler.
// The package's pre-existing writeError handles read-path errors with
// a different signature; mirroring the slice 137 controls pattern
// keeps export error shape consistent across slices.
func exportWriteError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
