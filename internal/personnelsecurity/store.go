package personnelsecurity

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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("personnel security: pool is required")
	}
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) WithClock(clock func() time.Time) *Store {
	copy := *s
	if clock != nil {
		copy.clock = clock
	}
	return &copy
}

func (s *Store) CreateManual(ctx context.Context, in ManualInput) (Checklist, error) {
	norm, err := normalizeManualInput(in, s.clock())
	if err != nil {
		return Checklist{}, err
	}
	return s.create(ctx, createInput{
		Kind:              norm.Kind,
		Source:            SourceManual,
		PersonExternalID:  norm.PersonExternalID,
		PersonWorkEmail:   norm.PersonWorkEmail,
		PersonDisplayName: norm.PersonDisplayName,
		ControlID:         norm.ControlID,
		DueAt:             norm.DueAt,
		CreatedBy:         norm.CreatedBy,
	})
}

func (s *Store) HandleWorkerEvent(ctx context.Context, w worker.Worker, sourceEventID string, controlID uuid.UUID) (Checklist, error) {
	kind, ok := workflowFromWorker(w)
	if !ok {
		return Checklist{}, ErrNoChecklistForEvent
	}
	source, ok := sourceFromHRIS(w.SourceHRIS)
	if !ok {
		return Checklist{}, ErrInvalidSource
	}
	personID := strings.TrimSpace(w.WorkerID)
	if personID == "" {
		return Checklist{}, ErrPersonRequired
	}
	sourceEventID = strings.TrimSpace(sourceEventID)
	eventAt := w.StartDate
	if kind == WorkflowOffboarding {
		eventAt = w.EndDate
	}
	dueAt := defaultDueAt(kind, eventAt, s.clock())
	return s.create(ctx, createInput{
		Kind:             kind,
		Source:           source,
		SourceEventID:    sourceEventID,
		PersonExternalID: personID,
		PersonWorkEmail:  strings.TrimSpace(w.WorkEmail),
		ControlID:        controlID,
		DueAt:            dueAt,
		CreatedBy:        "connector:" + string(source),
	})
}

func (s *Store) Load(ctx context.Context, id uuid.UUID) (Checklist, error) {
	var out Checklist
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetPersonnelChecklist(ctx, dbx.GetPersonnelChecklistParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			return err
		}
		items, err := q.ListPersonnelChecklistItems(ctx, dbx.ListPersonnelChecklistItemsParams{
			TenantID:    pgUUID(tenantID),
			ChecklistID: row.ID,
		})
		if err != nil {
			return err
		}
		out = checklistFromRow(row, items)
		return nil
	})
	return out, err
}

func (s *Store) CompleteItem(ctx context.Context, in Completion) (Item, error) {
	if strings.TrimSpace(in.CompletedBy) == "" {
		return Item{}, ErrCompleterRequired
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = s.clock()
	}
	var out Item
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		item, err := q.GetPersonnelChecklistItem(ctx, dbx.GetPersonnelChecklistItemParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.ItemID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrItemNotFound
			}
			return err
		}
		if item.CompletedAt.Valid {
			return ErrItemNotFound
		}
		checklist, err := q.GetPersonnelChecklist(ctx, dbx.GetPersonnelChecklistParams{
			TenantID: pgUUID(tenantID),
			ID:       item.ChecklistID,
		})
		if err != nil {
			return err
		}
		evID, err := s.insertEvidence(ctx, q, tenantID, checklist, item, in)
		if err != nil {
			return err
		}
		completer := strings.TrimSpace(in.CompletedBy)
		completed, err := q.CompletePersonnelChecklistItem(ctx, dbx.CompletePersonnelChecklistItemParams{
			TenantID:         pgUUID(tenantID),
			ID:               item.ID,
			CompletedAt:      pgTime(in.ObservedAt),
			CompletedBy:      &completer,
			EvidenceRecordID: pgUUID(evID),
			EvidenceUri:      strings.TrimSpace(in.EvidenceURI),
			Notes:            strings.TrimSpace(in.Notes),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrItemNotFound
			}
			return err
		}
		openItems, err := q.CountOpenPersonnelChecklistItems(ctx, dbx.CountOpenPersonnelChecklistItemsParams{
			TenantID:    pgUUID(tenantID),
			ChecklistID: item.ChecklistID,
		})
		if err != nil {
			return err
		}
		if openItems == 0 {
			_, err = q.MarkPersonnelChecklistCompleted(ctx, dbx.MarkPersonnelChecklistCompletedParams{
				TenantID:    pgUUID(tenantID),
				ID:          item.ChecklistID,
				CompletedAt: pgTime(in.ObservedAt),
			})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		out = itemFromRow(completed)
		return nil
	})
	return out, err
}

func (s *Store) ListOverdueOffboarding(ctx context.Context, now time.Time) ([]Checklist, error) {
	if now.IsZero() {
		now = s.clock()
	}
	var out []Checklist
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListOverdueOffboardingChecklists(ctx, dbx.ListOverdueOffboardingChecklistsParams{
			TenantID: pgUUID(tenantID),
			DueAt:    pgTime(now),
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, checklistFromRow(row, nil))
		}
		return nil
	})
	return out, err
}

func (s *Store) SurfaceOverdueOffboarding(ctx context.Context, now time.Time, recipientUserID string) ([]Checklist, error) {
	if now.IsZero() {
		now = s.clock()
	}
	recipientUserID = strings.TrimSpace(recipientUserID)
	var out []Checklist
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListOverdueOffboardingChecklists(ctx, dbx.ListOverdueOffboardingChecklistsParams{
			TenantID: pgUUID(tenantID),
			DueAt:    pgTime(now),
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			c := checklistFromRow(row, nil)
			out = append(out, c)
			if recipientUserID == "" {
				continue
			}
			// Idempotency (OE-661): the notification row itself is the dedup
			// marker — one offboarding-overdue notification per (checklist,
			// recipient), ever. A re-run of the sweep re-lists the overdue
			// checklists (the return value is unchanged) but never
			// double-notifies. Probe + insert share this tx, so the marker
			// and the notification cannot diverge.
			already, err := q.CountPersonnelOverdueNotificationsForChecklist(ctx, dbx.CountPersonnelOverdueNotificationsForChecklistParams{
				TenantID:        pgUUID(tenantID),
				RecipientUserID: recipientUserID,
				ChecklistID:     c.ID.String(),
			})
			if err != nil {
				return err
			}
			if already > 0 {
				continue
			}
			payload, _ := json.Marshal(map[string]any{
				"checklist_id":       c.ID.String(),
				"person_external_id": c.PersonExternalID,
				"person_work_email":  c.PersonWorkEmail,
				"due_at":             c.DueAt.UTC().Format(time.RFC3339Nano),
				"priority":           "high",
			})
			if _, err := q.CreateNotification(ctx, dbx.CreateNotificationParams{
				ID:              pgUUID(uuid.New()),
				TenantID:        pgUUID(tenantID),
				RecipientUserID: recipientUserID,
				Type:            NotificationTypeOffboardingOverdue,
				Payload:         payload,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

type createInput struct {
	Kind              WorkflowKind
	Source            EventSource
	SourceEventID     string
	PersonExternalID  string
	PersonWorkEmail   string
	PersonDisplayName string
	ControlID         uuid.UUID
	DueAt             time.Time
	CreatedBy         string
}

func (s *Store) create(ctx context.Context, in createInput) (Checklist, error) {
	var out Checklist
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if in.SourceEventID != "" {
			existing, err := q.GetPersonnelChecklistBySourceEvent(ctx, dbx.GetPersonnelChecklistBySourceEventParams{
				TenantID:      pgUUID(tenantID),
				Source:        string(in.Source),
				SourceEventID: &in.SourceEventID,
			})
			if err == nil {
				items, err := q.ListPersonnelChecklistItems(ctx, dbx.ListPersonnelChecklistItemsParams{
					TenantID:    pgUUID(tenantID),
					ChecklistID: existing.ID,
				})
				if err != nil {
					return err
				}
				out = checklistFromRow(existing, items)
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		checklistID := uuid.New()
		row, err := q.InsertPersonnelChecklist(ctx, dbx.InsertPersonnelChecklistParams{
			ID:                pgUUID(checklistID),
			TenantID:          pgUUID(tenantID),
			WorkflowKind:      string(in.Kind),
			Source:            string(in.Source),
			SourceEventID:     optionalString(in.SourceEventID),
			PersonExternalID:  in.PersonExternalID,
			PersonWorkEmail:   in.PersonWorkEmail,
			PersonDisplayName: in.PersonDisplayName,
			ControlID:         pgUUID(in.ControlID),
			DueAt:             pgTime(in.DueAt),
			CreatedBy:         in.CreatedBy,
		})
		if err != nil {
			return err
		}
		var items []dbx.PersonnelSecurityChecklistItem
		for i, item := range itemTemplates(in.Kind) {
			inserted, err := q.InsertPersonnelChecklistItem(ctx, dbx.InsertPersonnelChecklistItemParams{
				ID:          pgUUID(uuid.New()),
				TenantID:    pgUUID(tenantID),
				ChecklistID: row.ID,
				Slug:        item.Slug,
				Title:       item.Title,
				Category:    item.Category,
				SortOrder:   int32(i),
			})
			if err != nil {
				return err
			}
			items = append(items, inserted)
		}
		out = checklistFromRow(row, items)
		return nil
	})
	return out, err
}

func (s *Store) insertEvidence(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, checklist dbx.PersonnelSecurityChecklist, item dbx.PersonnelSecurityChecklistItem, in Completion) (uuid.UUID, error) {
	evID := uuid.New()
	checklistID := uuid.UUID(checklist.ID.Bytes)
	itemID := uuid.UUID(item.ID.Bytes)
	controlRef := DefaultControlRef
	controlID := pgtype.UUID{}
	if checklist.ControlID.Valid {
		controlID = checklist.ControlID
		controlRef = uuid.UUID(checklist.ControlID.Bytes).String()
	}
	payload, _ := json.Marshal(map[string]any{
		"checklist_id":       checklistID.String(),
		"item_id":            itemID.String(),
		"workflow_kind":      checklist.WorkflowKind,
		"person_external_id": checklist.PersonExternalID,
		"person_work_email":  checklist.PersonWorkEmail,
		"item_slug":          item.Slug,
		"completed_by":       strings.TrimSpace(in.CompletedBy),
		"evidence_uri":       strings.TrimSpace(in.EvidenceURI),
		"notes":              strings.TrimSpace(in.Notes),
	})
	sum := sha256.Sum256(payload)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	provenance, _ := json.Marshal(map[string]any{
		"ingestion_path": "manual_upload",
		"actor":          "personnel_security.workflow",
	})
	source, _ := json.Marshal(map[string]any{
		"actor_type": "system",
		"actor_id":   "personnel_security.workflow",
	})
	scope, _ := json.Marshal([]map[string]any{
		{"key": "person_external_id", "values": []string{checklist.PersonExternalID}},
		{"key": "workflow_kind", "values": []string{checklist.WorkflowKind}},
	})
	observedNanos := in.ObservedAt.UnixNano()
	kind := EvidenceKind
	version := SchemaVersion
	idem := "personnel-security:" + checklistID.String() + ":" + itemID.String()
	_, err := q.InsertEvidenceRecord(ctx, dbx.InsertEvidenceRecordParams{
		ID:                pgUUID(evID),
		TenantID:          pgUUID(tenantID),
		ControlID:         controlID,
		ControlRef:        controlRef,
		ScopeID:           pgtype.UUID{},
		ObservedAt:        pgTime(in.ObservedAt),
		Provenance:        provenance,
		Result:            dbx.EvidenceResultPass,
		Payload:           payload,
		Hash:              hash,
		FreshnessClass:    dbx.EvidenceFreshnessClassMonthly,
		IdempotencyKey:    &idem,
		EvidenceKind:      &kind,
		SchemaVersion:     &version,
		IngestionPath:     "manual_upload",
		SourceAttribution: source,
		ScopeCanonical:    scope,
		ObservedAtNanos:   &observedNanos,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("personnel security: insert evidence: %w", err)
	}
	return evID, nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("personnel security: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("personnel security: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx), tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func checklistFromRow(row dbx.PersonnelSecurityChecklist, itemRows []dbx.PersonnelSecurityChecklistItem) Checklist {
	items := make([]Item, 0, len(itemRows))
	for _, item := range itemRows {
		items = append(items, itemFromRow(item))
	}
	return Checklist{
		ID:                uuid.UUID(row.ID.Bytes),
		Kind:              WorkflowKind(row.WorkflowKind),
		Source:            EventSource(row.Source),
		SourceEventID:     deref(row.SourceEventID),
		PersonExternalID:  row.PersonExternalID,
		PersonWorkEmail:   row.PersonWorkEmail,
		PersonDisplayName: row.PersonDisplayName,
		ControlID:         uuidFromPG(row.ControlID),
		DueAt:             row.DueAt.Time,
		Status:            row.Status,
		Items:             items,
	}
}

func itemFromRow(row dbx.PersonnelSecurityChecklistItem) Item {
	completedBy := ""
	if row.CompletedBy != nil {
		completedBy = *row.CompletedBy
	}
	return Item{
		ID:               uuid.UUID(row.ID.Bytes),
		ChecklistID:      uuid.UUID(row.ChecklistID.Bytes),
		Slug:             row.Slug,
		Title:            row.Title,
		Category:         row.Category,
		SortOrder:        row.SortOrder,
		CompletedAt:      row.CompletedAt.Time,
		CompletedBy:      completedBy,
		EvidenceRecordID: uuidFromPG(row.EvidenceRecordID),
		EvidenceURI:      row.EvidenceUri,
		Notes:            row.Notes,
	}
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func uuidFromPG(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return uuid.UUID(u.Bytes)
}
