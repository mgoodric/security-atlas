// Package adminauditperiods is the slice-139 HTTP surface for the
// audit-periods data-export endpoint:
//
//	GET /v1/admin/audit-periods/export?format=<csv|json|xlsx>
//
// Spillover from slice 135 (data-export library, merged at 6d4d2a0).
// The endpoint reuses `internal/export/` verbatim and follows the
// slice 135 reference impl in `internal/api/adminauditlog/` for the
// concurrency cap (slice 145), meta-audit (`audit_periods_export`),
// streaming write, and filename sanitization contract.
//
// # Column set
//
// The canonical column set mirrors `auditperiods.periodWire` from the
// slice-028 read endpoint, with one additive column at the end
// (`frozen_hash`) so an operator auditing the freeze trail sees every
// piece of constitutional invariant #10 (audit-period freezing)
// metadata that lives in the row:
//
//	id, name, framework_version_id, period_start, period_end,
//	status, frozen_at, frozen_by, frozen_hash, created_by,
//	created_at, updated_at
//
// `frozen_at` / `frozen_by` / `frozen_hash` are intentionally INCLUDED
// (slice 139 constitutional addendum) so the freeze trail is legible
// offline. The cosigned bundle bytes themselves are NOT included —
// slice 030 owns that surface (P0-A-AP-1).
//
// # Threat model
//
// Inherits slice 135. Per-entity addendums:
//
//   - Frozen periods carry `frozen_by` (an operator identifier) and
//     `frozen_hash` (a content hash). Both are forensic primitives the
//     export deliberately surfaces. They are NOT PII by themselves;
//     `frozen_by` is a UUID-shaped user id, not an email.
//   - `frozen_artifact_uri` (referenced in the slice doc threat model)
//     does not live on the `audit_periods` row in the current schema;
//     the slice-030 bundle export is the canonical surface for the
//     bundle ref + bytes. This export does NOT invent a column for it.
//
// # Constitutional posture
//
//   - Invariant #6 (RLS): every read goes through the slice-028
//     `period.Store.List` which applies `tenancy.ApplyTenant` under
//     `atlas_app`. NO `BYPASSRLS`. The cross-tenant isolation
//     integration test in `export_integration_test.go` is the
//     merge-blocking evidence (slice 139 P0-A10).
//
//   - Invariant #10 (audit-period freezing): the column set surfaces
//     the freeze metadata so an operator auditing the freeze trail
//     can confirm `frozen_at <= period_end` for every frozen row in
//     one offline read.
//
//   - Slice 135 P0-A4 (meta-audit on every outcome): every code path
//     in [Handler.ExportAuditPeriods] writes a `me_audit_log` row
//     before returning — success, 403, 413, 429, or 500.
//
//   - Slice 135 P0-A7 (streaming write): the row iterator is built
//     against the `period.Store.List` result slice (one tenant's
//     audit_periods, naturally bounded), and the encoder pipes per-row
//     into the HTTP response. Per-row allocation is bounded.
//
//   - Slice 145 P0-HARDEN-3 (concurrency cap): every export acquires
//     the slice-145 per-(tenant, user) semaphore BEFORE the DB read.
//     The release is deferred on every exit path.
package adminauditperiods

import (
	"context"
	"encoding/hex"
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
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// defaultExportRowCap is the slice 139 P0-A7 row-cap default.
	// 50K is the slice doc's stated ceiling — small enough that a
	// pathological tenant with a runaway audit_periods table still
	// streams in one HTTP/2 budget, large enough to cover any
	// realistic operator (audit periods are quarterly artifacts; a
	// tenant accumulating 50K is already a forensic-event-grade
	// outlier worth narrowing the filter for).
	defaultExportRowCap = 50_000

	// metaAuditActionExport is the slice 139 meta-audit action value.
	// Distinct from slice 135's `audit_log_export` so a forensic
	// query can cleanly enumerate audit_periods bulk dumps. The
	// migration `20260519000000_audit_periods_vendors_export.sql`
	// extends the `me_audit_log.action` CHECK to permit this value.
	metaAuditActionExport = "audit_periods_export"

	// exportEntity is the slice 135 filename-builder entity string.
	// Sanitized by [export.BuildFilename] before reaching the
	// Content-Disposition header.
	exportEntity = "audit-periods"
)

// Handler owns the slice-139 audit-periods export endpoint. Constructed
// via [New]; tests inject a small-capacity limiter via [Handler.WithLimiter].
type Handler struct {
	pool    *pgxpool.Pool
	store   *period.Store
	limiter *export.Limiter
}

// New constructs a Handler over the provided pgxpool. The internal
// [period.Store] is the slice-028 store; the slice-139 export reuses it
// rather than touching `internal/audit/period/` to keep the slice
// surgical.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		pool:  pool,
		store: period.NewStore(pool),
	}
}

// WithLimiter installs a Limiter into the handler — used by integration
// tests to pin a small, deterministic cap (default 2) without setting
// the env var across the whole test process. Production callers MUST
// NOT use this — the default singleton is the only correct shape for a
// process-wide cap.
func (h *Handler) WithLimiter(l *export.Limiter) *Handler {
	h.limiter = l
	return h
}

// ExportAuditPeriods handles `GET /v1/admin/audit-periods/export?format=...`.
//
// Returns the encoded file body (CSV / JSON / XLSX) on success,
// streamed back with the appropriate Content-Type and a sanitized
// Content-Disposition filename. Writes a `me_audit_log` row on EVERY
// terminal outcome (slice 135 P0-A4).
func (h *Handler) ExportAuditPeriods(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	// Parse + validate format. Bad format -> 400, meta-audit fires
	// with denied:bad_request so the trail captures the attempt.
	format, parseErr := parseFormat(r)
	if parseErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:bad_request",
			Reason: parseErr.Error(),
		})
		writeError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	// Role gate (defense-in-depth) — admin OR auditor OR
	// grc_engineer, mirroring slice 135's admit set. The upstream
	// slice-035 OPA middleware is the canonical gate; this handler
	// adds the local check so unit-test servers (which run without
	// OPA wired) still 403 cleanly.
	if !callerAllowedExport(cred) {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:forbidden",
			Reason: "caller lacks admin|auditor|grc_engineer role",
		})
		writeError(w, http.StatusForbidden, "admin, auditor, or grc_engineer role required")
		return
	}

	// Slice 145 concurrency cap. Acquired AFTER auth + role gate but
	// BEFORE encoder resolve / DB work. Defer release on every exit
	// path (P0-A9).
	limiter := h.exportLimiter()
	release, capErr := limiter.Acquire(tenantID, userIdentifier)
	if capErr != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
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

	// Resolve encoder — secondary safe lookup; parseFormat already
	// validated.
	encoder, err := export.ResolveExporter(format)
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "denied:bad_format",
			Reason: err.Error(),
		})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Row read. The slice-028 store's List opens its own transaction
	// + applies the tenant GUC; no need to re-do that here. The list
	// is naturally bounded by the tenant's audit_periods count; we
	// apply the row cap on the materialized slice for the same
	// "narrow the filter" UX as slice 135.
	periods, err := h.store.List(ctx)
	if err != nil {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format: string(format),
			Result: "error:db",
			Reason: err.Error(),
		})
		httperr.WriteInternal(w, r, "list audit periods", err)
		return
	}

	if len(periods) > defaultExportRowCap {
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:   string(format),
			Result:   "denied:row_cap_exceeded",
			Reason:   fmt.Sprintf("rowCap=%d", defaultExportRowCap),
			RowCount: len(periods),
		})
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("export would exceed row cap of %d audit periods; "+
				"narrow the request scope and retry", defaultExportRowCap))
		return
	}

	// Stream the body. Header order matches [auditPeriodsExportHeader];
	// row projection is in [periodsToRowIter].
	header := auditPeriodsExportHeader()
	filename := export.BuildFilename(exportEntity, encoder.FileExt(), nil)
	w.Header().Set("Content-Type", encoder.ContentType())
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := &countingWriter{w: w}
	if err := encoder.WriteRows(cw, header, periodsToRowIter(periods)); err != nil {
		// Body already started; we cannot change status. Record the
		// partial state in the meta-audit.
		h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
			Format:    string(format),
			Result:    "error:encode",
			Reason:    err.Error(),
			RowCount:  len(periods),
			ByteCount: cw.n,
		})
		return
	}
	h.writeExportMetaAudit(ctx, tenantID, userIdentifier, exportMetaAudit{
		Format:    string(format),
		Result:    "success",
		RowCount:  len(periods),
		ByteCount: cw.n,
	})
}

// ===== Parsing =====

// parseFormat extracts and validates the `?format=` query parameter.
// Default is CSV (matching slice 135). Anything outside csv|json|xlsx
// is a 400.
func parseFormat(r *http.Request) (export.Format, error) {
	raw := r.URL.Query().Get("format")
	if raw == "" {
		return export.FormatCSV, nil
	}
	f := export.Format(strings.ToLower(raw))
	if !export.IsValid(f) {
		return f, fmt.Errorf("unsupported format %q (want csv|json|xlsx)", raw)
	}
	return f, nil
}

// ===== Meta-audit =====

// exportMetaAudit is the JSON shape persisted to me_audit_log.after on
// every export attempt. Mirrors slice 135's exportMetaAudit minus the
// audit-log-specific filter columns (kinds, actor, from/to) — the
// audit-periods export has no filter surface at v1, so the meta-audit
// shape is correspondingly narrower.
type exportMetaAudit struct {
	Format    string `json:"format"`
	Result    string `json:"result"`
	Reason    string `json:"reason,omitempty"`
	RowCount  int    `json:"row_count"`
	ByteCount int64  `json:"byte_count"`
}

// writeExportMetaAudit writes ONE me_audit_log row with
// action='audit_periods_export'. Slice 135 P0-A4 — EVERY terminal
// outcome path writes a row. Uses a fresh tx (not the export's outer
// tx) so success-path writes commit even when the outer body-streaming
// transaction is already closed.
func (h *Handler) writeExportMetaAudit(ctx context.Context, tenantID uuid.UUID, userIdentifier string, meta exportMetaAudit) {
	beforeBlob, _ := json.Marshal(map[string]string{"format": meta.Format})
	afterBlob, _ := json.Marshal(meta)

	uID, parseErr := uuid.Parse(userIdentifier)
	if parseErr != nil {
		// Bootstrap-key callers carry a non-UUID id; the zero UUID
		// is the honest fallback. Matches slice 124 / 135 pattern.
		uID = uuid.Nil
	}

	_ = h.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		return q.InsertMeAuditLog(ctx, dbx.InsertMeAuditLogParams{
			TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
			UserID:   pgtype.UUID{Bytes: uID, Valid: true},
			Action:   metaAuditActionExport,
			Before:   beforeBlob,
			After:    afterBlob,
		})
	})
}

// ===== Column set + row projection =====

// auditPeriodsExportHeader is the canonical column list for slice 139.
// Includes all freeze metadata columns (slice 139 constitutional
// addendum / invariant #10). Stable: changing this list is a breaking
// change for downstream consumers.
//
// Excludes `frozen_artifact_uri` / cosigned bundle bytes — slice 030
// owns that surface (P0-A-AP-1).
func auditPeriodsExportHeader() []string {
	return []string{
		"id",
		"name",
		"framework_version_id",
		"period_start",
		"period_end",
		"status",
		"frozen_at",
		"frozen_by",
		"frozen_hash",
		"created_by",
		"created_at",
		"updated_at",
	}
}

// periodsToRowIter projects a slice of [period.Period] into an
// iter.Seq[[]string] in the canonical column order. Pure projection —
// no DB I/O, O(1) per row.
//
// `frozen_at` renders as RFC3339Nano UTC when set, empty string when
// the period is still open. `frozen_hash` renders as lowercase hex
// when non-empty (mirroring `periodWireFrom`), empty string otherwise.
// `frozen_by` renders as the raw string identifier (UUID-shaped for
// user-issued freezes, "" for un-frozen periods).
func periodsToRowIter(periods []period.Period) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, p := range periods {
			frozenAt := ""
			if p.FrozenAt != nil {
				frozenAt = p.FrozenAt.UTC().Format(time.RFC3339Nano)
			}
			frozenHash := ""
			if len(p.FrozenHash) > 0 {
				frozenHash = hex.EncodeToString(p.FrozenHash)
			}
			row := []string{
				p.ID.String(),
				p.Name,
				p.FrameworkVersionID.String(),
				p.PeriodStart.UTC().Format("2006-01-02"),
				p.PeriodEnd.UTC().Format("2006-01-02"),
				string(p.Status),
				frozenAt,
				p.FrozenBy,
				frozenHash,
				p.CreatedBy,
				p.CreatedAt.UTC().Format(time.RFC3339Nano),
				p.UpdatedAt.UTC().Format(time.RFC3339Nano),
			}
			if !yield(row) {
				return
			}
		}
	}
}

// ===== Helpers =====

// exportLimiter returns the per-(tenant, user) concurrency limiter
// used by this handler. Default implementation returns the
// process-wide singleton; tests override via [Handler.WithLimiter].
func (h *Handler) exportLimiter() *export.Limiter {
	if h.limiter != nil {
		return h.limiter
	}
	return export.DefaultLimiter()
}

// callerAllowedExport mirrors slice 135's `callerAllowedUnified`
// admit-set: admin OR auditor OR grc_engineer. The role check is local
// defense-in-depth; the canonical gate is the slice-035 OPA middleware.
func callerAllowedExport(cred credstore.Credential) bool {
	if cred.IsAdmin {
		return true
	}
	for _, role := range cred.OwnerRoles {
		if role == "auditor" || role == "grc_engineer" {
			return true
		}
	}
	return false
}

// inTx opens one transaction, applies the tenant GUC under `atlas_app`,
// runs the callback, commits. Identical posture to the adminauditlog
// inTx helper — used here for the meta-audit insert. The export read
// uses the slice-028 store directly (which has its own tx).
func (h *Handler) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ===== HTTP helpers =====

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// countingWriter wraps an io.Writer and counts bytes written. Used to
// record body byte-count for the meta-audit row.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
