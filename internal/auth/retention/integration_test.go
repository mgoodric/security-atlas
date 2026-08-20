//go:build integration

package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/retention"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestSweepOncePurgesBoundedIdentitySurfaces(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	tenantID := uuid.New()
	userID := uuid.New()
	staleSession := "retention-stale-session-" + uuid.NewString()
	freshSession := "retention-fresh-session-" + uuid.NewString()
	activeSession := "retention-active-session-" + uuid.NewString()
	staleExchangeJTI := "retention-stale-exchange-" + uuid.NewString()
	freshExchangeJTI := "retention-fresh-exchange-" + uuid.NewString()
	staleRevocationJTI := "retention-stale-revocation-" + uuid.NewString()
	freshRevocationJTI := "retention-fresh-revocation-" + uuid.NewString()
	staleHotJTI := "retention-stale-hot-" + uuid.NewString()
	freshHotJTI := "retention-fresh-hot-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE id = ANY($1)`, []string{staleSession, freshSession, activeSession})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_token_exchanges WHERE subject_token_jti = ANY($1)`, []string{staleExchangeJTI, freshExchangeJTI})
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_revocation_events WHERE jti = ANY($1)`, []string{staleRevocationJTI, freshRevocationJTI})
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_revoked_tokens WHERE jti = ANY($1)`, []string{staleHotJTI, freshHotJTI})
	})

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Retention User', 'active')
	`, userID, tenantID, "retention-"+uuid.NewString()+"@example.test")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (id, tenant_id, user_id, expires_at, revoked_at, user_agent, ip_address)
		VALUES
			($1, $4, $5, $6, $7, 'old ua', '203.0.113.10'),
			($2, $4, $5, $8, $9, 'fresh ua', '203.0.113.11'),
			($3, $4, $5, $10, NULL, 'active ua', '203.0.113.12')
	`, staleSession, freshSession, activeSession, tenantID, userID,
		now.Add(-(retention.SessionsRetentionWindow + time.Hour)),
		now.Add(-(retention.SessionsRetentionWindow + time.Hour)),
		now.Add(time.Hour),
		now.Add(-(retention.SessionsRetentionWindow - time.Hour)),
		now.Add(time.Hour))
	if err != nil {
		t.Fatalf("insert sessions: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO oauth_token_exchanges (
			tenant_id, subject_token_jti, from_tenant_id, to_tenant_id,
			subject_token_iss, subject_token_sub, exchanged_at, ip_address
		)
		VALUES
			($1, $2, NULL, $1, 'issuer', 'subject-old', $4, '203.0.113.20'),
			($1, $3, NULL, $1, 'issuer', 'subject-fresh', $5, '203.0.113.21')
	`, tenantID, staleExchangeJTI, freshExchangeJTI,
		now.Add(-(retention.OAuthAuditRetentionWindow + time.Hour)),
		now.Add(-(retention.OAuthAuditRetentionWindow - time.Hour)))
	if err != nil {
		t.Fatalf("insert oauth_token_exchanges: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO oauth_revocation_events (jti, revoked_at, revoked_by, ip_address)
		VALUES
			($1, $3, 'user:old', '203.0.113.30'),
			($2, $4, 'user:fresh', '203.0.113.31')
	`, staleRevocationJTI, freshRevocationJTI,
		now.Add(-(retention.OAuthAuditRetentionWindow + time.Hour)),
		now.Add(-(retention.OAuthAuditRetentionWindow - time.Hour)))
	if err != nil {
		t.Fatalf("insert oauth_revocation_events: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO oauth_revoked_tokens (jti, revoked_at, expires_at, revoked_by)
		VALUES
			($1, $3, $4, 'user:old'),
			($2, $3, $5, 'user:fresh')
	`, staleHotJTI, freshHotJTI, now.Add(-48*time.Hour),
		now.Add(-(retention.RevokedTokenExpiryGrace + time.Hour)),
		now.Add(-(retention.RevokedTokenExpiryGrace - time.Hour)))
	if err != nil {
		t.Fatalf("insert oauth_revoked_tokens: %v", err)
	}

	summary, err := retention.New(pool).WithClock(func() time.Time { return now }).SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if summary.Sessions != 1 || summary.OAuthTokenExchanges != 1 ||
		summary.OAuthRevocationEvents != 1 || summary.OAuthRevokedTokens != 1 {
		t.Fatalf("summary = %+v, want one row per surface", summary)
	}

	assertMissing(t, pool, `SELECT 1 FROM sessions WHERE id = $1`, staleSession)
	assertPresent(t, pool, `SELECT 1 FROM sessions WHERE id = $1`, freshSession)
	assertPresent(t, pool, `SELECT 1 FROM sessions WHERE id = $1`, activeSession)
	assertPresent(t, pool, `SELECT 1 FROM users WHERE id = $1`, userID)
	assertMissing(t, pool, `SELECT 1 FROM oauth_token_exchanges WHERE subject_token_jti = $1`, staleExchangeJTI)
	assertPresent(t, pool, `SELECT 1 FROM oauth_token_exchanges WHERE subject_token_jti = $1`, freshExchangeJTI)
	assertMissing(t, pool, `SELECT 1 FROM oauth_revocation_events WHERE jti = $1`, staleRevocationJTI)
	assertPresent(t, pool, `SELECT 1 FROM oauth_revocation_events WHERE jti = $1`, freshRevocationJTI)
	assertMissing(t, pool, `SELECT 1 FROM oauth_revoked_tokens WHERE jti = $1`, staleHotJTI)
	assertPresent(t, pool, `SELECT 1 FROM oauth_revoked_tokens WHERE jti = $1`, freshHotJTI)
}

func assertPresent(t *testing.T, pool *pgxpool.Pool, q string, arg any) {
	t.Helper()
	var one int
	if err := pool.QueryRow(context.Background(), q, arg).Scan(&one); err != nil {
		t.Fatalf("expected row for %v: %v", arg, err)
	}
}

func assertMissing(t *testing.T, pool *pgxpool.Pool, q string, arg any) {
	t.Helper()
	var one int
	if err := pool.QueryRow(context.Background(), q, arg).Scan(&one); err == nil {
		t.Fatalf("expected no row for %v", arg)
	}
}
