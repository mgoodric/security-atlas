// Package scheduler is the slice-582 notification-channel digest scheduler:
// the recurring fan-out DRIVER over the slice-445 (email) and slice-543
// (Slack/webhook) delivery sinks.
//
// The delivery primitives those slices shipped — each channel's
// DeliverDigest(ctx, userID, recipientUserID) — build, claim, and deliver
// ONE user's minimum-disclosure digest on demand. Nobody walked all the
// opted-in users on a schedule and called them. This package is that walk.
//
// Shape (mirrors the slice-076 metrics scheduler, internal/metrics/scheduler,
// and the slice-510 backup scheduler — in-process tick-loops, NO external
// cron, for the single-VM self-host target, D1):
//
//   - A migrator-role pool (BYPASSRLS) enumerates the opted-in (tenant, user)
//     pairs per channel once per tick. This is the deliberate,
//     deployment-privileged cross-tenant read (the only one) — it returns
//     ONLY the (tenant, user) keys, never notification content.
//   - Each per-user delivery then runs the EXISTING DeliverDigest under that
//     user's OWN tenant context (tenancy.WithTenant), so every read inside
//     the sink is RLS-scoped to the user's tenant. Tenant A's notifications
//     can never reach Tenant B's user (canvas invariant #6 / P0-582 tenant
//     isolation).
//   - Idempotency is the sinks' existing claim-before-send (the per-UTC-day
//     digest_key UNIQUE in slice-445/543): a second tick the same UTC day is
//     a no-op. The driver adds NO new idempotency surface.
//   - Per-user try/log/continue — one failing delivery does NOT abort the
//     sweep for the rest (the metrics-scheduler AC-13 pattern).
//
// Scope discipline (P0): this is a DELIVERY DRIVER, not a producer. It reads
// the opt-in tables and drives the existing sinks unchanged — it never writes
// a notification and never widens per-channel disclosure.
//
// Honest-interval discipline (canvas anti-pattern): the digest period is
// named explicitly (per-UTC-day; the default tick is daily). It is NOT
// marketed as "continuous monitoring".
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultInterval is the digest tick cadence. Daily matches the per-UTC-day
// digest_key the sinks key idempotency on: a finer tick is harmless (the
// claim makes extra passes no-ops) but daily is the honest period name.
// ATLAS_DIGEST_INTERVAL overrides for dev loops (mirrors
// ATLAS_METRICS_INTERVAL).
const DefaultInterval = 24 * time.Hour

// Delivery is the outcome of one DeliverDigest call, projected to the two
// fields the driver tallies. Every concrete channel (email/slack/webhook)
// returns its own DeliveryResult{Sent,Skipped,Reason}; the channel adapters
// (channels.go) flatten those into this shared shape so the driver does not
// import three result types.
type Delivery struct {
	Sent    bool
	Skipped bool
}

// DigestDeliverer is the one method the driver needs from a channel: deliver
// one user's digest under the tenant already on ctx. The email, Slack, and
// webhook channels satisfy this via the thin adapters in channels.go. The
// ctx MUST already carry the user's tenant (the driver applies it before
// calling).
type DigestDeliverer interface {
	DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (Delivery, error)
}

// OptInLister enumerates the opted-in (tenant, user) pairs for one channel.
// It is called with the migrator (BYPASSRLS) queries so it sees every
// tenant's opt-in rows in a single pass. enabled=false / no-row are excluded
// by the underlying SELECT (default opted-OUT).
type OptInLister func(ctx context.Context, q *dbx.Queries) ([]OptIn, error)

// OptIn is one enumerated opted-in (tenant, user) pair.
type OptIn struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// Channel binds a channel's enumeration query to its delivery sink. The
// three production channels are registered via the constructors in
// channels.go; tests register fakes implementing the same two seams.
type Channel struct {
	// Name is the channel discriminator for logging (email/slack/webhook).
	Name string
	// List enumerates this channel's opted-in users (migrator pool).
	List OptInLister
	// Deliverer is the channel's delivery sink (app pool, per-tenant ctx).
	Deliverer DigestDeliverer
}

// Scheduler drives every registered channel's opted-in users on a fixed
// cadence.
type Scheduler struct {
	migratorPool *pgxpool.Pool
	channels     []Channel
	logger       *slog.Logger

	// onInlineSweep, when non-nil, is invoked exactly once with the report
	// and error from the immediate pre-tick sweep Run fires on start. It
	// exists so an integration test can assert on the inline sweep's
	// RETURNED result rather than racing a wall-clock deadline (the
	// metrics-scheduler chronic-flake fix). It does NOT alter Run's external
	// behavior. Set via setInlineSweepHook (in-package; test-only).
	onInlineSweep func(SweepReport, error)
}

// New constructs a Scheduler. migratorPool MUST be the migrator role
// (BYPASSRLS) — it enumerates every tenant's opt-in rows. Each Channel's
// Deliverer reads + writes through its own app-role pool with
// tenancy.WithTenant applied per user (wired by the channels.go
// constructors), so RLS is honored on the delivery path.
func New(migratorPool *pgxpool.Pool, channels []Channel, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Scheduler{
		migratorPool: migratorPool,
		channels:     channels,
		logger:       logger,
	}
}

// setInlineSweepHook registers a one-shot callback fired after Run's
// immediate pre-tick sweep completes. In-package only (test seam).
func (s *Scheduler) setInlineSweepHook(fn func(SweepReport, error)) {
	s.onInlineSweep = fn
}

// Run executes the digest tick until ctx is cancelled. Fires SweepOnce
// immediately on start (so a fresh deploy doesn't wait a full interval for
// first delivery) then on every ticker fire. Mirrors the metrics scheduler.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultInterval
	}
	s.logger.Info("digest scheduler starting", "interval", interval.String(), "channels", len(s.channels))

	sweep := func() SweepReport {
		rep, err := s.SweepOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("digest scheduler sweep", "err", err.Error())
		}
		return rep
	}

	// First sweep fires inline so a fresh deploy delivers without waiting a
	// full interval. The test-only hook observes its outcome
	// deterministically; the production path logs-and-drops as the closure
	// does.
	if s.onInlineSweep != nil {
		rep, err := s.SweepOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("digest scheduler sweep", "err", err.Error())
		}
		s.onInlineSweep(rep, err)
	} else {
		sweep()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("digest scheduler stopping")
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// SweepReport tallies one full sweep across all channels.
type SweepReport struct {
	UsersEnumerated int
	Sent            int
	Skipped         int
	Failures        int
}

// SweepOnce runs one cycle: for each channel, enumerate its opted-in users
// (migrator pool, one cross-tenant read of keys only) then drive
// DeliverDigest per user under that user's own tenant context. Returns
// aggregate counts. Exposed for integration tests. A channel-level
// enumeration error is logged and the sweep continues with the next channel;
// a per-user delivery error is logged and the sweep continues with the next
// user (try/log/continue) — neither aborts the run.
func (s *Scheduler) SweepOnce(ctx context.Context) (SweepReport, error) {
	rep := SweepReport{}
	q := dbx.New(s.migratorPool)
	for _, ch := range s.channels {
		optins, err := ch.List(ctx, q)
		if err != nil {
			s.logger.Error("digest scheduler: enumerate opt-ins", "channel", ch.Name, "err", err.Error())
			continue
		}
		for _, oi := range optins {
			rep.UsersEnumerated++
			if err := s.deliverOne(ctx, ch, oi, &rep); err != nil {
				// deliverOne already logged with full context; the error is
				// captured in rep.Failures. Continue with the next user.
				continue
			}
		}
	}
	s.logger.Info("digest scheduler sweep complete",
		"enumerated", rep.UsersEnumerated,
		"sent", rep.Sent,
		"skipped", rep.Skipped,
		"failures", rep.Failures,
	)
	return rep, nil
}

// deliverOne drives one channel's DeliverDigest for one opted-in user under
// the user's OWN tenant context. The slice-029 recipient_user_id is the
// user's UUID rendered as a string (the contract the sinks + their tests
// use). The claim-before-send inside DeliverDigest makes a re-run within the
// same UTC day a no-op (idempotency), so a partial sweep that re-runs never
// double-sends.
func (s *Scheduler) deliverOne(ctx context.Context, ch Channel, oi OptIn, rep *SweepReport) error {
	tctx, err := tenancy.WithTenant(ctx, oi.TenantID.String())
	if err != nil {
		rep.Failures++
		s.logger.Error("digest scheduler: tenant ctx",
			"channel", ch.Name, "tenant", oi.TenantID, "user", oi.UserID, "err", err.Error())
		return err
	}
	res, err := ch.Deliverer.DeliverDigest(tctx, oi.UserID, oi.UserID.String())
	if err != nil {
		rep.Failures++
		s.logger.Error("digest scheduler: deliver",
			"channel", ch.Name, "tenant", oi.TenantID, "user", oi.UserID, "err", err.Error())
		return err
	}
	switch {
	case res.Sent:
		rep.Sent++
	case res.Skipped:
		rep.Skipped++
	}
	return nil
}

// listOptInRows is the shared adapter from a sqlc enumeration row's
// (pgtype.UUID, pgtype.UUID) shape to []OptIn. Invalid rows (NULL keys —
// not expectable given NOT NULL columns, but defensive) are dropped.
func listOptInRows[T any](rows []T, project func(T) (pgtype.UUID, pgtype.UUID)) []OptIn {
	out := make([]OptIn, 0, len(rows))
	for _, r := range rows {
		t, u := project(r)
		if !t.Valid || !u.Valid {
			continue
		}
		out = append(out, OptIn{TenantID: uuid.UUID(t.Bytes), UserID: uuid.UUID(u.Bytes)})
	}
	return out
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
