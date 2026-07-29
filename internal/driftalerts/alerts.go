// Package driftalerts produces push-alert notification rows when the existing
// freshness/drift read models change state. Delivery is intentionally left to
// internal/notify/scheduler and its Slack/webhook sinks.
package driftalerts

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

	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	DefaultDebounceInterval = 15 * time.Minute
	DriftViewPath           = "/dashboard#control-drift"
	FreshnessViewPath       = "/dashboard#evidence-freshness"
)

type Config struct {
	Enabled                  bool
	SlackEnabled             bool
	PagerDutyEnabled         bool
	ControlDriftEnabled      bool
	EvidenceStalenessEnabled bool
	MinDriftedControls       int
	MinStaleAge              time.Duration
	DebounceInterval         time.Duration
}

type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

type TenantReport struct {
	Recipients       int
	DriftWritten     int
	DriftDeduped     int
	StalenessWritten int
	StalenessDeduped int
}

type Evaluation struct {
	BeforeFreshness []freshness.ControlFreshness
	AfterFreshness  []freshness.ControlFreshness
	BeforeDrift     *drift.Snapshot
	AfterDrift      drift.Snapshot
}

func (s *Store) AlertTenant(ctx context.Context, ev Evaluation) (TenantReport, error) {
	now := s.now()
	rep := TenantReport{}
	tenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		return rep, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rep, fmt.Errorf("driftalerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return rep, err
	}
	q := dbx.New(tx)

	cfg, ok, err := s.getConfig(ctx, q, tenantID)
	if err != nil {
		return rep, err
	}
	if !ok || !cfg.Enabled {
		return rep, tx.Commit(ctx)
	}

	users, err := q.ListActiveUsersForTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return rep, fmt.Errorf("driftalerts: list users: %w", err)
	}
	rep.Recipients = len(users)
	if len(users) == 0 {
		return rep, tx.Commit(ctx)
	}

	drifted := driftedControls(ev.BeforeDrift, ev.AfterDrift)
	if len(drifted) < cfg.MinDriftedControls {
		drifted = nil
	}
	stale := staleCrossings(ev.BeforeFreshness, ev.AfterFreshness, now, cfg.MinStaleAge)
	bucket := debounceBucket(now, cfg.DebounceInterval)

	for _, u := range users {
		recipient := uuid.UUID(u.ID.Bytes).String()
		if cfg.ControlDriftEnabled {
			for _, controlID := range drifted {
				key := "drift:" + bucket
				payload := driftPayload(controlID, len(drifted), ev.AfterDrift.SnapshotDate)
				wrote, err := s.writeIfClaimed(ctx, q, tenantID, recipient, notifications.TypeControlDrift, controlID, key, payload)
				if err != nil {
					return rep, err
				}
				if wrote {
					rep.DriftWritten++
				} else {
					rep.DriftDeduped++
				}
			}
		}
		if cfg.EvidenceStalenessEnabled {
			for _, c := range stale {
				key := "stale:" + bucket
				payload := stalenessPayload(c, now)
				wrote, err := s.writeIfClaimed(ctx, q, tenantID, recipient, notifications.TypeEvidenceStaleness, c.ControlID, key, payload)
				if err != nil {
					return rep, err
				}
				if wrote {
					rep.StalenessWritten++
				} else {
					rep.StalenessDeduped++
				}
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("driftalerts: commit: %w", err)
	}
	return rep, nil
}

func (s *Store) getConfig(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) (Config, bool, error) {
	row, err := q.GetDriftFreshnessAlertConfig(ctx, pgUUID(tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("driftalerts: get config: %w", err)
	}
	return configFromRow(row), true, nil
}

func (s *Store) writeIfClaimed(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, recipient, eventType string, controlID uuid.UUID, stateKey string, payload any) (bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("driftalerts: marshal payload: %w", err)
	}
	_, err = q.ClaimDriftFreshnessAlert(ctx, dbx.ClaimDriftFreshnessAlertParams{
		ID:              pgUUID(uuid.New()),
		TenantID:        pgUUID(tenantID),
		RecipientUserID: recipient,
		EventType:       eventType,
		ControlID:       pgUUID(controlID),
		StateKey:        stateKey,
		NotificationID:  pgtype.UUID{},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("driftalerts: claim %s: %w", eventType, err)
	}
	if _, err := q.CreateNotification(ctx, dbx.CreateNotificationParams{
		ID:              pgUUID(uuid.New()),
		TenantID:        pgUUID(tenantID),
		RecipientUserID: recipient,
		Type:            eventType,
		Payload:         raw,
	}); err != nil {
		return false, fmt.Errorf("driftalerts: create notification: %w", err)
	}
	return true, nil
}

func driftedControls(before *drift.Snapshot, after drift.Snapshot) []uuid.UUID {
	if before == nil {
		return nil
	}
	afterPassing := make(map[uuid.UUID]bool, len(after.PassingControlIDs))
	for _, id := range after.PassingControlIDs {
		afterPassing[id] = true
	}
	out := make([]uuid.UUID, 0)
	for _, id := range before.PassingControlIDs {
		if !afterPassing[id] {
			out = append(out, id)
		}
	}
	return out
}

func staleCrossings(before, after []freshness.ControlFreshness, now time.Time, minStaleAge time.Duration) []freshness.ControlFreshness {
	wasStale := make(map[uuid.UUID]bool, len(before))
	for _, r := range before {
		wasStale[r.ControlID] = r.IsStale
	}
	out := make([]freshness.ControlFreshness, 0)
	for _, r := range after {
		if !r.IsStale || wasStale[r.ControlID] {
			continue
		}
		if r.ValidUntil != nil && minStaleAge > 0 && now.Sub(r.ValidUntil.UTC()) < minStaleAge {
			continue
		}
		out = append(out, r)
	}
	return out
}

func debounceBucket(now time.Time, interval time.Duration) string {
	if interval <= 0 {
		interval = DefaultDebounceInterval
	}
	return now.UTC().Truncate(interval).Format("20060102T150405Z")
}

type driftAlertPayload struct {
	Subtype      string `json:"subtype"`
	ControlID    string `json:"control_id"`
	DriftedCount int    `json:"drifted_count"`
	SnapshotDate string `json:"snapshot_date"`
	Message      string `json:"message"`
	DriftViewURL string `json:"drift_view_url"`
}

func driftPayload(controlID uuid.UUID, count int, day time.Time) driftAlertPayload {
	return driftAlertPayload{
		Subtype:      "control_out_of_passing",
		ControlID:    controlID.String(),
		DriftedCount: count,
		SnapshotDate: day.UTC().Format("2006-01-02"),
		Message:      "A control has flipped out of passing.",
		DriftViewURL: DriftViewPath,
	}
}

type stalenessAlertPayload struct {
	Subtype          string  `json:"subtype"`
	ControlID        string  `json:"control_id"`
	FreshnessClass   string  `json:"freshness_class,omitempty"`
	ValidUntil       *string `json:"valid_until,omitempty"`
	Message          string  `json:"message"`
	FreshnessViewURL string  `json:"freshness_view_url"`
}

func stalenessPayload(c freshness.ControlFreshness, now time.Time) stalenessAlertPayload {
	var validUntil *string
	if c.ValidUntil != nil {
		v := c.ValidUntil.UTC().Format(time.RFC3339)
		validUntil = &v
	}
	return stalenessAlertPayload{
		Subtype:          "evidence_stale",
		ControlID:        c.ControlID.String(),
		FreshnessClass:   c.FreshnessClass,
		ValidUntil:       validUntil,
		Message:          fmt.Sprintf("Evidence for a control crossed its freshness threshold during the %s debounce window.", debounceBucket(now, DefaultDebounceInterval)),
		FreshnessViewURL: FreshnessViewPath,
	}
}

func configFromRow(r dbx.DriftFreshnessAlertConfig) Config {
	return Config{
		Enabled:                  r.Enabled,
		SlackEnabled:             r.SlackEnabled,
		PagerDutyEnabled:         r.PagerdutyEnabled,
		ControlDriftEnabled:      r.ControlDriftEnabled,
		EvidenceStalenessEnabled: r.EvidenceStalenessEnabled,
		MinDriftedControls:       int(r.MinDriftedControls),
		MinStaleAge:              durationFromInterval(r.MinStaleAge),
		DebounceInterval:         durationFromInterval(r.DebounceInterval),
	}
}

func durationFromInterval(i pgtype.Interval) time.Duration {
	if !i.Valid {
		return 0
	}
	return time.Duration(i.Microseconds) * time.Microsecond
}

func tenantIDFromCtx(ctx context.Context) (uuid.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(tenantStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("driftalerts: parse tenant id: %w", err)
	}
	return id, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
