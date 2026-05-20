// Slice 138 — evidence ledger metadata data-export handler.
//
// `GET /v1/admin/evidence/export?format=<csv|json|xlsx>` reuses the
// slice 135 data-export library (`internal/export/`) and the slice 145
// per-(tenant, user) concurrency cap. Closest precedent is slice 137's
// controls export (`internal/api/controls/export.go`); this handler
// mirrors the same shape with one slice-specific difference:
//
//   - **Column projection EXCLUDES `payload`** (slice 138 P0-A-Ledger-1
//     / D1). The evidence payload may contain vendor secrets (an AWS
//     S3 evidence record's bucket-policy JSON, a 1Password evidence
//     record's KDF parameter blob, etc.). Operators who need payload
//     introspection use the evidence-detail page (which is
//     RLS-protected read), NOT bulk export. Surfaced columns: id,
//     evidence_query_id, control_id, scope_id, observed_at, ingested_at,
//     result, content_hash (the `hash` column), payload_uri (artifact
//     pointer — opaque), freshness_class, valid_until, created_at.
//
// Constitutional posture inherits slice 137 verbatim (RLS, library
// reuse, concurrency cap, streaming-write only).
//
// This package is in the coverage-gate `excludes` list
// (`cmd/scripts/coverage-thresholds.json`), so unit-test coverage
// floors do not apply here; correctness is validated by the
// per-package unit suite + the cross-tenant integration test
// inherited from slice 135's library.

package evidence

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
	// defaultEvidenceExportRowCap is the slice 138 row-cap for the
	// evidence ledger metadata export. Matches the slice 135 library
	// default; the evidence ledger is large but per-row payloads are
	// excluded, so individual rows are narrow.
	defaultEvidenceExportRowCap = 50_000

	// metaAuditActionEvidenceExport is the slice 138 meta-audit action
	// value. The migration extends the `me_audit_log.action` CHECK to
	// permit this value. Plural `evidence_` matches the slice 137 /
	// 139 plural-of-entity-name convention.
	metaAuditActionEvidenceExport = "evidence_export"

	// evidenceExportEntity is the slice 135 BuildFilename entity
	// identifier for the evidence export. The downloaded filename will
	// look like `evidence_20260520.csv`.
	evidenceExportEntity = "evidence"
)

// ExportHandler owns the slice 138 evidence ledger metadata export.
type ExportHandler struct {
	source  evidenceExportSource
	pool    *pgxpool.Pool
	limiter *export.Limiter
}

// evidenceExportSource lets tests inject a deterministic data source
// without standing up the full RLS plumbing.
type evidenceExportSource interface {
	listForExport(ctx context.Context, limit int) ([]evidenceExportRow, bool, error)
}

// evidenceExportRow is the narrow projection the exporter needs.
// Payload column is intentionally absent (slice 138 P0-A-Ledger-1 / D1).
type evidenceExportRow struct {
	ID              uuid.UUID
	EvidenceQueryID string // empty when null
	ControlID       uuid.UUID
	ScopeID         string // empty when null
	ObservedAt      time.Time
	IngestedAt      time.Time
	Result          string
	ContentHash     string // surfaced as "content_hash" (the `hash` column)
	PayloadURI      string // empty when null — opaque artifact pointer
	FreshnessClass  string
	ValidUntil      string // empty when null; RFC3339 when set
	CreatedAt       time.Time
}

// NewExportHandler constructs the slice 138 evidence export handler.
func NewExportHandler(pool *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{pool: pool}
}

// WithSource installs an evidenceExportSource for testing.
func (h *ExportHandler) WithSource(s evidenceExportSource) *ExportHandler {
	h.source = s
	return h
}

// WithLimiter installs a Limiter for testing.
func (h *ExportHandler) WithLimiter(l *export.Limiter) *ExportHandler {
	h.limiter = l
	return h
}

// ExportEvidence handles `GET /v1/admin/evidence/export?format=...`.
func (h *ExportHandler) ExportEvidence(w http.ResponseWriter, r *http.Request) {
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

	format, formatErr := parseEvidenceExportFormat(r)
	if formatErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: formatErr.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, formatErr.Error())
		return
	}

	if !evidenceHasProgramRead(cred) {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "role does not grant evidence/program-read access",
		})
		exportWriteError(w, http.StatusForbidden, "role does not grant evidence/program-read access")
		return
	}

	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
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
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	rowCap := defaultEvidenceExportRowCap
	rows, exceededCap, err := h.listEvidenceForExport(ctx, rowCap+1)
	if err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format: string(format),
			Result: "error:query",
			Reason: err.Error(),
		})
		exportWriteError(w, http.StatusInternalServerError, "list evidence for export: "+err.Error())
		return
	}

	if exceededCap {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", rowCap),
			RowCount: len(rows),
		})
		exportWriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d evidence records; "+
				"contact the maintainer if your ledger legitimately exceeds this ceiling",
				rowCap))
		return
	}

	header := evidenceExportHeader()
	filename := export.BuildFilename(evidenceExportEntity, encoder.FileExt(), nil)

	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &evidenceCountingWriter{w: w}
	if err := encoder.WriteRows(cw, header, evidenceToRowIter(rows)); err != nil {
		h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
			Format:    string(format),
			Result:    "error:encoder",
			Reason:    "encoder: " + err.Error(),
			RowCount:  len(rows),
			ByteCount: cw.n,
		})
		return
	}

	h.writeMetaAudit(ctx, tenantID, userIdentifier, evidenceExportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(rows),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

func parseEvidenceExportFormat(r *http.Request) (export.Format, error) {
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

// evidenceExportHeader is the canonical column list for the slice 138
// evidence ledger metadata export. **Payload is intentionally absent**
// (slice 138 P0-A-Ledger-1 / D1). The decisions log records this as
// the load-bearing constraint of the slice.
//
// Column groups:
//
//	identity:    id, control_id, scope_id, evidence_query_id
//	observation: observed_at, ingested_at, result, freshness_class
//	integrity:   content_hash (the `hash` column at the data layer)
//	pointer:     payload_uri  (opaque — artifact-store pointer for
//	             large payloads; an operator can fetch the artifact
//	             through the RLS-protected /v1/artifacts/{id} surface
//	             when authorized)
//	lifecycle:   valid_until, created_at
func evidenceExportHeader() []string {
	return []string{
		"id",
		"control_id",
		"scope_id",
		"evidence_query_id",
		"observed_at",
		"ingested_at",
		"result",
		"freshness_class",
		"content_hash",
		"payload_uri",
		"valid_until",
		"created_at",
	}
}

func evidenceToRowIter(rows []evidenceExportRow) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, r := range rows {
			row := []string{
				r.ID.String(),
				r.ControlID.String(),
				r.ScopeID,
				r.EvidenceQueryID,
				r.ObservedAt.UTC().Format(time.RFC3339),
				r.IngestedAt.UTC().Format(time.RFC3339),
				r.Result,
				r.FreshnessClass,
				r.ContentHash,
				r.PayloadURI,
				r.ValidUntil,
				r.CreatedAt.UTC().Format(time.RFC3339),
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== Store adapter =====

func (h *ExportHandler) listEvidenceForExport(ctx context.Context, limit int) ([]evidenceExportRow, bool, error) {
	if h.source != nil {
		return h.source.listForExport(ctx, limit)
	}
	return h.listEvidenceDirect(ctx, limit)
}

// listEvidenceDirect runs the RLS-scoped SELECT directly off the pool.
// Inline SQL is used (rather than a sqlc-generated query) because the
// column projection is slice-138-specific (payload excluded) and adding
// a one-off sqlc query for a single handler exceeds the slice budget;
// the inline SELECT is colocated with the column projection in
// evidenceExportHeader so a future contributor sees both shapes at
// once.
//
// RLS posture: the tenant GUC is set via tenancy.ApplyTenant, and a
// defensive WHERE tenant_id = $1 clause backs it up. The atlas_app
// role is NOSUPERUSER NOBYPASSRLS; the RLS policy on evidence_records
// enforces tenant isolation.
func (h *ExportHandler) listEvidenceDirect(ctx context.Context, limit int) ([]evidenceExportRow, bool, error) {
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
			scope_id,
			evidence_query_id,
			observed_at,
			ingested_at,
			result::text,
			freshness_class::text,
			hash,
			payload_uri,
			valid_until,
			created_at
		FROM evidence_records
		WHERE tenant_id = $1
		ORDER BY observed_at DESC, id ASC
		LIMIT $2
	`, tenantID, int32(limit))
	if err != nil {
		return nil, false, fmt.Errorf("query evidence_records: %w", err)
	}
	defer rs.Close()

	var out []evidenceExportRow
	for rs.Next() {
		var (
			id              uuid.UUID
			controlID       uuid.UUID
			scopeID         pgtype.UUID
			evidenceQueryID pgtype.UUID
			observedAt      time.Time
			ingestedAt      time.Time
			result          string
			freshnessClass  string
			contentHash     string
			payloadURI      pgtype.Text
			validUntil      pgtype.Timestamptz
			createdAt       time.Time
		)
		if err := rs.Scan(
			&id, &controlID, &scopeID, &evidenceQueryID,
			&observedAt, &ingestedAt, &result, &freshnessClass,
			&contentHash, &payloadURI, &validUntil, &createdAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan evidence row: %w", err)
		}
		row := evidenceExportRow{
			ID:             id,
			ControlID:      controlID,
			ObservedAt:     observedAt,
			IngestedAt:     ingestedAt,
			Result:         result,
			FreshnessClass: freshnessClass,
			ContentHash:    contentHash,
			CreatedAt:      createdAt,
		}
		if scopeID.Valid {
			row.ScopeID = uuid.UUID(scopeID.Bytes).String()
		}
		if evidenceQueryID.Valid {
			row.EvidenceQueryID = uuid.UUID(evidenceQueryID.Bytes).String()
		}
		if payloadURI.Valid {
			row.PayloadURI = payloadURI.String
		}
		if validUntil.Valid {
			row.ValidUntil = validUntil.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	if rs.Err() != nil {
		return nil, false, fmt.Errorf("iterate evidence rows: %w", rs.Err())
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

type evidenceExportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

func (h *ExportHandler) writeMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta evidenceExportMetaAudit) {
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
		Action:   metaAuditActionEvidenceExport,
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

// evidenceHasProgramRead reports whether the credential grants
// program-read access to the evidence ledger. Bare push credentials
// (no flags) MUST NOT enumerate the ledger; that's a deliberate
// separation from the push-side rate-limited POST surface.
func evidenceHasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// ===== Counting writer / shared helpers =====

type evidenceCountingWriter struct {
	w io.Writer
	n int64
}

func (c *evidenceCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// exportWriteError is a JSON error helper local to the export handler
// (the package's writeJSON exists but is package-scoped to the push
// path's content negotiation; we mirror the slice 137 controls package
// pattern for export-specific error responses).
func exportWriteError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
