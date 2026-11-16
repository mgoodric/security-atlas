// Risk treatment and acceptance transitions (#2105).
//
// Treatment and acceptance are not record fields; PATCH refuses them. They
// change only through these transitions, each validated against the risk's
// current state and written with one risk_audit_log row in the same
// transaction:
//
//	change treatment   any treatment but accept -> untreated | mitigate |
//	                   transfer | avoid          (treatment_changed)
//	propose acceptance any treatment but accept -> accept (proposed)
//	                                             (acceptance_proposed)
//	approve acceptance accept (proposed) -> accept (approved)
//	                                             (acceptance_approved)
//	reject acceptance  accept (proposed) -> untreated (acceptance_rejected)
//	revoke acceptance  accept (approved) -> untreated (acceptance_revoked)
//
// A closed risk takes none of them (#2107); it is reopened first.
//
// A risk with an acceptance in flight, proposed or approved, changes
// treatment only by rejecting or revoking it first. A rejected or revoked
// acceptance leaves the risk untreated: no treatment decision stands, and
// the risk counts as open exposure until its owner makes one.
//
// Approval follows the shared approval rule (internal/approval, ADR-0024):
// only a person approves, never the proposer unless the organisation allows
// self-approval, and a self-approval is flagged on the risk and in the
// audit row.
//
// The functions ending in Tx run inside the caller's transaction, so the
// REST store and the MCP treatment-proposal applier write the same
// transitions under the same rules.

package risk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Audit actions the transitions write (migration 20261206000000).
const (
	ActionTreatmentChanged   = "treatment_changed"
	ActionAcceptanceProposed = "acceptance_proposed"
	ActionAcceptanceApproved = "acceptance_approved"
	ActionAcceptanceRejected = "acceptance_rejected"
	ActionAcceptanceRevoked  = "acceptance_revoked"
)

var (
	// ErrTransitionState is returned when the risk is not in the state a
	// transition starts from. The HTTP layer renders it as a 409.
	ErrTransitionState = errors.New("risk: transition not allowed from the risk's current state")
	// ErrTransitionInput is returned for a missing or invalid transition
	// field (justification, expiry, reason). The HTTP layer renders a 400.
	ErrTransitionInput = errors.New("risk: invalid transition input")
	// ErrAcceptanceSelfApproval is returned when the proposer approves
	// their own acceptance and the organisation does not allow it.
	ErrAcceptanceSelfApproval = errors.New("risk: the proposer cannot approve their own acceptance")
	// ErrAcceptanceApproverNotPerson is returned when the approving
	// credential belongs to no user (an API key, an OAuth client, the
	// bootstrap admin). Only people approve.
	ErrAcceptanceApproverNotPerson = errors.New("risk: only a person can approve an acceptance")
	// ErrAcceptanceProposerUnknown is returned when the acceptance names no
	// proposer a person can be compared with: none was recorded (proposed
	// before #2105) or a session was (pre-#1807). No approver can be shown
	// to differ from the proposer, so, as a framework scope with no
	// submitter, it must be proposed again first (#2371). It wraps
	// ErrTransitionState, so REST and the MCP confirm surface render a 409.
	ErrAcceptanceProposerUnknown = fmt.Errorf("%w: the acceptance has no recorded proposer, so a second approver cannot be verified; reject it and propose it again", ErrTransitionState)
)

// IsTransitionInput reports whether err is a caller error in a transition
// request, rendered as a 400.
func IsTransitionInput(err error) bool {
	return errors.Is(err, ErrTransitionInput) || errors.Is(err, ErrTreatmentValidation)
}

// IsApprovalRefusal reports whether err is the approval rule refusing the
// approver, rendered as a 403.
func IsApprovalRefusal(err error) bool {
	return errors.Is(err, ErrAcceptanceSelfApproval) || errors.Is(err, ErrAcceptanceApproverNotPerson)
}

// ChangeTreatmentInput changes a risk's treatment to anything but accept.
// InstrumentReference applies to transfer only; nil keeps the risk's own.
type ChangeTreatmentInput struct {
	Treatment           dbx.RiskTreatment
	InstrumentReference *string
	Actor               string
}

// ProposeAcceptanceInput proposes accepting a risk until ProposedUntil.
type ProposeAcceptanceInput struct {
	Justification string
	ProposedUntil time.Time
	Proposer      string
}

// ApproveAcceptanceInput approves a proposed acceptance until
// AcceptedUntil. The approver is recorded as the accepter.
type ApproveAcceptanceInput struct {
	AcceptedUntil time.Time
	Approver      approval.Approver
}

// EndAcceptanceInput rejects a proposed acceptance or revokes an approved
// one. Revoke requires a reason; reject takes one optionally.
type EndAcceptanceInput struct {
	Reason string
	Actor  string
}

// ChangeTreatment runs ChangeTreatmentTx in its own transaction.
func (s *Store) ChangeTreatment(ctx context.Context, id uuid.UUID, in ChangeTreatmentInput) (Risk, error) {
	return s.transition(ctx, func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error) {
		return ChangeTreatmentTx(ctx, q, tenantID, id, in)
	})
}

// ProposeAcceptance runs ProposeAcceptanceTx in its own transaction.
func (s *Store) ProposeAcceptance(ctx context.Context, id uuid.UUID, in ProposeAcceptanceInput) (Risk, error) {
	return s.transition(ctx, func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error) {
		return ProposeAcceptanceTx(ctx, q, tenantID, id, in, now)
	})
}

// ApproveAcceptance runs ApproveAcceptanceTx in its own transaction.
func (s *Store) ApproveAcceptance(ctx context.Context, id uuid.UUID, in ApproveAcceptanceInput) (Risk, error) {
	return s.transition(ctx, func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error) {
		return ApproveAcceptanceTx(ctx, q, tenantID, id, in, now)
	})
}

// RejectAcceptance runs RejectAcceptanceTx in its own transaction.
func (s *Store) RejectAcceptance(ctx context.Context, id uuid.UUID, in EndAcceptanceInput) (Risk, error) {
	return s.transition(ctx, func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error) {
		return RejectAcceptanceTx(ctx, q, tenantID, id, in)
	})
}

// RevokeAcceptance runs RevokeAcceptanceTx in its own transaction.
func (s *Store) RevokeAcceptance(ctx context.Context, id uuid.UUID, in EndAcceptanceInput) (Risk, error) {
	return s.transition(ctx, func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error) {
		return RevokeAcceptanceTx(ctx, q, tenantID, id, in)
	})
}

// transition runs fn in a transaction and fills the response shape (links,
// category names). Treatment is not an aggregation input, so the
// aggregation hooks do not run and no rule lock is taken: the only lock is
// the risk row, taken by fn.
func (s *Store) transition(ctx context.Context, fn func(q *dbx.Queries, tenantID uuid.UUID, now time.Time) (Risk, error)) (Risk, error) {
	var out Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		r, err := fn(q, tenantID, time.Now().UTC())
		if err != nil {
			return err
		}
		return s.finishPatch(ctx, q, tenantID, r, &out, false)
	})
	return out, err
}

// ChangeTreatmentTx changes the treatment of a risk with no acceptance in
// flight. The target is validated as create validates it (mitigate needs a
// linked control or action plan; transfer needs an instrument reference).
// Changing to the current treatment with nothing else changed writes
// nothing.
func ChangeTreatmentTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in ChangeTreatmentInput) (Risk, error) {
	if in.Treatment == dbx.RiskTreatmentAccept {
		return Risk{}, fmt.Errorf("%w: accept a risk by proposing an acceptance", ErrTransitionInput)
	}
	if in.InstrumentReference != nil && in.Treatment != dbx.RiskTreatmentTransfer {
		return Risk{}, fmt.Errorf("%w: instrument_reference applies only to treatment=transfer", ErrTransitionInput)
	}
	current, err := lockOpenRisk(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if current.Treatment == dbx.RiskTreatmentAccept {
		return Risk{}, fmt.Errorf("%w: the risk has an acceptance %s; reject or revoke it first", ErrTransitionState, current.AcceptanceStatus)
	}
	next := current
	next.Treatment = in.Treatment
	if in.InstrumentReference != nil {
		next.InstrumentReference = strings.TrimSpace(*in.InstrumentReference)
	}
	controls, plans, err := linkCounts(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if err := ValidateTreatment(TreatmentInput{
		Treatment:             next.Treatment,
		InstrumentReference:   next.InstrumentReference,
		LinkedControlCount:    controls,
		LinkedActionPlanCount: plans,
	}); err != nil {
		return Risk{}, err
	}
	if next.Treatment == current.Treatment && next.InstrumentReference == current.InstrumentReference {
		return current, nil
	}
	before := map[string]any{"treatment": current.Treatment}
	after := map[string]any{"treatment": next.Treatment}
	if next.InstrumentReference != current.InstrumentReference {
		before["instrument_reference"] = current.InstrumentReference
		after["instrument_reference"] = next.InstrumentReference
	}
	return writeTreatment(ctx, q, tenantID, next, in.Actor, ActionTreatmentChanged, before, after)
}

// ProposeAcceptanceTx proposes accepting a risk with no acceptance in
// flight. The justification is required and the proposed expiry must fall
// after today (UTC). Any earlier approval's accepter and expiry are
// cleared; the approver sets them.
func ProposeAcceptanceTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in ProposeAcceptanceInput, now time.Time) (Risk, error) {
	justification := strings.TrimSpace(in.Justification)
	if justification == "" {
		return Risk{}, fmt.Errorf("%w: justification is required", ErrTransitionInput)
	}
	if err := checkExpiry("proposed_until", in.ProposedUntil, now); err != nil {
		return Risk{}, err
	}
	if in.Proposer == "" {
		return Risk{}, fmt.Errorf("%w: proposer is required", ErrTransitionInput)
	}
	current, err := lockOpenRisk(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if current.Treatment == dbx.RiskTreatmentAccept {
		return Risk{}, fmt.Errorf("%w: the risk already has an acceptance %s", ErrTransitionState, current.AcceptanceStatus)
	}
	until := dateOnly(in.ProposedUntil)
	next := current
	next.Treatment = dbx.RiskTreatmentAccept
	next.AcceptanceStatus = dbx.RiskAcceptanceStatusProposed
	next.Acceptance = Acceptance{
		Justification: justification,
		ProposedUntil: &until,
		ProposedBy:    in.Proposer,
		ProposedAt:    &now,
	}
	next.AcceptedUntil, next.Accepter = nil, ""
	before := map[string]any{"treatment": current.Treatment}
	after := map[string]any{
		"treatment":         next.Treatment,
		"acceptance_status": next.AcceptanceStatus,
		"justification":     justification,
		"proposed_until":    until.Format(time.DateOnly),
	}
	return writeTreatment(ctx, q, tenantID, next, in.Proposer, ActionAcceptanceProposed, before, after)
}

// ApproveAcceptanceTx approves a proposed acceptance. accepted_until is
// required and must fall after today; it may differ from the proposed
// expiry. The shared approval rule is read and applied in this
// transaction.
func ApproveAcceptanceTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in ApproveAcceptanceInput, now time.Time) (Risk, error) {
	if err := checkExpiry("accepted_until", in.AcceptedUntil, now); err != nil {
		return Risk{}, err
	}
	current, err := lockOpenRisk(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if current.AcceptanceStatus != dbx.RiskAcceptanceStatusProposed {
		return Risk{}, fmt.Errorf("%w: only a proposed acceptance can be approved", ErrTransitionState)
	}
	allowSelf, err := approval.AllowSelf(ctx, q, tenantID)
	if err != nil {
		return Risk{}, err
	}
	self, err := CheckAcceptanceApprover(current.Acceptance.ProposedBy, in.Approver, allowSelf)
	if err != nil {
		return Risk{}, err
	}
	until := dateOnly(in.AcceptedUntil)
	next := current
	next.AcceptanceStatus = dbx.RiskAcceptanceStatusApproved
	next.AcceptedUntil = &until
	next.Accepter = in.Approver.UserID
	next.Acceptance.ApprovedAt = &now
	next.Acceptance.SelfApproved = self
	before := map[string]any{"acceptance_status": current.AcceptanceStatus}
	after := map[string]any{
		"acceptance_status": next.AcceptanceStatus,
		"accepted_until":    until.Format(time.DateOnly),
		"accepter":          next.Accepter,
		"self_approved":     self,
	}
	return writeTreatment(ctx, q, tenantID, next, in.Approver.UserID, ActionAcceptanceApproved, before, after)
}

// CheckAcceptanceApprover applies the shared approval rule to an
// acceptance and maps its errors onto this package's. An acceptance with
// no comparable proposer is refused rather than admitted: approval.Check
// would let anyone approve it, its creator included (#2371). GET
// /v1/approvals (#2119) calls it per row, so it lists exactly what
// ApproveAcceptanceTx accepts.
func CheckAcceptanceApprover(proposer string, a approval.Approver, allowSelf bool) (selfApproved bool, err error) {
	if a.UserID == "" {
		return false, ErrAcceptanceApproverNotPerson
	}
	if proposer == "" {
		return false, ErrAcceptanceProposerUnknown
	}
	self, err := approval.Check(proposer, a, allowSelf)
	switch {
	case errors.Is(err, approval.ErrNotPerson):
		return false, ErrAcceptanceApproverNotPerson
	case errors.Is(err, approval.ErrRequesterUnknown):
		return false, ErrAcceptanceProposerUnknown
	case errors.Is(err, approval.ErrSelfApproval):
		return false, ErrAcceptanceSelfApproval
	}
	return self, err
}

// RejectAcceptanceTx rejects a proposed acceptance. The risk returns to
// untreated and the proposal's record is cleared; the audit row keeps it.
func RejectAcceptanceTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in EndAcceptanceInput) (Risk, error) {
	return endAcceptanceTx(ctx, q, tenantID, id, in, dbx.RiskAcceptanceStatusProposed, ActionAcceptanceRejected)
}

// RevokeAcceptanceTx revokes an approved acceptance, with a required
// reason. The risk returns to untreated.
func RevokeAcceptanceTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in EndAcceptanceInput) (Risk, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return Risk{}, fmt.Errorf("%w: reason is required", ErrTransitionInput)
	}
	return endAcceptanceTx(ctx, q, tenantID, id, in, dbx.RiskAcceptanceStatusApproved, ActionAcceptanceRevoked)
}

func endAcceptanceTx(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID, in EndAcceptanceInput, from dbx.RiskAcceptanceStatus, action string) (Risk, error) {
	current, err := lockOpenRisk(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if current.AcceptanceStatus != from {
		return Risk{}, fmt.Errorf("%w: the risk has no %s acceptance", ErrTransitionState, from)
	}
	next := current
	next.Treatment = dbx.RiskTreatmentUntreated
	next.AcceptanceStatus = ""
	next.AcceptedUntil, next.Accepter = nil, ""
	next.Acceptance = Acceptance{}
	before := acceptanceState(current)
	after := map[string]any{"treatment": next.Treatment}
	if reason := strings.TrimSpace(in.Reason); reason != "" {
		after["reason"] = reason
	}
	return writeTreatment(ctx, q, tenantID, next, in.Actor, action, before, after)
}

// acceptanceState is the before half of a reject or revoke row: the
// acceptance as it stood, so the trail keeps what the risk row clears.
func acceptanceState(r Risk) map[string]any {
	s := map[string]any{
		"treatment":         r.Treatment,
		"acceptance_status": r.AcceptanceStatus,
		"justification":     r.Acceptance.Justification,
		"proposed_by":       r.Acceptance.ProposedBy,
	}
	if r.Acceptance.ProposedUntil != nil {
		s["proposed_until"] = r.Acceptance.ProposedUntil.Format(time.DateOnly)
	}
	if r.AcceptedUntil != nil {
		s["accepted_until"] = r.AcceptedUntil.Format(time.DateOnly)
		s["accepter"] = r.Accepter
		s["self_approved"] = r.Acceptance.SelfApproved
	}
	return s
}

// lockOpenRisk locks a risk for a treatment or acceptance transition and
// refuses a closed one (#2107): a closed risk is retired, so its
// treatment and acceptance stand as they were until it is reopened.
func lockOpenRisk(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID) (Risk, error) {
	r, err := lockRisk(ctx, q, tenantID, id)
	if err != nil {
		return Risk{}, err
	}
	if r.Status == dbx.RiskStatusClosed {
		return Risk{}, fmt.Errorf("%w: the risk is closed; reopen it first", ErrTransitionState)
	}
	return r, nil
}

func lockRisk(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID) (Risk, error) {
	row, err := q.LockRiskByID(ctx, dbx.LockRiskByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Risk{}, ErrNotFound
	}
	if err != nil {
		return Risk{}, fmt.Errorf("lock risk: %w", err)
	}
	return riskFromRow(row), nil
}

func linkCounts(ctx context.Context, q *dbx.Queries, tenantID, id uuid.UUID) (controls, plans int, err error) {
	c, err := q.CountRiskControlLinks(ctx, dbx.CountRiskControlLinksParams{TenantID: pgUUID(tenantID), RiskID: pgUUID(id)})
	if err != nil {
		return 0, 0, fmt.Errorf("count risk control links: %w", err)
	}
	p, err := q.CountRiskActionPlanLinks(ctx, dbx.CountRiskActionPlanLinksParams{TenantID: pgUUID(tenantID), RiskID: pgUUID(id)})
	if err != nil {
		return 0, 0, fmt.Errorf("count risk action plan links: %w", err)
	}
	return int(c), int(p), nil
}

// checkExpiry requires an expiry date after today (UTC).
func checkExpiry(field string, t, now time.Time) error {
	if t.IsZero() {
		return fmt.Errorf("%w: %s is required", ErrTransitionInput, field)
	}
	if !dateOnly(t).After(dateOnly(now)) {
		return fmt.Errorf("%w: %s must be after today", ErrTransitionInput, field)
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// writeTreatment writes the treatment and acceptance columns of next and
// its audit row.
func writeTreatment(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, next Risk, actor, action string, before, after any) (Risk, error) {
	a := next.Acceptance
	row, err := q.SetRiskTreatment(ctx, dbx.SetRiskTreatmentParams{
		TenantID:                pgUUID(tenantID),
		ID:                      pgUUID(next.ID),
		Treatment:               next.Treatment,
		AcceptanceStatus:        NullAcceptanceStatus(next.AcceptanceStatus),
		InstrumentReference:     next.InstrumentReference,
		AcceptedUntil:           pgDatePtr(next.AcceptedUntil),
		Accepter:                next.Accepter,
		AcceptanceJustification: a.Justification,
		AcceptanceProposedUntil: pgDatePtr(a.ProposedUntil),
		AcceptanceProposedBy:    a.ProposedBy,
		AcceptanceProposedAt:    pgTimestamptzPtr(a.ProposedAt),
		AcceptanceApprovedAt:    pgTimestamptzPtr(a.ApprovedAt),
		SelfApproved:            a.SelfApproved,
	})
	if err != nil {
		return Risk{}, fmt.Errorf("%s: %w", action, err)
	}
	if err := insertRiskAudit(ctx, q, tenantID, next.ID, actor, action, before, after); err != nil {
		return Risk{}, err
	}
	return riskFromRow(row), nil
}

// acceptanceFromRow reads the acceptance record columns.
func acceptanceFromRow(r dbx.Risk) Acceptance {
	return Acceptance{
		Justification: r.AcceptanceJustification,
		ProposedUntil: dateFromPg(r.AcceptanceProposedUntil),
		ProposedBy:    r.AcceptanceProposedBy,
		ProposedAt:    timeFromPg(r.AcceptanceProposedAt),
		ApprovedAt:    timeFromPg(r.AcceptanceApprovedAt),
		SelfApproved:  r.SelfApproved,
	}
}

func dateFromPg(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func timeFromPg(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
