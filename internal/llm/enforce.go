package llm

import (
	"errors"
	"strings"
)

// ErrApproverRequired is returned by EnforceApproval for the one forbidden
// AI-assist shape: an AI-assisted record marked human_approved without a
// human_approver. It is the Go-layer mirror of the DB CHECK template
// ai_assist_human_approver_guard (migrations/sql/20260607000000_ai_generations.sql).
//
// The DB CHECK is the AUTHORITATIVE gate (proven at the DB layer by AC-9);
// this helper exists so an approvable AI-assist record's write path can
// reject the bad shape early with a friendly error instead of surfacing a
// raw 23514 check_violation. Every approvable AI-assist consumer (440/441/471)
// calls this before its approval UPDATE, AND adopts the DB CHECK -- belt and
// suspenders, never one without the other.
var ErrApproverRequired = errors.New("llm: human_approved AI-assisted record requires a human_approver")

// ApprovalState is the minimal shape of an approvable AI-assist record's
// boundary columns. Consumers map their own row into this to reuse the guard.
type ApprovalState struct {
	// AIAssisted is true when the record originated from an AI-assist surface.
	AIAssisted bool
	// HumanApproved is true when an operator has approved the record.
	HumanApproved bool
	// HumanApprover is the operator id/credential that approved it. Must be
	// non-empty when both AIAssisted and HumanApproved are true.
	HumanApprover string
}

// EnforceApproval returns ErrApproverRequired iff the record is in the single
// forbidden shape (AIAssisted && HumanApproved && missing-or-blank
// HumanApprover), and nil otherwise. It is the exact predicate the DB CHECK
// ai_assist_human_approver_guard enforces, so the Go and SQL gates agree by
// construction.
//
// Mirroring the SQL `length(human_approver) > 0` hardening, a whitespace-only
// approver is treated as absent -- a confused-deputy that supplies "  "
// instead of a real id is rejected here as it would be at the DB.
func EnforceApproval(s ApprovalState) error {
	if s.AIAssisted && s.HumanApproved && strings.TrimSpace(s.HumanApprover) == "" {
		return ErrApproverRequired
	}
	return nil
}
