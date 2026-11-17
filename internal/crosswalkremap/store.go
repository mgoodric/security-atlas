// Package crosswalkremap decides crosswalk-edge remap proposals (#1777).
//
// After a new SCF release becomes current, scfimport.QueueEdgeRemapProposals
// queues one proposal per catalog edge (fw_to_scf_edges) left on an older
// release's anchor. A super-admin resolves each one:
//
//   - approve creates a copy of the edge on a successor anchor in the target
//     release, with the same requirement, relationship, strength,
//     rationale, source attribution and mapping tier; final. The legacy edge
//     is not touched.
//   - reject leaves the edge on its legacy anchor only; the proposal can be
//     approved or kept legacy later.
//   - keep legacy is the same, final.
//
// The import itself approves the proposals whose anchor kept its code and
// title (scfimport.QueueEdgeRemapProposals, #2315), recorded with
// decided_by scfimport.CarryForwardDecider; a person decides the rest.
//
// The edges are global catalog rows shared by every tenant, so the store
// sets no tenant context. The super-admin gate is the caller's
// (internal/api/admincrosswalkremap and policies/authz). In the database the
// app role can only read the queue and call scf_edge_remap_decide, which
// validates each decision and appends it to scf_edge_remap_decisions in the
// same statement.
package crosswalkremap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Proposal statuses.
const (
	StatusOpen       = "open"
	StatusApproved   = "approved"
	StatusRejected   = "rejected"
	StatusKeptLegacy = "kept_legacy"
)

// Statuses lists every proposal status.
var Statuses = []string{StatusOpen, StatusApproved, StatusRejected, StatusKeptLegacy}

// maxReason bounds a decision's reason, matching the column CHECK.
const maxReason = 2000

var (
	// ErrNotFound: no proposal with that id.
	ErrNotFound = errors.New("edge remap: proposal not found")
	// ErrDecided: the proposal's status does not admit the decision.
	ErrDecided = errors.New("edge remap: proposal already decided")
	// ErrAnchorRequired: no single successor, so approval must name one.
	ErrAnchorRequired = errors.New("edge remap: choose the successor anchor (scf_id) to approve this proposal")
	// ErrAnchorNotInRelease: the chosen code is not in the target release.
	ErrAnchorNotInRelease = errors.New("edge remap: the chosen anchor is not in the target SCF release")
	// ErrEdgeChanged: the legacy edge was retired or moved since queued.
	ErrEdgeChanged = errors.New("edge remap: the legacy edge changed since the proposal was made")
	// ErrEdgeExists: the requirement already maps to the chosen anchor.
	ErrEdgeExists = errors.New("edge remap: the requirement already maps to the chosen anchor")
	// ErrBulkRunning: another bulk approval holds the proposals' locks.
	ErrBulkRunning = errors.New("edge remap: a bulk approval is already running")
	// ErrDeciderRequired: no deciding person was named.
	ErrDeciderRequired = errors.New("edge remap: a deciding person is required")
	// ErrReasonTooLong: the reason exceeds maxReason characters.
	ErrReasonTooLong = fmt.Errorf("edge remap: reason exceeds %d characters", maxReason)
	// ErrTenantContext: the transaction carries app.current_tenant. A catalog
	// decision never runs in one, so this is a caller bug (HTTP 500).
	ErrTenantContext = errors.New("edge remap: refused under a tenant context")
)

// pgUniqueViolation is SQLSTATE 23505. Two proposals approved onto the same
// (requirement, anchor) at once can both pass the function's existence
// check; the second insert then hits fw_to_scf_edges' unique constraint.
const pgUniqueViolation = "23505"

// dbErrors maps the stable message prefixes scf_edge_remap_decide raises.
var dbErrors = []struct {
	prefix string
	err    error
}{
	{"edge-remap: not found", ErrNotFound},
	{"edge-remap: decided", ErrDecided},
	{"edge-remap: anchor required", ErrAnchorRequired},
	{"edge-remap: anchor not in release", ErrAnchorNotInRelease},
	{"edge-remap: edge changed", ErrEdgeChanged},
	{"edge-remap: edge exists", ErrEdgeExists},
	{"edge-remap: decider required", ErrDeciderRequired},
	{"edge-remap: refused under a tenant context", ErrTenantContext},
}

// Proposal is one queued edge remap with what a reviewer needs to decide it.
type Proposal struct {
	ID               string   `json:"id"`
	EdgeID           string   `json:"edge_id"`
	FrameworkSlug    string   `json:"framework_slug"`
	FrameworkVersion string   `json:"framework_version"`
	RequirementID    string   `json:"requirement_id"`
	RequirementCode  string   `json:"requirement_code"`
	RelationshipType string   `json:"relationship_type"`
	Strength         float64  `json:"strength"`
	MappingTier      string   `json:"mapping_tier"`
	LegacyAnchorID   string   `json:"legacy_anchor_id"`
	LegacySCFID      string   `json:"legacy_scf_id"`
	LegacyTitle      string   `json:"legacy_anchor_title"`
	LegacyRelease    string   `json:"legacy_release"`
	TargetRelease    string   `json:"target_release"`
	Class            string   `json:"class"`
	Status           string   `json:"status"`
	SuccessorSCFIDs  []string `json:"successor_scf_ids"`
	TargetAnchorID   string   `json:"target_anchor_id,omitempty"`
	NewEdgeID        string   `json:"new_edge_id,omitempty"`
	CreatedAt        string   `json:"created_at"`
}

// Decision is one decision as recorded in scf_edge_remap_decisions. The
// New* fields are set on an approval only.
type Decision struct {
	ID             string `json:"id"`
	ProposalID     string `json:"proposal_id"`
	Decision       string `json:"decision"`
	Bulk           bool   `json:"bulk"`
	LegacyEdgeID   string `json:"legacy_edge_id"`
	LegacyAnchorID string `json:"legacy_anchor_id"`
	LegacySCFID    string `json:"legacy_scf_id"`
	LegacyRelease  string `json:"legacy_release"`
	NewEdgeID      string `json:"new_edge_id,omitempty"`
	NewAnchorID    string `json:"new_anchor_id,omitempty"`
	NewSCFID       string `json:"new_scf_id,omitempty"`
	NewRelease     string `json:"new_release,omitempty"`
	Reason         string `json:"reason,omitempty"`
	DecidedBy      string `json:"decided_by"`
	DecidedAt      string `json:"decided_at"`
}

// DecideInput is one decision. SCFID applies to an approval: the successor
// code in the target release; empty takes the proposal's single successor.
type DecideInput struct {
	ProposalID string
	Decision   string
	SCFID      string
	Reason     string
	DecidedBy  string
}

// BulkSkip is an unchanged proposal bulk approval left open, with why.
type BulkSkip struct {
	ProposalID string `json:"proposal_id"`
	Reason     string `json:"reason"`
}

// BulkResult reports a bulk approval of unchanged proposals.
type BulkResult struct {
	ApprovedCount int        `json:"approved_count"`
	Approved      []Decision `json:"approved"`
	Skipped       []BulkSkip `json:"skipped"`
}

// Store reads and decides edge remap proposals over the app pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const listSQL = `
SELECT p.id, p.edge_id, f.slug, fv.version, r.id, r.code,
       e.relationship_type::text, e.strength, e.mapping_tier::text,
       p.legacy_anchor_id, p.legacy_scf_id, la.title, p.legacy_release,
       p.target_release, p.class, p.status, p.successor_scf_ids,
       p.target_anchor_id, p.new_edge_id, p.created_at
FROM scf_edge_remap_proposals p
JOIN fw_to_scf_edges e         ON e.id = p.edge_id
JOIN framework_requirements r  ON r.id = e.framework_requirement_id
JOIN framework_versions fv     ON fv.id = r.framework_version_id
JOIN frameworks f              ON f.id = fv.framework_id
JOIN scf_anchors la            ON la.id = p.legacy_anchor_id
WHERE ($1 = '' OR p.class = $1)
  AND ($2 = '' OR p.status = $2)
ORDER BY p.class, p.legacy_scf_id, f.slug, r.code, p.id`

// List returns the queue, optionally narrowed by class and status ("" for
// any).
func (s *Store) List(ctx context.Context, class, status string) ([]Proposal, error) {
	rows, err := s.pool.Query(ctx, listSQL, class, status)
	if err != nil {
		return nil, fmt.Errorf("edge remap: list proposals: %w", err)
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		var p Proposal
		var id, edge, req, legacyAnchor uuid.UUID
		var target, newEdge *uuid.UUID
		var created time.Time
		if err := rows.Scan(&id, &edge, &p.FrameworkSlug, &p.FrameworkVersion, &req, &p.RequirementCode,
			&p.RelationshipType, &p.Strength, &p.MappingTier,
			&legacyAnchor, &p.LegacySCFID, &p.LegacyTitle, &p.LegacyRelease,
			&p.TargetRelease, &p.Class, &p.Status, &p.SuccessorSCFIDs,
			&target, &newEdge, &created); err != nil {
			return nil, fmt.Errorf("edge remap: scan proposal: %w", err)
		}
		p.ID, p.EdgeID, p.RequirementID, p.LegacyAnchorID = id.String(), edge.String(), req.String(), legacyAnchor.String()
		if target != nil {
			p.TargetAnchorID = target.String()
		}
		if newEdge != nil {
			p.NewEdgeID = newEdge.String()
		}
		p.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("edge remap: list proposals: %w", err)
	}
	return out, nil
}

// Decide applies one decision: approved, rejected or kept_legacy.
func (s *Store) Decide(ctx context.Context, in DecideInput) (Decision, error) {
	if err := checkInput(in.DecidedBy, in.Reason); err != nil {
		return Decision{}, err
	}
	pid, err := uuid.Parse(strings.TrimSpace(in.ProposalID))
	if err != nil {
		return Decision{}, ErrNotFound
	}
	return decide(ctx, s.pool, pid, in.Decision, in.SCFID, in.Reason, in.DecidedBy, false)
}

// BulkApproveUnchanged approves every open unchanged proposal with its
// single successor. Rejected and kept-legacy proposals are never touched. A
// concurrent bulk run gets ErrBulkRunning instead of queueing; a re-run
// after success finds nothing to do.
func (s *Store) BulkApproveUnchanged(ctx context.Context, decidedBy string) (BulkResult, error) {
	if err := checkInput(decidedBy, ""); err != nil {
		return BulkResult{}, err
	}
	out := BulkResult{Approved: []Decision{}, Skipped: []BulkSkip{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BulkResult{}, fmt.Errorf("edge remap: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `SELECT * FROM scf_edge_remap_lock_open_unchanged()`)
	if err != nil {
		return BulkResult{}, lockErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return BulkResult{}, lockErr(err)
	}
	for _, id := range ids {
		// A savepoint per proposal: one that cannot be applied is skipped
		// without aborting the others.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return BulkResult{}, fmt.Errorf("edge remap: savepoint: %w", err)
		}
		d, err := decide(ctx, sp, id, StatusApproved, "", "", decidedBy, true)
		switch {
		case err == nil:
			if err := sp.Commit(ctx); err != nil {
				return BulkResult{}, fmt.Errorf("edge remap: release savepoint: %w", err)
			}
			out.Approved = append(out.Approved, d)
		case errors.Is(err, ErrEdgeChanged), errors.Is(err, ErrEdgeExists),
			errors.Is(err, ErrAnchorNotInRelease), errors.Is(err, ErrAnchorRequired):
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				return BulkResult{}, fmt.Errorf("edge remap: roll back savepoint: %w", rbErr)
			}
			out.Skipped = append(out.Skipped, BulkSkip{
				ProposalID: id.String(), Reason: strings.TrimPrefix(err.Error(), "edge remap: "),
			})
		default:
			_ = sp.Rollback(ctx)
			return BulkResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BulkResult{}, fmt.Errorf("edge remap: commit: %w", err)
	}
	out.ApprovedCount = len(out.Approved)
	return out, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func decide(ctx context.Context, q querier, id uuid.UUID, decision, scfID, reason, decidedBy string, bulk bool) (Decision, error) {
	var d Decision
	var did, pid, legacyEdge, legacyAnchor uuid.UUID
	var newEdge, newAnchor *uuid.UUID
	var newSCF, newRelease, why *string
	var at time.Time
	err := q.QueryRow(ctx, `
		SELECT id, proposal_id, decision, bulk, legacy_edge_id, legacy_anchor_id,
		       legacy_scf_id, legacy_release, new_edge_id, new_anchor_id,
		       new_scf_id, new_release, reason, decided_by, decided_at
		FROM scf_edge_remap_decide($1, $2, $3, $4, $5, $6)`,
		id, decision, scfID, reason, decidedBy, bulk).Scan(
		&did, &pid, &d.Decision, &d.Bulk, &legacyEdge, &legacyAnchor,
		&d.LegacySCFID, &d.LegacyRelease, &newEdge, &newAnchor,
		&newSCF, &newRelease, &why, &d.DecidedBy, &at)
	if err != nil {
		return Decision{}, mapDBErr(err)
	}
	d.ID, d.ProposalID = did.String(), pid.String()
	d.LegacyEdgeID, d.LegacyAnchorID = legacyEdge.String(), legacyAnchor.String()
	if newEdge != nil {
		d.NewEdgeID = newEdge.String()
	}
	if newAnchor != nil {
		d.NewAnchorID = newAnchor.String()
	}
	if newSCF != nil {
		d.NewSCFID = *newSCF
	}
	if newRelease != nil {
		d.NewRelease = *newRelease
	}
	if why != nil {
		d.Reason = *why
	}
	d.DecidedAt = at.UTC().Format(time.RFC3339)
	return d, nil
}

func checkInput(decidedBy, reason string) error {
	if strings.TrimSpace(decidedBy) == "" {
		return ErrDeciderRequired
	}
	if len([]rune(strings.TrimSpace(reason))) > maxReason {
		return ErrReasonTooLong
	}
	return nil
}

func mapDBErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		for _, m := range dbErrors {
			if strings.HasPrefix(pgErr.Message, m.prefix) {
				return fmt.Errorf("%w (%s)", m.err, pgErr.Message)
			}
		}
		if pgErr.Code == pgUniqueViolation {
			return fmt.Errorf("%w (%s)", ErrEdgeExists, pgErr.Message)
		}
	}
	return fmt.Errorf("edge remap: decide: %w", err)
}

// pgLockNotAvailable is SQLSTATE 55P03, raised by FOR UPDATE NOWAIT.
const pgLockNotAvailable = "55P03"

func lockErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgLockNotAvailable {
		return ErrBulkRunning
	}
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Message, "edge-remap: refused under a tenant context") {
		return fmt.Errorf("%w (%s)", ErrTenantContext, pgErr.Message)
	}
	return fmt.Errorf("edge remap: lock unchanged proposals: %w", err)
}
