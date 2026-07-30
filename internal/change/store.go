package change

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const (
	pgErrUniqueViolation     = "23505"
	pgErrForeignKeyViolation = "23503"
	pgErrCheckViolation      = "23514"
)

var nilUUID uuid.UUID

type Change struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Title       string
	Description string
	Source      string
	SourceRef   string
	SourceURL   string
	Status      string

	ProposedBy    uuid.UUID
	ProposedAt    time.Time
	ApproverID    *uuid.UUID
	ApprovedAt    *time.Time
	ImplementedBy *uuid.UUID
	ImplementedAt *time.Time
	VerifiedBy    *uuid.UUID
	VerifiedAt    *time.Time

	RiskNotes     string
	RollbackNotes string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ControlLink struct {
	ControlID uuid.UUID
	LinkedAt  time.Time
	LinkedBy  uuid.UUID
}

type AuditEntry struct {
	ID          uuid.UUID
	ChangeID    uuid.UUID
	ActorID     uuid.UUID
	ActionType  string
	BeforeState json.RawMessage
	AfterState  json.RawMessage
	CreatedAt   time.Time
}

type Rollup struct {
	Total              int64
	Proposed           int64
	Approved           int64
	Implemented        int64
	Verified           int64
	Backlog            int64
	VerifiedLast30Days int64
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type CreateInput struct {
	Title              string
	Description        string
	Source             string
	SourceRef          string
	SourceURL          string
	ProposedBy         uuid.UUID
	RiskNotes          string
	RollbackNotes      string
	AffectedControlIDs []uuid.UUID
}

// JiraTicket is the canonical Jira change-ticket projection consumed by the
// register. It intentionally mirrors connectors/jira/internal/jiratickets.Ticket
// without importing that internal package across Go visibility boundaries.
type JiraTicket struct {
	TicketKey string
	Summary   string
	Status    string
	URL       string
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Change, error) {
	if in.Source == "" {
		in.Source = SourceManual
	}
	if err := validateCreate(in); err != nil {
		return Change{}, err
	}

	var out Change
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := ensureUser(ctx, q, tenantID, in.ProposedBy); err != nil {
			return err
		}
		for _, controlID := range uniqueUUIDs(in.AffectedControlIDs) {
			if err := ensureControl(ctx, q, tenantID, controlID); err != nil {
				return err
			}
		}

		row, err := q.CreateChange(ctx, dbx.CreateChangeParams{
			ID:            pgUUID(uuid.New()),
			TenantID:      pgUUID(tenantID),
			Title:         in.Title,
			Description:   in.Description,
			Source:        in.Source,
			SourceRef:     in.SourceRef,
			SourceUrl:     in.SourceURL,
			ProposedBy:    pgUUID(in.ProposedBy),
			RiskNotes:     in.RiskNotes,
			RollbackNotes: in.RollbackNotes,
		})
		if err != nil {
			return mapCreateError(err)
		}
		out = changeFromRow(row)
		after, _ := json.Marshal(changeSnapshot(out))
		if err := writeAudit(ctx, q, tenantID, out.ID, in.ProposedBy, ActionCreated, nil, after); err != nil {
			return err
		}
		for _, controlID := range uniqueUUIDs(in.AffectedControlIDs) {
			if err := linkControl(ctx, q, tenantID, out.ID, controlID, in.ProposedBy); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) ImportJira(ctx context.Context, proposedBy uuid.UUID, tickets []JiraTicket, controlIDs []uuid.UUID) ([]Change, error) {
	out := make([]Change, 0, len(tickets))
	for _, t := range tickets {
		in := CreateInput{
			Title:              t.Summary,
			Description:        strings.TrimSpace("Jira " + t.TicketKey + " (" + t.Status + ")"),
			Source:             SourceJira,
			SourceRef:          t.TicketKey,
			SourceURL:          t.URL,
			ProposedBy:         proposedBy,
			RiskNotes:          "Imported from Jira change ticket.",
			RollbackNotes:      "See Jira ticket for implementation and rollback details.",
			AffectedControlIDs: controlIDs,
		}
		ch, err := s.Create(ctx, in)
		if err != nil {
			if errors.Is(err, ErrAlreadyImported) {
				continue
			}
			return out, err
		}
		out = append(out, ch)
	}
	return out, nil
}

func (s *Store) ImportCSV(ctx context.Context, proposedBy uuid.UUID, r io.Reader) ([]Change, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("change: read csv: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrCSVHeaderInvalid
	}
	idx := csvHeader(rows[0])
	if idx["title"] < 0 || idx["control_id"] < 0 {
		return nil, ErrCSVHeaderInvalid
	}
	var out []Change
	for _, row := range rows[1:] {
		title := csvCell(row, idx["title"])
		controlID, err := uuid.Parse(csvCell(row, idx["control_id"]))
		if err != nil {
			return out, fmt.Errorf("change: parse control_id: %w", err)
		}
		in := CreateInput{
			Title:              title,
			Description:        csvCell(row, idx["description"]),
			Source:             SourceCSV,
			SourceRef:          csvCell(row, idx["source_ref"]),
			SourceURL:          csvCell(row, idx["source_url"]),
			ProposedBy:         proposedBy,
			RiskNotes:          csvCell(row, idx["risk_notes"]),
			RollbackNotes:      csvCell(row, idx["rollback_notes"]),
			AffectedControlIDs: []uuid.UUID{controlID},
		}
		ch, err := s.Create(ctx, in)
		if err != nil {
			if errors.Is(err, ErrAlreadyImported) {
				continue
			}
			return out, err
		}
		out = append(out, ch)
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Change, error) {
	var out Change
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetChangeByID(ctx, dbx.GetChangeByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get change: %w", err)
		}
		out = changeFromRow(row)
		return nil
	})
	return out, err
}

func (s *Store) List(ctx context.Context, status string, limit int) ([]Change, error) {
	if status != "" && !ValidStatus(status) {
		return nil, ErrWrongState
	}
	var out []Change
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListChanges(ctx, dbx.ListChangesParams{
			TenantID: pgUUID(tenantID),
			Status:   nilIfEmpty(status),
			RowLimit: int32(clampLimit(limit)),
		})
		if err != nil {
			return fmt.Errorf("list changes: %w", err)
		}
		out = make([]Change, len(rows))
		for i, r := range rows {
			out[i] = changeFromRow(r)
		}
		return nil
	})
	return out, err
}

func (s *Store) Approve(ctx context.Context, id, approver uuid.UUID, now time.Time) (Change, error) {
	if approver == nilUUID {
		return Change{}, ErrApproverRequired
	}
	return s.transition(ctx, id, approver, now, StatusProposed, ActionApproved, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, when time.Time) (dbx.Change, error) {
		return q.ApproveChange(ctx, dbx.ApproveChangeParams{TenantID: pgUUID(tenantID), ID: pgUUID(id), ApproverID: pgUUID(approver), ApprovedAt: pgTS(when)})
	})
}

func (s *Store) Implement(ctx context.Context, id, actor uuid.UUID, now time.Time) (Change, error) {
	if actor == nilUUID {
		return Change{}, ErrActorRequired
	}
	return s.transition(ctx, id, actor, now, StatusApproved, ActionImplemented, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, when time.Time) (dbx.Change, error) {
		return q.ImplementChange(ctx, dbx.ImplementChangeParams{TenantID: pgUUID(tenantID), ID: pgUUID(id), ImplementedBy: pgUUID(actor), ImplementedAt: pgTS(when)})
	})
}

func (s *Store) Verify(ctx context.Context, id, actor uuid.UUID, now time.Time) (Change, error) {
	if actor == nilUUID {
		return Change{}, ErrActorRequired
	}
	return s.transition(ctx, id, actor, now, StatusImplemented, ActionVerified, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, when time.Time) (dbx.Change, error) {
		return q.VerifyChange(ctx, dbx.VerifyChangeParams{TenantID: pgUUID(tenantID), ID: pgUUID(id), VerifiedBy: pgUUID(actor), VerifiedAt: pgTS(when)})
	})
}

func (s *Store) ListControls(ctx context.Context, id uuid.UUID) ([]ControlLink, error) {
	var out []ControlLink
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListChangeControls(ctx, dbx.ListChangeControlsParams{TenantID: pgUUID(tenantID), ChangeID: pgUUID(id)})
		if err != nil {
			return fmt.Errorf("list change controls: %w", err)
		}
		out = make([]ControlLink, len(rows))
		for i, r := range rows {
			out[i] = ControlLink{ControlID: uuid.UUID(r.ControlID.Bytes), LinkedAt: ts(r.LinkedAt), LinkedBy: uuid.UUID(r.LinkedBy.Bytes)}
		}
		return nil
	})
	return out, err
}

func (s *Store) ListAuditLog(ctx context.Context, id uuid.UUID) ([]AuditEntry, error) {
	var out []AuditEntry
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListChangeAuditLog(ctx, dbx.ListChangeAuditLogParams{TenantID: pgUUID(tenantID), ChangeID: pgUUID(id)})
		if err != nil {
			return fmt.Errorf("list change audit log: %w", err)
		}
		out = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out[i] = AuditEntry{
				ID:          uuid.UUID(r.ID.Bytes),
				ChangeID:    uuid.UUID(r.ChangeID.Bytes),
				ActorID:     uuid.UUID(r.ActorID.Bytes),
				ActionType:  r.ActionType,
				BeforeState: append(json.RawMessage(nil), r.BeforeState...),
				AfterState:  append(json.RawMessage(nil), r.AfterState...),
				CreatedAt:   ts(r.CreatedAt),
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) Rollup(ctx context.Context) (Rollup, error) {
	var out Rollup
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		r, err := q.ChangeRollup(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("change rollup: %w", err)
		}
		out = Rollup{
			Total:              r.Total,
			Proposed:           r.Proposed,
			Approved:           r.Approved,
			Implemented:        r.Implemented,
			Verified:           r.Verified,
			Backlog:            r.Backlog,
			VerifiedLast30Days: r.VerifiedLast30Days,
		}
		return nil
	})
	return out, err
}

func (s *Store) transition(ctx context.Context, id, actor uuid.UUID, now time.Time, expected, action string, update func(context.Context, *dbx.Queries, uuid.UUID, time.Time) (dbx.Change, error)) (Change, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var out Change
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := ensureUser(ctx, q, tenantID, actor); err != nil {
			return err
		}
		beforeRow, err := q.GetChangeByID(ctx, dbx.GetChangeByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get change before transition: %w", err)
		}
		before := changeFromRow(beforeRow)
		if before.Status != expected || !AllowedTransition(before.Status, nextStatus(action)) {
			return ErrWrongState
		}
		controls, err := q.ListChangeControls(ctx, dbx.ListChangeControlsParams{TenantID: pgUUID(tenantID), ChangeID: pgUUID(id)})
		if err != nil {
			return fmt.Errorf("list affected controls: %w", err)
		}
		if len(controls) == 0 {
			return ErrNoAffectedControls
		}
		updated, err := update(ctx, q, tenantID, now.UTC())
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWrongState
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrCheckViolation {
				return ErrWrongState
			}
			return fmt.Errorf("transition change: %w", err)
		}
		out = changeFromRow(updated)
		beforeJSON, _ := json.Marshal(changeSnapshot(before))
		afterJSON, _ := json.Marshal(changeSnapshot(out))
		if err := writeAudit(ctx, q, tenantID, id, actor, action, beforeJSON, afterJSON); err != nil {
			return err
		}
		if action == ActionApproved || action == ActionVerified {
			for _, c := range controls {
				if err := insertEvidence(ctx, q, tenantID, out, uuid.UUID(c.ControlID.Bytes), action, now.UTC()); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return out, err
}

func linkControl(ctx context.Context, q *dbx.Queries, tenantID, changeID, controlID, actor uuid.UUID) error {
	if err := q.LinkChangeControl(ctx, dbx.LinkChangeControlParams{
		ChangeID: pgUUID(changeID), ControlID: pgUUID(controlID), TenantID: pgUUID(tenantID), LinkedBy: pgUUID(actor),
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case pgErrUniqueViolation:
				return nil
			case pgErrForeignKeyViolation:
				return ErrControlNotInTenant
			}
		}
		return fmt.Errorf("link change control: %w", err)
	}
	detail, _ := json.Marshal(map[string]string{"control_id": controlID.String()})
	return writeAudit(ctx, q, tenantID, changeID, actor, ActionControlLinked, nil, detail)
}

func insertEvidence(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ch Change, controlID uuid.UUID, action string, observed time.Time) error {
	kind := EvidenceKindApproval
	if action == ActionVerified {
		kind = EvidenceKindVerification
	}
	payload, _ := json.Marshal(map[string]any{
		"change_id":      ch.ID.String(),
		"title":          ch.Title,
		"status":         ch.Status,
		"source":         ch.Source,
		"source_ref":     ch.SourceRef,
		"risk_notes":     ch.RiskNotes,
		"rollback_notes": ch.RollbackNotes,
		"action":         action,
	})
	provenance, _ := json.Marshal(map[string]any{"system": "security-atlas", "source": "change_register", "change_id": ch.ID.String()})
	sourceAttribution, _ := json.Marshal(map[string]any{"type": "change_register", "change_id": ch.ID.String(), "source_url": ch.SourceURL})
	sum := sha256.Sum256(bytes.Join([][]byte{[]byte(kind), []byte(ch.ID.String()), []byte(controlID.String()), []byte(action), payload}, []byte{0}))
	idem := "change:" + ch.ID.String() + ":" + controlID.String() + ":" + action
	nanos := observed.UnixNano()
	_, err := q.InsertEvidenceRecord(ctx, dbx.InsertEvidenceRecordParams{
		ID:                pgUUID(uuid.New()),
		TenantID:          pgUUID(tenantID),
		ControlID:         pgUUID(controlID),
		ControlRef:        controlID.String(),
		ScopeID:           pgtype.UUID{},
		ObservedAt:        pgTS(observed),
		Provenance:        provenance,
		Result:            dbx.EvidenceResultPass,
		Payload:           payload,
		PayloadUri:        nil,
		Hash:              hex.EncodeToString(sum[:]),
		FreshnessClass:    dbx.EvidenceFreshnessClassMonthly,
		ValidUntil:        pgtype.Timestamptz{},
		IdempotencyKey:    &idem,
		EvidenceKind:      &kind,
		SchemaVersion:     strPtr("1.0.0"),
		CredentialID:      strPtr("system:change-register"),
		IngestionPath:     "manual_upload",
		SourceAttribution: sourceAttribution,
		ScopeCanonical:    []byte(`[]`),
		ObservedAtNanos:   &nanos,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			return nil
		}
		return fmt.Errorf("insert change evidence: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("change: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("change: begin tx: %w", err)
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
		return fmt.Errorf("change: commit: %w", err)
	}
	return nil
}

func ensureUser(ctx context.Context, q *dbx.Queries, tenantID, userID uuid.UUID) error {
	ok, err := q.ChangeUserExistsInTenant(ctx, dbx.ChangeUserExistsInTenantParams{TenantID: pgUUID(tenantID), ID: pgUUID(userID)})
	if err != nil {
		return fmt.Errorf("user existence probe: %w", err)
	}
	if !ok {
		return ErrUserNotInTenant
	}
	return nil
}

func ensureControl(ctx context.Context, q *dbx.Queries, tenantID, controlID uuid.UUID) error {
	if controlID == nilUUID {
		return ErrControlRequired
	}
	ok, err := q.ChangeControlExistsInTenant(ctx, dbx.ChangeControlExistsInTenantParams{TenantID: pgUUID(tenantID), ID: pgUUID(controlID)})
	if err != nil {
		return fmt.Errorf("control existence probe: %w", err)
	}
	if !ok {
		return ErrControlNotInTenant
	}
	return nil
}

func writeAudit(ctx context.Context, q *dbx.Queries, tenantID, changeID, actor uuid.UUID, action string, before, after []byte) error {
	if _, err := q.WriteChangeAuditLog(ctx, dbx.WriteChangeAuditLogParams{
		ID:          pgUUID(uuid.New()),
		TenantID:    pgUUID(tenantID),
		ChangeID:    pgUUID(changeID),
		ActorID:     pgUUID(actor),
		ActionType:  action,
		BeforeState: before,
		AfterState:  after,
	}); err != nil {
		return fmt.Errorf("write change audit (%s): %w", action, err)
	}
	return nil
}

func changeFromRow(r dbx.Change) Change {
	out := Change{
		ID:            uuid.UUID(r.ID.Bytes),
		TenantID:      uuid.UUID(r.TenantID.Bytes),
		Title:         r.Title,
		Description:   r.Description,
		Source:        r.Source,
		SourceRef:     r.SourceRef,
		SourceURL:     r.SourceUrl,
		Status:        r.Status,
		ProposedBy:    uuid.UUID(r.ProposedBy.Bytes),
		ProposedAt:    ts(r.ProposedAt),
		RiskNotes:     r.RiskNotes,
		RollbackNotes: r.RollbackNotes,
		CreatedAt:     ts(r.CreatedAt),
		UpdatedAt:     ts(r.UpdatedAt),
	}
	out.ApproverID = uuidPtr(r.ApproverID)
	out.ApprovedAt = timePtr(r.ApprovedAt)
	out.ImplementedBy = uuidPtr(r.ImplementedBy)
	out.ImplementedAt = timePtr(r.ImplementedAt)
	out.VerifiedBy = uuidPtr(r.VerifiedBy)
	out.VerifiedAt = timePtr(r.VerifiedAt)
	return out
}

func changeSnapshot(ch Change) map[string]any {
	return map[string]any{
		"title":          ch.Title,
		"source":         ch.Source,
		"source_ref":     ch.SourceRef,
		"status":         ch.Status,
		"approver_id":    uuidString(ch.ApproverID),
		"risk_notes":     ch.RiskNotes,
		"rollback_notes": ch.RollbackNotes,
	}
}

func mapCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrUniqueViolation:
			return ErrAlreadyImported
		case pgErrForeignKeyViolation:
			return ErrUserNotInTenant
		case pgErrCheckViolation:
			return ErrSourceInvalid
		}
	}
	return fmt.Errorf("create change: %w", err)
}

func nextStatus(action string) string {
	switch action {
	case ActionApproved:
		return StatusApproved
	case ActionImplemented:
		return StatusImplemented
	case ActionVerified:
		return StatusVerified
	}
	return ""
}

func uniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func csvHeader(row []string) map[string]int {
	out := map[string]int{
		"title": -1, "description": -1, "control_id": -1, "source_ref": -1,
		"source_url": -1, "risk_notes": -1, "rollback_notes": -1,
	}
	for i, cell := range row {
		key := strings.ToLower(strings.TrimSpace(cell))
		if _, ok := out[key]; ok {
			out[key] = i
		}
	}
	return out
}

func csvCell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func pgTS(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func uuidPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	out := uuid.UUID(u.Bytes)
	return &out
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time
	return &out
}

func uuidString(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
