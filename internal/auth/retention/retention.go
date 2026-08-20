// Package retention enforces bounded retention for identity and OAuth
// security-audit surfaces that intentionally carry online identifiers.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// SessionsRetentionWindow keeps revoked or expired session metadata for
	// one quarter. That is long enough for account-compromise triage and
	// access-review follow-up without retaining IP/User-Agent forever.
	SessionsRetentionWindow = 90 * 24 * time.Hour

	// OAuthAuditRetentionWindow keeps tenant-switch and revocation audit
	// events across an annual audit/lookback cycle plus investigation slack.
	OAuthAuditRetentionWindow = 400 * 24 * time.Hour

	// RevokedTokenExpiryGrace is the skew cushion for the hot revocation
	// list. Access tokens default to 1h (internal/api/oauth), so pruning
	// after exp+24h is still at least 24x the max token lifetime and cannot
	// defeat revocation before natural expiry.
	RevokedTokenExpiryGrace = 24 * time.Hour

	// DefaultSweepInterval runs daily. Operators can override the startup
	// cadence via ATLAS_IDENTITY_RETENTION_SWEEP_INTERVAL.
	DefaultSweepInterval = 24 * time.Hour
)

var ErrNoPool = errors.New("identity retention: store has no pgxpool")

// Summary reports rows deleted by one retention sweep.
type Summary struct {
	Sessions              int64
	OAuthTokenExchanges   int64
	OAuthRevocationEvents int64
	OAuthRevokedTokens    int64
}

func (s Summary) Total() int64 {
	return s.Sessions + s.OAuthTokenExchanges + s.OAuthRevocationEvents + s.OAuthRevokedTokens
}

// Store runs retention deletes through a privileged maintenance pool. The
// pool must be the migrate/BYPASSRLS role because the sweep crosses tenants
// and touches append-only audit tables that atlas_app intentionally cannot
// DELETE from.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:  pool,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

func (s *Store) WithClock(clock func() time.Time) *Store {
	if clock != nil {
		s.clock = clock
	}
	return s
}

func (s *Store) SweepOnce(ctx context.Context) (Summary, error) {
	if s.pool == nil {
		return Summary{}, ErrNoPool
	}
	now := s.clock().UTC()
	sessionCutoff := now.Add(-SessionsRetentionWindow)
	oauthAuditCutoff := now.Add(-OAuthAuditRetentionWindow)
	revokedTokenCutoff := now.Add(-RevokedTokenExpiryGrace)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("identity retention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out Summary
	if out.Sessions, err = execRows(ctx, tx, `
		DELETE FROM sessions
		WHERE (revoked_at IS NOT NULL AND revoked_at < $1)
		   OR (revoked_at IS NULL AND expires_at < $1)
	`, sessionCutoff); err != nil {
		return Summary{}, fmt.Errorf("identity retention: sessions: %w", err)
	}
	if out.OAuthTokenExchanges, err = execRows(ctx, tx, `
		DELETE FROM oauth_token_exchanges
		WHERE exchanged_at < $1
	`, oauthAuditCutoff); err != nil {
		return Summary{}, fmt.Errorf("identity retention: oauth_token_exchanges: %w", err)
	}
	if out.OAuthRevocationEvents, err = execRows(ctx, tx, `
		DELETE FROM oauth_revocation_events
		WHERE revoked_at < $1
	`, oauthAuditCutoff); err != nil {
		return Summary{}, fmt.Errorf("identity retention: oauth_revocation_events: %w", err)
	}
	if out.OAuthRevokedTokens, err = execRows(ctx, tx, `
		DELETE FROM oauth_revoked_tokens
		WHERE expires_at < $1
	`, revokedTokenCutoff); err != nil {
		return Summary{}, fmt.Errorf("identity retention: oauth_revoked_tokens: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Summary{}, fmt.Errorf("identity retention: commit: %w", err)
	}
	return out, nil
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func execRows(ctx context.Context, ex execer, sql string, args ...any) (int64, error) {
	tag, err := ex.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Runner is the scheduled retention sweeper.
type Runner struct {
	store    *Store
	logger   *slog.Logger
	interval time.Duration
}

func NewRunner(store *Store, logger *slog.Logger, interval time.Duration) *Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Runner{store: store, logger: logger, interval: interval}
}

func (r *Runner) Run(ctx context.Context) error {
	r.logger.Info("identity retention sweeper starting", "interval", r.interval.String())
	if _, err := r.sweep(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("identity retention initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("identity retention sweeper stopping")
			return nil
		case <-ticker.C:
			if _, err := r.sweep(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Error("identity retention sweep", "err", err.Error())
			}
		}
	}
}

func (r *Runner) sweep(ctx context.Context) (Summary, error) {
	summary, err := r.store.SweepOnce(ctx)
	if err != nil {
		return Summary{}, err
	}
	if summary.Total() > 0 {
		r.logger.Info("identity retention swept",
			"sessions", summary.Sessions,
			"oauth_token_exchanges", summary.OAuthTokenExchanges,
			"oauth_revocation_events", summary.OAuthRevocationEvents,
			"oauth_revoked_tokens", summary.OAuthRevokedTokens,
		)
	}
	return summary, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
