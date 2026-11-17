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
//	draft -> in_progress -> blocked -> completed <-> verified
//
// with no edge back to `draft` (a new plan is the way to restart). The only
// way out of `verified` is the system's: a linked control failing its latest
// evaluation returns the plan to `completed` (#2109). The allowed edges are enumerated in AllowedTransition;
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

// SystemActor names the system in an audit row it writes: the
// completed <-> verified moves made from control evaluations (#2109). It
// must match the action_plan_audit_log_system_actor_known CHECK.
const SystemActor = "system:action-plan-verification"

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
//	verified    -> completed                 (system only, #2109)
//
// No edge BACK to draft from any state (AC-15: "* -> draft" rejected except
// creation). verified -> completed is the system's move, made by
// action_plans_reconcile_verification when a linked control fails; Update
// refuses it for a person and the DB trigger refuses it outside that
// function. Unknown statuses are never allowed.
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
		return to == StatusCompleted // system only (#2109)
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
	// ErrOwnerNotInTenant — owner_id does not resolve to an active tenant user. 400.
	ErrOwnerNotInTenant = errors.New("actionplan: owner_id is not an active user in this tenant")
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
	// ErrVerifyThroughVerify — a PATCH asked for status verified. A person
	// verifies through POST .../verify (#2108). 422.
	ErrVerifyThroughVerify = errors.New("actionplan: verify a plan through its verify action, not by setting its status")
	// ErrVerifyHasControls — verify on a plan with linked controls; their
	// evaluations verify it (#2109). 409.
	ErrVerifyHasControls = errors.New("actionplan: a plan with linked controls is verified by their evaluations, not by hand")
	// ErrOwnerCannotVerify — the plan's owner tried to verify it. The owner
	// never verifies their own plan, whatever the self-approval setting
	// (#2108). 403.
	ErrOwnerCannotVerify = errors.New("actionplan: the plan's owner cannot verify it")
	// ErrVerifierNotPerson — the verifying credential belongs to no user. 403.
	ErrVerifierNotPerson = errors.New("actionplan: only a person can verify a plan")
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
