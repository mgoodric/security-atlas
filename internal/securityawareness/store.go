package securityawareness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const pgErrUniqueViolation = "23505"

type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) WithClock(fn func() time.Time) *Store {
	s.clock = fn
	return s
}

func (s *Store) UpsertPerson(ctx context.Context, in UpsertPersonInput) (Person, error) {
	if in.Source == "" {
		in.Source = PersonSourceManual
	}
	if err := validatePerson(in); err != nil {
		return Person{}, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	var out Person
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		id := uuid.New()
		row := tx.QueryRow(ctx, `
INSERT INTO security_training_people
    (id, tenant_id, source, source_person_id, display_name, work_email, recipient_user_id, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, source, source_person_id) WHERE source_person_id IS NOT NULL
DO UPDATE SET
    display_name = EXCLUDED.display_name,
    work_email = EXCLUDED.work_email,
    recipient_user_id = EXCLUDED.recipient_user_id,
    active = EXCLUDED.active,
    updated_at = now()
RETURNING id, source, source_person_id, display_name, work_email, recipient_user_id, active`,
			id, tenantID, in.Source, optTrim(in.SourcePersonID), strings.TrimSpace(in.DisplayName), optLower(in.WorkEmail), optTrim(in.RecipientUserID), active)
		return scanPerson(row, &out)
	})
	return out, translateErr(err)
}

func (s *Store) CreateCourse(ctx context.Context, in CreateCourseInput) (Course, error) {
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Title) == "" {
		return Course{}, ErrInvalidInput
	}
	var out Course
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		id := uuid.New()
		row := tx.QueryRow(ctx, `
INSERT INTO security_training_courses (id, tenant_id, code, title, description, required)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, code, title, description, required`,
			id, tenantID, strings.TrimSpace(in.Code), strings.TrimSpace(in.Title), in.Description, in.Required)
		return row.Scan(&out.ID, &out.Code, &out.Title, &out.Description, &out.Required)
	})
	return out, translateErr(err)
}

func (s *Store) CreateCampaign(ctx context.Context, in CreateCampaignInput) (Campaign, error) {
	if in.CourseID == uuid.Nil || strings.TrimSpace(in.Name) == "" || in.DueAt.IsZero() {
		return Campaign{}, ErrInvalidInput
	}
	starts := in.StartsAt
	if starts.IsZero() {
		starts = s.clock()
	}
	if in.DueAt.Before(starts) {
		return Campaign{}, ErrInvalidInput
	}
	var out Campaign
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		id := uuid.New()
		row := tx.QueryRow(ctx, `
INSERT INTO security_training_campaigns (id, tenant_id, course_id, name, starts_at, due_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, course_id, name, starts_at, due_at`,
			id, tenantID, in.CourseID, strings.TrimSpace(in.Name), starts.UTC(), in.DueAt.UTC())
		return row.Scan(&out.ID, &out.CourseID, &out.Name, &out.StartsAt, &out.DueAt)
	})
	return out, translateErr(err)
}

func (s *Store) Assign(ctx context.Context, in AssignInput) (Assignment, error) {
	if in.CampaignID == uuid.Nil || in.PersonID == uuid.Nil {
		return Assignment{}, ErrInvalidInput
	}
	var out Assignment
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		due := in.DueAt
		if due.IsZero() {
			if err := tx.QueryRow(ctx, `
SELECT due_at FROM security_training_campaigns WHERE tenant_id = $1 AND id = $2`,
				tenantID, in.CampaignID).Scan(&due); err != nil {
				return err
			}
		}
		id := uuid.New()
		row := tx.QueryRow(ctx, `
INSERT INTO security_training_assignments (id, tenant_id, campaign_id, person_id, due_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, campaign_id, person_id, due_at, assigned_at, completed_at, completion_source, evidence_record_id`,
			id, tenantID, in.CampaignID, in.PersonID, due.UTC())
		return scanAssignment(row, &out)
	})
	return out, translateErr(err)
}

func (s *Store) Complete(ctx context.Context, in CompleteInput) (AssignmentDetail, error) {
	if in.AssignmentID == uuid.Nil || in.CompletedAt.IsZero() || !validCompletionSource(in.Source) {
		return AssignmentDetail{}, ErrInvalidInput
	}
	var out AssignmentDetail
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row := tx.QueryRow(ctx, `
UPDATE security_training_assignments
SET completed_at = $3, completion_source = $4, updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING id, campaign_id, person_id, due_at, assigned_at, completed_at, completion_source, evidence_record_id`,
			tenantID, in.AssignmentID, in.CompletedAt.UTC(), in.Source)
		if err := scanAssignment(row, &out.Assignment); err != nil {
			return err
		}
		return loadAssignmentDetail(ctx, tx, tenantID, &out)
	})
	return out, translateErr(err)
}

func (s *Store) SetEvidenceRecordID(ctx context.Context, assignmentID, evidenceRecordID uuid.UUID) error {
	if assignmentID == uuid.Nil || evidenceRecordID == uuid.Nil {
		return ErrInvalidInput
	}
	return translateErr(s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		tag, err := tx.Exec(ctx, `
UPDATE security_training_assignments
SET evidence_record_id = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2`, tenantID, assignmentID, evidenceRecordID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	}))
}

func (s *Store) RecordPhishing(ctx context.Context, in PhishingInput) (PhishingResult, error) {
	if in.AssignmentID == uuid.Nil || strings.TrimSpace(in.SimulationID) == "" || in.SentAt.IsZero() || !validPhishingOutcome(in.Outcome) {
		return PhishingResult{}, ErrInvalidInput
	}
	var out PhishingResult
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		id := uuid.New()
		row := tx.QueryRow(ctx, `
INSERT INTO security_training_phishing_results
    (id, tenant_id, assignment_id, simulation_id, sent_at, outcome, clicked_at, reported_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, assignment_id, simulation_id)
DO UPDATE SET outcome = EXCLUDED.outcome, clicked_at = EXCLUDED.clicked_at, reported_at = EXCLUDED.reported_at, updated_at = now()
RETURNING id, assignment_id, simulation_id, sent_at, outcome, clicked_at, reported_at`,
			id, tenantID, in.AssignmentID, strings.TrimSpace(in.SimulationID), in.SentAt.UTC(), in.Outcome, timePtrUTC(in.ClickedAt), timePtrUTC(in.ReportedAt))
		return row.Scan(&out.ID, &out.AssignmentID, &out.SimulationID, &out.SentAt, &out.Outcome, &out.ClickedAt, &out.ReportedAt)
	})
	return out, translateErr(err)
}

func (s *Store) Rollup(ctx context.Context, campaignID uuid.UUID, asOf time.Time) (Rollup, error) {
	if campaignID == uuid.Nil {
		return Rollup{}, ErrInvalidInput
	}
	if asOf.IsZero() {
		asOf = s.clock()
	}
	out := Rollup{CampaignID: campaignID, MetricID: MetricTrainingCompletionRate}
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		if err := tx.QueryRow(ctx, `
SELECT COUNT(*)::bigint,
       COUNT(*) FILTER (WHERE completed_at IS NOT NULL)::bigint,
       COUNT(*) FILTER (WHERE completed_at IS NULL AND due_at < $3)::bigint
FROM security_training_assignments
WHERE tenant_id = $1 AND campaign_id = $2`,
			tenantID, campaignID, asOf.UTC()).Scan(&out.Assigned, &out.Completed, &out.Overdue); err != nil {
			return err
		}
		if out.Assigned > 0 {
			rate := float64(out.Completed) / float64(out.Assigned)
			out.CompletionRate = &rate
		}
		rows, err := tx.Query(ctx, overdueDetailSQL+` AND a.campaign_id = $3 ORDER BY a.due_at ASC, p.display_name ASC`, tenantID, asOf.UTC(), campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out.OverdueList, err = scanDetails(rows)
		return err
	})
	return out, translateErr(err)
}

func (s *Store) OverdueReminders(ctx context.Context, asOf time.Time) ([]Reminder, error) {
	if asOf.IsZero() {
		asOf = s.clock()
	}
	var out []Reminder
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
SELECT a.id, p.id, p.recipient_user_id, c.title, camp.name, a.due_at
FROM security_training_assignments a
JOIN security_training_people p ON p.tenant_id = a.tenant_id AND p.id = a.person_id
JOIN security_training_campaigns camp ON camp.tenant_id = a.tenant_id AND camp.id = a.campaign_id
JOIN security_training_courses c ON c.tenant_id = camp.tenant_id AND c.id = camp.course_id
WHERE a.tenant_id = $1
  AND a.completed_at IS NULL
  AND a.due_at < $2
  AND p.active = true
  AND p.recipient_user_id IS NOT NULL
ORDER BY a.due_at ASC, p.display_name ASC`, tenantID, asOf.UTC())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Reminder
			if err := rows.Scan(&r.AssignmentID, &r.PersonID, &r.RecipientUserID, &r.CourseTitle, &r.CampaignName, &r.DueAt); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, translateErr(err)
}

func (s *Store) EmitOverdueNotifications(ctx context.Context, asOf time.Time) (int, error) {
	reminders, err := s.OverdueReminders(ctx, asOf)
	if err != nil {
		return 0, err
	}
	count := 0
	err = s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		for _, r := range reminders {
			payload, err := json.Marshal(map[string]any{
				"assignment_id": r.AssignmentID.String(),
				"person_id":     r.PersonID.String(),
				"course_title":  r.CourseTitle,
				"campaign_name": r.CampaignName,
				"due_at":        r.DueAt.UTC().Format(time.RFC3339Nano),
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
VALUES ($1, $2, $3, $4, $5, now())`,
				uuid.New(), tenantID, r.RecipientUserID, NotificationTypeTrainingOverdue, payload); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, translateErr(err)
}

func (s *Store) CalendarEvents(ctx context.Context, from, to, asOf time.Time) ([]CalendarEvent, error) {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return nil, ErrInvalidInput
	}
	if asOf.IsZero() {
		asOf = s.clock()
	}
	var out []CalendarEvent
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
SELECT a.id,
       ('Training due: ' || c.title || ' - ' || p.display_name)::text,
       a.due_at,
       a.id,
       CASE WHEN a.completed_at IS NOT NULL THEN 'completed'
            WHEN a.due_at < $4 THEN 'overdue'
            ELSE 'upcoming' END
FROM security_training_assignments a
JOIN security_training_people p ON p.tenant_id = a.tenant_id AND p.id = a.person_id
JOIN security_training_campaigns camp ON camp.tenant_id = a.tenant_id AND camp.id = a.campaign_id
JOIN security_training_courses c ON c.tenant_id = camp.tenant_id AND c.id = camp.course_id
WHERE a.tenant_id = $1 AND a.due_at >= $2 AND a.due_at < $3
ORDER BY a.due_at ASC, a.id ASC`, tenantID, from.UTC(), to.UTC(), asOf.UTC())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev CalendarEvent
			if err := rows.Scan(&ev.ID, &ev.Title, &ev.StartsAt, &ev.RelatedEntityID, &ev.Status); err != nil {
				return err
			}
			ev.RelatedEntityKind = "security_training_assignment"
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out, translateErr(err)
}

func BuildCompletionEvidence(detail AssignmentDetail, actorID string) (*evidencev1.EvidenceRecord, error) {
	if detail.ID == uuid.Nil || detail.CompletedAt == nil || detail.CompletionSource == nil {
		return nil, ErrInvalidInput
	}
	payload := map[string]any{
		"assignment_id":     detail.ID.String(),
		"campaign_id":       detail.CampaignID.String(),
		"campaign_name":     detail.CampaignName,
		"course_code":       detail.CourseCode,
		"course_title":      detail.CourseTitle,
		"person_id":         detail.PersonID.String(),
		"completed_at":      detail.CompletedAt.UTC().Format(time.RFC3339Nano),
		"completion_source": *detail.CompletionSource,
	}
	if detail.PhishingOutcome != nil {
		payload["phishing_outcome"] = *detail.PhishingOutcome
	}
	ps, err := structpb.NewStruct(payload)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: trainingEvidenceKey(detail.ID, *detail.CompletedAt),
		EvidenceKind:   EvidenceKindTrainingCompletion,
		SchemaVersion:  EvidenceVersionTraining,
		ControlId:      "soc2_cc1_4_competence_training",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "scf_anchor", Values: []string{"HRS-04"}},
			{Key: "framework_controls", Values: []string{"SOC2:CC1.4", "ISO27001:A.6.3"}},
			{Key: "metric", Values: []string{MetricTrainingCompletionRate}},
		},
		ObservedAt: timestamppb.New(detail.CompletedAt.UTC()),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    ps,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "human",
			ActorId:   actorID,
		},
	}, nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("securityawareness: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("securityawareness: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("securityawareness: commit: %w", err)
	}
	return nil
}

const overdueDetailSQL = `
SELECT a.id, a.campaign_id, a.person_id, a.due_at, a.assigned_at, a.completed_at, a.completion_source, a.evidence_record_id,
       c.code, c.title, camp.name, p.display_name, p.work_email, p.recipient_user_id,
       pr.outcome
FROM security_training_assignments a
JOIN security_training_people p ON p.tenant_id = a.tenant_id AND p.id = a.person_id
JOIN security_training_campaigns camp ON camp.tenant_id = a.tenant_id AND camp.id = a.campaign_id
JOIN security_training_courses c ON c.tenant_id = camp.tenant_id AND c.id = camp.course_id
LEFT JOIN LATERAL (
    SELECT outcome
    FROM security_training_phishing_results pr
    WHERE pr.tenant_id = a.tenant_id AND pr.assignment_id = a.id
    ORDER BY pr.sent_at DESC
    LIMIT 1
) pr ON true
WHERE a.tenant_id = $1 AND a.completed_at IS NULL AND a.due_at < $2`

func scanDetails(rows pgx.Rows) ([]AssignmentDetail, error) {
	var out []AssignmentDetail
	for rows.Next() {
		var d AssignmentDetail
		if err := scanDetail(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func loadAssignmentDetail(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, d *AssignmentDetail) error {
	row := tx.QueryRow(ctx, `
SELECT c.code, c.title, camp.name, p.display_name, p.work_email, p.recipient_user_id,
       pr.outcome
FROM security_training_assignments a
JOIN security_training_people p ON p.tenant_id = a.tenant_id AND p.id = a.person_id
JOIN security_training_campaigns camp ON camp.tenant_id = a.tenant_id AND camp.id = a.campaign_id
JOIN security_training_courses c ON c.tenant_id = camp.tenant_id AND c.id = camp.course_id
LEFT JOIN LATERAL (
    SELECT outcome
    FROM security_training_phishing_results pr
    WHERE pr.tenant_id = a.tenant_id AND pr.assignment_id = a.id
    ORDER BY pr.sent_at DESC
    LIMIT 1
) pr ON true
WHERE a.tenant_id = $1 AND a.id = $2`, tenantID, d.ID)
	return row.Scan(&d.CourseCode, &d.CourseTitle, &d.CampaignName, &d.PersonDisplayName, &d.PersonWorkEmail, &d.RecipientUserID, &d.PhishingOutcome)
}

func scanDetail(row pgx.Row, d *AssignmentDetail) error {
	return row.Scan(&d.ID, &d.CampaignID, &d.PersonID, &d.DueAt, &d.AssignedAt, &d.CompletedAt, &d.CompletionSource, &d.EvidenceRecordID,
		&d.CourseCode, &d.CourseTitle, &d.CampaignName, &d.PersonDisplayName, &d.PersonWorkEmail, &d.RecipientUserID, &d.PhishingOutcome)
}

func scanPerson(row pgx.Row, out *Person) error {
	return row.Scan(&out.ID, &out.Source, &out.SourcePersonID, &out.DisplayName, &out.WorkEmail, &out.RecipientUserID, &out.Active)
}

func scanAssignment(row pgx.Row, out *Assignment) error {
	return row.Scan(&out.ID, &out.CampaignID, &out.PersonID, &out.DueAt, &out.AssignedAt, &out.CompletedAt, &out.CompletionSource, &out.EvidenceRecordID)
}

func validatePerson(in UpsertPersonInput) error {
	if strings.TrimSpace(in.DisplayName) == "" {
		return ErrInvalidInput
	}
	if in.Source == "" {
		in.Source = PersonSourceManual
	}
	if in.Source != PersonSourceManual && in.Source != PersonSourceHRIS && in.Source != PersonSourceSCIM {
		return ErrInvalidInput
	}
	if in.Source != PersonSourceManual && (in.SourcePersonID == nil || strings.TrimSpace(*in.SourcePersonID) == "") {
		return ErrInvalidInput
	}
	return nil
}

func validCompletionSource(v string) bool {
	return v == CompletionSourceManual || v == CompletionSourceCSV || v == CompletionSourceLMSConnector
}

func validPhishingOutcome(v string) bool {
	return v == PhishingNoClick || v == PhishingClicked || v == PhishingReported || v == PhishingCredentialSubmitted
}

func optTrim(v *string) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	return &s
}

func optLower(v *string) *string {
	v = optTrim(v)
	if v == nil {
		return nil
	}
	s := strings.ToLower(*v)
	return &s
}

func timePtrUTC(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}

func trainingEvidenceKey(assignmentID uuid.UUID, completedAt time.Time) string {
	sum := sha256.Sum256([]byte("security_awareness.training_completion|" + assignmentID.String() + "|" + completedAt.UTC().Format("2006-01-02")))
	return hex.EncodeToString(sum[:])
}

func translateErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrNotFound) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
		return ErrAlreadyExists
	}
	return err
}
