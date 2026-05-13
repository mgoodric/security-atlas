// Package policy implements the slice-022 policy library.
//
// A Policy is a governance document — title, version, body_md, owner_role,
// approver_role, linked_control_ids — that references the controls it
// governs (canvas §2.6 + CONTEXT.md "Policy (slice 022)").
//
// The state machine has five states:
//
//	draft        -> under_review -> approved -> published -> superseded   (happy path)
//
// Every publish creates a NEW row referencing the prior via the self-FK
// predecessor_id, and the prior row simultaneously transitions to
// 'superseded' (single transaction).
//
// AC-7 orphan policy: a policy whose linked_control_ids is empty surfaces
// a warning on read; the application BLOCKS publication of an orphan
// (anti-criterion P0 "Does NOT permit publish without linked controls").
//
// Approver role gate: under_review->approved and approved->published both
// require cred.IsApprover || cred.IsAdmin. Publish is gated because it
// creates an audit-binding artifact; defense in depth.
package policy

import (
	"context"
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

// State constants mirror the policies.status CHECK enum.
const (
	StateDraft       = "draft"
	StateUnderReview = "under_review"
	StateApproved    = "approved"
	StatePublished   = "published"
	StateSuperseded  = "superseded"
)

// Source attribution constants mirror the policies.source_attribution CHECK
// enum.
const (
	SourceCommunityDraft = "community_draft"
	SourceTenantAuthored = "tenant_authored"
	SourceVendorProvided = "vendor_provided"
)

// WarningOrphanPolicy is the literal warning code surfaced in API responses
// for any policy whose linked_control_ids is empty. AC-7.
const WarningOrphanPolicy = "orphan_policy"

// Errors surfaced by Store. Handlers map these to HTTP status codes.
var (
	// ErrNotFound is returned when an id doesn't resolve under the active
	// tenant. RLS-friendly: same shape as "in another tenant".
	ErrNotFound = errors.New("policy: not found")
	// ErrWrongState is returned when a transition is requested from a
	// state that doesn't permit it. HTTP 409.
	ErrWrongState = errors.New("policy: not in expected state")
	// ErrOrphanPublish is returned when publish is attempted on a policy
	// with empty linked_control_ids (AC-7 + anti-criterion P0).
	ErrOrphanPublish = errors.New("policy: cannot publish orphan policy (no linked controls)")
	// ErrTitleRequired / ErrVersionRequired / ErrBodyRequired /
	// ErrOwnerRoleRequired / ErrApproverRoleRequired / ErrCreatedByRequired
	// surface 400 from the create path.
	ErrTitleRequired        = errors.New("policy: title is required")
	ErrVersionRequired      = errors.New("policy: version is required")
	ErrBodyRequired         = errors.New("policy: body_md is required")
	ErrOwnerRoleRequired    = errors.New("policy: owner_role is required")
	ErrApproverRoleRequired = errors.New("policy: approver_role is required")
	ErrCreatedByRequired    = errors.New("policy: created_by is required")
	// ErrInvalidVersion is returned when the operator-supplied new version
	// on Publish is empty or equals the predecessor's version.
	ErrInvalidVersion = errors.New("policy: new version must be non-empty and differ from predecessor")
)

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when a
// composite FK fails (predecessor in another tenant).
const pgErrForeignKeyViolation = "23503"

// pgErrCheckViolation is the SQLSTATE Postgres returns when a CHECK
// constraint fails.
const pgErrCheckViolation = "23514"

// Policy is the domain shape returned from store calls.
type Policy struct {
	ID                          uuid.UUID
	TenantID                    uuid.UUID
	PredecessorID               *uuid.UUID
	Title                       string
	Version                     string
	EffectiveDate               *time.Time // date-only; the wire layer formats as YYYY-MM-DD
	BodyMd                      string
	OwnerRole                   string
	ApproverRole                string
	LinkedControlIDs            []uuid.UUID
	AcknowledgmentRequiredRoles []string
	Status                      string
	SourceAttribution           string
	CreatedBy                   string
	SubmittedAt                 *time.Time
	SubmittedBy                 *string
	ApprovedAt                  *time.Time
	ApprovedBy                  *string
	PublishedAt                 *time.Time
	PublishedBy                 *string
	SupersededAt                *time.Time
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

// IsOrphan reports whether p has no linked controls. ISC-19 / AC-7.
func (p Policy) IsOrphan() bool {
	return len(p.LinkedControlIDs) == 0
}

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as risk.Store / exception.Store: every method opens a tx,
// applies the tenant GUC, and runs queries inside that transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateInput is the API shape for POST /v1/policies.
type CreateInput struct {
	Title                       string
	Version                     string
	BodyMd                      string
	OwnerRole                   string
	ApproverRole                string
	LinkedControlIDs            []uuid.UUID
	AcknowledgmentRequiredRoles []string
	SourceAttribution           string
	CreatedBy                   string
}

// Create inserts a new policy in `draft` state.
func (s *Store) Create(ctx context.Context, in CreateInput) (Policy, error) {
	if err := validateCreate(in); err != nil {
		return Policy{}, err
	}
	source := in.SourceAttribution
	if source == "" {
		source = SourceTenantAuthored
	}
	links := in.LinkedControlIDs
	if links == nil {
		links = []uuid.UUID{}
	}
	ackRoles := in.AcknowledgmentRequiredRoles
	if ackRoles == nil {
		ackRoles = []string{}
	}
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		row, err := q.CreatePolicy(ctx, dbx.CreatePolicyParams{
			ID:                          pgUUID(id),
			TenantID:                    pgUUID(tenantID),
			PredecessorID:               pgtype.UUID{}, // NULL — drafts have no predecessor
			Title:                       in.Title,
			Version:                     in.Version,
			BodyMd:                      in.BodyMd,
			OwnerRole:                   in.OwnerRole,
			ApproverRole:                in.ApproverRole,
			LinkedControlIds:            uuidsToPg(links),
			AcknowledgmentRequiredRoles: ackRoles,
			SourceAttribution:           source,
			CreatedBy:                   in.CreatedBy,
		})
		if err != nil {
			return mapCreateError(err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// Get returns a single policy by id. ErrNotFound when absent.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Policy, error) {
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get policy: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// ListFilter narrows the result set of List. Empty fields are ignored.
type ListFilter struct {
	Status string
}

// List returns every policy for the active tenant, newest first. Status
// filter is applied in-memory; cardinality is small for v1.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Policy, error) {
	var out []Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListPolicies(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list policies: %w", err)
		}
		out = make([]Policy, 0, len(rows))
		for _, r := range rows {
			if filter.Status != "" && r.Status != filter.Status {
				continue
			}
			out = append(out, policyFromRow(r))
		}
		return nil
	})
	return out, err
}

// SubmitForReview transitions draft -> under_review. Operator action; no
// role gate.
func (s *Store) SubmitForReview(ctx context.Context, id uuid.UUID, actor string) (Policy, error) {
	if actor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateDraft,
		newState:      StateUnderReview,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error) {
			return q.SubmitPolicyForReview(ctx, dbx.SubmitPolicyForReviewParams{
				TenantID:    pgUUID(tenantID),
				ID:          pgUUID(id),
				SubmittedBy: stringPtr(actor),
			})
		},
	})
}

// Approve transitions under_review -> approved. Handler validates
// IsApprover before calling.
func (s *Store) Approve(ctx context.Context, id uuid.UUID, approver string) (Policy, error) {
	if approver == "" {
		return Policy{}, ErrCreatedByRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateUnderReview,
		newState:      StateApproved,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error) {
			return q.ApprovePolicy(ctx, dbx.ApprovePolicyParams{
				TenantID:   pgUUID(tenantID),
				ID:         pgUUID(id),
				ApprovedBy: stringPtr(approver),
			})
		},
	})
}

// PublishInput is the API shape for POST /v1/policies/{id}/publish. The
// publisher names the new version string; for the very first publish, the
// new version may equal the approved row's version (no predecessor exists
// to differ from). For second-and-later publishes, the version MUST
// differ from the predecessor's.
type PublishInput struct {
	NewVersion    string
	EffectiveDate *time.Time
	PublishedBy   string
}

// Publish transitions approved -> published. For policies that already
// have a predecessor (i.e. this is the SECOND or later publish), this is
// a two-step atomic operation:
//
//	step 1: mark current 'published' chain tip as 'superseded'
//	step 2: insert a NEW row with status='published', predecessor_id set
//
// For first-publish (no predecessor chain tip), this is a single UPDATE
// from 'approved' -> 'published' on the approved row itself.
//
// AC-1 (versioned rows on publish) + AC-7 (orphan blocks publish).
//
// Returns the NEW published row (the one carrying the freshly published
// version). For first-publish that's the approved row itself; for
// subsequent publishes that's a brand-new row.
func (s *Store) Publish(ctx context.Context, approvedID uuid.UUID, in PublishInput) (Policy, error) {
	if in.PublishedBy == "" {
		return Policy{}, ErrCreatedByRequired
	}
	effective := time.Now().UTC().Truncate(24 * time.Hour)
	if in.EffectiveDate != nil {
		effective = in.EffectiveDate.UTC().Truncate(24 * time.Hour)
	}
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Fetch the approved row to determine first-publish vs
		// subsequent-publish, and to check orphan-block.
		approved, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(approvedID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get approved policy: %w", err)
		}
		if approved.Status != StateApproved {
			return ErrWrongState
		}
		// AC-7 orphan block.
		if len(approved.LinkedControlIds) == 0 {
			return ErrOrphanPublish
		}
		if in.NewVersion == "" {
			return ErrInvalidVersion
		}
		// First-publish path: the approved row has no predecessor AND
		// no other row in the chain has been published. We can simply
		// UPDATE the approved row in-place.
		if !approved.PredecessorID.Valid {
			row, err := q.PublishApprovedPolicy(ctx, dbx.PublishApprovedPolicyParams{
				TenantID:      pgUUID(tenantID),
				ID:            pgUUID(approvedID),
				EffectiveDate: pgDate(effective),
				PublishedBy:   stringPtr(in.PublishedBy),
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrWrongState
				}
				return fmt.Errorf("publish (first): %w", err)
			}
			out = policyFromRow(row)
			return nil
		}
		// Subsequent-publish path: predecessor_id is set (this approved
		// row was forked from a prior published row by an
		// out-of-this-slice operator action, OR we're treating any
		// approved-with-predecessor as a chain continuation).
		// Step 1: supersede the prior published chain tip.
		predecessorID := uuid.UUID(approved.PredecessorID.Bytes)
		if _, err := q.SupersedePolicyAtPublish(ctx, dbx.SupersedePolicyAtPublishParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(predecessorID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("publish: predecessor %s not in 'published' state", predecessorID)
			}
			return fmt.Errorf("supersede predecessor: %w", err)
		}
		// Step 2: insert the new published row with predecessor = the
		// just-superseded chain tip. Reject equal-version (the
		// predecessor's version must differ; we look at the prior chain
		// tip's version which we just superseded).
		if in.NewVersion == approved.Version {
			return ErrInvalidVersion
		}
		newID := uuid.New()
		row, err := q.InsertPublishedPolicy(ctx, dbx.InsertPublishedPolicyParams{
			ID:                          pgUUID(newID),
			TenantID:                    pgUUID(tenantID),
			PredecessorID:               pgUUID(predecessorID),
			Title:                       approved.Title,
			Version:                     in.NewVersion,
			BodyMd:                      approved.BodyMd,
			OwnerRole:                   approved.OwnerRole,
			ApproverRole:                approved.ApproverRole,
			LinkedControlIds:            approved.LinkedControlIds,
			AcknowledgmentRequiredRoles: approved.AcknowledgmentRequiredRoles,
			EffectiveDate:               pgDate(effective),
			SourceAttribution:           approved.SourceAttribution,
			CreatedBy:                   approved.CreatedBy,
			PublishedBy:                 stringPtr(in.PublishedBy),
		})
		if err != nil {
			return fmt.Errorf("insert published policy: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// VersionChain returns the version history for a policy id, oldest first.
func (s *Store) VersionChain(ctx context.Context, id uuid.UUID) ([]Policy, error) {
	var out []Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListPolicyVersionChain(ctx, dbx.ListPolicyVersionChainParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list version chain: %w", err)
		}
		out = make([]Policy, len(rows))
		for i, r := range rows {
			out[i] = policyFromChainRow(r)
		}
		return nil
	})
	return out, err
}

// ----- transition plumbing -----

type transitionParams struct {
	expectedPrior string
	newState      string
	run           func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error)
}

func (s *Store) transition(ctx context.Context, id uuid.UUID, p transitionParams) (Policy, error) {
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := p.run(q, tenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Either missing row or wrong prior state. Probe to disambiguate.
				_, gErr := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
					TenantID: pgUUID(tenantID),
					ID:       pgUUID(id),
				})
				if errors.Is(gErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return ErrWrongState
			}
			return fmt.Errorf("transition: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// ----- tenancy plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("policy: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("policy: begin tx: %w", err)
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
		return fmt.Errorf("policy: commit: %w", err)
	}
	return nil
}

// ----- validation -----

func validateCreate(in CreateInput) error {
	if in.Title == "" {
		return ErrTitleRequired
	}
	if in.Version == "" {
		return ErrVersionRequired
	}
	if in.BodyMd == "" {
		return ErrBodyRequired
	}
	if in.OwnerRole == "" {
		return ErrOwnerRoleRequired
	}
	if in.ApproverRole == "" {
		return ErrApproverRoleRequired
	}
	if in.CreatedBy == "" {
		return ErrCreatedByRequired
	}
	if in.SourceAttribution != "" && !validSourceAttribution(in.SourceAttribution) {
		return fmt.Errorf("policy: invalid source_attribution %q", in.SourceAttribution)
	}
	return nil
}

func validSourceAttribution(s string) bool {
	switch s {
	case SourceCommunityDraft, SourceTenantAuthored, SourceVendorProvided:
		return true
	}
	return false
}

// ----- error mapping -----

func mapCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrForeignKeyViolation:
			return fmt.Errorf("policy: predecessor not in tenant: %w", err)
		case pgErrCheckViolation:
			return fmt.Errorf("policy: check constraint %s violated: %w", pgErr.ConstraintName, err)
		}
	}
	return fmt.Errorf("create policy: %w", err)
}

// ----- row conversion -----

func policyFromRow(r dbx.Policy) Policy {
	out := Policy{
		ID:                          uuid.UUID(r.ID.Bytes),
		TenantID:                    uuid.UUID(r.TenantID.Bytes),
		Title:                       r.Title,
		Version:                     r.Version,
		BodyMd:                      r.BodyMd,
		OwnerRole:                   r.OwnerRole,
		ApproverRole:                r.ApproverRole,
		LinkedControlIDs:            uuidsFromPg(r.LinkedControlIds),
		AcknowledgmentRequiredRoles: append([]string(nil), r.AcknowledgmentRequiredRoles...),
		Status:                      r.Status,
		SourceAttribution:           r.SourceAttribution,
		CreatedBy:                   r.CreatedBy,
	}
	if r.PredecessorID.Valid {
		pid := uuid.UUID(r.PredecessorID.Bytes)
		out.PredecessorID = &pid
	}
	if r.EffectiveDate.Valid {
		d := r.EffectiveDate.Time
		out.EffectiveDate = &d
	}
	if r.SubmittedAt.Valid {
		t := r.SubmittedAt.Time
		out.SubmittedAt = &t
	}
	if r.SubmittedBy != nil {
		v := *r.SubmittedBy
		out.SubmittedBy = &v
	}
	if r.ApprovedAt.Valid {
		t := r.ApprovedAt.Time
		out.ApprovedAt = &t
	}
	if r.ApprovedBy != nil {
		v := *r.ApprovedBy
		out.ApprovedBy = &v
	}
	if r.PublishedAt.Valid {
		t := r.PublishedAt.Time
		out.PublishedAt = &t
	}
	if r.PublishedBy != nil {
		v := *r.PublishedBy
		out.PublishedBy = &v
	}
	if r.SupersededAt.Valid {
		t := r.SupersededAt.Time
		out.SupersededAt = &t
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}

// policyFromChainRow handles the sqlc-generated ListPolicyVersionChainRow
// shape (which has the same fields as Policy because the recursive CTE
// returns the full row).
func policyFromChainRow(r dbx.ListPolicyVersionChainRow) Policy {
	// The chain row mirrors dbx.Policy verbatim; reuse the conversion by
	// reconstructing a dbx.Policy.
	return policyFromRow(dbx.Policy{
		ID:                          r.ID,
		TenantID:                    r.TenantID,
		Title:                       r.Title,
		Version:                     r.Version,
		EffectiveDate:               r.EffectiveDate,
		BodyMd:                      r.BodyMd,
		AcknowledgmentRequiredRoles: r.AcknowledgmentRequiredRoles,
		Status:                      r.Status,
		CreatedAt:                   r.CreatedAt,
		UpdatedAt:                   r.UpdatedAt,
		PredecessorID:               r.PredecessorID,
		OwnerRole:                   r.OwnerRole,
		ApproverRole:                r.ApproverRole,
		LinkedControlIds:            r.LinkedControlIds,
		SourceAttribution:           r.SourceAttribution,
		CreatedBy:                   r.CreatedBy,
		SubmittedAt:                 r.SubmittedAt,
		SubmittedBy:                 r.SubmittedBy,
		ApprovedAt:                  r.ApprovedAt,
		ApprovedBy:                  r.ApprovedBy,
		PublishedAt:                 r.PublishedAt,
		PublishedBy:                 r.PublishedBy,
		SupersededAt:                r.SupersededAt,
	})
}

func uuidsToPg(us []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(us))
	for i, u := range us {
		out[i] = pgUUID(u)
	}
	return out
}

func uuidsFromPg(us []pgtype.UUID) []uuid.UUID {
	out := make([]uuid.UUID, len(us))
	for i, u := range us {
		out[i] = uuid.UUID(u.Bytes)
	}
	return out
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
