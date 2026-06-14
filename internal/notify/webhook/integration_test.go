//go:build integration

// Integration tests for the slice 543 generic-webhook delivery channel.
// Real Postgres + RLS; an in-memory fake Transport stands in for a live
// webhook endpoint. Mirrors the slack integration suite.
package webhook_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/notify/webhook"
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
	return dbtest.WithTenantCtx(t, tenantID.String())
}

// seedNotification inserts one unread notification of the given type (slice-583
// per-kind filter tests build a multi-kind digest the filter then narrows).
func seedNotification(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, ntype string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, now())
	`, uuid.New(), tenantID, userID.String(), ntype); err != nil {
		t.Fatalf("seed notification %q: %v", ntype, err)
	}
}

// setPref writes one explicit slice-108 per-(event, channel) preference row.
// enabled=false is the per-kind opt-out the slice-583 filter honors.
func setPref(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, event, channel string, enabled bool) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO user_notification_preferences (tenant_id, user_id, event, channel, enabled)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, user_id, event, channel) DO UPDATE SET enabled = EXCLUDED.enabled
	`, tenantID, userID, event, channel, enabled); err != nil {
		t.Fatalf("set pref %s/%s=%v: %v", event, channel, enabled, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(),
			`DELETE FROM user_notification_preferences WHERE tenant_id = $1`, tenantID)
	})
}

// TestWebhookDeliver_PerKindFilter_MutesOneKeepsOther: master-on + kind-off on
// WEBHOOK mutes that kind; a sibling with no row is delivered (default-on).
func TestWebhookDeliver_PerKindFilter_MutesOneKeepsOther(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "wh-pk1@example.test", true) // control.drift
	seedNotification(t, admin, tenantID, userID, "policy_ack_due")
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	setPref(t, admin, tenantID, userID, "control_drift", "webhook", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected Sent (policy_ack_due survives): %+v", res)
	}
	var p webhook.Payload
	if err := json.Unmarshal(tr.sent[0], &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, muted := p.Counts["Control-drift alerts"]; muted {
		t.Errorf("muted kind (control.drift) present in webhook payload: %+v", p.Counts)
	}
	if p.Counts["Policy acknowledgments due"] != 1 {
		t.Errorf("default-on sibling missing from webhook payload: %+v", p.Counts)
	}
}

// TestWebhookDeliver_PerKindFilter_EmailOptOutDoesNotMuteWebhook: an email
// opt-out for a kind does NOT mute it on webhook (per-channel isolation).
func TestWebhookDeliver_PerKindFilter_EmailOptOutDoesNotMuteWebhook(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "wh-pk2@example.test", true) // control.drift
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	setPref(t, admin, tenantID, userID, "control_drift", "email", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("email opt-out must not mute webhook; expected Sent: %+v", res)
	}
	var p webhook.Payload
	if err := json.Unmarshal(tr.sent[0], &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if p.Counts["Control-drift alerts"] != 1 {
		t.Errorf("control.drift wrongly muted on webhook by an EMAIL opt-out: %+v", p.Counts)
	}
}

// TestWebhookDeliver_PerKindFilter_AllMutedSkips: muting every unread kind on
// webhook collapses the digest to zero -> skip (no post).
func TestWebhookDeliver_PerKindFilter_AllMutedSkips(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tr := &fakeTransport{}
	ch := webhook.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "wh-pk3@example.test", true) // control.drift
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	setPref(t, admin, tenantID, userID, "control_drift", "webhook", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent || tr.count() != 0 {
		t.Fatalf("all kinds muted on webhook must skip: %+v posts=%d", res, tr.count())
	}
}

func TestWebhookDeliver_OptedIn_MinimumDisclosure(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
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
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
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
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
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
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
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
