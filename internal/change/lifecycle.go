// Package change implements a lightweight operational change-management
// register for SOC 2 CC8 evidence.
package change

import (
	"errors"
	"strings"
)

const (
	StatusProposed    = "proposed"
	StatusApproved    = "approved"
	StatusImplemented = "implemented"
	StatusVerified    = "verified"
)

const (
	SourceManual = "manual"
	SourceJira   = "jira"
	SourceCSV    = "csv"
)

const (
	ActionCreated       = "created"
	ActionApproved      = "approved"
	ActionImplemented   = "implemented"
	ActionVerified      = "verified"
	ActionControlLinked = "control_linked"
)

const (
	EvidenceKindApproval     = "change.approval.v1"
	EvidenceKindVerification = "change.verification.v1"
)

const (
	MaxTitleLen         = 200
	MaxDescriptionLen   = 4000
	MaxSourceRefLen     = 200
	MaxSourceURLLen     = 1000
	MaxRiskNotesLen     = 4000
	MaxRollbackNotesLen = 4000
	DefaultPageLimit    = 25
	MaxPageLimit        = 100
)

var (
	ErrNotFound             = errors.New("change: not found")
	ErrWrongState           = errors.New("change: not in expected state")
	ErrTitleRequired        = errors.New("change: title is required")
	ErrTitleTooLong         = errors.New("change: title exceeds 200 characters")
	ErrDescriptionTooLong   = errors.New("change: description exceeds 4000 characters")
	ErrSourceInvalid        = errors.New("change: invalid source")
	ErrSourceRefTooLong     = errors.New("change: source_ref exceeds 200 characters")
	ErrSourceURLTooLong     = errors.New("change: source_url exceeds 1000 characters")
	ErrRiskNotesTooLong     = errors.New("change: risk_notes exceeds 4000 characters")
	ErrRollbackNotesTooLong = errors.New("change: rollback_notes exceeds 4000 characters")
	ErrActorRequired        = errors.New("change: actor is required")
	ErrApproverRequired     = errors.New("change: approver is required")
	ErrUserNotInTenant      = errors.New("change: user is not in this tenant")
	ErrControlRequired      = errors.New("change: at least one affected control is required")
	ErrControlNotInTenant   = errors.New("change: affected control is not in this tenant")
	ErrNoAffectedControls   = errors.New("change: no affected controls linked")
	ErrAlreadyImported      = errors.New("change: source record already imported")
	ErrCSVHeaderInvalid     = errors.New("change: csv header must include title and control_id")
)

func ValidStatus(s string) bool {
	switch s {
	case StatusProposed, StatusApproved, StatusImplemented, StatusVerified:
		return true
	}
	return false
}

func AllowedTransition(from, to string) bool {
	if !ValidStatus(from) || !ValidStatus(to) {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StatusProposed:
		return to == StatusApproved
	case StatusApproved:
		return to == StatusImplemented
	case StatusImplemented:
		return to == StatusVerified
	}
	return false
}

func validateCreate(in CreateInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return ErrTitleRequired
	}
	if len(in.Title) > MaxTitleLen {
		return ErrTitleTooLong
	}
	if len(in.Description) > MaxDescriptionLen {
		return ErrDescriptionTooLong
	}
	if in.Source == "" {
		in.Source = SourceManual
	}
	switch in.Source {
	case SourceManual, SourceJira, SourceCSV:
	default:
		return ErrSourceInvalid
	}
	if len(in.SourceRef) > MaxSourceRefLen {
		return ErrSourceRefTooLong
	}
	if len(in.SourceURL) > MaxSourceURLLen {
		return ErrSourceURLTooLong
	}
	if len(in.RiskNotes) > MaxRiskNotesLen {
		return ErrRiskNotesTooLong
	}
	if len(in.RollbackNotes) > MaxRollbackNotesLen {
		return ErrRollbackNotesTooLong
	}
	if in.ProposedBy == nilUUID {
		return ErrActorRequired
	}
	if len(in.AffectedControlIDs) == 0 {
		return ErrControlRequired
	}
	return nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}
