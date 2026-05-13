// Slice 023 — policy acknowledgment workflow.
//
// An `Ack` is an affirmative, per-user attestation that a published
// policy version has been read and accepted (canvas §2.6 + §7.1;
// CONTEXT.md "Policy acknowledgment (slice 023)").
//
// This package owns the domain Store; the HTTP surface lives in
// internal/api/policyacks/ and the evidence emission flows through
// slice 013's ingest.Service.Process. The two are wired in
// internal/api/httpserver.go.
//
// Annual recurrence is computed at READ time. There is no cron.
// `AckStore.PendingForUser` returns policies whose most-recent ack
// for the calling user is older than 365 days (or doesn't exist).
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// AcknowledgmentFreshness is the annual recurrence window. An ack older
// than this is treated as expired by PendingForUser; the rate handler
// uses the same window when computing the numerator.
//
// Canvas §7.1 (Policy attestation rate KPI) reads "% required
// acknowledgments completed in window". The slice issue file (AC-5)
// pins the window at 365 days. Canvas §2.3's "annual = 400d" governs
// EvidenceRecord.valid_until and is distinct from this user-facing
// task-reappearance threshold.
const AcknowledgmentFreshness = 365 * 24 * time.Hour

// Errors surfaced by AckStore. Handlers map these to HTTP codes.
var (
	// ErrAckPolicyNotPublished is returned by Record when the policy row
	// at policy_version_id is not in `published` status. Handler maps to
	// 409 (anti-criterion P0-3 — ack of superseded does not satisfy
	// current).
	ErrAckPolicyNotPublished = errors.New("policy_ack: target policy version is not published")
	// ErrAckNotRequired is returned when the calling user's roles do not
	// intersect the policy's acknowledgment_required_roles AND the user
	// is not an admin. Handler maps to 403 (AC-3).
	ErrAckNotRequired = errors.New("policy_ack: caller's roles do not intersect required roles")
	// ErrAckMissingUser is returned when cred.UserID is empty. Handler
	// would already have 401'd via the auth middleware; this is a
	// defensive shoulder-tap.
	ErrAckMissingUser = errors.New("policy_ack: credential carries no user id")
	// ErrAckMissingPolicyID is returned when the input policy id is the
	// nil UUID.
	ErrAckMissingPolicyID = errors.New("policy_ack: policy id is required")
)

// AckCaller is the slice-034 credential subset the ack store needs.
// Decoupled from the full credstore.Credential so unit tests can build
// a minimal caller and the API package boundary stays clean (this
// package has no dependency on internal/api/credstore).
type AckCaller struct {
	UserID     string
	OwnerRoles []string
	IsAdmin    bool
}

// Pending describes one policy that requires acknowledgment from the
// calling user. Returned by AckStore.PendingForUser.
type Pending struct {
	PolicyID        uuid.UUID
	PolicyVersionID uuid.UUID
	Title           string
	Version         string
	EffectiveDate   *time.Time
	RequiredRoles   []string
	// LastAcknowledgedAt is the most recent ack timestamp for this user
	// + policy_version_id, or nil if no ack exists. When non-nil and
	// older than AcknowledgmentFreshness ago, the policy is in the
	// pending list because of annual recurrence (vs first-time
	// acknowledgment).
	LastAcknowledgedAt *time.Time
}

// Ack is the result of AckStore.Record. Carries the new row's id, the
// idempotency token, and the post-emission evidence record id.
type Ack struct {
	ID               uuid.UUID
	PolicyID         uuid.UUID
	PolicyVersionID  uuid.UUID
	UserID           uuid.UUID
	AcknowledgedAt   time.Time
	AckToken         string
	EvidenceRecordID *uuid.UUID
	Deduplicated     bool
}

// RateResult is the result of AckStore.Rate. Denominator zero produces
// a nil Percent (the handler emits `null` so consumers can distinguish
// "0% acknowledged" from "no required-role members exist").
type RateResult struct {
	Numerator     int64
	Denominator   int64
	Percent       *float64
	WindowSeconds int64
}

// AckStore wraps the pgx pool with the tenancy plumbing the four-policy
// RLS requires. Mirrors policy.Store from slice 022 (tx-per-call,
// ApplyTenant, sqlc).
type AckStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// NewAckStore constructs a store over an existing pgx pool. The pool
// must be connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for the
// four-policy RLS to fire.
func NewAckStore(pool *pgxpool.Pool) *AckStore {
	return &AckStore{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the time source. Tests use this to drive the
// 365-day freshness window without sleeping.
func (s *AckStore) WithClock(fn func() time.Time) *AckStore {
	s.clock = fn
	return s
}

// PendingForUser returns the policies that require an acknowledgment
// from caller, sorted by title ASC. A policy is "pending" when the
// caller's roles intersect acknowledgment_required_roles (or the caller
// is admin) AND there is no fresh (<= AcknowledgmentFreshness) ack of
// the current published version.
//
// Single-query implementation via ListPendingAcksForUser (LEFT JOIN
// LATERAL on policy_acknowledgments). The freshness predicate runs in
// Go so the response can distinguish "never ack'd" (LastAcknowledgedAt
// nil) from "stale ack" (LastAcknowledgedAt set but older than the
// freshness window) -- the UI labels these differently.
func (s *AckStore) PendingForUser(ctx context.Context, caller AckCaller) ([]Pending, error) {
	if caller.UserID == "" {
		return nil, ErrAckMissingUser
	}
	userID, err := uuid.Parse(caller.UserID)
	if err != nil {
		return nil, fmt.Errorf("policy_ack: parse user id: %w", err)
	}
	var out []Pending
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		roles := caller.OwnerRoles
		if roles == nil {
			roles = []string{}
		}
		rows, qerr := q.ListPendingAcksForUser(ctx, dbx.ListPendingAcksForUserParams{
			TenantID:   pgUUID(tenantID),
			UserID:     pgUUID(userID),
			IsAdmin:    caller.IsAdmin,
			OwnerRoles: roles,
		})
		if qerr != nil {
			return fmt.Errorf("policy_ack: list pending: %w", qerr)
		}
		cutoff := s.now().Add(-AcknowledgmentFreshness)
		for _, p := range rows {
			policyID := uuid.UUID(p.ID.Bytes)
			var lastAt *time.Time
			isFresh := false
			if p.LatestAckAt.Valid {
				t := p.LatestAckAt.Time
				lastAt = &t
				isFresh = !t.Before(cutoff)
			}
			if isFresh {
				continue
			}
			out = append(out, Pending{
				PolicyID:           policyID,
				PolicyVersionID:    policyID,
				Title:              p.Title,
				Version:            p.Version,
				EffectiveDate:      datePtr(p.EffectiveDate),
				RequiredRoles:      append([]string(nil), p.AcknowledgmentRequiredRoles...),
				LastAcknowledgedAt: lastAt,
			})
		}
		return nil
	})
	return out, err
}

// RecordInput is the AckStore.Record input. The handler validates
// auth + the policy id is non-nil before calling.
type RecordInput struct {
	PolicyID uuid.UUID
	Caller   AckCaller
	// EvidenceRecordID, when non-nil, is written to the row's
	// evidence_record_id column. The handler calls Record FIRST to
	// reserve the ack row, then calls ingest.Service.Process to emit
	// the evidence record, then calls SetEvidenceRecordID to backfill
	// (single transaction not feasible across packages; the handler
	// orchestrates).
	EvidenceRecordID *uuid.UUID
	// ObservedAt, when non-zero, sets acknowledged_at. Defaults to
	// store.now() when zero. Tests use this with WithClock for
	// deterministic timestamps.
	ObservedAt time.Time
}

// Record inserts a new ack row after validating the caller is eligible
// and the policy is currently published. Idempotency: a re-call within
// the same UTC day with the same (user_id, policy_version_id) returns
// the existing row with Deduplicated=true.
//
// The evidence emission lives in the HTTP handler -- this store call
// only writes the database row. AC-2's "ack writes ack AND emits
// evidence" is honored by the handler orchestration.
func (s *AckStore) Record(ctx context.Context, in RecordInput) (Ack, error) {
	if in.PolicyID == uuid.Nil {
		return Ack{}, ErrAckMissingPolicyID
	}
	if in.Caller.UserID == "" {
		return Ack{}, ErrAckMissingUser
	}
	userID, err := uuid.Parse(in.Caller.UserID)
	if err != nil {
		return Ack{}, fmt.Errorf("policy_ack: parse user id: %w", err)
	}
	observed := in.ObservedAt
	if observed.IsZero() {
		observed = s.now()
	}
	token := DeriveAckToken(in.Caller.UserID, in.PolicyID.String(), observed)
	var out Ack
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		policyRow, perr := q.GetPolicyForAcknowledge(ctx, dbx.GetPolicyForAcknowledgeParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.PolicyID),
		})
		if perr != nil {
			if errors.Is(perr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("policy_ack: load policy: %w", perr)
		}
		if policyRow.Status != StatePublished {
			return ErrAckPolicyNotPublished
		}
		if !rolesIntersect(in.Caller, policyRow.AcknowledgmentRequiredRoles) {
			return ErrAckNotRequired
		}
		// Idempotency probe: existing row with same token deduplicates.
		existing, gerr := q.GetAcknowledgmentByToken(ctx, dbx.GetAcknowledgmentByTokenParams{
			TenantID: pgUUID(tenantID),
			AckToken: token,
		})
		if gerr == nil {
			out = ackFromRow(existing, true)
			return nil
		}
		if !errors.Is(gerr, pgx.ErrNoRows) {
			return fmt.Errorf("policy_ack: idempotency probe: %w", gerr)
		}
		newID := uuid.New()
		row, ierr := q.InsertPolicyAcknowledgment(ctx, dbx.InsertPolicyAcknowledgmentParams{
			ID:               pgUUID(newID),
			TenantID:         pgUUID(tenantID),
			PolicyID:         pgUUID(in.PolicyID),
			PolicyVersionID:  pgUUID(in.PolicyID),
			UserID:           pgUUID(userID),
			AcknowledgedAt:   pgts(observed),
			AckToken:         token,
			EvidenceRecordID: pgUUIDPtr(in.EvidenceRecordID),
		})
		if ierr != nil {
			// Race: another inserter wrote a row with the same token
			// between our probe and our insert. Treat as dedup.
			var pgErr *pgconn.PgError
			if errors.As(ierr, &pgErr) && pgErr.Code == "23505" {
				existing2, gerr2 := q.GetAcknowledgmentByToken(ctx, dbx.GetAcknowledgmentByTokenParams{
					TenantID: pgUUID(tenantID),
					AckToken: token,
				})
				if gerr2 == nil {
					out = ackFromRow(existing2, true)
					return nil
				}
			}
			return fmt.Errorf("policy_ack: insert: %w", ierr)
		}
		out = ackFromRow(row, false)
		return nil
	})
	return out, err
}

// SetEvidenceRecordID backfills the evidence_record_id column after the
// handler's slice-013 emission succeeds. Best-effort: the ack row is
// already authoritative for "did the user attest"; the evidence id is
// the cross-reference for the audit dossier.
func (s *AckStore) SetEvidenceRecordID(ctx context.Context, ackID uuid.UUID, evidenceRecordID uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := q.SetAcknowledgmentEvidenceRecord(ctx, dbx.SetAcknowledgmentEvidenceRecordParams{
			TenantID:         pgUUID(tenantID),
			ID:               pgUUID(ackID),
			EvidenceRecordID: pgUUID(evidenceRecordID),
		}); err != nil {
			return fmt.Errorf("policy_ack: set evidence record id: %w", err)
		}
		return nil
	})
}

// Rate computes the policy attestation rate for the policy at policyID,
// resolved to its current published version. denominator = distinct
// users with the required role in `api_keys`; numerator = those who
// have a fresh ack (>= now - AcknowledgmentFreshness) of that version.
//
// Returns ErrNotFound if the row doesn't exist; ErrAckPolicyNotPublished
// if the row is in a non-published state.
func (s *AckStore) Rate(ctx context.Context, policyID uuid.UUID) (RateResult, error) {
	var out RateResult
	out.WindowSeconds = int64(AcknowledgmentFreshness / time.Second)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		policyRow, perr := q.GetPolicyForAcknowledge(ctx, dbx.GetPolicyForAcknowledgeParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(policyID),
		})
		if perr != nil {
			if errors.Is(perr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("policy_ack: rate: load policy: %w", perr)
		}
		if policyRow.Status != StatePublished {
			return ErrAckPolicyNotPublished
		}
		required := policyRow.AcknowledgmentRequiredRoles
		if required == nil {
			required = []string{}
		}
		// TODO(slice-035): the rate denominator currently uses the
		// slice-034 stand-in (api_keys.owner_roles + is_admin). When
		// slice 035 lands OPA-driven RBAC with first-class user-role
		// bindings, replace this with a query against the
		// user_role_bindings table. CONTEXT.md "Policy acknowledgment
		// (slice 023)" documents the stand-in.
		denom, derr := q.CountRequiredRoleUsersForVersion(ctx, dbx.CountRequiredRoleUsersForVersionParams{
			TenantID:      pgUUID(tenantID),
			RequiredRoles: required,
		})
		if derr != nil {
			return fmt.Errorf("policy_ack: rate denominator: %w", derr)
		}
		out.Denominator = denom
		cutoff := s.now().Add(-AcknowledgmentFreshness)
		num, nerr := q.CountFreshAcksForVersion(ctx, dbx.CountFreshAcksForVersionParams{
			TenantID:        pgUUID(tenantID),
			PolicyVersionID: pgUUID(policyID),
			FreshnessCutoff: pgts(cutoff),
			RequiredRoles:   required,
		})
		if nerr != nil {
			return fmt.Errorf("policy_ack: rate numerator: %w", nerr)
		}
		out.Numerator = num
		if denom > 0 {
			pct := (float64(num) / float64(denom)) * 100.0
			out.Percent = &pct
		}
		return nil
	})
	return out, err
}

// DeriveAckToken produces the deterministic idempotency token used by
// Record. Double-clicks within the same UTC day collapse to the same
// token (and dedup at the DB UNIQUE constraint); a re-ack 365 days
// later produces a fresh token (different dayBucket).
//
// Exported so tests can verify the dedup math without round-tripping
// through Record.
func DeriveAckToken(userID, policyVersionID string, t time.Time) string {
	dayBucket := t.UTC().Format("2006-01-02")
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{0})
	h.Write([]byte(policyVersionID))
	h.Write([]byte{0})
	h.Write([]byte(dayBucket))
	return "ack-" + hex.EncodeToString(h.Sum(nil))[:32]
}

// ----- internal helpers -----

func (s *AckStore) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock().UTC()
}

func (s *AckStore) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("policy_ack: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("policy_ack: begin tx: %w", err)
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
		return fmt.Errorf("policy_ack: commit: %w", err)
	}
	return nil
}

func rolesIntersect(caller AckCaller, required []string) bool {
	if caller.IsAdmin {
		return true
	}
	if len(required) == 0 || len(caller.OwnerRoles) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(required))
	for _, r := range required {
		set[r] = struct{}{}
	}
	for _, r := range caller.OwnerRoles {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}

func ackFromRow(r dbx.PolicyAcknowledgment, deduped bool) Ack {
	out := Ack{
		ID:              uuid.UUID(r.ID.Bytes),
		PolicyID:        uuid.UUID(r.PolicyID.Bytes),
		PolicyVersionID: uuid.UUID(r.PolicyVersionID.Bytes),
		UserID:          uuid.UUID(r.UserID.Bytes),
		AckToken:        r.AckToken,
		Deduplicated:    deduped,
	}
	if r.AcknowledgedAt.Valid {
		out.AcknowledgedAt = r.AcknowledgedAt.Time
	}
	if r.EvidenceRecordID.Valid {
		e := uuid.UUID(r.EvidenceRecordID.Bytes)
		out.EvidenceRecordID = &e
	}
	return out
}

func datePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func pgts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgUUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*u)
}
