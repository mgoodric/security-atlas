package period

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// LatestRevision selects a frozen period's latest freeze revision wherever a
// revision number is asked for.
const LatestRevision = math.MaxInt32

// ReasonAmendedByImport is the only reason a frozen period gains a revision:
// a historical evidence import named it (#1742).
const ReasonAmendedByImport = "amended_by_import"

// ErrNotFrozen is returned when a revision is asked of an open period.
var ErrNotFrozen = errors.New("auditperiod: not frozen")

// ErrNoRevision is returned when a period has no revision of the number asked.
var ErrNoRevision = errors.New("auditperiod: no such freeze revision")

// Revision is one freeze of a frozen period. Revision 1 is the original
// freeze, read from the audit_periods row, which is never rewritten. Each
// later revision was cut by an import amendment and adds EvidenceRecordIDs
// to the frozen population (#1742). The population at revision N is the
// original horizon (observed_at and ingested_at at or before the original
// frozen_at) plus the records of revisions 2..N.
type Revision struct {
	Revision     int
	FrozenHash   []byte
	PreviousHash []byte
	FrozenAt     time.Time
	Reason       string
	// Justification is the admin's free-text reason for an amendment,
	// required on every revision after the first (Matt, 2026-11-24).
	Justification string
	Actor         string
	// ImportBatchID is the import batch that cut the revision; nil for 1.
	ImportBatchID *uuid.UUID
	// EvidenceRecordIDs are the records this revision added; empty for 1.
	EvidenceRecordIDs []uuid.UUID
}

// Revisions lists a frozen period's freeze revisions, oldest first. An open
// period has none.
func (s *Store) Revisions(ctx context.Context, periodID uuid.UUID) ([]Revision, error) {
	var out []Revision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(periodID)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get period for revisions: %w", err)
		}
		out, err = revisionsOf(ctx, q, p)
		return err
	})
	return out, err
}

// revisionsOf reads p's revisions inside the caller's transaction.
func revisionsOf(ctx context.Context, q *dbx.Queries, p dbx.AuditPeriod) ([]Revision, error) {
	if p.Status != string(StatusFrozen) || !p.FrozenAt.Valid {
		return nil, nil
	}
	out := []Revision{{
		Revision:   1,
		FrozenHash: append([]byte(nil), p.FrozenHash...),
		FrozenAt:   p.FrozenAt.Time,
		Reason:     "frozen",
	}}
	if p.FrozenBy != nil {
		out[0].Actor = *p.FrozenBy
	}
	rows, err := q.ListAuditPeriodFreezeRevisions(ctx, dbx.ListAuditPeriodFreezeRevisionsParams{
		TenantID: p.TenantID, AuditPeriodID: p.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("list freeze revisions: %w", err)
	}
	for _, r := range rows {
		batch := uuid.UUID(r.ImportBatchID.Bytes)
		out = append(out, Revision{
			Revision:          int(r.Revision),
			FrozenHash:        r.FrozenHash,
			PreviousHash:      r.PreviousHash,
			FrozenAt:          r.FrozenAt.Time,
			Reason:            r.Reason,
			Justification:     r.Justification,
			Actor:             r.Actor,
			ImportBatchID:     &batch,
			EvidenceRecordIDs: pgUUIDsToUUIDs(r.EvidenceRecordIds),
		})
	}
	return out, nil
}

// ReplayHash recomputes the ADR 0003 freeze hash of a frozen period at
// revision (LatestRevision for the latest) from the ledger as it stands.
// Compared with the stored hash of that revision, it verifies the revision:
// every earlier revision stays replayable after a later one is cut.
func (s *Store) ReplayHash(ctx context.Context, periodID uuid.UUID, revision int) ([]byte, error) {
	var out []byte
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		p, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(periodID)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get period for replay: %w", err)
		}
		if p.Status != string(StatusFrozen) || !p.FrozenAt.Valid {
			return ErrNotFrozen
		}
		if revision < 1 {
			return ErrNoRevision
		}
		amended, err := AmendedIDs(ctx, q, p.TenantID, p.ID, revision)
		if err != nil {
			return err
		}
		if revision != LatestRevision && revision > 1 {
			revs, err := revisionsOf(ctx, q, p)
			if err != nil {
				return err
			}
			if revision > len(revs) {
				return ErrNoRevision
			}
		}
		out, err = revisionHash(ctx, q, p, pgUUIDsToUUIDs(amended))
		return err
	})
	return out, err
}

// AmendedIDs lists the evidence records freeze revisions 2..revision of a
// period added to its frozen population (LatestRevision for all). Every
// frozen-population read passes them as amended_ids, which admits them
// alongside the original horizon, still bounded by observed_at <=
// frozen_at. Empty for an open or never-amended period.
func AmendedIDs(ctx context.Context, q *dbx.Queries, tenantID, periodID pgtype.UUID, revision int) ([]pgtype.UUID, error) {
	ids, err := q.ListAmendedEvidenceIDsForPeriod(ctx, dbx.ListAmendedEvidenceIDsForPeriodParams{
		TenantID: tenantID, AuditPeriodID: periodID, MaxRevision: int32(min(revision, LatestRevision)),
	})
	if err != nil {
		return nil, fmt.Errorf("list amended evidence: %w", err)
	}
	return ids, nil
}

// revisionHash is the ADR 0003 hash over p's original horizon plus the
// amended records: the same inputs Freeze hashes, with the amended ids
// joined to the evidence set.
func revisionHash(ctx context.Context, q *dbx.Queries, p dbx.AuditPeriod, amended []uuid.UUID) ([]byte, error) {
	evIDs, err := q.ListEvidenceIDsForPeriodHash(ctx, dbx.ListEvidenceIDsForPeriodHashParams{
		TenantID: p.TenantID, ObservedAt: p.FrozenAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list evidence ids for hash: %w", err)
	}
	ctrlIDs, err := q.ListControlIDsForPeriodHash(ctx, p.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list control ids for hash: %w", err)
	}
	ids := pgUUIDsToUUIDs(evIDs)
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range amended {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return computeFreezeHash(freezeHashInputs{
		AuditPeriodID:      uuid.UUID(p.ID.Bytes),
		PeriodStart:        p.PeriodStart.Time,
		PeriodEnd:          p.PeriodEnd.Time,
		FrameworkVersionID: uuid.UUID(p.FrameworkVersionID.Bytes),
		EvidenceRecordIDs:  ids,
		ControlIDs:         pgUUIDsToUUIDs(ctrlIDs),
	})
}

// AmendInput is one import amendment of a frozen period.
type AmendInput struct {
	ImportBatchID uuid.UUID
	Actor         string
	// Reason is the admin's free-text justification; required.
	Reason string
	// EvidenceRecordIDs are the batch's records inside the period window.
	EvidenceRecordIDs []uuid.UUID
	At                time.Time
}

// AmendInTx re-freezes frozen period p as a new revision inside the caller's
// transaction, which carries the tenant GUC and holds p's row lock
// (dbx LockAuditPeriodsOverlapping). The new revision's population is the
// latest revision's plus in.EvidenceRecordIDs, with a new hash. The original
// freeze and every earlier revision are unchanged and stay replayable. It
// writes a period_amended_by_import row to the period's audit log naming
// both hashes, the importer and the batch.
func AmendInTx(ctx context.Context, q *dbx.Queries, p dbx.AuditPeriod, in AmendInput) (Revision, error) {
	if p.Status != string(StatusFrozen) || !p.FrozenAt.Valid {
		return Revision{}, ErrNotFrozen
	}
	if in.Actor == "" {
		return Revision{}, errors.New("auditperiod: amend actor must be non-empty")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return Revision{}, errors.New("auditperiod: amend reason must be non-empty")
	}
	if len(in.EvidenceRecordIDs) == 0 {
		return Revision{}, errors.New("auditperiod: amend needs at least one evidence record")
	}
	at := in.At.UTC()
	if in.At.IsZero() {
		at = time.Now().UTC()
	}
	revs, err := revisionsOf(ctx, q, p)
	if err != nil {
		return Revision{}, err
	}
	prev := revs[len(revs)-1]
	amended, err := AmendedIDs(ctx, q, p.TenantID, p.ID, LatestRevision)
	if err != nil {
		return Revision{}, err
	}
	all := append(pgUUIDsToUUIDs(amended), in.EvidenceRecordIDs...)
	hash, err := revisionHash(ctx, q, p, all)
	if err != nil {
		return Revision{}, fmt.Errorf("compute revision hash: %w", err)
	}
	next := prev.Revision + 1
	ids := append([]uuid.UUID(nil), in.EvidenceRecordIDs...)
	sortUUIDs(ids)
	pgIDs := uuidsToPG(ids)
	if _, err := q.InsertAuditPeriodFreezeRevision(ctx, dbx.InsertAuditPeriodFreezeRevisionParams{
		ID:                pgUUID(uuid.New()),
		TenantID:          p.TenantID,
		AuditPeriodID:     p.ID,
		Revision:          int32(next),
		FrozenHash:        hash,
		PreviousHash:      prev.FrozenHash,
		FrozenAt:          pgTimestamptz(at),
		Reason:            ReasonAmendedByImport,
		Justification:     strings.TrimSpace(in.Reason),
		Actor:             in.Actor,
		ImportBatchID:     pgUUID(in.ImportBatchID),
		EvidenceRecordIds: pgIDs,
	}); err != nil {
		return Revision{}, fmt.Errorf("insert freeze revision: %w", err)
	}
	if err := writeLog(ctx, q, uuid.UUID(p.TenantID.Bytes), p.ID, "period_amended_by_import", in.Actor,
		map[string]any{
			"revision":          next,
			"reason":            strings.TrimSpace(in.Reason),
			"previous_revision": prev.Revision,
			"previous_hash":     fmt.Sprintf("%x", prev.FrozenHash),
			"frozen_hash":       fmt.Sprintf("%x", hash),
			"original_hash":     fmt.Sprintf("%x", p.FrozenHash),
			"import_batch_id":   in.ImportBatchID.String(),
			"evidence_added_n":  len(ids),
			"frozen_at":         at.Format(time.RFC3339Nano),
		}); err != nil {
		return Revision{}, err
	}
	batch := in.ImportBatchID
	return Revision{
		Revision:          next,
		FrozenHash:        hash,
		PreviousHash:      prev.FrozenHash,
		FrozenAt:          at,
		Reason:            ReasonAmendedByImport,
		Justification:     strings.TrimSpace(in.Reason),
		Actor:             in.Actor,
		ImportBatchID:     &batch,
		EvidenceRecordIDs: ids,
	}, nil
}

func uuidsToPG(in []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(in))
	for i, u := range in {
		out[i] = pgUUID(u)
	}
	return out
}
