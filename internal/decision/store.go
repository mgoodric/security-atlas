// Package decision implements the slice-055 Decision Log CRUD + linkage.
//
// A Decision captures a non-compliance operational or architectural
// tradeoff -- "shipping MVP, deferring SAML to v1.2", "skipping IaC because
// the tool sunsets Q3". It is distinct from an Exception (canvas Â§6.3 / the
// internal/exception package): an Exception is a formal, scoped,
// time-bounded bypass of a specific control; a Decision is the broader
// rationale record. Canvas Â§6.7 is the design source; CONTEXT.md
// ("Decision Log (slice 055)") captures the precise domain definition.
//
// The lifecycle states (decision_status enum, slice 052):
//
//	active -> revisited                (review without change)
//	active -> superseded               (replacement decision exists)
//	active -> expired                  (relevance lapsed)
//
// `superseded` and `expired` are terminal. A superseded decision is NEVER
// deleted (P0 anti-criterion) -- it pairs with the superseded_by FK so the
// auditor trail stays legible.
//
// Every mutation -- PATCH, supersede, link add/remove, denied cross-tenant
// link attempt, overdue-notification emission -- writes one append-only row
// to decisions_audit (slice 055 migration _030). No path mutates a
// `decisions` row without a corresponding audit row.
package decision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Status constants mirror the decision_status enum.
const (
	StatusActive     = "active"
	StatusRevisited  = "revisited"
	StatusSuperseded = "superseded"
	StatusExpired    = "expired"
)

// Audit-action vocabulary -- must match decisions_audit_action_chk.
const (
	ActionCreated               = "created"
	ActionUpdated               = "updated"
	ActionSuperseded            = "superseded"
	ActionLinkAdded             = "link_added"
	ActionLinkRemoved           = "link_removed"
	ActionCrossTenantLinkDenied = "cross_tenant_link_denied"
	ActionOverdueNotified       = "overdue_notified"
)

// SystemActor is the actor recorded when the daily overdue-notification job
// writes audit rows. Distinct from any credential id so audit-trail review
// can segregate system-driven rows from human ones.
const SystemActor = "system:decision-overdue"

// LinkKind identifies which of the four M:N link tables a linkage targets.
type LinkKind string

// The four link kinds. Each maps to one slice-052 link table.
const (
	LinkRisk           LinkKind = "risks"
	LinkControl        LinkKind = "controls"
	LinkException      LinkKind = "exceptions"
	LinkScopePredicate LinkKind = "scope_predicates"
)

// ValidLinkKind reports whether s names one of the four link kinds.
func ValidLinkKind(s string) bool {
	switch LinkKind(s) {
	case LinkRisk, LinkControl, LinkException, LinkScopePredicate:
		return true
	}
	return false
}

// Errors surfaced by Store. Handlers map these to HTTP status codes.
var (
	// ErrNotFound is returned when an id doesn't resolve under the active
	// tenant context. RLS-friendly: same shape as "in another tenant".
	ErrNotFound = errors.New("decision: not found")
	// ErrWrongState is returned when a transition is requested from a
	// state that doesn't permit it (e.g. supersede on an already-superseded
	// decision). HTTP 409.
	ErrWrongState = errors.New("decision: not in expected state")
	// ErrTitleRequired is returned when create/update input lacks title.
	ErrTitleRequired = errors.New("decision: title is required")
	// ErrDecisionMakerRequired is returned when create input lacks
	// decision_maker. Human authorship is mandatory (P0 anti-criterion: no
	// AI auto-creation).
	ErrDecisionMakerRequired = errors.New("decision: decision_maker is required")
	// ErrDecidedAtRequired is returned when create input lacks decided_at.
	ErrDecidedAtRequired = errors.New("decision: decided_at is required")
	// ErrSupersededByRequired is returned when supersede input lacks the
	// replacement decision id.
	ErrSupersededByRequired = errors.New("decision: superseded_by is required")
	// ErrSelfSupersede is returned when a decision is asked to supersede
	// itself.
	ErrSelfSupersede = errors.New("decision: a decision cannot supersede itself")
	// ErrCrossTenantLink is returned when a link targets an entity that
	// does not resolve in the active tenant. Surfaced as HTTP 404
	// (existence-leak prevention).
	ErrCrossTenantLink = errors.New("decision: link target not found in tenant")
	// ErrInvalidLinkKind is returned for a link kind outside the four
	// known kinds.
	ErrInvalidLinkKind = errors.New("decision: invalid link kind")
)

// pgErrForeignKeyViolation is the SQLSTATE Postgres returns when a composite
// FK references a (tenant_id, target_id) tuple that does not resolve. On a
// link INSERT this means the target is missing or in another tenant -- both
// collapse to ErrCrossTenantLink / HTTP 404.
const pgErrForeignKeyViolation = "23503"

// Decision is the domain shape returned from store calls.
type Decision struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	DecisionID           string // human-readable "DL-YYYY-MM-DD-NNNN"
	Title                string
	Narrative            string
	Constraints          []string
	Tradeoffs            string
	DecisionMaker        string
	DecidedAt            time.Time
	RevisitBy            *time.Time
	Status               string
	SupersededBy         *uuid.UUID
	AuditNarrativeOptOut bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// Link is one M:N linkage row, normalised across the four link kinds.
type Link struct {
	Kind      LinkKind
	TargetID  uuid.UUID
	CreatedAt time.Time
}

// Linkage bundles every link of a decision, one slice per kind.
type Linkage struct {
	Risks           []Link
	Controls        []Link
	Exceptions      []Link
	ScopePredicates []Link
}

// AuditEntry is the public shape of a decisions_audit row.
type AuditEntry struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	DecisionID uuid.UUID
	Action     string
	Actor      string
	Detail     string
	OccurredAt time.Time
}

// Store wraps the sqlc Queries with the tenancy plumbing required for RLS.
// Same shape as exception.Store / risk.Store: every method opens a tx,
// applies the tenant GUC, and runs queries inside that transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS) for RLS to fire.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateInput is the API shape for POST /v1/decisions. The store
// re-validates every rule the handler should have validated already;
// defense in depth.
type CreateInput struct {
	Title         string
	Narrative     string
	Constraints   []string
	Tradeoffs     string
	DecisionMaker string
	DecidedAt     time.Time
	RevisitBy     *time.Time
}

// Create inserts a new decision in `active` state, generates its
// DL-YYYY-MM-DD-NNNN identifier, and writes the `created` audit row.
// AC-1 + AC-8.
func (s *Store) Create(ctx context.Context, in CreateInput) (Decision, error) {
	if in.Title == "" {
		return Decision{}, ErrTitleRequired
	}
	if in.DecisionMaker == "" {
		return Decision{}, ErrDecisionMakerRequired
	}
	if in.DecidedAt.IsZero() {
		return Decision{}, ErrDecidedAtRequired
	}
	if in.Constraints == nil {
		in.Constraints = []string{}
	}
	decidedAt := in.DecidedAt.UTC()

	var out Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// AC-1: generate the human-readable DL-YYYY-MM-DD-NNNN id. NNNN is
		// a per-tenant, per-day sequence -- count the decisions already
		// filed on decided_at's UTC calendar date and add one. The count
		// is computed in Go (start-of-day .. start-of-next-day window) so
		// the date arithmetic never enters a pgx placeholder type-inference
		// path.
		dayStart := time.Date(decidedAt.Year(), decidedAt.Month(), decidedAt.Day(), 0, 0, 0, 0, time.UTC)
		dayEnd := dayStart.AddDate(0, 0, 1)
		sameDay, err := q.CountDecisionsByDecidedDate(ctx, dbx.CountDecisionsByDecidedDateParams{
			TenantID:    pgUUID(tenantID),
			DecidedAt:   pgTimestamptz(dayStart),
			DecidedAt_2: pgTimestamptz(dayEnd),
		})
		if err != nil {
			return fmt.Errorf("count same-day decisions: %w", err)
		}
		decisionID := fmt.Sprintf("DL-%s-%04d", dayStart.Format("2006-01-02"), sameDay+1)

		id := uuid.New()
		row, err := q.CreateDecision(ctx, dbx.CreateDecisionParams{
			ID:            pgUUID(id),
			TenantID:      pgUUID(tenantID),
			DecisionID:    decisionID,
			Title:         in.Title,
			Narrative:     in.Narrative,
			Constraints:   in.Constraints,
			Tradeoffs:     in.Tradeoffs,
			DecisionMaker: in.DecisionMaker,
			DecidedAt:     pgTimestamptz(decidedAt),
			RevisitBy:     pgDate(in.RevisitBy),
			Status:        dbx.DecisionStatusActive,
			SupersededBy:  pgtype.UUID{},
		})
		if err != nil {
			return fmt.Errorf("create decision: %w", err)
		}
		out = decisionFromRow(row)
		return s.writeAudit(ctx, q, tenantID, id, ActionCreated, in.DecisionMaker,
			fmt.Sprintf("decision_id=%s", decisionID))
	})
	return out, err
}

// Get returns a single decision by id. ErrNotFound when absent.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Decision, error) {
	var out Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision: %w", err)
		}
		out = decisionFromRow(row)
		return nil
	})
	return out, err
}

// GetWithLinkage returns a decision plus all four linkage arrays in one
// call. AC-2 read shape. ErrNotFound when the decision is absent.
func (s *Store) GetWithLinkage(ctx context.Context, id uuid.UUID) (Decision, Linkage, error) {
	var dOut Decision
	var lOut Linkage
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision: %w", err)
		}
		dOut = decisionFromRow(row)
		lk, err := loadLinkage(ctx, q, tenantID, id)
		if err != nil {
			return err
		}
		lOut = lk
		return nil
	})
	return dOut, lOut, err
}

// ListFilter narrows the result set of List. Empty fields are ignored.
type ListFilter struct {
	// Status, when non-empty, restricts to decisions in that status.
	Status string
	// RevisitDueWithinDays, when > 0, restricts to active decisions whose
	// revisit_by falls on or before today + N days.
	RevisitDueWithinDays int
	// Now is the clock used for the revisit-window filter. Defaults to
	// time.Now().UTC() when zero -- callers override only in tests.
	Now time.Time
}

// List returns decisions for the active tenant, newest first, after
// applying the filter. AC-2 list shape.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Decision, error) {
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	var out []Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var rows []dbx.Decision
		var err error
		switch {
		case filter.RevisitDueWithinDays > 0:
			cutoff := filter.Now.AddDate(0, 0, filter.RevisitDueWithinDays)
			rows, err = q.ListDecisionsDueForRevisit(ctx, dbx.ListDecisionsDueForRevisitParams{
				TenantID:  pgUUID(tenantID),
				RevisitBy: pgDateValue(cutoff),
			})
		case filter.Status != "":
			rows, err = q.ListDecisionsByStatus(ctx, dbx.ListDecisionsByStatusParams{
				TenantID: pgUUID(tenantID),
				Status:   dbx.DecisionStatus(filter.Status),
			})
		default:
			rows, err = q.ListDecisions(ctx, pgUUID(tenantID))
		}
		if err != nil {
			return fmt.Errorf("list decisions: %w", err)
		}
		// The revisit-window query does not also filter on status, so when
		// both filters are set the status cut is applied here. statusCut is
		// false for the other two branches (the query already scoped them).
		statusCut := filter.RevisitDueWithinDays > 0 && filter.Status != ""
		out = make([]Decision, 0, len(rows))
		for _, r := range rows {
			d := decisionFromRow(r)
			if statusCut && d.Status != filter.Status {
				continue
			}
			out = append(out, d)
		}
		return nil
	})
	return out, err
}

// UpdateInput is the API shape for PATCH /v1/decisions/{id}. Every field is
// a pointer; nil means "leave unchanged".
type UpdateInput struct {
	Title         *string
	Narrative     *string
	Constraints   *[]string
	Tradeoffs     *string
	DecisionMaker *string
	DecidedAt     *time.Time
	RevisitBy     *time.Time
	// ClearRevisitBy, when true, sets revisit_by to NULL (distinct from a
	// nil RevisitBy which means "leave unchanged").
	ClearRevisitBy bool
	Status         *string
	// Actor is the credential id recorded in the decisions_audit row.
	Actor string
}

// Update applies the non-nil fields of in to the decision and writes an
// `updated` audit row capturing a compact diff. AC-3.
//
// Supersession is NOT done through Update -- it has its own endpoint
// (Supersede) so the superseded_by linkage and the `superseded` audit
// action stay explicit.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Decision, error) {
	if in.Title != nil && *in.Title == "" {
		return Decision{}, ErrTitleRequired
	}
	if in.DecisionMaker != nil && *in.DecisionMaker == "" {
		return Decision{}, ErrDecisionMakerRequired
	}
	actor := in.Actor
	if actor == "" {
		actor = "unknown"
	}

	var out Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision (update): %w", err)
		}
		cur := decisionFromRow(before)

		// Merge: start from the current row, overlay the non-nil inputs.
		next := cur
		var diff []string
		if in.Title != nil && *in.Title != cur.Title {
			next.Title = *in.Title
			diff = append(diff, "title")
		}
		if in.Narrative != nil && *in.Narrative != cur.Narrative {
			next.Narrative = *in.Narrative
			diff = append(diff, "narrative")
		}
		if in.Constraints != nil {
			next.Constraints = *in.Constraints
			diff = append(diff, "constraints")
		}
		if in.Tradeoffs != nil && *in.Tradeoffs != cur.Tradeoffs {
			next.Tradeoffs = *in.Tradeoffs
			diff = append(diff, "tradeoffs")
		}
		if in.DecisionMaker != nil && *in.DecisionMaker != cur.DecisionMaker {
			next.DecisionMaker = *in.DecisionMaker
			diff = append(diff, "decision_maker")
		}
		if in.DecidedAt != nil {
			next.DecidedAt = in.DecidedAt.UTC()
			diff = append(diff, "decided_at")
		}
		switch {
		case in.ClearRevisitBy:
			next.RevisitBy = nil
			diff = append(diff, "revisit_by")
		case in.RevisitBy != nil:
			rb := in.RevisitBy.UTC()
			next.RevisitBy = &rb
			diff = append(diff, "revisit_by")
		}
		if in.Status != nil && *in.Status != cur.Status {
			// Update does not drive supersession; reject it here so the
			// dedicated endpoint stays the only path to `superseded`.
			if *in.Status == StatusSuperseded {
				return ErrWrongState
			}
			next.Status = *in.Status
			diff = append(diff, "status")
		}

		row, err := q.UpdateDecision(ctx, dbx.UpdateDecisionParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(id),
			Title:         next.Title,
			Narrative:     next.Narrative,
			Constraints:   next.Constraints,
			Tradeoffs:     next.Tradeoffs,
			DecisionMaker: next.DecisionMaker,
			DecidedAt:     pgTimestamptz(next.DecidedAt),
			RevisitBy:     pgDate(next.RevisitBy),
			Status:        dbx.DecisionStatus(next.Status),
			SupersededBy:  pgUUIDPtr(next.SupersededBy),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update decision: %w", err)
		}
		out = decisionFromRow(row)

		detail := "no-op"
		if len(diff) > 0 {
			detail = "changed: " + strings.Join(diff, ", ")
		}
		return s.writeAudit(ctx, q, tenantID, id, ActionUpdated, actor, detail)
	})
	return out, err
}

// Supersede marks the decision identified by id as `superseded`, points its
// superseded_by at supersededBy (which must already exist as an `active` or
// any-status decision in the same tenant), and writes a `superseded` audit
// row. The old decision is never deleted (P0 anti-criterion). AC-4.
func (s *Store) Supersede(ctx context.Context, id, supersededBy uuid.UUID, actor string) (Decision, error) {
	if supersededBy == uuid.Nil {
		return Decision{}, ErrSupersededByRequired
	}
	if supersededBy == id {
		return Decision{}, ErrSelfSupersede
	}
	if actor == "" {
		actor = "unknown"
	}

	var out Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// The replacement decision must resolve in this tenant. A missing
		// or cross-tenant id is a 404.
		if _, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(supersededBy),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get replacement decision: %w", err)
		}

		row, err := q.SupersedeDecision(ctx, dbx.SupersedeDecisionParams{
			TenantID:     pgUUID(tenantID),
			ID:           pgUUID(id),
			SupersededBy: pgUUID(supersededBy),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Either the decision is missing or it is not `active`.
				// Probe to disambiguate ErrNotFound from ErrWrongState.
				if _, gErr := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
					TenantID: pgUUID(tenantID),
					ID:       pgUUID(id),
				}); errors.Is(gErr, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return ErrWrongState
			}
			return fmt.Errorf("supersede decision: %w", err)
		}
		out = decisionFromRow(row)
		return s.writeAudit(ctx, q, tenantID, id, ActionSuperseded, actor,
			fmt.Sprintf("superseded_by=%s", supersededBy))
	})
	return out, err
}

// SetAuditNarrativeOptOut flips the per-decision OSCAL-narrative opt-out
// flag and writes an `updated` audit row. When opt-out is true the decision
// is excluded from OSCAL SSP narrative emission (AC-7 / P0 anti-criterion).
func (s *Store) SetAuditNarrativeOptOut(ctx context.Context, id uuid.UUID, optOut bool, actor string) (Decision, error) {
	if actor == "" {
		actor = "unknown"
	}
	var out Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.SetDecisionAuditNarrativeOptOut(ctx, dbx.SetDecisionAuditNarrativeOptOutParams{
			TenantID:             pgUUID(tenantID),
			ID:                   pgUUID(id),
			AuditNarrativeOptOut: optOut,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("set audit-narrative opt-out: %w", err)
		}
		out = decisionFromRow(row)
		return s.writeAudit(ctx, q, tenantID, id, ActionUpdated, actor,
			fmt.Sprintf("audit_narrative_opt_out=%t", optOut))
	})
	return out, err
}

// AddLink links the decision to a target entity of the given kind. The
// operation is idempotent (re-linking is a no-op). A target that does not
// resolve in the active tenant -- missing or cross-tenant -- returns
// ErrCrossTenantLink (HTTP 404, existence-leak prevention) and records a
// `cross_tenant_link_denied` audit row. AC-5 + AC-9.
//
// A failed link INSERT (FK violation) aborts the surrounding transaction,
// so the `cross_tenant_link_denied` audit row CANNOT be written in the
// same tx -- it is written in a separate, fresh transaction after the
// poisoned one rolls back. The happy path (link succeeds) writes the
// `link_added` audit row in the same tx as the INSERT, so a link without
// an audit row is impossible.
func (s *Store) AddLink(ctx context.Context, id uuid.UUID, kind LinkKind, targetID uuid.UUID, actor string) error {
	if !ValidLinkKind(string(kind)) {
		return ErrInvalidLinkKind
	}
	if actor == "" {
		actor = "unknown"
	}
	var crossTenant bool
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// The decision itself must exist in this tenant.
		if _, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision (link): %w", err)
		}

		linkErr := s.runLink(ctx, q, kind, id, targetID, tenantID)
		if linkErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(linkErr, &pgErr) && pgErr.Code == pgErrForeignKeyViolation {
				// The composite (tenant_id, target_id) FK did not resolve:
				// the target is missing or in another tenant. The INSERT
				// has aborted this transaction, so the denied-attempt audit
				// row is written in a fresh tx below. Roll back here by
				// returning the sentinel.
				crossTenant = true
				return ErrCrossTenantLink
			}
			return fmt.Errorf("add link: %w", linkErr)
		}
		return s.writeAudit(ctx, q, tenantID, id, ActionLinkAdded, actor,
			fmt.Sprintf("kind=%s target=%s", kind, targetID))
	})
	if crossTenant {
		// AC-9: record the denied attempt in a separate transaction (the
		// original one was poisoned by the FK violation). A failure to
		// write the audit row here is logged-by-return but does not mask
		// the 404 -- the caller still sees ErrCrossTenantLink.
		if aErr := s.writeAuditTx(ctx, id, ActionCrossTenantLinkDenied, actor,
			fmt.Sprintf("kind=%s target=%s", kind, targetID)); aErr != nil {
			return fmt.Errorf("%w (audit write also failed: %v)", ErrCrossTenantLink, aErr)
		}
		return ErrCrossTenantLink
	}
	return err
}

// RemoveLink removes a linkage. Idempotent: removing a link that does not
// exist is a no-op (and still records a `link_removed` audit row for the
// attempt -- the audit trail captures intent). AC-5.
func (s *Store) RemoveLink(ctx context.Context, id uuid.UUID, kind LinkKind, targetID uuid.UUID, actor string) error {
	if !ValidLinkKind(string(kind)) {
		return ErrInvalidLinkKind
	}
	if actor == "" {
		actor = "unknown"
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision (unlink): %w", err)
		}
		if err := s.runUnlink(ctx, q, kind, id, targetID, tenantID); err != nil {
			return fmt.Errorf("remove link: %w", err)
		}
		return s.writeAudit(ctx, q, tenantID, id, ActionLinkRemoved, actor,
			fmt.Sprintf("kind=%s target=%s", kind, targetID))
	})
}

// Linkage returns all four linkage arrays for a decision. ErrNotFound when
// the decision is absent.
func (s *Store) Linkage(ctx context.Context, id uuid.UUID) (Linkage, error) {
	var out Linkage
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetDecisionByID(ctx, dbx.GetDecisionByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get decision (linkage): %w", err)
		}
		lk, err := loadLinkage(ctx, q, tenantID, id)
		if err != nil {
			return err
		}
		out = lk
		return nil
	})
	return out, err
}

// Overdue returns active decisions whose revisit_by is strictly before
// `today`. AC-6 read surface for GET /v1/decisions/overdue.
func (s *Store) Overdue(ctx context.Context, today time.Time) ([]Decision, error) {
	if today.IsZero() {
		today = time.Now().UTC()
	}
	var out []Decision
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListOverdueDecisions(ctx, dbx.ListOverdueDecisionsParams{
			TenantID:  pgUUID(tenantID),
			RevisitBy: pgDateValue(today),
		})
		if err != nil {
			return fmt.Errorf("list overdue decisions: %w", err)
		}
		out = make([]Decision, len(rows))
		for i, r := range rows {
			out[i] = decisionFromRow(r)
		}
		return nil
	})
	return out, err
}

// ListAudit returns the audit trail for a single decision, oldest first.
func (s *Store) ListAudit(ctx context.Context, id uuid.UUID) ([]AuditEntry, error) {
	var out []AuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListDecisionAudit(ctx, dbx.ListDecisionAuditParams{
			TenantID:   pgUUID(tenantID),
			DecisionID: pgUUID(id),
		})
		if err != nil {
			return fmt.Errorf("list decision audit: %w", err)
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = auditFromRow(r)
		}
		return nil
	})
	return out, err
}

// ----- link plumbing -----

func (s *Store) runLink(ctx context.Context, q *dbx.Queries, kind LinkKind, decisionID, targetID, tenantID uuid.UUID) error {
	switch kind {
	case LinkRisk:
		return q.LinkDecisionRisk(ctx, dbx.LinkDecisionRiskParams{
			DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID), TenantID: pgUUID(tenantID),
		})
	case LinkControl:
		return q.LinkDecisionControl(ctx, dbx.LinkDecisionControlParams{
			DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID), TenantID: pgUUID(tenantID),
		})
	case LinkException:
		return q.LinkDecisionException(ctx, dbx.LinkDecisionExceptionParams{
			DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID), TenantID: pgUUID(tenantID),
		})
	case LinkScopePredicate:
		return q.LinkDecisionScopePredicate(ctx, dbx.LinkDecisionScopePredicateParams{
			DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID), TenantID: pgUUID(tenantID),
		})
	}
	return ErrInvalidLinkKind
}

func (s *Store) runUnlink(ctx context.Context, q *dbx.Queries, kind LinkKind, decisionID, targetID, tenantID uuid.UUID) error {
	switch kind {
	case LinkRisk:
		return q.UnlinkDecisionRisk(ctx, dbx.UnlinkDecisionRiskParams{
			TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID),
		})
	case LinkControl:
		return q.UnlinkDecisionControl(ctx, dbx.UnlinkDecisionControlParams{
			TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID),
		})
	case LinkException:
		return q.UnlinkDecisionException(ctx, dbx.UnlinkDecisionExceptionParams{
			TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID),
		})
	case LinkScopePredicate:
		return q.UnlinkDecisionScopePredicate(ctx, dbx.UnlinkDecisionScopePredicateParams{
			TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID), TargetID: pgUUID(targetID),
		})
	}
	return ErrInvalidLinkKind
}

func loadLinkage(ctx context.Context, q *dbx.Queries, tenantID, decisionID uuid.UUID) (Linkage, error) {
	var lk Linkage
	riskRows, err := q.ListDecisionRisks(ctx, dbx.ListDecisionRisksParams{
		TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list decision risks: %w", err)
	}
	for _, r := range riskRows {
		lk.Risks = append(lk.Risks, Link{Kind: LinkRisk, TargetID: uuid.UUID(r.TargetID.Bytes), CreatedAt: r.CreatedAt.Time})
	}
	ctrlRows, err := q.ListDecisionControls(ctx, dbx.ListDecisionControlsParams{
		TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list decision controls: %w", err)
	}
	for _, r := range ctrlRows {
		lk.Controls = append(lk.Controls, Link{Kind: LinkControl, TargetID: uuid.UUID(r.TargetID.Bytes), CreatedAt: r.CreatedAt.Time})
	}
	excRows, err := q.ListDecisionExceptions(ctx, dbx.ListDecisionExceptionsParams{
		TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list decision exceptions: %w", err)
	}
	for _, r := range excRows {
		lk.Exceptions = append(lk.Exceptions, Link{Kind: LinkException, TargetID: uuid.UUID(r.TargetID.Bytes), CreatedAt: r.CreatedAt.Time})
	}
	scopeRows, err := q.ListDecisionScopePredicates(ctx, dbx.ListDecisionScopePredicatesParams{
		TenantID: pgUUID(tenantID), DecisionID: pgUUID(decisionID),
	})
	if err != nil {
		return Linkage{}, fmt.Errorf("list decision scope predicates: %w", err)
	}
	for _, r := range scopeRows {
		lk.ScopePredicates = append(lk.ScopePredicates, Link{Kind: LinkScopePredicate, TargetID: uuid.UUID(r.TargetID.Bytes), CreatedAt: r.CreatedAt.Time})
	}
	return lk, nil
}

// writeAudit appends one decisions_audit row inside the caller's tx.
func (s *Store) writeAudit(ctx context.Context, q *dbx.Queries, tenantID, decisionID uuid.UUID, action, actor, detail string) error {
	if _, err := q.WriteDecisionAudit(ctx, dbx.WriteDecisionAuditParams{
		ID:         pgUUID(uuid.New()),
		TenantID:   pgUUID(tenantID),
		DecisionID: pgUUID(decisionID),
		Action:     action,
		Actor:      actor,
		Detail:     detail,
	}); err != nil {
		return fmt.Errorf("write decision audit (%s): %w", action, err)
	}
	return nil
}

// writeAuditTx appends one decisions_audit row in its own fresh
// transaction. Used for the `cross_tenant_link_denied` action, whose
// triggering FK violation poisons the surrounding transaction so the audit
// row cannot be written inline.
func (s *Store) writeAuditTx(ctx context.Context, decisionID uuid.UUID, action, actor, detail string) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return s.writeAudit(ctx, q, tenantID, decisionID, action, actor, detail)
	})
}

// ----- tenancy plumbing -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("decision: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("decision: begin tx: %w", err)
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
		return fmt.Errorf("decision: commit: %w", err)
	}
	return nil
}

// ----- row conversion -----

func decisionFromRow(r dbx.Decision) Decision {
	out := Decision{
		ID:                   uuid.UUID(r.ID.Bytes),
		TenantID:             uuid.UUID(r.TenantID.Bytes),
		DecisionID:           r.DecisionID,
		Title:                r.Title,
		Narrative:            r.Narrative,
		Constraints:          append([]string(nil), r.Constraints...),
		Tradeoffs:            r.Tradeoffs,
		DecisionMaker:        r.DecisionMaker,
		Status:               string(r.Status),
		AuditNarrativeOptOut: r.AuditNarrativeOptOut,
	}
	if r.DecidedAt.Valid {
		out.DecidedAt = r.DecidedAt.Time
	}
	if r.RevisitBy.Valid {
		t := r.RevisitBy.Time
		out.RevisitBy = &t
	}
	if r.SupersededBy.Valid {
		u := uuid.UUID(r.SupersededBy.Bytes)
		out.SupersededBy = &u
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}

func auditFromRow(r dbx.DecisionsAudit) AuditEntry {
	out := AuditEntry{
		ID:         uuid.UUID(r.ID.Bytes),
		TenantID:   uuid.UUID(r.TenantID.Bytes),
		DecisionID: uuid.UUID(r.DecisionID.Bytes),
		Action:     r.Action,
		Actor:      r.Actor,
		Detail:     r.Detail,
	}
	if r.OccurredAt.Valid {
		out.OccurredAt = r.OccurredAt.Time
	}
	return out
}

// ----- pgtype helpers -----

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgUUIDPtr(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// pgDate converts an optional time to a pgtype.Date (NULL when nil).
func pgDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t.UTC(), Valid: true}
}

// pgDateValue converts a required time to a non-NULL pgtype.Date.
func pgDateValue(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t.UTC(), Valid: true}
}
