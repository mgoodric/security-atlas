package securityawareness

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	PersonSourceManual = "manual"
	PersonSourceHRIS   = "hris"
	PersonSourceSCIM   = "scim"

	CompletionSourceManual       = "manual"
	CompletionSourceCSV          = "csv"
	CompletionSourceLMSConnector = "lms_connector"

	PhishingNoClick             = "no_click"
	PhishingClicked             = "clicked"
	PhishingReported            = "reported"
	PhishingCredentialSubmitted = "credential_submitted"

	NotificationTypeTrainingOverdue = "security_training.overdue"
	EvidenceKindTrainingCompletion  = "security_awareness.training_completion.v1"
	EvidenceVersionTraining         = "1.0.0"
	MetricTrainingCompletionRate    = "training_completion_rate"
)

var (
	ErrInvalidInput  = errors.New("securityawareness: invalid input")
	ErrNotFound      = errors.New("securityawareness: not found")
	ErrAlreadyExists = errors.New("securityawareness: already exists")
)

type Person struct {
	ID              uuid.UUID
	Source          string
	SourcePersonID  *string
	DisplayName     string
	WorkEmail       *string
	RecipientUserID *string
	Active          *bool
}

type Course struct {
	ID          uuid.UUID
	Code        string
	Title       string
	Description string
	Required    bool
}

type Campaign struct {
	ID       uuid.UUID
	CourseID uuid.UUID
	Name     string
	StartsAt time.Time
	DueAt    time.Time
}

type Assignment struct {
	ID               uuid.UUID
	CampaignID       uuid.UUID
	PersonID         uuid.UUID
	DueAt            time.Time
	AssignedAt       time.Time
	CompletedAt      *time.Time
	CompletionSource *string
	EvidenceRecordID *uuid.UUID
}

type AssignmentDetail struct {
	Assignment
	CourseCode        string
	CourseTitle       string
	CampaignName      string
	PersonDisplayName string
	PersonWorkEmail   *string
	RecipientUserID   *string
	PhishingOutcome   *string
}

type PhishingResult struct {
	ID           uuid.UUID
	AssignmentID uuid.UUID
	SimulationID string
	SentAt       time.Time
	Outcome      string
	ClickedAt    *time.Time
	ReportedAt   *time.Time
}

type UpsertPersonInput struct {
	Source          string
	SourcePersonID  *string
	DisplayName     string
	WorkEmail       *string
	RecipientUserID *string
	Active          *bool
}

type CreateCourseInput struct {
	Code        string
	Title       string
	Description string
	Required    bool
}

type CreateCampaignInput struct {
	CourseID uuid.UUID
	Name     string
	StartsAt time.Time
	DueAt    time.Time
}

type AssignInput struct {
	CampaignID uuid.UUID
	PersonID   uuid.UUID
	DueAt      time.Time
}

type CompleteInput struct {
	AssignmentID uuid.UUID
	CompletedAt  time.Time
	Source       string
}

type PhishingInput struct {
	AssignmentID uuid.UUID
	SimulationID string
	SentAt       time.Time
	Outcome      string
	ClickedAt    *time.Time
	ReportedAt   *time.Time
}

type Rollup struct {
	CampaignID     uuid.UUID
	Assigned       int64
	Completed      int64
	Overdue        int64
	CompletionRate *float64
	OverdueList    []AssignmentDetail
	MetricID       string
}

type Reminder struct {
	AssignmentID    uuid.UUID
	PersonID        uuid.UUID
	RecipientUserID string
	CourseTitle     string
	CampaignName    string
	DueAt           time.Time
}

type CalendarEvent struct {
	ID                uuid.UUID
	Title             string
	StartsAt          time.Time
	RelatedEntityID   uuid.UUID
	RelatedEntityKind string
	Status            string
}
