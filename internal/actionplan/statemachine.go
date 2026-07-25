// Package actionplan implements the slice-384 ActionPlan primitive: a
// forward-looking commitment to close a gap, with owner + due date + M2M
// linkage to the risks and controls the gap touches.
//
// It is the fourth first-class risk-register primitive, distinct from:
//
//   - Exception (internal/exception): a backward-looking, time-bounded
//     waiver of a control's normal evaluation.
//   - DecisionLog (internal/decision): an operational / architectural
//     tradeoff record with rationale.
//
// The ActionPlan lifecycle is its own state machine:
//
//	draft -> in_progress -> blocked -> completed -> verified
//
// with `verified` terminal and no edge back to `draft` (a new plan is the
// way to restart). The allowed edges are enumerated in AllowedTransition;
// they are validated at the handler/store layer AND by the DB transition
// trigger (defense in depth, AC-13/AC-15).
//
// This file holds the PURE-GO core (state machine + input validators) so it
// is unit-testable without Postgres (slice-353 Q-2 pure-Go-first
// convention). The Store (store.go) carries the DB/tenancy plumbing.
package actionplan

import (
	"errors"
	"time"
)

// Status constants mirror the action_plans.status CHECK enum.
const (
	StatusDraft      = "draft"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusCompleted  = "completed"
	StatusVerified   = "verified"
)

// Audit-log action_type vocabulary — must match the
// action_plan_audit_log_action_type CHECK enum.
const (
	ActionCreated         = "created"
	ActionUpdated         = "updated"
	ActionStatusChanged   = "status_changed"
	ActionRiskLinked      = "risk_linked"
	ActionRiskUnlinked    = "risk_unlinked"
	ActionControlLinked   = "control_linked"
	ActionControlUnlinked = "control_unlinked"
	ActionTombstoned      = "tombstoned"
)

// Field length bounds (threat-model T) — mirror the DB CHECK constraints.
const (
	MaxTitleLen           = 200
	MaxDescriptionLen     = 4000
	MaxTriggeringEventLen = 500
)

// MaxDueDateHorizon caps how far in the future a due_date may be set
// (P0-384-8: due_date <= now + 5 years). 5 years; leap years rounded by the
// caller via AddDate which is calendar-correct.
const MaxDueDateYears = 5

// Per-plan linkage caps (P0-384-7). Separate caps for risks and controls.
const (
	MaxLinkedRisks    = 50
	MaxLinkedControls = 50
)

// Pagination bounds (AC-11).
const (
	DefaultPageLimit = 25
	MaxPageLimit     = 100
)

// ValidStatus reports whether s is one of the five lifecycle states.
func ValidStatus(s string) bool {
	switch s {
	case StatusDraft, StatusInProgress, StatusBlocked, StatusCompleted, StatusVerified:
		return true
	}
	return false
}

// AllowedTransition reports whether moving an action plan from `from` to
// `to` is permitted by the state machine. A same-status move (from == to)
// is allowed — it represents a non-status field edit. The allowed edges:
//
//	draft       -> in_progress
//	in_progress -> blocked | completed
//	blocked     -> in_progress | completed
//	completed   -> verified | in_progress    (reopen if verification fails)
//	verified    -> (terminal)
//
// No edge BACK to draft from any state (AC-15: "* -> draft" rejected except
// creation). `verified` has no outbound edge (terminal). Unknown statuses
// are never allowed.
func AllowedTransition(from, to string) bool {
	if !ValidStatus(from) || !ValidStatus(to) {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StatusDraft:
		return to == StatusInProgress
	case StatusInProgress:
		return to == StatusBlocked || to == StatusCompleted
	case StatusBlocked:
		return to == StatusInProgress || to == StatusCompleted
	case StatusCompleted:
		return to == StatusVerified || to == StatusInProgress
	case StatusVerified:
		return false // terminal
	}
	return false
}

// Errors surfaced by validators + Store. Handlers map these to HTTP status.
var (
	// ErrNotFound — id does not resolve under the active tenant (or is
	// tombstoned). RLS-friendly: same shape as "in another tenant". 404.
	ErrNotFound = errors.New("actionplan: not found")
	// ErrIllegalTransition — a status edge the state machine forbids. 422.
	ErrIllegalTransition = errors.New("actionplan: illegal status transition")
	// ErrTitleRequired — create/update title is empty. 400.
	ErrTitleRequired = errors.New("actionplan: title is required")
	// ErrTitleTooLong — title exceeds 200 chars. 400.
	ErrTitleTooLong = errors.New("actionplan: title exceeds 200 characters")
	// ErrDescriptionTooLong — description exceeds 4000 chars. 400.
	ErrDescriptionTooLong = errors.New("actionplan: description exceeds 4000 characters")
	// ErrTriggeringEventTooLong — triggering_event exceeds 500 chars. 400.
	ErrTriggeringEventTooLong = errors.New("actionplan: triggering_event exceeds 500 characters")
	// ErrOwnerRequired — create owner_id is empty. 400.
	ErrOwnerRequired = errors.New("actionplan: owner_id is required")
	// ErrOwnerNotInTenant — owner_id does not resolve to a tenant user. 400.
	ErrOwnerNotInTenant = errors.New("actionplan: owner_id is not a user in this tenant")
	// ErrDueDateTooFar — due_date is more than 5 years out (P0-384-8). 400.
	ErrDueDateTooFar = errors.New("actionplan: due_date exceeds 5-year horizon")
	// ErrInvalidStatus — a status value outside the five-state enum. 400.
	ErrInvalidStatus = errors.New("actionplan: invalid status")
	// ErrLinkTargetNotFound — a link target (risk/control) does not resolve
	// in the caller's tenant (cross-tenant or absent). 404 (existence-leak
	// prevention, P0-384-4).
	ErrLinkTargetNotFound = errors.New("actionplan: link target not found in tenant")
	// ErrAlreadyLinked — the link already exists. 409.
	ErrAlreadyLinked = errors.New("actionplan: already linked")
	// ErrNotLinked — an unlink targets a non-existent link. 404.
	ErrNotLinked = errors.New("actionplan: link does not exist")
	// ErrLimitExceeded — the per-plan 50-risk / 50-control cap is hit
	// (P0-384-7). 422 with code limit_exceeded.
	ErrLimitExceeded = errors.New("actionplan: linkage limit exceeded")
)

// ValidateTitle enforces the title length bounds (AC-1 / threat-model T).
func ValidateTitle(title string) error {
	if title == "" {
		return ErrTitleRequired
	}
	if len(title) > MaxTitleLen {
		return ErrTitleTooLong
	}
	return nil
}

// ValidateDescription enforces the description length bound. Empty is OK
// (description is nullable).
func ValidateDescription(desc string) error {
	if len(desc) > MaxDescriptionLen {
		return ErrDescriptionTooLong
	}
	return nil
}

// ValidateTriggeringEvent enforces the triggering_event length bound. Empty
// is OK (nullable, free-text per P0-384-11).
func ValidateTriggeringEvent(ev string) error {
	if len(ev) > MaxTriggeringEventLen {
		return ErrTriggeringEventTooLong
	}
	return nil
}

// ValidateDueDate enforces the 5-year horizon (P0-384-8). A zero/nil
// due_date (no deadline) is allowed; the caller passes nil. now is the
// request-time clock used to compute the horizon (injectable for tests).
func ValidateDueDate(due *time.Time, now time.Time) error {
	if due == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	horizon := now.AddDate(MaxDueDateYears, 0, 0)
	if due.After(horizon) {
		return ErrDueDateTooFar
	}
	return nil
}

// ClampLimit normalizes a requested page limit into [1, MaxPageLimit],
// defaulting to DefaultPageLimit when the request omits it (limit <= 0).
func ClampLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}
