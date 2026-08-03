package personnelsecurity

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

const (
	EvidenceKind      = "personnel_security.workflow.v1"
	SchemaVersion     = "1.0.0"
	DefaultControlRef = "soc2:cc1/access-controls"

	// NotificationTypeOffboardingOverdue is the notifications.type value the
	// overdue-offboarding surfacing emits. The dedup probe in
	// SurfaceOverdueOffboarding keys on this same string (it is baked into the
	// CountPersonnelOverdueNotificationsForChecklist query).
	NotificationTypeOffboardingOverdue = "personnel_security.offboarding_overdue"
)

type WorkflowKind string

const (
	WorkflowOnboarding  WorkflowKind = "onboarding"
	WorkflowOffboarding WorkflowKind = "offboarding"
)

type EventSource string

const (
	SourceManual   EventSource = "manual"
	SourceRippling EventSource = "rippling"
	SourceBambooHR EventSource = "bamboohr"
	SourceSCIM     EventSource = "scim"
)

type Checklist struct {
	ID                uuid.UUID
	Kind              WorkflowKind
	Source            EventSource
	SourceEventID     string
	PersonExternalID  string
	PersonWorkEmail   string
	PersonDisplayName string
	ControlID         uuid.UUID
	DueAt             time.Time
	Status            string
	Items             []Item
}

type Item struct {
	ID               uuid.UUID
	ChecklistID      uuid.UUID
	Slug             string
	Title            string
	Category         string
	SortOrder        int32
	CompletedAt      time.Time
	CompletedBy      string
	EvidenceRecordID uuid.UUID
	EvidenceURI      string
	Notes            string
}

type ManualInput struct {
	Kind              WorkflowKind
	PersonExternalID  string
	PersonWorkEmail   string
	PersonDisplayName string
	ControlID         uuid.UUID
	DueAt             time.Time
	CreatedBy         string
}

type Completion struct {
	ItemID      uuid.UUID
	CompletedBy string
	EvidenceURI string
	Notes       string
	ObservedAt  time.Time
}

var (
	ErrInvalidWorkflow     = errors.New("personnel security: invalid workflow")
	ErrInvalidSource       = errors.New("personnel security: invalid source")
	ErrPersonRequired      = errors.New("personnel security: person_external_id is required")
	ErrCompleterRequired   = errors.New("personnel security: completed_by is required")
	ErrNoChecklistForEvent = errors.New("personnel security: worker event is not a joiner or leaver")
	ErrItemNotFound        = errors.New("personnel security: checklist item not found")
)

func workflowFromWorker(w worker.Worker) (WorkflowKind, bool) {
	switch w.Status {
	case worker.StatusPending, worker.StatusActive:
		return WorkflowOnboarding, true
	case worker.StatusTerminated:
		return WorkflowOffboarding, true
	default:
		return "", false
	}
}

func sourceFromHRIS(h worker.HRIS) (EventSource, bool) {
	switch h {
	case worker.HRISRippling:
		return SourceRippling, true
	case worker.HRISBambooHR:
		return SourceBambooHR, true
	default:
		return "", false
	}
}

func defaultDueAt(kind WorkflowKind, eventAt, now time.Time) time.Time {
	if eventAt.IsZero() {
		eventAt = now
	}
	eventAt = eventAt.UTC()
	switch kind {
	case WorkflowOffboarding:
		return eventAt.Add(24 * time.Hour)
	default:
		return eventAt.Add(7 * 24 * time.Hour)
	}
}

func itemTemplates(kind WorkflowKind) []Item {
	switch kind {
	case WorkflowOnboarding:
		return []Item{
			{Slug: "manager_approval", Title: "Manager approves required access", Category: "access_provisioning"},
			{Slug: "provision_access", Title: "Grant approved application and infrastructure access", Category: "access_provisioning"},
			{Slug: "attach_access_grant_evidence", Title: "Attach evidence of granted access", Category: "evidence"},
		}
	case WorkflowOffboarding:
		return []Item{
			{Slug: "confirm_departure", Title: "Confirm departure date and systems in scope", Category: "offboarding"},
			{Slug: "revoke_access", Title: "Revoke application and infrastructure access", Category: "access_removal"},
			{Slug: "disable_sessions", Title: "Disable active sessions and shared credentials", Category: "access_removal"},
			{Slug: "attach_revocation_evidence", Title: "Attach evidence of access revocation", Category: "evidence"},
		}
	default:
		return nil
	}
}

func normalizeManualInput(in ManualInput, now time.Time) (ManualInput, error) {
	in.PersonExternalID = strings.TrimSpace(in.PersonExternalID)
	in.PersonWorkEmail = strings.TrimSpace(in.PersonWorkEmail)
	in.PersonDisplayName = strings.TrimSpace(in.PersonDisplayName)
	in.CreatedBy = strings.TrimSpace(in.CreatedBy)
	switch in.Kind {
	case WorkflowOnboarding, WorkflowOffboarding:
	default:
		return ManualInput{}, ErrInvalidWorkflow
	}
	if in.PersonExternalID == "" {
		return ManualInput{}, ErrPersonRequired
	}
	if in.DueAt.IsZero() {
		in.DueAt = defaultDueAt(in.Kind, now, now)
	}
	return in, nil
}
