// Package audit owns the audit-workflow primitives: populations (a tenant-
// scoped target evidence set defined by control + scope predicate + time
// window) and samples (a deterministic, reproducible draw of N records
// from a population), plus per-record auditor annotations and the
// append-only sample audit log.
//
// Slice 026 introduces this package. Slice 028 will add AuditPeriod (the
// owner of frozen_at) on top of the schema laid down here. Slice 027
// (walkthroughs) and slice 029 (audit hub comments) join in alongside.
//
// The package is a thin orchestration layer around three concerns:
//
//  1. Populations: build a Population row from (control_id, scope_predicate,
//     time_window), count the matching evidence rows, persist the count.
//     Scope predicate intersection is computed in Go using the
//     internal/scope JSON-AST evaluator (we don't push the predicate down
//     into SQL on v1; the cell universe is small enough).
//
//  2. Samples: take a Population, an N, and a seed text, hydrate the
//     stable id-ordered evidence list from Postgres, hand it to the
//     internal/audit/sample partial Fisher-Yates sampler, and persist
//     the resulting N record ids into sample_evidence + a samples row.
//
//  3. Annotations: per-record auditor decision (passed | failed |
//     not-applicable) plus freeform notes. Idempotent UPSERT keyed by
//     (sample_id, evidence_record_id) so re-annotating doesn't dup.
//
// Every store method opens a single transaction and applies the tenant
// GUC via internal/tenancy. This is the same pattern as scope.Store,
// risk.Store, and frameworkscope.Store. RLS is the safety net, not the
// gate.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/sample"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrNotFound is returned when a tenant-scoped lookup yields zero rows.
// Sentinel (not pgx.ErrNoRows) so callers do not need to import pgx.
var ErrNotFound = errors.New("audit: not found")

// ErrEmptyPopulation is returned when a sample is requested against a
// population that resolves to zero records.
var ErrEmptyPopulation = errors.New("audit: population is empty")

// ErrInvalidAnnotation is returned when a write-side annotation has an
// out-of-range result value (handler-level CHECK; DB CHECK is the second
// line of defense).
var ErrInvalidAnnotation = errors.New("audit: invalid annotation result")

// AnnotationResults enumerates the valid result values for an annotation.
// Mirrors the DB CHECK constraint on sample_annotations.result.
var AnnotationResults = map[string]struct{}{
	"passed":         {},
	"failed":         {},
	"not-applicable": {},
}

// Store is the entry point for slice-026 read/write operations. Constructed
// with NewStore over an existing pgx pool; that pool MUST be connected as
// atlas_app (NOSUPERUSER NOBYPASSRLS) so RLS is actually enforced.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the Store. The pool is held but not owned -- callers
// (typically internal/api.New) close it.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ----- domain types -----

// Population is the public shape returned from CreatePopulation and
// GetPopulation. It is the auditor-facing representation; the raw dbx.Population
// row stays inside the package.
type Population struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	ControlID       uuid.UUID
	ScopePredicate  json.RawMessage
	TimeWindowStart time.Time
	TimeWindowEnd   time.Time
	FrozenAt        *time.Time
	RowCount        int64
	CreatedBy       string
	CreatedAt       time.Time
}

// Sample mirrors the samples row plus its realized evidence list.
type Sample struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	PopulationID      uuid.UUID
	N                 int
	Seed              string
	CreatedBy         string
	CreatedAt         time.Time
	EvidenceRecordIDs []uuid.UUID
}

// Annotation is one auditor decision against one sampled evidence record.
type Annotation struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	SampleID         uuid.UUID
	EvidenceRecordID uuid.UUID
	Result           string
	AnnotatedBy      string
	AnnotatedAt      time.Time
	Notes            string
}

// ----- inputs -----

// CreatePopulationInput is the API-shape for POST /v1/populations.
type CreatePopulationInput struct {
	ControlID       uuid.UUID
	ScopePredicate  json.RawMessage
	TimeWindowStart time.Time
	TimeWindowEnd   time.Time
	CreatedBy       string
}

// DrawSampleInput is the API-shape for POST /v1/samples.
type DrawSampleInput struct {
	PopulationID uuid.UUID
	N            int
	Seed         string
	CreatedBy    string
}

// AnnotateSampleInput is the API-shape for POST /v1/samples/{id}/annotations.
type AnnotateSampleInput struct {
	SampleID         uuid.UUID
	EvidenceRecordID uuid.UUID
	Result           string
	AnnotatedBy      string
	Notes            string
}

// ----- public methods -----

// CreatePopulation builds a Population row from in: counts the matching
// evidence records (respecting AC-5 frozen_at horizon), persists the
// population, and writes a population_created audit log row. Returns the
// hydrated Population.
//
// Scope-predicate intersection: if in.ScopePredicate is non-empty and
// non-trivial, the count is taken AFTER filtering evidence_records.scope_id
// against the predicate's matching cell set. v1 evaluates the predicate in
// Go (internal/scope.Evaluate) -- the cell universe is small.
func (s *Store) CreatePopulation(ctx context.Context, in CreatePopulationInput) (Population, error) {
	if in.CreatedBy == "" {
		return Population{}, fmt.Errorf("audit: created_by must be non-empty")
	}
	if in.TimeWindowStart.After(in.TimeWindowEnd) {
		return Population{}, fmt.Errorf("audit: time_window_start must be <= time_window_end")
	}
	predicate := canonicalPredicate(in.ScopePredicate)

	var out Population
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Evaluate predicate -> resolve the matching evidence ids. For the
		// row_count we walk the same path as ListPopulationEvidenceIDs so
		// the count is exact (we apply the in-Go scope filter, not a SQL
		// count that ignores scope).
		ids, err := resolvePopulationEvidenceIDs(ctx, q, tenantID, in, predicate)
		if err != nil {
			return err
		}

		row, err := q.CreatePopulation(ctx, dbx.CreatePopulationParams{
			ID:              pgUUID(uuid.New()),
			TenantID:        pgUUID(tenantID),
			ControlID:       pgUUID(in.ControlID),
			ScopePredicate:  predicate,
			TimeWindowStart: pgTimestamptz(in.TimeWindowStart),
			TimeWindowEnd:   pgTimestamptz(in.TimeWindowEnd),
			FrozenAt:        pgtype.Timestamptz{}, // slice 028 sets this
			RowCount:        int64(len(ids)),
			CreatedBy:       in.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create population: %w", err)
		}
		if err := writeAuditLog(ctx, q, tenantID, auditLogEntry{
			Action:       "population_created",
			Actor:        in.CreatedBy,
			PopulationID: &row.ID,
			NRequested:   nullableInt32(int32(len(ids))),
			NReturned:    nullableInt32(int32(len(ids))),
		}); err != nil {
			return err
		}
		out = populationFromRow(row)
		return nil
	})
	return out, err
}

// GetPopulation returns one population by id. ErrNotFound if absent.
func (s *Store) GetPopulation(ctx context.Context, id uuid.UUID) (Population, error) {
	var out Population
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetPopulationByID(ctx, dbx.GetPopulationByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get population: %w", err)
		}
		out = populationFromRow(row)
		return nil
	})
	return out, err
}

// DrawSample resolves the population's evidence list, hands it to the
// deterministic sampler, persists the result (one samples row + N
// sample_evidence rows), and writes a sample_drawn audit log row. Returns
// the hydrated Sample. Same seed + same population => identical output.
func (s *Store) DrawSample(ctx context.Context, in DrawSampleInput) (Sample, error) {
	if in.N <= 0 {
		return Sample{}, fmt.Errorf("audit: n must be positive")
	}
	if in.Seed == "" {
		return Sample{}, fmt.Errorf("audit: seed must be non-empty")
	}
	if in.CreatedBy == "" {
		return Sample{}, fmt.Errorf("audit: created_by must be non-empty")
	}

	var out Sample
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		pop, err := q.GetPopulationByID(ctx, dbx.GetPopulationByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.PopulationID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get population for sample: %w", err)
		}

		ids, err := resolvePopulationEvidenceIDsFromRow(ctx, q, tenantID, pop)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			_ = writeAuditLog(ctx, q, tenantID, auditLogEntry{
				Action:       "sample_rejected",
				Actor:        in.CreatedBy,
				PopulationID: &pop.ID,
				Seed:         &in.Seed,
				NRequested:   nullableInt32(int32(in.N)),
				ReasonCode:   "empty_population",
			})
			return ErrEmptyPopulation
		}

		drawn, err := sample.Sample(ids, in.N, in.Seed)
		if err != nil {
			return fmt.Errorf("sample: %w", err)
		}

		sampleID := uuid.New()
		sampleRow, err := q.CreateSample(ctx, dbx.CreateSampleParams{
			ID:           pgUUID(sampleID),
			TenantID:     pgUUID(tenantID),
			PopulationID: pop.ID,
			N:            int32(len(drawn)),
			Seed:         in.Seed,
			CreatedBy:    in.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create sample: %w", err)
		}
		for i, recID := range drawn {
			if err := q.InsertSampleEvidence(ctx, dbx.InsertSampleEvidenceParams{
				SampleID:         sampleRow.ID,
				TenantID:         pgUUID(tenantID),
				EvidenceRecordID: pgUUID(recID),
				Ordinal:          int32(i),
			}); err != nil {
				return fmt.Errorf("insert sample_evidence ordinal=%d: %w", i, err)
			}
		}
		if err := writeAuditLog(ctx, q, tenantID, auditLogEntry{
			Action:       "sample_drawn",
			Actor:        in.CreatedBy,
			PopulationID: &pop.ID,
			SampleID:     &sampleRow.ID,
			Seed:         &in.Seed,
			NRequested:   nullableInt32(int32(in.N)),
			NReturned:    nullableInt32(int32(len(drawn))),
		}); err != nil {
			return err
		}
		out = sampleFromRow(sampleRow)
		out.EvidenceRecordIDs = drawn
		return nil
	})
	return out, err
}

// GetSample returns one sample with its evidence list hydrated.
func (s *Store) GetSample(ctx context.Context, id uuid.UUID) (Sample, error) {
	var out Sample
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetSampleByID(ctx, dbx.GetSampleByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get sample: %w", err)
		}
		evRows, err := q.ListSampleEvidence(ctx, dbx.ListSampleEvidenceParams{
			TenantID: pgUUID(tenantID),
			SampleID: row.ID,
		})
		if err != nil {
			return fmt.Errorf("list sample evidence: %w", err)
		}
		out = sampleFromRow(row)
		out.EvidenceRecordIDs = make([]uuid.UUID, len(evRows))
		for i, r := range evRows {
			out.EvidenceRecordIDs[i] = uuid.UUID(r.EvidenceRecordID.Bytes)
		}
		return nil
	})
	return out, err
}

// AnnotateSample upserts one annotation against one (sample, evidence_record)
// pair. Re-annotating overwrites the previous result + notes. Each call
// writes one sample_annotated audit log row for the re-audit trail.
func (s *Store) AnnotateSample(ctx context.Context, in AnnotateSampleInput) (Annotation, error) {
	if _, ok := AnnotationResults[in.Result]; !ok {
		return Annotation{}, ErrInvalidAnnotation
	}
	if in.AnnotatedBy == "" {
		return Annotation{}, fmt.Errorf("audit: annotated_by must be non-empty")
	}

	var out Annotation
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Validate the sample belongs to the tenant first; the FK on
		// sample_annotations enforces this at the DB layer, but we return
		// a clean ErrNotFound rather than a 23503.
		if _, err := q.GetSampleByID(ctx, dbx.GetSampleByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(in.SampleID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("annotate get sample: %w", err)
		}

		row, err := q.UpsertSampleAnnotation(ctx, dbx.UpsertSampleAnnotationParams{
			ID:               pgUUID(uuid.New()),
			TenantID:         pgUUID(tenantID),
			SampleID:         pgUUID(in.SampleID),
			EvidenceRecordID: pgUUID(in.EvidenceRecordID),
			Result:           in.Result,
			AnnotatedBy:      in.AnnotatedBy,
			Notes:            in.Notes,
		})
		if err != nil {
			return fmt.Errorf("upsert annotation: %w", err)
		}
		if err := writeAuditLog(ctx, q, tenantID, auditLogEntry{
			Action:   "sample_annotated",
			Actor:    in.AnnotatedBy,
			SampleID: &row.SampleID,
		}); err != nil {
			return err
		}
		out = annotationFromRow(row)
		return nil
	})
	return out, err
}

// ListAnnotations returns the annotations for one sample, newest first.
func (s *Store) ListAnnotations(ctx context.Context, sampleID uuid.UUID) ([]Annotation, error) {
	var out []Annotation
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListSampleAnnotations(ctx, dbx.ListSampleAnnotationsParams{
			TenantID: pgUUID(tenantID),
			SampleID: pgUUID(sampleID),
		})
		if err != nil {
			return fmt.Errorf("list annotations: %w", err)
		}
		out = make([]Annotation, len(rows))
		for i, r := range rows {
			out[i] = annotationFromRow(r)
		}
		return nil
	})
	return out, err
}

// ListAuditLog returns the most recent N audit log rows for the tenant.
// Used by re-audit flows (AC-6) and the audit hub view.
func (s *Store) ListAuditLog(ctx context.Context, limit int) ([]dbx.SampleAuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []dbx.SampleAuditLog
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListSampleAuditLog(ctx, dbx.ListSampleAuditLogParams{
			TenantID: pgUUID(tenantID),
			Limit:    int32(limit),
		})
		if err != nil {
			return fmt.Errorf("list audit log: %w", err)
		}
		out = rows
		return nil
	})
	return out, err
}

// ----- internals -----

// resolvePopulationEvidenceIDs walks the evidence_records ledger for the
// inputs and applies the scope predicate filter in memory. Returns the
// stable id-ordered slice the sampler then operates on.
func resolvePopulationEvidenceIDs(
	ctx context.Context, q *dbx.Queries, tenantID uuid.UUID,
	in CreatePopulationInput, predicate []byte,
) ([]uuid.UUID, error) {
	rows, err := q.ListPopulationEvidenceIDs(ctx, dbx.ListPopulationEvidenceIDsParams{
		TenantID:     pgUUID(tenantID),
		ControlID:    pgUUID(in.ControlID),
		ObservedAt:   pgTimestamptz(in.TimeWindowStart),
		ObservedAt_2: pgTimestamptz(in.TimeWindowEnd),
		FrozenAt:     pgtype.Timestamptz{}, // CreatePopulation horizon is NULL (live)
	})
	if err != nil {
		return nil, fmt.Errorf("list population evidence: %w", err)
	}
	return applyScopeFilter(ctx, q, tenantID, predicate, rows)
}

// resolvePopulationEvidenceIDsFromRow is the DrawSample-time equivalent:
// honors the population's persisted frozen_at horizon (which is NULL until
// slice 028 ships).
func resolvePopulationEvidenceIDsFromRow(
	ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, pop dbx.Population,
) ([]uuid.UUID, error) {
	rows, err := q.ListPopulationEvidenceIDs(ctx, dbx.ListPopulationEvidenceIDsParams{
		TenantID:     pop.TenantID,
		ControlID:    pop.ControlID,
		ObservedAt:   pop.TimeWindowStart,
		ObservedAt_2: pop.TimeWindowEnd,
		FrozenAt:     pop.FrozenAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list population evidence (from row): %w", err)
	}
	return applyScopeFilter(ctx, q, tenantID, pop.ScopePredicate, rows)
}

// applyScopeFilter intersects the candidate evidence row scope_ids with the
// scope-predicate matching cell set. If the predicate is the trivial
// "match all" form, we skip the cell-universe walk entirely.
func applyScopeFilter(
	ctx context.Context, q *dbx.Queries, tenantID uuid.UUID,
	predicate []byte, candidates []dbx.ListPopulationEvidenceIDsRow,
) ([]uuid.UUID, error) {
	if isTrivialPredicate(predicate) {
		out := make([]uuid.UUID, len(candidates))
		for i, c := range candidates {
			out[i] = uuid.UUID(c.ID.Bytes)
		}
		return out, nil
	}

	cellRows, err := q.ListScopeCells(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list scope cells: %w", err)
	}
	universe := make([]scope.Cell, len(cellRows))
	for i, r := range cellRows {
		c := scope.Cell{
			ID:       uuid.UUID(r.ID.Bytes),
			TenantID: uuid.UUID(r.TenantID.Bytes),
			Label:    r.Label,
		}
		// Decode dimensions; tolerate corruption (treat as no-dims cell)
		// rather than failing the whole sample pull.
		var dims map[string]string
		if err := json.Unmarshal(r.Dimensions, &dims); err == nil {
			c.Dimensions = dims
		}
		universe[i] = c
	}
	matching, err := scope.Evaluate(predicate, universe)
	if err != nil {
		return nil, fmt.Errorf("evaluate scope predicate: %w", err)
	}
	allowed := make(map[uuid.UUID]struct{}, len(matching))
	for _, m := range matching {
		allowed[m.ID] = struct{}{}
	}

	out := make([]uuid.UUID, 0, len(candidates))
	for _, c := range candidates {
		// Evidence with a NULL scope_id falls outside every predicate by
		// definition; predicate-scoped populations exclude unsotred
		// evidence. Auditors who want everything can pass an empty
		// predicate (handled by isTrivialPredicate above).
		if !c.ScopeID.Valid {
			continue
		}
		scopeID := uuid.UUID(c.ScopeID.Bytes)
		if _, ok := allowed[scopeID]; ok {
			out = append(out, uuid.UUID(c.ID.Bytes))
		}
	}
	return out, nil
}

// isTrivialPredicate detects the "match every cell" shapes: nil, empty,
// "{}" object, JSON null, and the literal {"op":"true"} canonical form.
func isTrivialPredicate(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	// Strict: only the three canonical forms.
	switch string(b) {
	case "null", "{}":
		return true
	}
	// Tolerant parse for {"op":"true"} regardless of whitespace.
	var probe struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(b, &probe); err == nil && probe.Op == "true" {
		return true
	}
	return false
}

// canonicalPredicate normalizes the caller's predicate JSON. nil / empty
// becomes the canonical {"op":"true"} form so the persisted column always
// has a parseable AST.
func canonicalPredicate(in json.RawMessage) []byte {
	if len(in) == 0 {
		return []byte(`{"op":"true"}`)
	}
	return []byte(in)
}

// ----- audit log helper -----

type auditLogEntry struct {
	Action       string
	Actor        string
	PopulationID *pgtype.UUID
	SampleID     *pgtype.UUID
	Seed         *string
	NRequested   *int32
	NReturned    *int32
	ReasonCode   string
}

func writeAuditLog(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, e auditLogEntry) error {
	var popID, sampID pgtype.UUID
	if e.PopulationID != nil {
		popID = *e.PopulationID
	}
	if e.SampleID != nil {
		sampID = *e.SampleID
	}
	var seed *string
	if e.Seed != nil {
		s := *e.Seed
		seed = &s
	}
	auditID := uuid.New()
	_, err := q.WriteSampleAuditLog(ctx, dbx.WriteSampleAuditLogParams{
		ID:           pgUUID(auditID),
		TenantID:     pgUUID(tenantID),
		Action:       e.Action,
		Actor:        e.Actor,
		PopulationID: popID,
		SampleID:     sampID,
		Seed:         seed,
		NRequested:   e.NRequested,
		NReturned:    e.NReturned,
		ReasonCode:   e.ReasonCode,
	})
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	// Slice 126: fan out to the external sink. Target id is the sample id
	// when present, else the population id (one of the two is always set).
	targetType, targetID := "sample", ""
	if sampID.Valid {
		targetID = uuid.UUID(sampID.Bytes).String()
	} else if popID.Valid {
		targetType = "population"
		targetID = uuid.UUID(popID.Bytes).String()
	}
	sinkPayload, _ := json.Marshal(map[string]any{
		"seed":        e.Seed,
		"n_requested": e.NRequested,
		"n_returned":  e.NReturned,
		"reason_code": e.ReasonCode,
	})
	sink.EmitDefault(ctx, unifiedlog.Entry{
		OccurredAt:    time.Now().UTC(),
		ActorID:       e.Actor,
		TenantID:      tenantID,
		Kind:          unifiedlog.KindSample,
		TargetType:    targetType,
		TargetID:      targetID,
		Action:        e.Action,
		RowID:         auditID,
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   sinkPayload,
	})
	return nil
}

// ----- inTx -----

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("audit: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
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
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}

// ----- row converters -----

func populationFromRow(r dbx.Population) Population {
	out := Population{
		ID:             uuid.UUID(r.ID.Bytes),
		TenantID:       uuid.UUID(r.TenantID.Bytes),
		ControlID:      uuid.UUID(r.ControlID.Bytes),
		ScopePredicate: json.RawMessage(r.ScopePredicate),
		RowCount:       r.RowCount,
		CreatedBy:      r.CreatedBy,
	}
	if r.TimeWindowStart.Valid {
		out.TimeWindowStart = r.TimeWindowStart.Time
	}
	if r.TimeWindowEnd.Valid {
		out.TimeWindowEnd = r.TimeWindowEnd.Time
	}
	if r.FrozenAt.Valid {
		t := r.FrozenAt.Time
		out.FrozenAt = &t
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	return out
}

func sampleFromRow(r dbx.Sample) Sample {
	out := Sample{
		ID:           uuid.UUID(r.ID.Bytes),
		TenantID:     uuid.UUID(r.TenantID.Bytes),
		PopulationID: uuid.UUID(r.PopulationID.Bytes),
		N:            int(r.N),
		Seed:         r.Seed,
		CreatedBy:    r.CreatedBy,
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	return out
}

func annotationFromRow(r dbx.SampleAnnotation) Annotation {
	out := Annotation{
		ID:               uuid.UUID(r.ID.Bytes),
		TenantID:         uuid.UUID(r.TenantID.Bytes),
		SampleID:         uuid.UUID(r.SampleID.Bytes),
		EvidenceRecordID: uuid.UUID(r.EvidenceRecordID.Bytes),
		Result:           r.Result,
		AnnotatedBy:      r.AnnotatedBy,
		Notes:            r.Notes,
	}
	if r.AnnotatedAt.Valid {
		out.AnnotatedAt = r.AnnotatedAt.Time
	}
	return out
}

// ----- pg helpers -----

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func nullableInt32(v int32) *int32 {
	return &v
}
