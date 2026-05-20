// store.go — the slice 054 aggregation-rule Store: rule CRUD, the HITL
// activate/deactivate lifecycle transitions, and the append-only audit-log
// writes that record who flipped a rule and when.
//
// Like risk.Store and scope.Store, every method opens a transaction, applies
// the tenant GUC so RLS fires, and runs queries inside it. The pool must be
// connected as `atlas_app` (NOSUPERUSER NOBYPASSRLS).
package aggrule

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

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// emitSinkAggregationRule is the slice-126 fanout helper for the three
// per-rule lifecycle write sites (create/activate/deactivate). Non-blocking;
// never returns an error.
func emitSinkAggregationRule(ctx context.Context, tenantID, ruleID, auditID uuid.UUID, event, actor, fromStatus, toStatus string) {
	payload, _ := json.Marshal(map[string]any{
		"from_status": fromStatus,
		"to_status":   toStatus,
	})
	sink.EmitDefault(ctx, unifiedlog.Entry{
		OccurredAt:    time.Now().UTC(),
		ActorID:       actor,
		TenantID:      tenantID,
		Kind:          unifiedlog.KindAggregationRule,
		TargetType:    "aggregation_rule",
		TargetID:      ruleID.String(),
		Action:        event,
		RowID:         auditID,
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   payload,
	})
}

// Sentinel errors. Kept as package sentinels (not pgx.ErrNoRows) so the HTTP
// handler maps them without importing pgx.
var (
	// ErrNotFound — a tenant-scoped rule lookup yielded zero rows.
	ErrNotFound = errors.New("aggrule: rule not found")
	// ErrWrongState — an activate/deactivate transition was attempted from
	// a status that does not allow it (e.g. activate on an already-active
	// rule, deactivate on a staged rule).
	ErrWrongState = errors.New("aggrule: rule is not in a state that allows this transition")
	// ErrDuplicateRuleID — a rule with this rule_id already exists for the
	// tenant (the DB UNIQUE (tenant_id, rule_id) constraint fired).
	ErrDuplicateRuleID = errors.New("aggrule: a rule with this rule_id already exists")
)

// Store wraps the sqlc Queries with tenancy plumbing for the aggregation_rules
// tables.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over an existing pgx pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// StoredRule is the domain shape returned from Store calls — the typed
// columns plus the parsed Rule body.
type StoredRule struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Rule        Rule
	Status      string // staged | active | inactive
	ActivatedBy *string
	ActivatedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AuditEvent is one row of the append-only aggregation_rule_audit_log.
type AuditEvent struct {
	ID         uuid.UUID
	RuleID     uuid.UUID
	Event      string // created | activated | deactivated | reactivated | threshold_changed
	Actor      string
	FromStatus *string
	ToStatus   *string
	Detail     json.RawMessage
	CreatedAt  time.Time
}

// Create persists a new aggregation rule. The rule ALWAYS lands as `staged` —
// the HITL gate. The caller (HTTP handler) must have validated the Rule
// already; Create re-validates as defense in depth and writes a `created`
// audit-log row naming the actor.
func (s *Store) Create(ctx context.Context, r Rule, actor string) (StoredRule, error) {
	if err := r.Validate(); err != nil {
		return StoredRule{}, err
	}
	body, err := r.CanonicalJSON()
	if err != nil {
		return StoredRule{}, fmt.Errorf("aggrule: marshal rule body: %w", err)
	}

	var out StoredRule
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		row, err := q.CreateAggregationRule(ctx, dbx.CreateAggregationRuleParams{
			ID:               pgUUID(id),
			TenantID:         pgUUID(tenantID),
			RuleID:           r.RuleID,
			TargetTheme:      r.TargetTheme,
			MinRisks:         toInt32(r.MinRisks),
			MinTeams:         toInt32(r.MinTeams),
			WindowDays:       toInt32(r.WindowDays),
			ParentLevel:      dbx.RiskLevel(r.ParentLevel),
			SeverityFunction: r.SeverityFunction,
			RuleBody:         body,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateRuleID
			}
			return fmt.Errorf("aggrule: create rule: %w", err)
		}
		auditID := uuid.New()
		if _, err := q.WriteAggregationRuleAuditLog(ctx, dbx.WriteAggregationRuleAuditLogParams{
			ID:         pgUUID(auditID),
			TenantID:   pgUUID(tenantID),
			RuleID:     row.ID,
			Event:      "created",
			Actor:      actor,
			FromStatus: nil,
			ToStatus:   strPtr("staged"),
			Detail:     []byte("{}"),
		}); err != nil {
			return fmt.Errorf("aggrule: write created audit log: %w", err)
		}
		// Slice 126: fan out to the external sink.
		emitSinkAggregationRule(ctx, tenantID, uuid.UUID(row.ID.Bytes), auditID,
			"created", actor, "", "staged")
		sr, cerr := storedRuleFromRow(row)
		if cerr != nil {
			return cerr
		}
		out = sr
		return nil
	})
	return out, err
}

// Get returns a single rule by id. ErrNotFound when absent.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (StoredRule, error) {
	var out StoredRule
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetAggregationRuleByID(ctx, dbx.GetAggregationRuleByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("aggrule: get rule: %w", err)
		}
		sr, cerr := storedRuleFromRow(row)
		if cerr != nil {
			return cerr
		}
		out = sr
		return nil
	})
	return out, err
}

// List returns every rule for the active tenant, newest first. The optional
// statusFilter narrows the result in memory (cardinality is small — a solo
// security lead runs a handful of rules).
func (s *Store) List(ctx context.Context, statusFilter string) ([]StoredRule, error) {
	var out []StoredRule
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAggregationRules(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("aggrule: list rules: %w", err)
		}
		out = make([]StoredRule, 0, len(rows))
		for _, row := range rows {
			if statusFilter != "" && row.Status != statusFilter {
				continue
			}
			sr, cerr := storedRuleFromRow(row)
			if cerr != nil {
				return cerr
			}
			out = append(out, sr)
		}
		return nil
	})
	return out, err
}

// Activate is the HITL transition: staged -> active (or inactive -> active,
// a re-activation). It sets activated_by + activated_at and writes an
// `activated` (or `reactivated`) audit-log row. ErrWrongState when the rule
// is already active; ErrNotFound when no such rule exists.
//
// activated_at becomes the engine's "do not consider risks older than this"
// cut-off, so a re-activation never re-fires on stale data (anti-criterion P0).
func (s *Store) Activate(ctx context.Context, id uuid.UUID, actor string) (StoredRule, error) {
	var out StoredRule
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Read the current row first so we can disambiguate
		// missing-rule (ErrNotFound) from wrong-state (ErrWrongState) and
		// record the correct from/to + event in the audit log.
		current, err := q.GetAggregationRuleByID(ctx, dbx.GetAggregationRuleByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("aggrule: activate get: %w", err)
		}
		if current.Status == "active" {
			return ErrWrongState
		}
		fromStatus := current.Status
		event := "activated"
		if fromStatus == "inactive" {
			event = "reactivated"
		}

		row, err := q.ActivateAggregationRule(ctx, dbx.ActivateAggregationRuleParams{
			TenantID:    pgUUID(tenantID),
			ID:          pgUUID(id),
			ActivatedBy: strPtr(actor),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Lost a race — the WHERE status guard refused the update.
				return ErrWrongState
			}
			return fmt.Errorf("aggrule: activate rule: %w", err)
		}
		auditID := uuid.New()
		if _, err := q.WriteAggregationRuleAuditLog(ctx, dbx.WriteAggregationRuleAuditLogParams{
			ID:         pgUUID(auditID),
			TenantID:   pgUUID(tenantID),
			RuleID:     row.ID,
			Event:      event,
			Actor:      actor,
			FromStatus: strPtr(fromStatus),
			ToStatus:   strPtr("active"),
			Detail:     []byte("{}"),
		}); err != nil {
			return fmt.Errorf("aggrule: write %s audit log: %w", event, err)
		}
		// Slice 126: fan out to the external sink.
		emitSinkAggregationRule(ctx, tenantID, uuid.UUID(row.ID.Bytes), auditID,
			event, actor, fromStatus, "active")
		sr, cerr := storedRuleFromRow(row)
		if cerr != nil {
			return cerr
		}
		out = sr
		return nil
	})
	return out, err
}

// Deactivate is the active -> inactive transition. It stops new firings while
// preserving historical meta-risks, and writes a `deactivated` audit-log row.
// ErrWrongState when the rule is not currently active.
func (s *Store) Deactivate(ctx context.Context, id uuid.UUID, actor string) (StoredRule, error) {
	var out StoredRule
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		current, err := q.GetAggregationRuleByID(ctx, dbx.GetAggregationRuleByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("aggrule: deactivate get: %w", err)
		}
		if current.Status != "active" {
			return ErrWrongState
		}

		row, err := q.DeactivateAggregationRule(ctx, dbx.DeactivateAggregationRuleParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWrongState
			}
			return fmt.Errorf("aggrule: deactivate rule: %w", err)
		}
		auditID := uuid.New()
		if _, err := q.WriteAggregationRuleAuditLog(ctx, dbx.WriteAggregationRuleAuditLogParams{
			ID:         pgUUID(auditID),
			TenantID:   pgUUID(tenantID),
			RuleID:     row.ID,
			Event:      "deactivated",
			Actor:      actor,
			FromStatus: strPtr("active"),
			ToStatus:   strPtr("inactive"),
			Detail:     []byte("{}"),
		}); err != nil {
			return fmt.Errorf("aggrule: write deactivated audit log: %w", err)
		}
		// Slice 126: fan out to the external sink.
		emitSinkAggregationRule(ctx, tenantID, uuid.UUID(row.ID.Bytes), auditID,
			"deactivated", actor, "active", "inactive")
		sr, cerr := storedRuleFromRow(row)
		if cerr != nil {
			return cerr
		}
		out = sr
		return nil
	})
	return out, err
}

// AuditLog returns the append-only event history for one rule, newest first.
func (s *Store) AuditLog(ctx context.Context, ruleID uuid.UUID) ([]AuditEvent, error) {
	var out []AuditEvent
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAggregationRuleAuditLog(ctx, dbx.ListAggregationRuleAuditLogParams{
			TenantID: pgUUID(tenantID),
			RuleID:   pgUUID(ruleID),
		})
		if err != nil {
			return fmt.Errorf("aggrule: list audit log: %w", err)
		}
		out = make([]AuditEvent, len(rows))
		for i, row := range rows {
			out[i] = AuditEvent{
				ID:         uuid.UUID(row.ID.Bytes),
				RuleID:     uuid.UUID(row.RuleID.Bytes),
				Event:      row.Event,
				Actor:      row.Actor,
				FromStatus: row.FromStatus,
				ToStatus:   row.ToStatus,
				Detail:     json.RawMessage(row.Detail),
			}
			if row.CreatedAt.Valid {
				out[i].CreatedAt = row.CreatedAt.Time
			}
		}
		return nil
	})
	return out, err
}

// Evaluations returns the append-only evaluation ledger for one rule, newest
// first — the auditor view (AC-8).
func (s *Store) Evaluations(ctx context.Context, ruleID uuid.UUID) ([]Evaluation, error) {
	var out []Evaluation
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAggregationRuleEvaluations(ctx, dbx.ListAggregationRuleEvaluationsParams{
			TenantID: pgUUID(tenantID),
			RuleID:   pgUUID(ruleID),
		})
		if err != nil {
			return fmt.Errorf("aggrule: list evaluations: %w", err)
		}
		out = make([]Evaluation, len(rows))
		for i, row := range rows {
			ev := Evaluation{
				ID:        uuid.UUID(row.ID.Bytes),
				RuleID:    uuid.UUID(row.RuleID.Bytes),
				Outcome:   row.Outcome,
				RiskCount: int(row.RiskCount),
				TeamCount: int(row.TeamCount),
			}
			if row.EvaluatedAt.Valid {
				ev.EvaluatedAt = row.EvaluatedAt.Time
			}
			if row.WindowStart.Valid {
				ws := row.WindowStart.Time
				ev.WindowStart = &ws
			}
			if row.MetaRiskID.Valid {
				mr := uuid.UUID(row.MetaRiskID.Bytes)
				ev.MetaRiskID = &mr
			}
			out[i] = ev
		}
		return nil
	})
	return out, err
}

// Evaluate runs the aggregation engine for every active rule of the active
// tenant, inside one transaction. triggerThemes narrows which rules run: a
// rule only evaluates when its target_theme is in triggerThemes (the themes
// of the risk that was just written). Pass nil to evaluate every active rule.
func (s *Store) Evaluate(ctx context.Context, triggerThemes []string) ([]Evaluation, error) {
	var out []Evaluation
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		eng := NewEngine(q)
		evals, eerr := eng.Evaluate(ctx, tenantID, triggerThemes)
		if eerr != nil {
			return eerr
		}
		out = evals
		return nil
	})
	return out, err
}

// inTx mirrors risk.Store.inTx — sets the tenant GUC inside a transaction so
// RLS sees it, runs fn, commits on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("aggrule: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("aggrule: begin tx: %w", err)
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
		return fmt.Errorf("aggrule: commit: %w", err)
	}
	return nil
}

// ----- conversion helpers -----

func storedRuleFromRow(row dbx.AggregationRule) (StoredRule, error) {
	var rule Rule
	if err := json.Unmarshal(row.RuleBody, &rule); err != nil {
		return StoredRule{}, fmt.Errorf("aggrule: unmarshal rule_body for %s: %w",
			uuid.UUID(row.ID.Bytes), err)
	}
	sr := StoredRule{
		ID:          uuid.UUID(row.ID.Bytes),
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		Rule:        rule,
		Status:      row.Status,
		ActivatedBy: row.ActivatedBy,
	}
	if row.ActivatedAt.Valid {
		t := row.ActivatedAt.Time
		sr.ActivatedAt = &t
	}
	if row.CreatedAt.Valid {
		sr.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		sr.UpdatedAt = row.UpdatedAt.Time
	}
	return sr, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func strPtr(s string) *string { return &s }

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
