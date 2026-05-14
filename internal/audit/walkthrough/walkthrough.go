// Package walkthrough owns the slice 027 walkthrough recording primitive.
//
// Canvas §8.3: a Walkthrough(control, narrative, attachments[]) is an
// auditor's or control-owner's recorded explanation of how a control
// operates, hashed and signed. Each walkthrough is a tenant-scoped audit
// artifact with provenance (created_by + created_at), an optional pin to
// an audit_period_id (slice 028), zero-or-more attachments stored in the
// slice 036 artifact store, and a SHA-256 content commitment over the
// canonical JSON form.
//
// The hash inputs follow ADR 0003's content-only-inputs principle:
// content-derived fields only -- no random salt, no wall-clock other than
// the immutable created_at -- so the same content rehashes to the same
// bytes (tamper detection at retrieval, AC-6).
//
// Lifecycle:
//
//	draft       -> draft     attachment added; hash recomputed.
//	draft       -> finalized terminal; one final hash recompute is committed.
//	finalized   -> finalized any mutation attempt returns ErrFinalized (409).
//
// Audit-period freeze (constitutional invariant #10): when the walkthrough's
// audit_period_id points at a frozen audit_periods row, the store rejects
// all mutation paths with ErrPeriodFrozen.
package walkthrough

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned when a tenant-scoped lookup yields zero rows.
var ErrNotFound = errors.New("walkthrough: not found")

// ErrFinalized is returned when a mutation is attempted on a walkthrough
// in status='finalized'. The HTTP handler surfaces this as 409 Conflict.
var ErrFinalized = errors.New("walkthrough: already finalized")

// ErrPeriodFrozen is returned when the walkthrough's audit_period_id
// points at a frozen audit_periods row. Constitutional invariant #10 +
// anti-criterion P0-3 ("no mutation after period freeze").
var ErrPeriodFrozen = errors.New("walkthrough: audit period is frozen")

// Status enumerates the walkthrough lifecycle states. The DB CHECK
// constraint mirrors this enum.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusFinalized Status = "finalized"
)

// Walkthrough is the public shape returned from the Store. Mirrors the
// walkthroughs row plus any attachment list the caller requested.
type Walkthrough struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	AuditPeriodID *uuid.UUID
	ControlID     uuid.UUID
	Narrative     string
	Transcript    string
	CanonicalHash []byte
	Status        Status
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Attachments is populated by Get / List when the caller requests it.
	// nil when the caller asked for the metadata-only shape.
	Attachments []Attachment
	// TamperDetected is set on Get when the stored canonical_hash does
	// not match the re-computed hash from the current attachment set.
	// Walkthroughs that pass the check have TamperDetected = false. (AC-6)
	TamperDetected bool
}

// Attachment is the metadata for one file attached to a walkthrough.
type Attachment struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	WalkthroughID  uuid.UUID
	StorageKey     string
	ContentType    string
	SizeBytes      int64
	SHA256Hex      string
	AnnotationsRaw []byte // raw JSON bytes; callers decode per-feature
	UploadedBy     string
	UploadedAt     time.Time
}

// CreateInput is the API shape for POST /v1/walkthroughs.
type CreateInput struct {
	ControlID     uuid.UUID
	AuditPeriodID *uuid.UUID // optional pin
	Narrative     string
	Transcript    string // optional
	CreatedBy     string
}

// AttachInput is the API shape for adding one attachment to an existing
// walkthrough.
type AttachInput struct {
	WalkthroughID  uuid.UUID
	StorageKey     string
	ContentType    string
	SizeBytes      int64
	SHA256Hex      string
	AnnotationsRaw []byte
	UploadedBy     string
}

// Store is the entry point for slice-027 read/write operations.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// Config wires the Store.
type Config struct {
	Pool  *pgxpool.Pool
	Clock func() time.Time // optional; defaults to time.Now
}

// NewStore constructs a Store.
func NewStore(cfg Config) *Store {
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: cfg.Pool, clock: clock}
}

// Create inserts a new walkthrough in status='draft', stamps the initial
// canonical_hash (over the no-attachment shape), and writes a
// walkthrough_created audit log row. AC-1.
//
// If AuditPeriodID is set, the period must exist for the caller's tenant
// AND not be frozen; otherwise ErrPeriodFrozen / ErrNotFound is returned.
func (s *Store) Create(ctx context.Context, in CreateInput) (Walkthrough, error) {
	if in.Narrative == "" {
		return Walkthrough{}, fmt.Errorf("walkthrough: narrative must be non-empty")
	}
	if in.CreatedBy == "" {
		return Walkthrough{}, fmt.Errorf("walkthrough: created_by must be non-empty")
	}
	if in.ControlID == uuid.Nil {
		return Walkthrough{}, fmt.Errorf("walkthrough: control_id must be non-zero")
	}

	now := s.clock().UTC()
	var out Walkthrough
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := s.assertPeriodNotFrozen(ctx, q, tenantID, in.AuditPeriodID); err != nil {
			return err
		}
		id := uuid.New()
		initialHash, err := computeHash(hashInputs{
			ControlID:        in.ControlID,
			Narrative:        in.Narrative,
			Transcript:       in.Transcript,
			CreatedBy:        in.CreatedBy,
			CreatedAt:        now,
			AttachmentHashes: nil,
		})
		if err != nil {
			return fmt.Errorf("compute initial hash: %w", err)
		}
		row, err := q.CreateWalkthrough(ctx, dbx.CreateWalkthroughParams{
			ID:            pgUUID(id),
			TenantID:      pgUUID(tenantID),
			AuditPeriodID: pgUUIDPtr(in.AuditPeriodID),
			ControlID:     pgUUID(in.ControlID),
			Narrative:     in.Narrative,
			Transcript:    nullableString(in.Transcript),
			CanonicalHash: initialHash,
			CreatedBy:     in.CreatedBy,
			CreatedAt:     pgTimestamptz(now),
		})
		if err != nil {
			return fmt.Errorf("create walkthrough: %w", err)
		}
		if err := writeLog(ctx, q, tenantID, row.ID, "walkthrough_created", in.CreatedBy,
			map[string]any{
				"control_id":      in.ControlID.String(),
				"audit_period_id": uuidPtrString(in.AuditPeriodID),
			}); err != nil {
			return err
		}
		out = walkthroughFromRow(row)
		return nil
	})
	return out, err
}

// Get returns one walkthrough by id, with the attachment list and a
// tamper-detection flag. AC-4 + AC-6.
//
// Tamper detection: the stored canonical_hash is compared to the hash
// re-computed from the current attachment set. A mismatch flips
// TamperDetected to true and writes a tamper_detected audit log row.
// The walkthrough is still returned (the auditor can inspect what
// changed) -- the surfaced flag is the signal.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Walkthrough, error) {
	var out Walkthrough
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetWalkthroughByID(ctx, dbx.GetWalkthroughByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get walkthrough: %w", err)
		}
		wt := walkthroughFromRow(row)
		atts, err := q.ListWalkthroughAttachments(ctx, dbx.ListWalkthroughAttachmentsParams{
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list attachments: %w", err)
		}
		wt.Attachments = make([]Attachment, len(atts))
		for i, a := range atts {
			wt.Attachments[i] = attachmentFromRow(a)
		}
		// AC-6 tamper detection: re-hash the current content + sorted
		// attachment hashes, compare to the stored canonical_hash.
		recomputed, err := computeHash(hashInputs{
			ControlID:        wt.ControlID,
			Narrative:        wt.Narrative,
			Transcript:       wt.Transcript,
			CreatedBy:        wt.CreatedBy,
			CreatedAt:        wt.CreatedAt,
			AttachmentHashes: sortedHashes(wt.Attachments),
		})
		if err != nil {
			return fmt.Errorf("recompute hash: %w", err)
		}
		// Copy the assembled walkthrough into the function result, then
		// flip TamperDetected on `out` directly when the hash mismatch
		// is detected. (Writing `wt.TamperDetected = true` before the
		// copy is functionally identical, but CodeQL's
		// `go/useless-assignment-to-field` taint analysis can't see
		// through the value copy and flags the local-var write as
		// useless.)
		out = wt
		if !hashEqual(recomputed, wt.CanonicalHash) {
			out.TamperDetected = true
			// Best-effort: log the detection. A failure here does NOT
			// fail the Get -- the auditor still needs to see the row.
			_ = writeLog(ctx, q, tenantID, row.ID, "tamper_detected", "system",
				map[string]any{
					"stored_hash":     hex.EncodeToString(wt.CanonicalHash),
					"recomputed_hash": hex.EncodeToString(recomputed),
				})
		}
		return nil
	})
	return out, err
}

// List returns walkthroughs for the current tenant. Newest first.
// Attachments are NOT populated on the list path -- callers needing the
// full shape call Get per row.
func (s *Store) List(ctx context.Context) ([]Walkthrough, error) {
	var out []Walkthrough
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListWalkthroughsByTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list walkthroughs: %w", err)
		}
		out = make([]Walkthrough, len(rows))
		for i, r := range rows {
			out[i] = walkthroughFromRow(r)
		}
		return nil
	})
	return out, err
}

// AddAttachment appends one attachment to a walkthrough and recomputes
// the canonical_hash to commit to the new attachment set. AC-2 + AC-3.
//
// Rejects with ErrFinalized when the walkthrough is finalized; rejects
// with ErrPeriodFrozen when the walkthrough's audit_period is frozen.
func (s *Store) AddAttachment(ctx context.Context, in AttachInput) (Walkthrough, error) {
	if in.WalkthroughID == uuid.Nil {
		return Walkthrough{}, fmt.Errorf("walkthrough: walkthrough_id required")
	}
	if in.StorageKey == "" {
		return Walkthrough{}, fmt.Errorf("walkthrough: storage_key required")
	}
	if in.SHA256Hex == "" || len(in.SHA256Hex) != 64 {
		return Walkthrough{}, fmt.Errorf("walkthrough: sha256_hex must be 64 lowercase hex chars")
	}
	if in.UploadedBy == "" {
		return Walkthrough{}, fmt.Errorf("walkthrough: uploaded_by required")
	}
	annotations := in.AnnotationsRaw
	if len(annotations) == 0 {
		annotations = []byte(`{}`)
	}

	var out Walkthrough
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetWalkthroughByID(ctx, dbx.GetWalkthroughByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.WalkthroughID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get walkthrough (attach): %w", err)
		}
		if row.Status != string(StatusDraft) {
			return ErrFinalized
		}
		var periodPtr *uuid.UUID
		if row.AuditPeriodID.Valid {
			pid := uuid.UUID(row.AuditPeriodID.Bytes)
			periodPtr = &pid
		}
		if err := s.assertPeriodNotFrozen(ctx, q, tenantID, periodPtr); err != nil {
			if errors.Is(err, ErrPeriodFrozen) {
				_ = writeLog(ctx, q, tenantID, row.ID, "mutation_rejected_frozen", in.UploadedBy,
					map[string]any{"attempted_action": "attachment_added"})
			}
			return err
		}
		if _, err := q.CreateWalkthroughAttachment(ctx, dbx.CreateWalkthroughAttachmentParams{
			ID:            pgUUID(uuid.New()),
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(in.WalkthroughID),
			StorageKey:    in.StorageKey,
			ContentType:   in.ContentType,
			SizeBytes:     in.SizeBytes,
			Sha256Hash:    in.SHA256Hex,
			Annotations:   annotations,
			UploadedBy:    in.UploadedBy,
		}); err != nil {
			return fmt.Errorf("create attachment: %w", err)
		}

		// Recompute the hash over the new attachment set. The hashes
		// come back already sorted by SQL ORDER BY sha256_hash, so the
		// Go-side sort is defensive but free of complexity.
		hashes, err := q.ListWalkthroughAttachmentHashes(ctx, dbx.ListWalkthroughAttachmentHashesParams{
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(in.WalkthroughID),
		})
		if err != nil {
			return fmt.Errorf("list attachment hashes: %w", err)
		}
		var transcript string
		if row.Transcript != nil {
			transcript = *row.Transcript
		}
		createdAt := time.Time{}
		if row.CreatedAt.Valid {
			createdAt = row.CreatedAt.Time
		}
		newHash, err := computeHash(hashInputs{
			ControlID:        uuid.UUID(row.ControlID.Bytes),
			Narrative:        row.Narrative,
			Transcript:       transcript,
			CreatedBy:        row.CreatedBy,
			CreatedAt:        createdAt,
			AttachmentHashes: hashes,
		})
		if err != nil {
			return fmt.Errorf("recompute hash after attach: %w", err)
		}
		updated, err := q.UpdateWalkthroughHash(ctx, dbx.UpdateWalkthroughHashParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(in.WalkthroughID),
			CanonicalHash: newHash,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Race: another caller finalized between our SELECT
				// and our UPDATE. Surface 409.
				return ErrFinalized
			}
			return fmt.Errorf("update walkthrough hash: %w", err)
		}
		if err := writeLog(ctx, q, tenantID, updated.ID, "attachment_added", in.UploadedBy,
			map[string]any{
				"sha256":       in.SHA256Hex,
				"size_bytes":   in.SizeBytes,
				"content_type": in.ContentType,
				"storage_key":  in.StorageKey,
			}); err != nil {
			return err
		}
		out = walkthroughFromRow(updated)
		return nil
	})
	return out, err
}

// Finalize flips status draft->finalized and stamps the as-finalized
// canonical_hash one final time. AC-3 (commitment time).
//
// Rejects with ErrFinalized when already finalized; rejects with
// ErrPeriodFrozen when the audit_period is frozen.
func (s *Store) Finalize(ctx context.Context, id uuid.UUID, actor string) (Walkthrough, error) {
	if actor == "" {
		return Walkthrough{}, fmt.Errorf("walkthrough: actor required")
	}
	var out Walkthrough
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetWalkthroughByID(ctx, dbx.GetWalkthroughByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get walkthrough (finalize): %w", err)
		}
		if row.Status != string(StatusDraft) {
			return ErrFinalized
		}
		var periodPtr *uuid.UUID
		if row.AuditPeriodID.Valid {
			pid := uuid.UUID(row.AuditPeriodID.Bytes)
			periodPtr = &pid
		}
		if err := s.assertPeriodNotFrozen(ctx, q, tenantID, periodPtr); err != nil {
			if errors.Is(err, ErrPeriodFrozen) {
				_ = writeLog(ctx, q, tenantID, row.ID, "mutation_rejected_frozen", actor,
					map[string]any{"attempted_action": "walkthrough_finalized"})
			}
			return err
		}
		// Recompute the hash one last time so the stored value matches
		// the as-finalized content.
		hashes, err := q.ListWalkthroughAttachmentHashes(ctx, dbx.ListWalkthroughAttachmentHashesParams{
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list attachment hashes (finalize): %w", err)
		}
		var transcript string
		if row.Transcript != nil {
			transcript = *row.Transcript
		}
		createdAt := time.Time{}
		if row.CreatedAt.Valid {
			createdAt = row.CreatedAt.Time
		}
		finalHash, err := computeHash(hashInputs{
			ControlID:        uuid.UUID(row.ControlID.Bytes),
			Narrative:        row.Narrative,
			Transcript:       transcript,
			CreatedBy:        row.CreatedBy,
			CreatedAt:        createdAt,
			AttachmentHashes: hashes,
		})
		if err != nil {
			return fmt.Errorf("compute final hash: %w", err)
		}
		updated, err := q.FinalizeWalkthrough(ctx, dbx.FinalizeWalkthroughParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(id),
			CanonicalHash: finalHash,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrFinalized
			}
			return fmt.Errorf("finalize walkthrough: %w", err)
		}
		if err := writeLog(ctx, q, tenantID, updated.ID, "walkthrough_finalized", actor,
			map[string]any{
				"hash":          hex.EncodeToString(finalHash),
				"attachments_n": len(hashes),
			}); err != nil {
			return err
		}
		out = walkthroughFromRow(updated)
		// Hydrate attachments for the caller's response.
		atts, err := q.ListWalkthroughAttachments(ctx, dbx.ListWalkthroughAttachmentsParams{
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list attachments after finalize: %w", err)
		}
		out.Attachments = make([]Attachment, len(atts))
		for i, a := range atts {
			out.Attachments[i] = attachmentFromRow(a)
		}
		return nil
	})
	return out, err
}

// ListAuditLog returns the lifecycle audit log entries for a walkthrough.
func (s *Store) ListAuditLog(ctx context.Context, id uuid.UUID) ([]dbx.WalkthroughAuditLog, error) {
	var out []dbx.WalkthroughAuditLog
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListWalkthroughAuditLog(ctx, dbx.ListWalkthroughAuditLogParams{
			TenantID:      pgUUID(tenantID),
			WalkthroughID: pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list walkthrough log: %w", err)
		}
		out = rows
		return nil
	})
	return out, err
}

// assertPeriodNotFrozen looks up the referenced audit_periods row (if
// any) and returns ErrPeriodFrozen when status='frozen'. A nil period
// reference is a no-op (live walkthrough). An unknown period returns
// ErrNotFound.
func (s *Store) assertPeriodNotFrozen(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, periodID *uuid.UUID) error {
	if periodID == nil {
		return nil
	}
	row, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(*periodID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get audit period (walkthrough): %w", err)
	}
	if row.Status == "frozen" {
		return ErrPeriodFrozen
	}
	return nil
}

// ----- hash -----
//
// hashInputs is the canonical-JSON shape committed to by the walkthrough's
// canonical_hash. ADR 0003 principles:
//   - Content-only inputs. No random salt, no wall-clock other than the
//     immutable created_at (the row's authorship moment).
//   - Stable key order via the wire-shape struct (encoding/json emits in
//     struct declaration order).
//   - Sorted attachment_hashes so any add/remove invalidates the hash but
//     reorderings do not.

type hashInputs struct {
	ControlID        uuid.UUID
	Narrative        string
	Transcript       string
	CreatedBy        string
	CreatedAt        time.Time
	AttachmentHashes []string
}

// computeHash returns the slice-027 walkthrough content commitment. The
// wire shape is identical between the Go writer and any future verifier
// reimplementation (Python / TS); the canonical-JSON contract is the
// declared key order + RFC-3339 timestamp + sorted-by-string attachment
// hashes.
func computeHash(in hashInputs) ([]byte, error) {
	type wire struct {
		ControlID        string   `json:"control_id"`
		Narrative        string   `json:"narrative"`
		Transcript       string   `json:"transcript"`
		CreatedBy        string   `json:"created_by"`
		CreatedAt        string   `json:"created_at"`
		AttachmentHashes []string `json:"attachment_hashes"`
	}
	hashes := append([]string(nil), in.AttachmentHashes...)
	sort.Strings(hashes)
	w := wire{
		ControlID:  in.ControlID.String(),
		Narrative:  in.Narrative,
		Transcript: in.Transcript,
		CreatedBy:  in.CreatedBy,
		// Render CreatedAt in UTC with nanosecond precision so two
		// freezes of the same row produce identical bytes. The
		// timestamp is part of the content commitment because it pins
		// the authoring moment -- changing the author or the moment
		// changes the content.
		CreatedAt:        in.CreatedAt.UTC().Format(time.RFC3339Nano),
		AttachmentHashes: hashes,
	}
	buf, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}

func sortedHashes(atts []Attachment) []string {
	out := make([]string, len(atts))
	for i, a := range atts {
		out[i] = a.SHA256Hex
	}
	sort.Strings(out)
	return out
}

func hashEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ----- audit log -----

func writeLog(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, walkthroughID pgtype.UUID, action, actor string, detail map[string]any) error {
	var detailJSON []byte
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("marshal log detail: %w", err)
		}
		detailJSON = b
	} else {
		detailJSON = []byte(`{}`)
	}
	_, err := q.WriteWalkthroughAuditLog(ctx, dbx.WriteWalkthroughAuditLogParams{
		ID:            pgUUID(uuid.New()),
		TenantID:      pgUUID(tenantID),
		WalkthroughID: walkthroughID,
		Action:        action,
		Actor:         actor,
		Detail:        detailJSON,
	})
	if err != nil {
		return fmt.Errorf("write walkthrough log: %w", err)
	}
	return nil
}

// ----- tx plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("walkthrough: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("walkthrough: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("walkthrough: commit: %w", err)
	}
	return nil
}

// ----- row converters / helpers -----

func walkthroughFromRow(r dbx.Walkthrough) Walkthrough {
	w := Walkthrough{
		ID:        uuid.UUID(r.ID.Bytes),
		TenantID:  uuid.UUID(r.TenantID.Bytes),
		ControlID: uuid.UUID(r.ControlID.Bytes),
		Narrative: r.Narrative,
		Status:    Status(r.Status),
		CreatedBy: r.CreatedBy,
	}
	if r.AuditPeriodID.Valid {
		p := uuid.UUID(r.AuditPeriodID.Bytes)
		w.AuditPeriodID = &p
	}
	if r.Transcript != nil {
		w.Transcript = *r.Transcript
	}
	if len(r.CanonicalHash) > 0 {
		w.CanonicalHash = append([]byte(nil), r.CanonicalHash...)
	}
	if r.CreatedAt.Valid {
		w.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		w.UpdatedAt = r.UpdatedAt.Time
	}
	return w
}

func attachmentFromRow(r dbx.WalkthroughAttachment) Attachment {
	a := Attachment{
		ID:             uuid.UUID(r.ID.Bytes),
		TenantID:       uuid.UUID(r.TenantID.Bytes),
		WalkthroughID:  uuid.UUID(r.WalkthroughID.Bytes),
		StorageKey:     r.StorageKey,
		ContentType:    r.ContentType,
		SizeBytes:      r.SizeBytes,
		SHA256Hex:      r.Sha256Hash,
		AnnotationsRaw: append([]byte(nil), r.Annotations...),
		UploadedBy:     r.UploadedBy,
	}
	if r.UploadedAt.Valid {
		a.UploadedAt = r.UploadedAt.Time
	}
	return a
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgUUIDPtr(p *uuid.UUID) pgtype.UUID {
	if p == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *p, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func uuidPtrString(p *uuid.UUID) string {
	if p == nil {
		return ""
	}
	return p.String()
}
