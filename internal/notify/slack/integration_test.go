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
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/notify/slack"
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

// openPools returns the RLS-enforcing atlas_app pool and the privileged
// BYPASSRLS migrate pool from the shared internal/dbtest harness (slice 435 /
// 742). The app pool backs every RLS-bound assertion; the migrate pool is used
// only for cross-tenant seeding and the append-only delivery-log cleanup the
// app role cannot DELETE.
func openPools(t *testing.T) (app, admin *pgxpool.Pool) {
	t.Helper()
	return dbtest.NewAppPool(t), dbtest.NewMigratePool(t)
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

// seedNotification inserts one unread notification of the given type for the
// user. Used by the slice-583 per-kind filter tests to build a multi-kind
// digest the filter then narrows.
func seedNotification(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, ntype string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, now())
	`, uuid.New(), tenantID, userID.String(), ntype); err != nil {
		t.Fatalf("seed notification %q: %v", ntype, err)
	}
}

// setPref writes one explicit slice-108 per-(event, channel) preference row for
// the user. enabled=false is the per-kind opt-out the slice-583 filter honors.
// Written as the admin (BYPASSRLS) with an explicit tenant_id so the row lands
// under the right tenant regardless of GUC.
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

func tenantCtx(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	return dbtest.WithTenantCtx(t, tenantID.String())
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

// TestSlackDeliver_PerKindFilter_MutesOneKeepsOther proves the slice-583
// per-kind filter narrows the Slack digest: with the master opt-in ON, a kind
// whose `slack` channel pref is explicitly false is muted, while a sibling kind
// with no pref row is delivered (default-on). The muted kind's count must NOT
// appear in the posted body.
func TestSlackDeliver_PerKindFilter_MutesOneKeepsOther(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	// seedUser already seeds one control.drift; add a policy_ack_due sibling.
	tenantID, userID := seedUser(t, admin, "slack-pk1@example.test", true)
	seedNotification(t, admin, tenantID, userID, "policy_ack_due")
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	// Mute control.drift for SLACK only (master-on + kind-off -> mute).
	setPref(t, admin, tenantID, userID, "control_drift", "slack", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected Sent (policy_ack_due survives): %+v", res)
	}
	body := string(tr.sent[0])
	if strings.Contains(body, "Control-drift alerts") {
		t.Errorf("muted kind (control.drift) leaked into Slack body:\n%s", body)
	}
	if !strings.Contains(body, "Policy acknowledgments due") {
		t.Errorf("default-on sibling (policy_ack_due) missing from body:\n%s", body)
	}
}

// TestSlackDeliver_PerKindFilter_EmailOptOutDoesNotMuteSlack proves the filter
// is per-CHANNEL: an explicit EMAIL opt-out for a kind does NOT mute that kind
// on SLACK (channel isolation — the slice-583 generalization). master-on +
// no SLACK row -> default-on deliver, even though an email row says false.
func TestSlackDeliver_PerKindFilter_EmailOptOutDoesNotMuteSlack(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "slack-pk2@example.test", true) // control.drift
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	// Opt OUT of control.drift on EMAIL only; SLACK has no row -> default-on.
	setPref(t, admin, tenantID, userID, "control_drift", "email", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("email opt-out must not mute slack; expected Sent: %+v", res)
	}
	if !strings.Contains(string(tr.sent[0]), "Control-drift alerts") {
		t.Errorf("control.drift wrongly muted on slack by an EMAIL opt-out:\n%s", string(tr.sent[0]))
	}
}

// TestSlackDeliver_PerKindFilter_AllMutedSkips proves that muting EVERY unread
// kind on slack collapses the digest to zero -> the delivery is skipped (no
// post), mirroring the email all-muted case.
func TestSlackDeliver_PerKindFilter_AllMutedSkips(t *testing.T) {
	app, admin := openPools(t)
	tr := &fakeTransport{}
	ch := slack.NewChannel(app, tr, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "slack-pk3@example.test", true) // control.drift
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetOptIn: %v", err)
	}
	setPref(t, admin, tenantID, userID, "control_drift", "slack", false)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent {
		t.Fatalf("all kinds muted on slack must skip, got Sent: %+v", res)
	}
	if tr.count() != 0 {
		t.Fatalf("all-muted slack digest still posted")
	}
}
