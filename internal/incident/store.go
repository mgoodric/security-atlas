// Package incident implements the OE-631 incident register backend.
package incident

import (
	"bytes"
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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	StateDetected  = "detected"
	StateTriaged   = "triaged"
	StateContained = "contained"
	StateResolved  = "resolved"
	StateClosed    = "closed"

	SeverityP3 = "p3"
	SeverityP2 = "p2"
	SeverityP1 = "p1"
	SeverityP0 = "p0"

	ActionCreated      = "created"
	ActionTransitioned = "transitioned"
	ActionClosed       = "closed"

	EvidenceKindPostmortem = "incident.postmortem.v1"
	SchemaVersion          = "1.0.0"
)

var (
	ErrNotFound               = errors.New("incident: not found")
	ErrWrongState             = errors.New("incident: invalid lifecycle transition")
	ErrTitleRequired          = errors.New("incident: title is required")
	ErrActorRequired          = errors.New("incident: actor is required")
	ErrSeverityInvalid        = errors.New("incident: severity must be one of p3, p2, p1, p0")
	ErrAffectedSystemsInvalid = errors.New("incident: affected_systems must be a JSON array")
	ErrPostmortemRequired     = errors.New("incident: postmortem summary is required")
	ErrIRControlRequired      = errors.New("incident: at least one linked IR control is required before close")
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type CreateInput struct {
	Title              string
	Description        string
	OperatorSeverity   string
	AffectedSystemTier *string
	AffectedSystems    []byte
	DetectedBy         string
	DetectedAt         time.Time
	ControlIDs         []uuid.UUID
	RiskIDs            []uuid.UUID
	VendorIDs          []uuid.UUID
	EvidenceIDs        []uuid.UUID
}

type CloseInput struct {
	Actor             string
	PostmortemSummary string
	ObservedAt        time.Time
}

type Incident struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	Title              string
	Description        string
	Status             string
	OperatorSeverity   string
	Severity           string
	AffectedSystemTier *string
	AffectedSystems    []byte
	DetectedBy         string
	DetectedAt         time.Time
	TriagedBy          *string
	TriagedAt          *time.Time
	ContainedBy        *string
	ContainedAt        *time.Time
	ResolvedBy         *string
	ResolvedAt         *time.Time
	ClosedBy           *string
	ClosedAt           *time.Time
	PostmortemSummary  *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Links struct {
	ControlIDs  []uuid.UUID
	RiskIDs     []uuid.UUID
	VendorIDs   []uuid.UUID
	EvidenceIDs []uuid.UUID
}

type Detail struct {
	Incident Incident
	Links    Links
	Timeline []TimelineEntry
}

type TimelineEntry struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	IncidentID uuid.UUID
	Action     string
	Actor      string
	FromState  *string
	ToState    string
	Summary    string
	Detail     []byte
	OccurredAt time.Time
}

func SeverityWithFloor(operatorSeverity string, affectedSystemTier *string) (string, error) {
	op := strings.ToLower(strings.TrimSpace(operatorSeverity))
	if _, ok := severityRank[op]; !ok {
		return "", ErrSeverityInvalid
	}
	if affectedSystemTier == nil || strings.TrimSpace(*affectedSystemTier) == "" {
		return op, nil
	}
	floor := severityFloorForTier(strings.ToLower(strings.TrimSpace(*affectedSystemTier)))
	if severityRank[floor] > severityRank[op] {
		return floor, nil
	}
	return op, nil
}

var severityRank = map[string]int{
	SeverityP3: 1,
	SeverityP2: 2,
	SeverityP1: 3,
	SeverityP0: 4,
}

func severityFloorForTier(tier string) string {
	switch tier {
	case "critical":
		return SeverityP1
	case "high":
		return SeverityP2
	default:
		return SeverityP3
	}
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Detail, error) {
	if strings.TrimSpace(in.Title) == "" {
		return Detail{}, ErrTitleRequired
	}
	if strings.TrimSpace(in.DetectedBy) == "" {
		return Detail{}, ErrActorRequired
	}
	if in.DetectedAt.IsZero() {
		in.DetectedAt = time.Now().UTC()
	}
	severity, err := SeverityWithFloor(in.OperatorSeverity, in.AffectedSystemTier)
	if err != nil {
		return Detail{}, err
	}
	affected := in.AffectedSystems
	if len(affected) == 0 {
		affected = []byte("[]")
	}
	if !json.Valid(affected) || jsonType(affected) != "array" {
		return Detail{}, ErrAffectedSystemsInvalid
	}

	var out Detail
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		row, err := q.CreateIncident(ctx, dbx.CreateIncidentParams{
			ID:                 pgUUID(id),
			TenantID:           pgUUID(tenantID),
			Title:              strings.TrimSpace(in.Title),
			Description:        strings.TrimSpace(in.Description),
			OperatorSeverity:   strings.ToLower(strings.TrimSpace(in.OperatorSeverity)),
			Severity:           severity,
			AffectedSystemTier: normalizedPtr(in.AffectedSystemTier),
			AffectedSystems:    affected,
			DetectedBy:         strings.TrimSpace(in.DetectedBy),
			DetectedAt:         pgTS(in.DetectedAt),
		})
		if err != nil {
			return mapWriteErr("create incident", err)
		}
		inc := fromRow(row)
		if err := addLinks(ctx, q, tenantID, inc.ID, in.DetectedBy, Links{
			ControlIDs:  in.ControlIDs,
			RiskIDs:     in.RiskIDs,
			VendorIDs:   in.VendorIDs,
			EvidenceIDs: in.EvidenceIDs,
		}); err != nil {
			return err
		}
		if err := writeTimeline(ctx, q, tenantID, inc.ID, ActionCreated, in.DetectedBy, nil, StateDetected, "incident logged", nil); err != nil {
			return err
		}
		out, err = detail(ctx, q, tenantID, inc)
		return err
	})
	return out, err
}

func (s *Store) List(ctx context.Context) ([]Incident, error) {
	var out []Incident
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListIncidents(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list incidents: %w", err)
		}
		out = make([]Incident, len(rows))
		for i, r := range rows {
			out[i] = fromRow(r)
		}
		return nil
	})
	return out, err
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Detail, error) {
	var out Detail
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetIncidentByID(ctx, dbx.GetIncidentByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get incident: %w", err)
		}
		out, err = detail(ctx, q, tenantID, fromRow(row))
		return err
	})
	return out, err
}

func (s *Store) Transition(ctx context.Context, id uuid.UUID, toState, actor, summary string) (Incident, error) {
	toState = strings.ToLower(strings.TrimSpace(toState))
	if strings.TrimSpace(actor) == "" {
		return Incident{}, ErrActorRequired
	}
	var out Incident
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, err := q.GetIncidentByID(ctx, dbx.GetIncidentByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get incident before transition: %w", err)
		}
		from := before.Status
		if !validTransition(from, toState) || toState == StateClosed {
			return ErrWrongState
		}
		row, err := q.TransitionIncident(ctx, dbx.TransitionIncidentParams{
			TenantID:  pgUUID(tenantID),
			ID:        pgUUID(id),
			Status:    toState,
			TriagedBy: strPtr(strings.TrimSpace(actor)),
			Status_2:  from,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWrongState
		}
		if err != nil {
			return mapWriteErr("transition incident", err)
		}
		if strings.TrimSpace(summary) == "" {
			summary = from + " -> " + toState
		}
		if err := writeTimeline(ctx, q, tenantID, id, ActionTransitioned, actor, &from, toState, summary, nil); err != nil {
			return err
		}
		out = fromRow(row)
		return nil
	})
	return out, err
}

func (s *Store) Close(ctx context.Context, id uuid.UUID, in CloseInput) (Detail, error) {
	if strings.TrimSpace(in.Actor) == "" {
		return Detail{}, ErrActorRequired
	}
	if strings.TrimSpace(in.PostmortemSummary) == "" {
		return Detail{}, ErrPostmortemRequired
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	}
	var out Detail
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		before, err := q.GetIncidentByID(ctx, dbx.GetIncidentByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get incident before close: %w", err)
		}
		if before.Status != StateResolved {
			return ErrWrongState
		}
		controls, err := q.ListIncidentControlLinks(ctx, dbx.ListIncidentControlLinksParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(id)})
		if err != nil {
			return fmt.Errorf("list incident controls for close: %w", err)
		}
		if len(controls) == 0 {
			return ErrIRControlRequired
		}
		for _, c := range controls {
			evidenceID, err := insertPostmortemEvidence(ctx, q, tenantID, fromRow(before), uuid.UUID(c.ControlID.Bytes), in)
			if err != nil {
				return err
			}
			if err := linkEvidence(ctx, q, tenantID, id, evidenceID, in.Actor); err != nil {
				return err
			}
		}
		summary := strings.TrimSpace(in.PostmortemSummary)
		row, err := q.CloseIncident(ctx, dbx.CloseIncidentParams{
			TenantID:          pgUUID(tenantID),
			ID:                pgUUID(id),
			ClosedBy:          strPtr(strings.TrimSpace(in.Actor)),
			PostmortemSummary: &summary,
		})
		if err != nil {
			return mapWriteErr("close incident", err)
		}
		from := before.Status
		if err := writeTimeline(ctx, q, tenantID, id, ActionClosed, in.Actor, &from, StateClosed, "incident closed with postmortem", nil); err != nil {
			return err
		}
		out, err = detail(ctx, q, tenantID, fromRow(row))
		return err
	})
	return out, err
}

func validTransition(from, to string) bool {
	next := map[string]string{
		StateDetected:  StateTriaged,
		StateTriaged:   StateContained,
		StateContained: StateResolved,
		StateResolved:  StateClosed,
	}
	return next[from] == to
}

func addLinks(ctx context.Context, q *dbx.Queries, tenantID, incidentID uuid.UUID, actor string, links Links) error {
	for _, id := range links.ControlIDs {
		if _, err := q.AddIncidentControlLink(ctx, dbx.AddIncidentControlLinkParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID), ControlID: pgUUID(id), LinkedBy: actor}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mapWriteErr("link incident control", err)
		}
	}
	for _, id := range links.RiskIDs {
		if _, err := q.AddIncidentRiskLink(ctx, dbx.AddIncidentRiskLinkParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID), RiskID: pgUUID(id), LinkedBy: actor}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mapWriteErr("link incident risk", err)
		}
	}
	for _, id := range links.VendorIDs {
		if _, err := q.AddIncidentVendorLink(ctx, dbx.AddIncidentVendorLinkParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID), VendorID: pgUUID(id), LinkedBy: actor}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mapWriteErr("link incident vendor", err)
		}
	}
	for _, id := range links.EvidenceIDs {
		if err := linkEvidence(ctx, q, tenantID, incidentID, id, actor); err != nil {
			return err
		}
	}
	return nil
}

func linkEvidence(ctx context.Context, q *dbx.Queries, tenantID, incidentID, evidenceID uuid.UUID, actor string) error {
	_, err := q.AddIncidentEvidenceLink(ctx, dbx.AddIncidentEvidenceLinkParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID), EvidenceID: pgUUID(evidenceID), LinkedBy: actor})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapWriteErr("link incident evidence", err)
	}
	return nil
}

func insertPostmortemEvidence(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, inc Incident, controlID uuid.UUID, in CloseInput) (uuid.UUID, error) {
	payload, _ := json.Marshal(map[string]any{
		"incident_id":        inc.ID.String(),
		"title":              inc.Title,
		"severity":           inc.Severity,
		"operator_severity":  inc.OperatorSeverity,
		"affected_systems":   json.RawMessage(inc.AffectedSystems),
		"postmortem_summary": strings.TrimSpace(in.PostmortemSummary),
		"closed_by":          strings.TrimSpace(in.Actor),
	})
	provenance, _ := json.Marshal(map[string]any{"system": "security-atlas", "source": "incident_register", "incident_id": inc.ID.String()})
	source, _ := json.Marshal(map[string]any{"type": "incident_register", "incident_id": inc.ID.String(), "actor": strings.TrimSpace(in.Actor)})
	sum := sha256.Sum256(bytes.Join([][]byte{[]byte(EvidenceKindPostmortem), []byte(inc.ID.String()), []byte(controlID.String()), payload}, []byte{0}))
	evidenceID := uuid.New()
	idem := "incident:" + inc.ID.String() + ":" + controlID.String() + ":postmortem"
	nanos := in.ObservedAt.UnixNano()
	_, err := q.InsertEvidenceRecord(ctx, dbx.InsertEvidenceRecordParams{
		ID:                pgUUID(evidenceID),
		TenantID:          pgUUID(tenantID),
		ControlID:         pgUUID(controlID),
		ControlRef:        controlID.String(),
		ScopeID:           pgtype.UUID{},
		ObservedAt:        pgTS(in.ObservedAt),
		Provenance:        provenance,
		Result:            dbx.EvidenceResultPass,
		Payload:           payload,
		PayloadUri:        nil,
		Hash:              hex.EncodeToString(sum[:]),
		FreshnessClass:    dbx.EvidenceFreshnessClassMonthly,
		ValidUntil:        pgtype.Timestamptz{},
		IdempotencyKey:    &idem,
		EvidenceKind:      strPtr(EvidenceKindPostmortem),
		SchemaVersion:     strPtr(SchemaVersion),
		CredentialID:      strPtr("system:incident-register"),
		IngestionPath:     "manual_upload",
		SourceAttribution: source,
		ScopeCanonical:    []byte("[]"),
		ObservedAtNanos:   &nanos,
	})
	if err != nil {
		return uuid.Nil, mapWriteErr("insert incident postmortem evidence", err)
	}
	return evidenceID, nil
}

func detail(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, inc Incident) (Detail, error) {
	links, err := loadLinks(ctx, q, tenantID, inc.ID)
	if err != nil {
		return Detail{}, err
	}
	timeline, err := q.ListIncidentTimeline(ctx, dbx.ListIncidentTimelineParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(inc.ID)})
	if err != nil {
		return Detail{}, fmt.Errorf("list incident timeline: %w", err)
	}
	out := Detail{Incident: inc, Links: links, Timeline: make([]TimelineEntry, len(timeline))}
	for i, r := range timeline {
		out.Timeline[i] = timelineFromRow(r)
	}
	return out, nil
}

func loadLinks(ctx context.Context, q *dbx.Queries, tenantID, incidentID uuid.UUID) (Links, error) {
	controlRows, err := q.ListIncidentControlLinks(ctx, dbx.ListIncidentControlLinksParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID)})
	if err != nil {
		return Links{}, fmt.Errorf("list incident control links: %w", err)
	}
	riskRows, err := q.ListIncidentRiskLinks(ctx, dbx.ListIncidentRiskLinksParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID)})
	if err != nil {
		return Links{}, fmt.Errorf("list incident risk links: %w", err)
	}
	vendorRows, err := q.ListIncidentVendorLinks(ctx, dbx.ListIncidentVendorLinksParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID)})
	if err != nil {
		return Links{}, fmt.Errorf("list incident vendor links: %w", err)
	}
	evidenceRows, err := q.ListIncidentEvidenceLinks(ctx, dbx.ListIncidentEvidenceLinksParams{TenantID: pgUUID(tenantID), IncidentID: pgUUID(incidentID)})
	if err != nil {
		return Links{}, fmt.Errorf("list incident evidence links: %w", err)
	}
	out := Links{
		ControlIDs:  make([]uuid.UUID, len(controlRows)),
		RiskIDs:     make([]uuid.UUID, len(riskRows)),
		VendorIDs:   make([]uuid.UUID, len(vendorRows)),
		EvidenceIDs: make([]uuid.UUID, len(evidenceRows)),
	}
	for i, r := range controlRows {
		out.ControlIDs[i] = uuid.UUID(r.ControlID.Bytes)
	}
	for i, r := range riskRows {
		out.RiskIDs[i] = uuid.UUID(r.RiskID.Bytes)
	}
	for i, r := range vendorRows {
		out.VendorIDs[i] = uuid.UUID(r.VendorID.Bytes)
	}
	for i, r := range evidenceRows {
		out.EvidenceIDs[i] = uuid.UUID(r.EvidenceID.Bytes)
	}
	return out, nil
}

func writeTimeline(ctx context.Context, q *dbx.Queries, tenantID, incidentID uuid.UUID, action, actor string, fromState *string, toState, summary string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := q.WriteIncidentTimeline(ctx, dbx.WriteIncidentTimelineParams{
		ID:         pgUUID(uuid.New()),
		TenantID:   pgUUID(tenantID),
		IncidentID: pgUUID(incidentID),
		Action:     action,
		Actor:      strings.TrimSpace(actor),
		FromState:  fromState,
		ToState:    toState,
		Summary:    summary,
		Detail:     detail,
	})
	if err != nil {
		return fmt.Errorf("write incident timeline: %w", err)
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
		return fmt.Errorf("incident: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("incident: begin tx: %w", err)
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
		return fmt.Errorf("incident: commit: %w", err)
	}
	return nil
}

func mapWriteErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return fmt.Errorf("%s: target does not exist in tenant: %w", op, err)
		case "23514":
			if pgErr.ConstraintName == "incidents_affected_systems_array_chk" {
				return ErrAffectedSystemsInvalid
			}
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func fromRow(r dbx.Incident) Incident {
	out := Incident{
		ID:               uuid.UUID(r.ID.Bytes),
		TenantID:         uuid.UUID(r.TenantID.Bytes),
		Title:            r.Title,
		Description:      r.Description,
		Status:           r.Status,
		OperatorSeverity: r.OperatorSeverity,
		Severity:         r.Severity,
		AffectedSystems:  append([]byte(nil), r.AffectedSystems...),
		DetectedBy:       r.DetectedBy,
	}
	out.AffectedSystemTier = cloneStringPtr(r.AffectedSystemTier)
	out.TriagedBy = cloneStringPtr(r.TriagedBy)
	out.ContainedBy = cloneStringPtr(r.ContainedBy)
	out.ResolvedBy = cloneStringPtr(r.ResolvedBy)
	out.ClosedBy = cloneStringPtr(r.ClosedBy)
	out.PostmortemSummary = cloneStringPtr(r.PostmortemSummary)
	if r.DetectedAt.Valid {
		out.DetectedAt = r.DetectedAt.Time
	}
	if r.TriagedAt.Valid {
		t := r.TriagedAt.Time
		out.TriagedAt = &t
	}
	if r.ContainedAt.Valid {
		t := r.ContainedAt.Time
		out.ContainedAt = &t
	}
	if r.ResolvedAt.Valid {
		t := r.ResolvedAt.Time
		out.ResolvedAt = &t
	}
	if r.ClosedAt.Valid {
		t := r.ClosedAt.Time
		out.ClosedAt = &t
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}

func timelineFromRow(r dbx.IncidentTimeline) TimelineEntry {
	out := TimelineEntry{
		ID:         uuid.UUID(r.ID.Bytes),
		TenantID:   uuid.UUID(r.TenantID.Bytes),
		IncidentID: uuid.UUID(r.IncidentID.Bytes),
		Action:     r.Action,
		Actor:      r.Actor,
		FromState:  cloneStringPtr(r.FromState),
		ToState:    r.ToState,
		Summary:    r.Summary,
		Detail:     append([]byte(nil), r.Detail...),
	}
	if r.OccurredAt.Valid {
		out.OccurredAt = r.OccurredAt.Time
	}
	return out
}

func jsonType(raw []byte) string {
	var v any
	_ = json.Unmarshal(raw, &v)
	switch v.(type) {
	case []any:
		return "array"
	default:
		return ""
	}
}

func normalizedPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.ToLower(strings.TrimSpace(*p))
	if v == "" {
		return nil
	}
	return &v
}

func cloneStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func strPtr(s string) *string {
	v := s
	return &v
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}
