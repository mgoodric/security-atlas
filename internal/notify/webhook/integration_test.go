//go:build integration

// Integration tests for the slice 543 generic-webhook delivery channel.
// Real Postgres + RLS; an in-memory fake Transport stands in for a live
// webhook endpoint. Mirrors the slack integration suite.
package webhook_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/notify/webhook"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeTransport struct {
	mu   sync.Mutex
	sent [][]byte
}

func (f *fakeTransport) Post(_ context.Context, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, append([]byte(nil), body...))
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
			`DELETE FROM webhook_channel_optin WHERE tenant_id = $1`,
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

func TestWebhookDeliver_OptedIn_MinimumDisclosure(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "wh-a@example.test", true)
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
	var p webhook.Payload
	if err := json.Unmarshal(tr.sent[0], &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if p.DeepLink != "https://atlas.example.test/notifications" {
		t.Errorf("deep link = %q", p.DeepLink)
	}
	if p.Counts["Control-drift alerts"] != 1 {
		t.Errorf("counts not closed-labeled: %+v", p.Counts)
	}
	if strings.Contains(string(tr.sent[0]), "control.drift") {
		t.Errorf("raw type leaked into webhook payload")
	}
}

func TestWebhookDeliver_DefaultOptedOut(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")
	tenantID, userID := seedUser(t, admin, "wh-b@example.test", true)
	ctx := tenantCtx(t, tenantID)
	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent || tr.count() != 0 {
		t.Fatalf("opted-out must not deliver: %+v posts=%d", res, tr.count())
	}
}

func TestWebhookDeliver_Idempotent(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")
	tenantID, userID := seedUser(t, admin, "wh-c@example.test", true)
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
	if second.Sent || tr.count() != 1 {
		t.Fatalf("idempotency violated: %+v posts=%d", second, tr.count())
	}
}

func TestWebhookDeliver_NoCrossTenant(t *testing.T) {
	app, admin := openPools(t)
	trA := &fakeTransport{}
	chA := webhook.NewChannel(app, trA, "https://atlas.example.test")
	tenantA, userA := seedUser(t, admin, "wh-ta@example.test", true)
	_, userB := seedUser(t, admin, "wh-tb@example.test", true)
	ctxA := tenantCtx(t, tenantA)
	if err := chA.SetOptIn(ctxA, tenantA, userA, true); err != nil {
		t.Fatalf("opt-in A: %v", err)
	}
	res, err := chA.DeliverDigest(ctxA, userB, userB.String())
	if err == nil && res.Sent {
		t.Fatalf("cross-tenant delivery under tenant A GUC: %+v", res)
	}
	if trA.count() != 0 {
		t.Fatalf("cross-tenant leak: tenant A posted for tenant B")
	}
}
