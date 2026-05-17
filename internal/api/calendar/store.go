// Package calendar serves the slice-094 compliance calendar.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	GET  /v1/calendar              JSON event list, cookie-auth
//	GET  /v1/calendar.ics          iCalendar 2.0 feed, URL-token auth
//	POST /v1/calendar/subscription mint the per-user ICS URL token
//
// All three are READ-ONLY over four existing source tables (audit_periods,
// exceptions, policies, controls + control_evaluations). No new write
// path. No change to the existing write paths or RLS policies on the
// four source tables (anti-criterion P0-A4). The only schema change in
// the slice is `policies.next_review_at` — see decision D4.
//
// Tenant isolation is enforced at the DB layer via slice-033 RLS. The
// Store opens a transaction per call and applies the tenant GUC via
// internal/tenancy; the platform pool connects as atlas_app
// (NOSUPERUSER NOBYPASSRLS) so the policies fire. The application-side
// `WHERE tenant_id = $1` predicate in the SQL is the primary guarantee;
// RLS is defense in depth.
//
// See docs/audit-log/094-compliance-calendar-decisions.md for the four
// design decisions the maintainer asked the implementing agent to
// resolve in-flight.
package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the calendar read layer. It holds no write methods on the four
// source tables — every query it issues over them is a SELECT. The single
// write surface is on `api_keys` (calendar subscription tokens) which the
// Store delegates to a separate SubscriptionStore.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool MUST
// connect as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced on the four source tables.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors dashboard.Store.inTx / controldetail.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("calendar: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("calendar: begin tx: %w", err)
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
		return fmt.Errorf("calendar: commit: %w", err)
	}
	return nil
}

// ListEvents reads one page of compliance calendar events between [from,
// to). typeFilter is a CSV ("audit,exception,policy,control") or "" for
// all. rowLimit caps the returned slice (the handler passes
// truncateThreshold+1 as a probe to detect the AC-5 truncation).
//
// The returned rows are ordered by starts_at ASC, then event_id ASC. The
// SQL itself does NOT split into a separate "next_from" — that's a
// handler-layer concern computed off the truncation probe.
func (s *Store) ListEvents(
	ctx context.Context,
	from, to, now time.Time,
	typeFilter string,
	rowLimit int32,
) ([]dbx.ListCalendarEventsRow, error) {
	var rows []dbx.ListCalendarEventsRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListCalendarEvents(ctx, dbx.ListCalendarEventsParams{
			TenantID:   pgUUID(tenantID),
			FromTs:     pgTimestamptz(from),
			ToTs:       pgTimestamptz(to),
			TypeFilter: typeFilter,
			NowTs:      pgTimestamptz(now),
			RowLimit:   rowLimit,
		})
		return qerr
	})
	return rows, err
}

// ----- pgtype helpers (kept local — mirror the dashboard package) -----

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
