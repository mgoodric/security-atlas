//go:build integration

// Integration tests for the slice 543 Slack delivery channel. Real
// Postgres + RLS; an in-memory fake Transport stands in for live Slack.
//
// Load-bearing coverage:
//   - opted-in user's digest delivered via the sink; outcome recorded sent.
//   - default opted-OUT (P0-543-3): a user with no opt-in row is skipped.
//   - idempotency: a second DeliverDigest the same UTC day does NOT double-send.
//   - cross-tenant isolation (P0-543-2 / invariant #6): tenant A's
//     notifications never deliver under tenant B's GUC.
//   - minimum disclosure (P0-543-1): the posted body carries counts + a
//     deep-link only; no raw type strings.
package slack_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/notify/slack"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeTransport struct {
	mu   sync.Mutex
	sent [][]byte
}

func (f *fakeTransport) Post(_ context.Context, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]byte(nil), body...)
	f.sent = append(f.sent, cp)
	return nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func openPools(t *testing.T) (app, admin *pgxpool.Pool) {
	t.Helper()
	appDSN := os.Getenv("DATABASE_URL_APP")
	adminDSN := os.Getenv("DATABASE_URL")
	if appDSN == "" || adminDSN == "" {
		t.Skip("DATABASE_URL_APP or DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(app): %v", err)
	}
	t.Cleanup(a.Close)
	b, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(admin): %v", err)
	}
	t.Cleanup(b.Close)
	return a, b
}

func seedUser(t *testing.T, admin *pgxpool.Pool, email string, withUnread bool) (tenantID, userID uuid.UUID) {
	t.Helper()
	tenantID = uuid.New()
	userID = uuid.New()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, 'Test User', 'active', '')
	`, userID, tenantID, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if withUnread {
		if _, err := admin.Exec(ctx, `
			INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
			VALUES ($1, $2, $3, 'control.drift', '{}'::jsonb, now())
		`, uuid.New(), tenantID, userID.String()); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM channel_delivery_log WHERE tenant_id = $1`,
			`DELETE FROM slack_channel_optin WHERE tenant_id = $1`,
			`DELETE FROM notifications WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenantID); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenantID, userID
}

func tenantCtx(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func TestSlackDeliver_OptedIn_MinimumDisclosure(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "slack-a@example.test", true)
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected Sent, got %+v", res)
	}
	if tr.count() != 1 {
		t.Fatalf("expected one post, got %d", tr.count())
	}
	body := string(tr.sent[0])
	if !strings.Contains(body, "https://atlas.example.test/notifications") {
		t.Errorf("missing deep link:\n%s", body)
	}
	if strings.Contains(body, "control.drift") {
		t.Errorf("raw type leaked into Slack body:\n%s", body)
	}
	if !strings.Contains(body, "Control-drift alerts") {
		t.Errorf("missing closed label:\n%s", body)
	}
}

func TestSlackDeliver_DefaultOptedOut(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "slack-b@example.test", true)
	ctx := tenantCtx(t, tenantID)
	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent {
		t.Fatalf("opted-out user must not receive: %+v", res)
	}
	if tr.count() != 0 {
		t.Fatalf("opted-out got a post")
	}
}

func TestSlackDeliver_Idempotent(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "slack-c@example.test", true)
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	if _, err := ch.DeliverDigest(ctx, userID, userID.String()); err != nil {
		t.Fatalf("deliver #1: %v", err)
	}
	second, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("deliver #2: %v", err)
	}
	if second.Sent {
		t.Fatalf("second same-day delivery must NOT re-send: %+v", second)
	}
	if tr.count() != 1 {
		t.Fatalf("idempotency violated: %d posts", tr.count())
	}
}

func TestSlackDeliver_NoCrossTenant(t *testing.T) {
	app, admin := openPools(t)
	trA := &fakeTransport{}
	chA := slack.NewChannel(app, trA, "https://atlas.example.test")

	tenantA, userA := seedUser(t, admin, "slack-ta@example.test", true)
	_, userB := seedUser(t, admin, "slack-tb@example.test", true)

	ctxA := tenantCtx(t, tenantA)
	if err := chA.SetOptIn(ctxA, tenantA, userA, true); err != nil {
		t.Fatalf("opt-in A: %v", err)
	}
	// Under tenant A's GUC, asking to deliver tenant B's user sees nothing
	// (RLS scopes notifications + opt-in to tenant A) — no post.
	res, err := chA.DeliverDigest(ctxA, userB, userB.String())
	if err == nil && res.Sent {
		t.Fatalf("cross-tenant delivery sent under tenant A GUC: %+v", res)
	}
	if trA.count() != 0 {
		t.Fatalf("cross-tenant leak: tenant A posted for tenant B's user")
	}
}
