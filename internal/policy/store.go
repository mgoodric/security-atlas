// Package policy implements the slice-022 policy library.
//
// A Policy is a governance document — title, version, body_md, owner_role,
// approver_role, linked_control_ids — that references the controls it
// governs (canvas §2.6 + GLOSSARY.md "Policy (slice 022)").
//
// The state machine has five states:
//
//	draft        -> under_review -> approved -> published -> superseded   (happy path)
//
// A draft can also be discarded (DiscardDraft, #1987): the row is deleted
// and the discard is recorded in policy_audit_log. No other state is
// deletable.
//
// Each version is its own row. A later version starts as a draft created
// with predecessor_id naming the published chain tip; publishing it
// transitions the prior row to 'superseded' and the draft row to
// 'published' in a single transaction.
//
// Import (#1747): an admin can bring an existing policy library in as
// already-published rows with source_attribution='imported', carrying the
// historical approver, approval date, effective date, next review date and
// source URL. Import bypasses the in-tool transitions; the next version of
// an imported policy goes through the normal workflow.
//
// AC-7 orphan policy: a policy whose linked_control_ids is empty surfaces
// a warning on read; the application BLOCKS publication of an orphan
// (anti-criterion P0 "Does NOT permit publish without linked controls").
//
// Approver role gate: under_review->approved and approved->published both
// require cred.IsApprover || cred.IsAdmin. Publish is gated because it
// creates an audit-binding artifact; defense in depth. Approve also
// refuses the person who submitted the policy (#1807).
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/identity"
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
	// SourceImported marks a policy brought in by Import with its
	// historical lifecycle (#1747). Never accepted on Create.
	SourceImported = "imported"
	// SourceSynced marks a policy written by a policy sync run (#2850): the
	// published copy of a page in an external system of record. Never
	// accepted on Create.
	SourceSynced = "synced"
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
	// ErrSelfApproval is returned when the approver is the person who
	// submitted the policy for review (#1807, segregation of duties) and
	// the organisation does not allow self-approval (#2101). HTTP 403.
	ErrSelfApproval = errors.New("policy: the submitter cannot approve their own policy; a second approver must approve it")
	// ErrEditorApproval is returned when the approver created or edited
	// the draft and the organisation does not allow self-approval (#2446).
	// Writing the text counts as authoring it, whoever submitted it.
	ErrEditorApproval = errors.New("policy: an author of this draft cannot approve it; a person who neither wrote nor edited it must approve it")
	// ErrEditorNotPerson is returned when the draft was created or edited
	// through a credential that names no person (an unbound API key, an
	// OAuth client or the bootstrap admin) and the organisation does not
	// allow self-approval (#2459). Whoever holds that credential is an
	// unknown author, so no approver can be shown not to be them. HTTP 403.
	ErrEditorNotPerson = errors.New("policy: this draft was written or edited through an unbound API key, OAuth client or bootstrap credential, so its author is unknown and no approver can be shown to differ from them; it can be approved only with Allow self-approval on")
	// ErrUnknownLinkedControl is returned when linked_control_ids names a
	// control that does not exist in the tenant (#2446) or is retired
	// (#2459).
	ErrUnknownLinkedControl = errors.New("policy: linked_control_ids names a control that does not exist in this tenant or is retired")
	// ErrApproverNotPerson is returned when the approving credential
	// belongs to no user: an unbound API key, an OAuth client or the
	// bootstrap admin. Only people approve policies (#1807). HTTP 403.
	ErrApproverNotPerson = errors.New("policy: only a person can approve a policy; approve with your own login or a key bound to your user, not an unbound API key, OAuth client or bootstrap credential")
	// ErrSubmitterUnknown is returned when the policy was submitted
	// before #1807 under a session or credential id rather than a
	// person (#2200), so no
	// approver can be shown to differ from the submitter. HTTP 409.
	ErrSubmitterUnknown = errors.New("policy: this policy was submitted before submitters were recorded by person, so a second approver cannot be verified; it must be re-submitted before it can be approved")
	// ErrOrphanPublish is returned when publish is attempted on a policy
	// with empty linked_control_ids (AC-7 + anti-criterion P0).
	ErrOrphanPublish = errors.New("policy: cannot publish orphan policy (no linked controls)")
	// ErrNoLiveLinkedControl is returned when publish is attempted on a
	// policy whose linked_control_ids are all retired or gone from the
	// tenant (#2478): the links were valid when last edited, but the policy
	// would now govern no live control. HTTP 422.
	ErrNoLiveLinkedControl = errors.New("policy: cannot publish a policy none of whose linked controls is live; every linked control is retired or no longer in this tenant")
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
	// ErrPredecessorNotFound is returned when Create names a predecessor
	// that does not resolve under the active tenant. HTTP 400.
	ErrPredecessorNotFound = errors.New("policy: predecessor_id not found")
	// ErrPredecessorNotPublished is returned when Create names a
	// predecessor that is not the published chain tip. HTTP 409.
	ErrPredecessorNotPublished = errors.New("policy: predecessor must be a published policy")
	// ErrPredecessorTaken is returned when the predecessor already has a
	// successor (version chains are linear). HTTP 409. The message points
	// the caller at both ways forward: continue the existing successor, or
	// discard it while it is still a draft (DELETE /v1/policies/{id}, #1987).
	ErrPredecessorTaken = errors.New("policy: predecessor already has a successor version; continue that version (submit, approve, publish), or if it is still a draft discard it with DELETE /v1/policies/{id} and create the new version again")
	// ErrInvalidSourceAttribution is returned when Create is given a
	// source_attribution outside the accepted set, or 'imported' (which
	// only Import sets). HTTP 400.
	ErrInvalidSourceAttribution = errors.New("policy: invalid source_attribution")
	// ErrManagedBySync is returned when an in-tool edit targets a synced
	// policy (#2852): creating its next version, changing its review date,
	// or any draft path on it or on a draft of its chain. Its page is the
	// authority, so the edit is made there. HTTP 409.
	ErrManagedBySync = errors.New("policy: managed in Confluence; edit it at its source page")
	// ErrNotSyncedPolicy is returned when SetSyncedLinks targets a policy
	// that is not a published synced row (#2852). Any other policy changes
	// its links in a draft or a new version. HTTP 409.
	ErrNotSyncedPolicy = errors.New("policy: control links change in place only on a published synced policy; change any other policy's links in a draft or a new version")
)

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when a
// composite FK fails (predecessor in another tenant).
const pgErrForeignKeyViolation = "23503"

// pgErrUniqueViolation is the SQLSTATE Postgres returns when a UNIQUE
// index rejects a row (a predecessor that already has a successor).
const pgErrUniqueViolation = "23505"

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
	// SelfApproved is true when the submitter approved the policy under
	// the organisation's Allow self-approval setting (#2101).
	SelfApproved bool
	PublishedAt  *time.Time
	PublishedBy  *string
	SupersededAt *time.Time
	NextReviewAt *time.Time
	// Import fields (#1747). Set only when SourceAttribution is
	// SourceImported. HistoricalApproverName is a free-text name, never a
	// credential id; ApprovedBy stays nil on imported rows.
	HistoricalApproverName *string
	HistoricalApprovedAt   *time.Time // date-only
	SourceURL              *string
	ImportedBy             *string
	ImportedAt             *time.Time
	// Sync drift (#2851). Set only on a published synced row whose page
	// body changed without a version bump: when a sync run first saw it,
	// and the hex sha256 of the body the source holds now.
	SyncDriftDetectedAt *time.Time
	SyncDriftBodySHA256 *string
	// Sync property drift (#2877). Set only on a published synced row
	// whose page properties changed without a version bump: when a sync
	// run first saw it, and the sorted property names that differ now.
	// Separate from body drift; either, both or neither may be set.
	SyncPropertyDriftDetectedAt *time.Time
	SyncPropertyDriftKeys       []string
	// SyncMissingSince is set on a published synced row whose page a sync
	// run found absent (#2853): when a run first found it missing. The
	// policy stays in force.
	SyncMissingSince *time.Time
	// SyncUnreadableSince and SyncUnreadableReason are set on a published
	// synced row whose page the latest sync run carried as unreadable
	// (#3000): the page is still in its source, but the connector could
	// not read its body. When a run first carried it so, and the pusher's
	// reason from the latest one. Nothing else on the row changed.
	SyncUnreadableSince  *time.Time
	SyncUnreadableReason *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// IsImported reports whether p was brought in by Import (#1747).
func (p Policy) IsImported() bool {
	return p.SourceAttribution == SourceImported
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
	// Editor is the creator's principal (credstore.PrincipalOf), the
	// first entry of the draft's editor set (#2446). Empty records none.
	Editor string
	// PredecessorID, when set, makes this draft the next version of a
	// published policy. The predecessor must be published and must not
	// already have a successor.
	PredecessorID *uuid.UUID
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
	editors := []string{}
	if in.Editor != "" {
		editors = append(editors, in.Editor)
	}
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		predecessor := pgtype.UUID{} // NULL — a first version has no predecessor
		if in.PredecessorID != nil {
			if err := checkPredecessor(ctx, q, tenantID, *in.PredecessorID); err != nil {
				return err
			}
			predecessor = pgUUID(*in.PredecessorID)
		}
		id := uuid.New()
		row, err := q.CreatePolicy(ctx, dbx.CreatePolicyParams{
			ID:                          pgUUID(id),
			TenantID:                    pgUUID(tenantID),
			PredecessorID:               predecessor,
			Title:                       in.Title,
			Version:                     in.Version,
			BodyMd:                      in.BodyMd,
			OwnerRole:                   in.OwnerRole,
			ApproverRole:                in.ApproverRole,
			LinkedControlIds:            uuidsToPg(links),
			AcknowledgmentRequiredRoles: ackRoles,
			SourceAttribution:           source,
			CreatedBy:                   in.CreatedBy,
			DraftEditors:                editors,
		})
		if err != nil {
			return mapCreateError(err)
		}
		// Linked controls are checked after the insert, so a bad or taken
		// predecessor (400/409) is reported before a bad link (422,
		// #2459); a failure here rolls the insert back.
		if err := checkLinkedControls(ctx, q, tenantID, links); err != nil {
			return err
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// checkLinkedControls verifies that every id in ids is a control in the
// tenant (#2446), so a policy cannot satisfy the orphan-publish rule with a
// made-up or another tenant's id. Duplicates count once.
func checkLinkedControls(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	distinct := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		distinct[id] = struct{}{}
	}
	n, err := q.CountTenantControls(ctx, dbx.CountTenantControlsParams{
		TenantID: pgUUID(tenantID),
		Ids:      uuidsToPg(ids),
	})
	if err != nil {
		return fmt.Errorf("check linked controls: %w", err)
	}
	if n != int64(len(distinct)) {
		return ErrUnknownLinkedControl
	}
	return nil
}

// countLiveLinkedControls counts the distinct ids in ids that are
// non-retired controls in the tenant (CountTenantControls).
func countLiveLinkedControls(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ids []pgtype.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := q.CountTenantControls(ctx, dbx.CountTenantControlsParams{
		TenantID: pgUUID(tenantID),
		Ids:      ids,
	})
	if err != nil {
		return 0, fmt.Errorf("count live linked controls: %w", err)
	}
	return n, nil
}

// bodySHA256 is the hex sha256 of a policy body, recorded in
// policy_audit_log in place of the body itself (#2446).
func bodySHA256(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// checkPredecessor verifies that pred is a published policy in the tenant.
// A predecessor that already has a successor is caught by the
// policies_predecessor_unique_when_set index at insert (mapCreateError).
func checkPredecessor(ctx context.Context, q *dbx.Queries, tenantID, pred uuid.UUID) error {
	row, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(pred),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPredecessorNotFound
		}
		return fmt.Errorf("get predecessor: %w", err)
	}
	if row.SourceAttribution == SourceSynced {
		return ErrManagedBySync
	}
	if row.Status != StatePublished {
		return ErrPredecessorNotPublished
	}
	return nil
}

// checkNotSynced refuses an in-tool edit of a synced policy (#2852): the
// row itself is synced, or, when chain is set, it is a draft whose
// predecessor is (a next version started before its chain was synced). A
// missing id passes, so the caller reports it as not found.
func checkNotSynced(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, chain bool) error {
	row, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       pgUUID(id),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check synced: %w", err)
	}
	if row.SourceAttribution == SourceSynced {
		return ErrManagedBySync
	}
	if !chain || !row.PredecessorID.Valid {
		return nil
	}
	pred, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
		TenantID: pgUUID(tenantID),
		ID:       row.PredecessorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check synced predecessor: %w", err)
	}
	if pred.SourceAttribution == SourceSynced {
		return ErrManagedBySync
	}
	return nil
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

// policy_audit_log actions for the review transitions (#2200). A
// self-approval writes auditActionSelfApproved instead of
// auditActionApproved, so each approval is one row.
const (
	auditActionSubmitted   = "submitted"
	auditActionResubmitted = "resubmitted"
	auditActionApproved    = "approved"
)

// legacyKeyPrefix marks a submitter recorded by credential id before
// #1807, when a key bound to a user recorded its own id. It names a key,
// not a person (#2200).
const legacyKeyPrefix = "key_"

// SubmitForReview transitions draft -> under_review. Operator action; no
// role gate. It also re-submits an under_review policy whose submitter
// was recorded before #1807 as a session or credential id, so that
// approval, which refuses such a row (ErrSubmitterUnknown), has a person
// to compare; the re-submitter becomes the submitter of record. Either
// way a policy_audit_log row naming the actor and the states is appended
// in the same transaction.
func (s *Store) SubmitForReview(ctx context.Context, id uuid.UUID, actor string) (Policy, error) {
	if actor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateDraft,
		newState:      StateUnderReview,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error) {
			prior, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(id),
			})
			if err != nil {
				return dbx.Policy{}, err
			}
			// Submit re-runs the create / draft-edit link check (#2478): a
			// control retired since the last edit is refused here, so the
			// author drops or replaces it while the policy is still
			// editable. An orphan stays submittable; publish blocks it.
			if prior.Status == StateDraft {
				if err := checkLinkedControls(ctx, q, tenantID, uuidsFromPg(prior.LinkedControlIds)); err != nil {
					return dbx.Policy{}, err
				}
			}
			row, err := q.SubmitPolicyForReview(ctx, dbx.SubmitPolicyForReviewParams{
				TenantID:    pgUUID(tenantID),
				ID:          pgUUID(id),
				SubmittedBy: stringPtr(actor),
			})
			if err != nil {
				return dbx.Policy{}, err
			}
			action := auditActionSubmitted
			if prior.Status == StateUnderReview {
				action = auditActionResubmitted
			}
			if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
				TenantID:      pgUUID(tenantID),
				PolicyID:      row.ID,
				Action:        action,
				Actor:         actor,
				PolicyTitle:   &row.Title,
				PolicyVersion: &row.Version,
				PreviousState: &prior.Status,
				NewState:      &row.Status,
				BodySha256:    stringPtr(bodySHA256(row.BodyMd)),
				Editors:       editorsOrEmpty(row.DraftEditors),
			}); err != nil {
				return dbx.Policy{}, fmt.Errorf("submit: audit log: %w", err)
			}
			return row, nil
		},
	})
}

// editorsOrEmpty returns the editor set as a non-nil slice, so a submit
// row with no recorded editor carries '{}' rather than NULL.
func editorsOrEmpty(e []string) []string {
	if e == nil {
		return []string{}
	}
	return e
}

// insertTransitionAudit appends the policy_audit_log row for a status
// transition of row, from previous to row's current status.
func insertTransitionAudit(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, row dbx.Policy, action, actor, previous string) error {
	_, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
		TenantID:      pgUUID(tenantID),
		PolicyID:      row.ID,
		Action:        action,
		Actor:         actor,
		PolicyTitle:   &row.Title,
		PolicyVersion: &row.Version,
		PreviousState: &previous,
		NewState:      &row.Status,
	})
	return err
}

// Approver names who is approving a policy (#1807). UserID is the person:
// the users(id) uuid behind the credential, empty when the credential
// belongs to no user. CredentialID is the credential itself, compared
// against submitters recorded by credential id before #1807.
type Approver = approval.Approver

// auditActionSelfApproved is the policy_audit_log action written when the
// submitter approves their own policy under Allow self-approval (#2101).
const auditActionSelfApproved = "self_approved"

// Approve transitions under_review -> approved. Handler validates
// IsApprover before calling; the store applies CheckApprover (#1807) in
// the same transaction as the approval, reading the organisation's Allow
// self-approval setting there too (#2101). Every approval appends a
// policy_audit_log row (#2200); a self-approval also flags the row and
// records its audit row as self_approved.
func (s *Store) Approve(ctx context.Context, id uuid.UUID, approver Approver) (Policy, error) {
	if approver.UserID == "" && approver.CredentialID == "" {
		return Policy{}, ErrCreatedByRequired
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateUnderReview,
		newState:      StateApproved,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error) {
			// submitted_by is written once, on draft -> under_review, and
			// no transition returns a policy to draft, so reading it here
			// and approving below in one transaction cannot race.
			current, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(id),
			})
			if err != nil {
				return dbx.Policy{}, err
			}
			var self bool
			if current.Status == StateUnderReview {
				allowSelf, err := approval.AllowSelf(ctx, q, tenantID)
				if err != nil {
					return dbx.Policy{}, err
				}
				if self, err = CheckApprover(current, approver, allowSelf); err != nil {
					return dbx.Policy{}, err
				}
			}
			row, err := q.ApprovePolicy(ctx, dbx.ApprovePolicyParams{
				TenantID:     pgUUID(tenantID),
				ID:           pgUUID(id),
				ApprovedBy:   stringPtr(approver.UserID),
				SelfApproved: self,
			})
			if err != nil {
				return row, err
			}
			action := auditActionApproved
			if self {
				action = auditActionSelfApproved
			}
			if err := insertTransitionAudit(ctx, q, tenantID, row, action, approver.UserID, current.Status); err != nil {
				return dbx.Policy{}, fmt.Errorf("approve: audit log: %w", err)
			}
			return row, nil
		},
	})
}

// CheckApprover applies the shared approval rule (internal/approval,
// #1807 / #2101) to a policy and maps its errors onto this package's. Only
// a person approves, and never the person who submitted the policy unless
// the organisation allows self-approval; selfApproved reports that case.
// A machine credential never approves, whatever the setting.
//
// A submitter recorded by credential id ("key_...", before #1807) is
// compared against the approving credential, so the same key is still
// caught; any other approver is refused with ErrSubmitterUnknown, because
// the key's owner could be approving through a JWT or another key (#2200).
// The policy is re-submitted to record the person.
//
// Every principal in the draft's editor set (its creator and everyone who
// edited it, #2446) is an author too: an editor approving is refused with
// ErrEditorApproval unless the organisation allows self-approval, and is
// then self-approved, whoever submitted the policy.
//
// An editor that names no person (an unbound key's "key_..." id,
// "oauth_client:<id>", "bootstrap-admin": anything but a users(id) uuid)
// is an unknown author (#2459). Anyone holding that credential could be
// the approver, so every approver is refused with ErrEditorNotPerson
// unless the organisation allows self-approval, and the approval is then
// flagged self-approved.
//
// GET /v1/approvals (#2119) calls it per row, so it lists exactly what
// Approve accepts.
func CheckApprover(p dbx.Policy, a Approver, allowSelf bool) (selfApproved bool, err error) {
	self, err := checkSubmitter(p, a, allowSelf)
	if err != nil {
		return false, err
	}
	for _, editor := range p.DraftEditors {
		if editor == "" {
			continue
		}
		if !isPersonPrincipal(editor) {
			if !allowSelf {
				return false, ErrEditorNotPerson
			}
			self = true
			continue
		}
		if editor != a.UserID && editor != a.CredentialID {
			continue
		}
		if !allowSelf {
			return false, ErrEditorApproval
		}
		self = true
	}
	return self, nil
}

// isPersonPrincipal reports whether principal (credstore.PrincipalOf) names
// a person: a bare users(id) uuid in its canonical 36-character form
// (identity.IsBareUserID, #2478). Every other shape names a credential,
// including the 32-hex, "{...}" and "urn:uuid:" forms uuid.Parse accepts.
func isPersonPrincipal(principal string) bool {
	return identity.IsBareUserID(principal)
}

// checkSubmitter is the submitter half of CheckApprover.
func checkSubmitter(p dbx.Policy, a Approver, allowSelf bool) (selfApproved bool, err error) {
	submitter := ""
	if p.SubmittedBy != nil {
		submitter = *p.SubmittedBy
	}
	self, err := approval.Check(submitter, a, allowSelf)
	switch {
	case errors.Is(err, approval.ErrNotPerson):
		return false, ErrApproverNotPerson
	case errors.Is(err, approval.ErrRequesterUnknown):
		return false, ErrSubmitterUnknown
	case errors.Is(err, approval.ErrSelfApproval):
		return false, ErrSelfApproval
	case err != nil:
		return false, err
	}
	if !self && strings.HasPrefix(submitter, legacyKeyPrefix) && submitter != a.CredentialID {
		return false, ErrSubmitterUnknown
	}
	return self, nil
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
	// NextReviewAt is the published version's review date (#1806). Nil
	// publishes with none; the predecessor's date is never inherited.
	NextReviewAt *time.Time
}

// Publish transitions approved -> published. For a row created as the
// next version of a published policy (predecessor_id set), this is a
// two-step atomic operation:
//
//	step 1: mark the predecessor (the 'published' chain tip) 'superseded'
//	step 2: publish the approved row in place
//
// For first-publish (no predecessor), it is step 2 alone.
//
// AC-1 (versioned rows on publish) + AC-7 (orphan blocks publish).
//
// Returns the published row, which is always the approved row itself.
// The version string is the one the row was created with; NewVersion must
// be non-empty (the wire contract requires it) and a later version must
// differ from the predecessor's.
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
		if err := checkNotSynced(ctx, q, tenantID, approvedID, true); err != nil {
			return err
		}
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
		// AC-7 orphan block. A link to a control retired since the last
		// edit does not count (#2478): the policy must govern at least one
		// live control in the tenant when it is published.
		if len(approved.LinkedControlIds) == 0 {
			return ErrOrphanPublish
		}
		live, err := countLiveLinkedControls(ctx, q, tenantID, approved.LinkedControlIds)
		if err != nil {
			return err
		}
		if live == 0 {
			return ErrNoLiveLinkedControl
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
				NextReviewAt:  pgTimestamptz(in.NextReviewAt),
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
		// Subsequent-publish path: this approved row was created as the
		// next version of a published policy (predecessor_id set).
		// Step 1: supersede the published chain tip.
		predecessorID := uuid.UUID(approved.PredecessorID.Bytes)
		prior, err := q.SupersedePolicyAtPublish(ctx, dbx.SupersedePolicyAtPublishParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(predecessorID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("publish: predecessor %s not in 'published' state", predecessorID)
			}
			return fmt.Errorf("supersede predecessor: %w", err)
		}
		// The new version must differ from the one it supersedes.
		if approved.Version == prior.Version {
			return ErrInvalidVersion
		}
		// Step 2: publish this row in place. predecessor_id already links
		// it to the row just superseded, so the chain stays linear.
		row, err := q.PublishApprovedPolicy(ctx, dbx.PublishApprovedPolicyParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(approvedID),
			EffectiveDate: pgDate(effective),
			PublishedBy:   stringPtr(in.PublishedBy),
			NextReviewAt:  pgTimestamptz(in.NextReviewAt),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWrongState
			}
			return fmt.Errorf("publish (subsequent): %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// auditActionReviewDateUpdated is the policy_audit_log action for a
// review-date change (#1806).
const auditActionReviewDateUpdated = "review_date_updated"

// SetNextReviewAt changes the review date of a published policy in place
// and appends a policy_audit_log row naming the actor and the before and
// after values, in one transaction (#1806). It does not create a new
// version. next nil clears the date. The handler checks the approver role
// before calling. ErrNotFound when the id does not resolve; ErrWrongState
// when the policy is not published.
func (s *Store) SetNextReviewAt(ctx context.Context, id uuid.UUID, next *time.Time, actor string) (Policy, error) {
	if actor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		prior, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get policy: %w", err)
		}
		// A synced policy's review date follows its page (#2852).
		if prior.SourceAttribution == SourceSynced {
			return ErrManagedBySync
		}
		if prior.Status != StatePublished {
			return ErrWrongState
		}
		row, err := q.UpdatePolicyNextReviewAt(ctx, dbx.UpdatePolicyNextReviewAtParams{
			TenantID:     pgUUID(tenantID),
			ID:           pgUUID(id),
			NextReviewAt: pgTimestamptz(next),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWrongState
			}
			return fmt.Errorf("update next_review_at: %w", err)
		}
		if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
			TenantID:             pgUUID(tenantID),
			PolicyID:             row.ID,
			Action:               auditActionReviewDateUpdated,
			Actor:                actor,
			PreviousNextReviewAt: prior.NextReviewAt,
			NewNextReviewAt:      row.NextReviewAt,
		}); err != nil {
			return fmt.Errorf("review date: audit log: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// auditActionDraftDiscarded is the policy_audit_log action for a discarded
// draft (#1987).
const auditActionDraftDiscarded = "draft_discarded"

// DiscardDraft deletes a policy that is still a draft and appends a
// policy_audit_log row naming the actor and the discarded title and
// version, in one transaction (#1987). Deleting a next-version draft frees
// its predecessor's successor slot, so a new version can be created.
// ErrNotFound when the id does not resolve; ErrWrongState when the policy
// has left draft (submitted, approved, published or superseded rows are
// never deleted). Returns the row as it was before the delete.
func (s *Store) DiscardDraft(ctx context.Context, id uuid.UUID, actor string) (Policy, error) {
	if actor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	var out Policy
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// A synced row is refused as managed in Confluence (#2852). A
		// draft of a synced chain may still be discarded: it holds the
		// successor slot the page's next version needs, so discarding it
		// is how the sync is unblocked.
		if err := checkNotSynced(ctx, q, tenantID, id, false); err != nil {
			return err
		}
		row, err := q.DeleteDraftPolicy(ctx, dbx.DeleteDraftPolicyParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Either missing row or not a draft. Probe to disambiguate.
				_, gErr := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
					TenantID: pgUUID(tenantID),
					ID:       pgUUID(id),
				})
				if errors.Is(gErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				if gErr != nil {
					return fmt.Errorf("discard draft: get policy: %w", gErr)
				}
				return ErrWrongState
			}
			return fmt.Errorf("discard draft: %w", err)
		}
		if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
			TenantID:      pgUUID(tenantID),
			PolicyID:      row.ID,
			Action:        auditActionDraftDiscarded,
			Actor:         actor,
			PolicyTitle:   stringPtr(row.Title),
			PolicyVersion: stringPtr(row.Version),
		}); err != nil {
			return fmt.Errorf("discard draft: audit log: %w", err)
		}
		out = policyFromRow(row)
		return nil
	})
	return out, err
}

// UpdateDraftInput is the API shape for PATCH /v1/policies/{id} (#2116):
// the fields a draft's author may change before submitting it. The
// version, predecessor and source attribution are fixed at create.
type UpdateDraftInput struct {
	Title                       string
	BodyMd                      string
	OwnerRole                   string
	ApproverRole                string
	LinkedControlIDs            []uuid.UUID
	AcknowledgmentRequiredRoles []string
	// Editor is the editing caller's principal (credstore.PrincipalOf).
	// Required: it is recorded in the editor set and the audit log.
	Editor string
}

// auditActionDraftEdited is the policy_audit_log action written for each
// draft edit (#2446).
const auditActionDraftEdited = "draft_edited"

// UpdateDraft rewrites a draft's editable fields in place. ErrNotFound when
// the id does not resolve; ErrWrongState once the policy has left draft, so
// a published policy changes only through a new version. The editor joins
// the draft's editor set, which the approval rule treats as authors, and
// a draft_edited policy_audit_log row naming them and the body's sha256 is
// appended in the same transaction (#2446).
func (s *Store) UpdateDraft(ctx context.Context, id uuid.UUID, in UpdateDraftInput) (Policy, error) {
	if err := validateUpdateDraft(in); err != nil {
		return Policy{}, err
	}
	if in.Editor == "" {
		return Policy{}, ErrCreatedByRequired
	}
	links := in.LinkedControlIDs
	if links == nil {
		links = []uuid.UUID{}
	}
	ackRoles := in.AcknowledgmentRequiredRoles
	if ackRoles == nil {
		ackRoles = []string{}
	}
	return s.transition(ctx, id, transitionParams{
		expectedPrior: StateDraft,
		newState:      StateDraft,
		run: func(q *dbx.Queries, tenantID uuid.UUID) (dbx.Policy, error) {
			// Existence and state first, so a missing (404) or non-draft
			// (409) policy is reported before a bad link (422, #2459).
			current, err := q.GetPolicyByID(ctx, dbx.GetPolicyByIDParams{
				TenantID: pgUUID(tenantID),
				ID:       pgUUID(id),
			})
			if err != nil {
				return dbx.Policy{}, err
			}
			if current.Status != StateDraft {
				return dbx.Policy{}, ErrWrongState
			}
			if err := checkLinkedControls(ctx, q, tenantID, links); err != nil {
				return dbx.Policy{}, err
			}
			row, err := q.UpdateDraftPolicy(ctx, dbx.UpdateDraftPolicyParams{
				TenantID:                    pgUUID(tenantID),
				ID:                          pgUUID(id),
				Title:                       in.Title,
				BodyMd:                      in.BodyMd,
				OwnerRole:                   in.OwnerRole,
				ApproverRole:                in.ApproverRole,
				LinkedControlIds:            uuidsToPg(links),
				AcknowledgmentRequiredRoles: ackRoles,
				Editor:                      in.Editor,
			})
			if err != nil {
				return row, err
			}
			if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
				TenantID:      pgUUID(tenantID),
				PolicyID:      row.ID,
				Action:        auditActionDraftEdited,
				Actor:         in.Editor,
				PolicyTitle:   &row.Title,
				PolicyVersion: &row.Version,
				BodySha256:    stringPtr(bodySHA256(row.BodyMd)),
			}); err != nil {
				return dbx.Policy{}, fmt.Errorf("update draft: audit log: %w", err)
			}
			return row, nil
		},
	})
}

// validateUpdateDraft holds an edit to the same required fields as create.
func validateUpdateDraft(in UpdateDraftInput) error {
	switch {
	case in.Title == "":
		return ErrTitleRequired
	case in.BodyMd == "":
		return ErrBodyRequired
	case in.OwnerRole == "":
		return ErrOwnerRoleRequired
	case in.ApproverRole == "":
		return ErrApproverRoleRequired
	}
	return nil
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
		// Every transition (submit, approve, draft edit) is a draft path a
		// synced policy refuses (#2852).
		if err := checkNotSynced(ctx, q, tenantID, id, true); err != nil {
			return err
		}
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
	if in.SourceAttribution == SourceImported {
		return fmt.Errorf("%w: %q is set only by import", ErrInvalidSourceAttribution, in.SourceAttribution)
	}
	if in.SourceAttribution != "" && !validSourceAttribution(in.SourceAttribution) {
		return fmt.Errorf("%w: %q", ErrInvalidSourceAttribution, in.SourceAttribution)
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
		case pgErrUniqueViolation:
			if pgErr.ConstraintName == "policies_predecessor_unique_when_set" {
				return ErrPredecessorTaken
			}
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
	out.SelfApproved = r.SelfApproved
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
	if r.NextReviewAt.Valid {
		t := r.NextReviewAt.Time
		out.NextReviewAt = &t
	}
	if r.HistoricalApproverName != nil {
		v := *r.HistoricalApproverName
		out.HistoricalApproverName = &v
	}
	if r.HistoricalApprovedAt.Valid {
		d := r.HistoricalApprovedAt.Time
		out.HistoricalApprovedAt = &d
	}
	if r.SourceUrl != nil {
		v := *r.SourceUrl
		out.SourceURL = &v
	}
	if r.ImportedBy != nil {
		v := *r.ImportedBy
		out.ImportedBy = &v
	}
	if r.ImportedAt.Valid {
		t := r.ImportedAt.Time
		out.ImportedAt = &t
	}
	if r.SyncDriftDetectedAt.Valid {
		t := r.SyncDriftDetectedAt.Time
		out.SyncDriftDetectedAt = &t
	}
	if r.SyncDriftBodySha256 != nil {
		v := *r.SyncDriftBodySha256
		out.SyncDriftBodySHA256 = &v
	}
	if r.SyncPropertyDriftDetectedAt.Valid {
		t := r.SyncPropertyDriftDetectedAt.Time
		out.SyncPropertyDriftDetectedAt = &t
	}
	if r.SyncPropertyDriftKeys != nil {
		out.SyncPropertyDriftKeys = append([]string{}, r.SyncPropertyDriftKeys...)
	}
	if r.SyncMissingSince.Valid {
		t := r.SyncMissingSince.Time
		out.SyncMissingSince = &t
	}
	if r.SyncUnreadableSince.Valid {
		t := r.SyncUnreadableSince.Time
		out.SyncUnreadableSince = &t
		out.SyncUnreadableReason = r.SyncUnreadableReason
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
	// The chain row mirrors dbx.Policy verbatim; conversion is a direct
	// type cast since the field set is identical.
	return policyFromRow(dbx.Policy(r))
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

// pgTimestamptz maps an optional time to a nullable timestamptz.
func pgTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
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
