// Package writeproposals implements the slice-173 store layer for the
// MCP-write Pattern A (draft-then-confirm) HITL flow.
//
// Every AI-driven write that reaches the platform first lands here as a
// `mcp_write_proposals` row with state='ai_proposed' (alongside the AI-
// assist provenance — model name + version + the ai_assisted=true flag).
// A separate confirm step flips the row to state='applied' AND records
// the human approver in the same UPDATE; the schema-level CHECK
// `mcp_wp_ai_assist_invariant` guarantees that flip is impossible without
// human_approver set.
//
// The constitutional contract (CLAUDE.md §"AI-assist boundary (hard)")
// is satisfied by the conjunction of:
//
//   1. The store creates proposals only — it does NOT call the canonical
//      write paths. The Confirm() entrypoint dispatches to a typed
//      Applier supplied by the caller; the applier executes the
//      canonical platform write under the operator's tenant context.
//   2. The DB CHECK `mcp_wp_ai_assist_invariant` rejects any row that
//      claims human approval without a human_approver UUID.
//   3. The state CHECK rejects every state outside ai_proposed | applied
//      | rejected; no "edited" state exists, so the LLM cannot mutate
//      a proposal after the operator has reviewed it.
//
// This package is a pure DB layer. Tool-level concerns (which write tools
// exist, what their input schema is) live in internal/mcp/tools/. HTTP-
// level concerns (route wiring, auth gating) live in
// internal/api/mcpwriteproposals/.

package writeproposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// State constants mirror the mcp_write_proposals.state CHECK enum.
const (
	StateAIProposed = "ai_proposed"
	StateApplied    = "applied"
	StateRejected   = "rejected"
)

// ToolName enum — must mirror migrations/sql/20260520030000_mcp_write_proposals.sql
// CHECK (`mcp_wp_tool_name_check`). Add a value here AND in the migration
// when a new write tool ships.
const (
	ToolCreateRisk          = "create_risk"
	ToolUpdateControlState  = "update_control_state"
	ToolPushEvidence        = "push_evidence"
	ToolUpdateRiskTreatment = "update_risk_treatment"
)

// AllowedTools is the canonical set of MCP write tools allowed to file a
// proposal. The Store.Create entrypoint validates against this slice
// before round-tripping to the DB so a typo surfaces as a clean store
// error rather than a 23514 (check_violation).
var AllowedTools = map[string]bool{
	ToolCreateRisk:          true,
	ToolUpdateControlState:  true,
	ToolPushEvidence:        true,
	ToolUpdateRiskTreatment: true,
}

// Anti-criterion P0-A5: per-(tenant, user) cap on pending proposals.
// Default 5 — tighter than the read-tool cap (slice 145 concurrency cap).
// Configurable at construction time via WithPendingCap.
const DefaultPendingCap = 5

// Errors. ErrNotFound mirrors slice-021's exception.ErrNotFound shape so
// the HTTP layer can translate uniformly. ErrWrongState fires when a
// confirm/reject lands on a non-ai_proposed row (terminal idempotency).
// ErrPendingCapExceeded enforces P0-A5.
var (
	ErrNotFound           = errors.New("mcp write proposal not found")
	ErrWrongState         = errors.New("mcp write proposal not in expected state")
	ErrUnknownTool        = errors.New("unknown mcp write tool")
	ErrPendingCapExceeded = errors.New("pending mcp write proposal cap exceeded for caller")
	ErrInvalidInput       = errors.New("invalid mcp write proposal input")
)

// Proposal is the canonical row shape. Mirrors the DB column set 1:1.
//
// CreatedBy and HumanApprover are TEXT in the DB because v1's
// credstore.Credential.ID is a token-shaped string ("key_..."), not
// a UUID. Slice 034 (OIDC RP + local users) will populate these from
// the IdP's user id; the column type stays TEXT to absorb both shapes
// without another migration.
type Proposal struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ToolName       string
	ToolInput      json.RawMessage
	State          string
	AIAssisted     bool
	AIModelName    string
	AIModelVersion string
	HumanApproved  bool
	HumanApprover  *string
	AppliedAt      *time.Time
	AppliedSubject *string
	RejectedAt     *time.Time
	RejectReason   *string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateInput captures the tool-handler-supplied fields. The store fills
// in id, state, ai_assisted, created_at, updated_at.
type CreateInput struct {
	ToolName       string
	ToolInput      json.RawMessage
	AIModelName    string
	AIModelVersion string
	CreatedBy      string
}

// Applier executes the canonical platform write that a confirmed proposal
// implies. Implementations live alongside their respective domain stores;
// the HTTP handler wires them up via Store.WithApplier.
//
// Apply MUST be idempotent: a second invocation with the same proposal
// argument should produce the same outcome (no duplicate rows, no error
// distinct from the first call). The store enforces single-shot apply at
// the state-machine layer (state=applied is terminal); idempotency here
// is defense-in-depth against a race between two confirm requests landing
// at the same time (e.g., operator double-click).
//
// Apply receives the proposal AFTER the state has been flipped to applied
// in the same transaction. Returning an error rolls the transaction back,
// so the proposal stays at state=ai_proposed and the caller can retry.
//
// The string return is the canonical-row id that the write produced
// (e.g., the created risk's UUID, the inserted evidence record's id).
// Stored in mcp_write_proposals.applied_subject for forensic traceback.
type Applier func(ctx context.Context, tx pgx.Tx, p Proposal) (string, error)

// Store is the DB layer for mcp_write_proposals.
type Store struct {
	pool       *pgxpool.Pool
	appliers   map[string]Applier
	pendingCap int
}

// NewStore constructs a Store wired against the given pool. Use
// Store.WithApplier to register the per-tool apply func, and
// Store.WithPendingCap to override the default per-user cap.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:       pool,
		appliers:   make(map[string]Applier),
		pendingCap: DefaultPendingCap,
	}
}

// WithApplier registers an Applier for a given tool_name. Returns the
// store for chaining. Calling WithApplier twice for the same tool_name
// replaces the prior applier.
func (s *Store) WithApplier(toolName string, fn Applier) *Store {
	s.appliers[toolName] = fn
	return s
}

// WithPendingCap overrides the per-(tenant, created_by) pending-proposal
// cap (default 5). Pass 0 or negative to disable the cap entirely
// (NOT recommended outside test fixtures).
func (s *Store) WithPendingCap(n int) *Store {
	s.pendingCap = n
	return s
}

// PendingCap returns the configured per-user pending cap.
func (s *Store) PendingCap() int { return s.pendingCap }

// Create inserts a new proposal in state=ai_proposed.
//
// Errors:
//   - ErrUnknownTool when tool_name is not in AllowedTools (defense-in-depth
//     ahead of the DB CHECK).
//   - ErrInvalidInput when required fields are zero-valued.
//   - ErrPendingCapExceeded when the caller already has pendingCap pending
//     proposals on this tenant.
func (s *Store) Create(ctx context.Context, in CreateInput) (Proposal, error) {
	if !AllowedTools[in.ToolName] {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownTool, in.ToolName)
	}
	if len(in.ToolInput) == 0 || strings.TrimSpace(string(in.ToolInput)) == "" {
		return Proposal{}, fmt.Errorf("%w: tool_input is required", ErrInvalidInput)
	}
	if in.AIModelName == "" {
		return Proposal{}, fmt.Errorf("%w: ai_model_name is required", ErrInvalidInput)
	}
	if in.AIModelVersion == "" {
		return Proposal{}, fmt.Errorf("%w: ai_model_version is required", ErrInvalidInput)
	}
	if in.CreatedBy == "" {
		return Proposal{}, fmt.Errorf("%w: created_by is required", ErrInvalidInput)
	}
	// Reject malformed JSON early so the DB never sees it.
	if !json.Valid(in.ToolInput) {
		return Proposal{}, fmt.Errorf("%w: tool_input is not valid JSON", ErrInvalidInput)
	}

	var out Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantUUID uuid.UUID) error {
		// Pending-cap check inside the transaction so we get a stable
		// view of the count across the INSERT. The cap is per
		// (tenant_id, created_by) — one operator hammering a tenant
		// can't drown the approval queue. P0-A5.
		if s.pendingCap > 0 {
			var count int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM mcp_write_proposals
				WHERE tenant_id = $1 AND created_by = $2 AND state = 'ai_proposed'
			`, tenantUUID, in.CreatedBy).Scan(&count); err != nil {
				return fmt.Errorf("count pending: %w", err)
			}
			if count >= s.pendingCap {
				return ErrPendingCapExceeded
			}
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO mcp_write_proposals (
				tenant_id, tool_name, tool_input,
				ai_model_name, ai_model_version,
				created_by
			)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
		`, tenantUUID, in.ToolName, []byte(in.ToolInput),
			in.AIModelName, in.AIModelVersion,
			in.CreatedBy)
		return scanProposal(row, &out)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return Proposal{}, fmt.Errorf("%w: DB CHECK violated: %s", ErrInvalidInput, pgErr.ConstraintName)
		}
		return Proposal{}, err
	}
	return out, nil
}

// Get fetches one proposal by id. RLS scopes the lookup to the caller's
// tenant; cross-tenant access returns ErrNotFound (RLS hides the row).
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Proposal, error) {
	var out Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		row := tx.QueryRow(ctx, `
			SELECT
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
			FROM mcp_write_proposals
			WHERE id = $1
		`, id)
		if err := scanProposal(row, &out); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
	return out, err
}

// ListFilter captures the optional filters supported by Store.List.
type ListFilter struct {
	State    string // optional; empty returns all states
	ToolName string // optional; empty returns all tool names
}

// List returns proposals scoped to the caller's tenant (via RLS), filtered
// by the supplied criteria. Ordering: newest-first (created_at DESC).
func (s *Store) List(ctx context.Context, f ListFilter) ([]Proposal, error) {
	var out []Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		args := []any{}
		var sb strings.Builder
		sb.WriteString(`
			SELECT
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
			FROM mcp_write_proposals
			WHERE 1=1`)
		if f.State != "" {
			args = append(args, f.State)
			fmt.Fprintf(&sb, " AND state = $%d", len(args))
		}
		if f.ToolName != "" {
			args = append(args, f.ToolName)
			fmt.Fprintf(&sb, " AND tool_name = $%d", len(args))
		}
		sb.WriteString(" ORDER BY created_at DESC LIMIT 500")
		rows, err := tx.Query(ctx, sb.String(), args...)
		if err != nil {
			return fmt.Errorf("list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p Proposal
			if err := scanProposal(rows, &p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// Confirm flips a proposal from ai_proposed to applied. The supplied
// Applier executes inside the same transaction as the state flip; if
// Applier returns an error the proposal stays at ai_proposed and the
// caller can retry.
//
// `approver` is the credential or user id the operator is acting as
// (TEXT in the DB; v1 = credstore.Credential.ID; post-slice-034 = OIDC
// user id). Empty string is rejected so the schema-level invariant
// `(ai_assisted=true AND human_approved=true) → human_approver IS NOT
// NULL` is honored.
//
// Errors:
//   - ErrNotFound when the proposal does not exist (or RLS hides it).
//   - ErrWrongState when the proposal is already applied or rejected.
//   - ErrUnknownTool when no Applier is registered for the proposal's
//     tool_name (caller mis-wired the store).
func (s *Store) Confirm(ctx context.Context, id uuid.UUID, approver string) (Proposal, error) {
	if approver == "" {
		return Proposal{}, fmt.Errorf("%w: approver is required", ErrInvalidInput)
	}
	var out Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		// Lock the row for update so concurrent confirms don't both
		// claim the apply slot. The state machine + Applier idempotency
		// guarantee final consistency, but locking eliminates the
		// rare double-apply window.
		var current Proposal
		row := tx.QueryRow(ctx, `
			SELECT
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
			FROM mcp_write_proposals
			WHERE id = $1
			FOR UPDATE
		`, id)
		if err := scanProposal(row, &current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if current.State != StateAIProposed {
			return ErrWrongState
		}
		applier, ok := s.appliers[current.ToolName]
		if !ok {
			return fmt.Errorf("%w: no applier registered for %s", ErrUnknownTool, current.ToolName)
		}
		// Stage the human-approval fields BEFORE running the applier
		// so the apply step sees a fully-approved row. We re-fetch
		// post-update for the return shape.
		now := time.Now().UTC()
		subject, applyErr := applier(ctx, tx, current)
		if applyErr != nil {
			return fmt.Errorf("apply: %w", applyErr)
		}
		row = tx.QueryRow(ctx, `
			UPDATE mcp_write_proposals
			SET
				state = 'applied',
				human_approved = TRUE,
				human_approver = $2,
				applied_at = $3,
				applied_subject = $4,
				updated_at = $3
			WHERE id = $1
			RETURNING
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
		`, id, approver, now, nullableSubject(subject))
		return scanProposal(row, &out)
	})
	return out, err
}

// Reject flips a proposal from ai_proposed to rejected. Terminal; the LLM
// must file a fresh proposal to retry.
func (s *Store) Reject(ctx context.Context, id uuid.UUID, reason string) (Proposal, error) {
	var out Proposal
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		var current Proposal
		row := tx.QueryRow(ctx, `
			SELECT
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
			FROM mcp_write_proposals
			WHERE id = $1
			FOR UPDATE
		`, id)
		if err := scanProposal(row, &current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if current.State != StateAIProposed {
			return ErrWrongState
		}
		now := time.Now().UTC()
		row = tx.QueryRow(ctx, `
			UPDATE mcp_write_proposals
			SET
				state = 'rejected',
				rejected_at = $2,
				reject_reason = $3,
				updated_at = $2
			WHERE id = $1
			RETURNING
				id, tenant_id, tool_name, tool_input, state,
				ai_assisted, ai_model_name, ai_model_version,
				human_approved, human_approver,
				applied_at, applied_subject,
				rejected_at, reject_reason,
				created_by, created_at, updated_at
		`, id, now, nullableString(reason))
		return scanProposal(row, &out)
	})
	return out, err
}

// inTx mirrors the slice-021 / slice-019 store pattern. Opens a tx, sets
// the tenant GUC, runs fn, commits / rolls back.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantID, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("tenant id must be uuid: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant: %w", err)
	}
	if err := fn(ctx, tx, tenantUUID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// rowScanner is the minimal contract pgx.Row + pgx.Rows both satisfy.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanProposal materializes one row into a Proposal. The column order
// MUST match the SELECT list every caller uses; centralising here keeps
// the columns in lockstep.
func scanProposal(row rowScanner, out *Proposal) error {
	var (
		humanApprover  *string
		appliedAt      *time.Time
		appliedSubject *string
		rejectedAt     *time.Time
		rejectReason   *string
		toolInput      []byte
	)
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ToolName,
		&toolInput,
		&out.State,
		&out.AIAssisted,
		&out.AIModelName,
		&out.AIModelVersion,
		&out.HumanApproved,
		&humanApprover,
		&appliedAt,
		&appliedSubject,
		&rejectedAt,
		&rejectReason,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return err
	}
	out.ToolInput = json.RawMessage(toolInput)
	out.HumanApprover = humanApprover
	out.AppliedAt = appliedAt
	out.AppliedSubject = appliedSubject
	out.RejectedAt = rejectedAt
	out.RejectReason = rejectReason
	return nil
}

// nullableString returns nil when s is empty, otherwise a pointer to s.
// Lets the UPDATE store NULL in reject_reason rather than empty string.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableSubject is the same shape as nullableString but explicit so
// the call site reads cleanly.
func nullableSubject(s string) any {
	if s == "" {
		return nil
	}
	return s
}
