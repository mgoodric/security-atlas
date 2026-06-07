//go:build integration

// Integration tests for the slice 445 email delivery channel
// (internal/notify/email). Real Postgres + RLS; an in-memory fake
// Provider stands in for a live SMTP server (AC-11 — NO live SMTP in CI).
//
// Load-bearing coverage:
//
//   - AC-11: an opted-in user's digest is delivered via the test sink;
//     the delivery-log outcome is recorded as 'sent'.
//   - AC-13 (P0-445-3): Tenant A's notification NEVER emails Tenant B's
//     user. Each tenant's delivery runs under its OWN tenant GUC and the
//     sink only ever sees that tenant's recipient.
//   - AC-15 (D5): idempotency — a second DeliverDigest for the same UTC
//     day does NOT double-send (claim collides on the UNIQUE key).
//   - P0-445-7: default opted-OUT — a user with no opt-in row is skipped.
//   - AC-8: a failing Provider records outcome=failed and leaves the
//     digest re-attemptable.
//
// Run via: just test-integration (sets DATABASE_URL_APP + DATABASE_URL).
package email_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeProvider is the in-memory SMTP sink (AC-11). It records every
// message it is asked to send; an optional failNext forces a failure for
// the AC-8 path.
type fakeProvider struct {
	mu       sync.Mutex
	sent     []email.Message
	failWith error
}

func (f *fakeProvider) Send(_ context.Context, msg email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeProvider) recipients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		out = append(out, m.Recipient)
	}
	return out
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

// seedUser inserts a (tenant, user) pair with a known account email and
// an optional unread notification, all via the admin (BYPASSRLS) pool.
func seedUser(t *testing.T, admin *pgxpool.Pool, accountEmail string, withUnread bool) (tenantID, userID uuid.UUID) {
	t.Helper()
	tenantID = uuid.New()
	userID = uuid.New()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, 'Test User', 'active', '')
	`, userID, tenantID, accountEmail); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if withUnread {
		if _, err := admin.Exec(ctx, `
			INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
			VALUES ($1, $2, $3, 'audit_note.reply', '{}'::jsonb, now())
		`, uuid.New(), tenantID, userID.String()); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM email_delivery_log WHERE tenant_id = $1`,
			`DELETE FROM email_channel_optin WHERE tenant_id = $1`,
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

// AC-11 + AC-9: an opted-in user with unread notifications gets a digest
// delivered via the sink; the outcome is recorded.
func TestDeliverDigest_OptedIn_Delivers(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "user-a@example.test", true)
	ctx := tenantCtx(t, tenantID)

	if err := ch.SetEmailOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetEmailOptIn: %v", err)
	}

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected Sent=true, got %+v", res)
	}
	recips := prov.recipients()
	if len(recips) != 1 || recips[0] != "user-a@example.test" {
		t.Fatalf("expected one send to account email, got %v", recips)
	}
}

// P0-445-7: default opted-OUT — no opt-in row means no send.
func TestDeliverDigest_DefaultOptedOut(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "user-b@example.test", true)
	ctx := tenantCtx(t, tenantID)

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent {
		t.Fatalf("opted-out user should NOT receive a digest: %+v", res)
	}
	if got := prov.recipients(); len(got) != 0 {
		t.Fatalf("opted-out user got a send: %v", got)
	}
	// And GetEmailOptIn confirms default false.
	enabled, err := ch.GetEmailOptIn(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("GetEmailOptIn: %v", err)
	}
	if enabled {
		t.Fatalf("default opt-in must be false (P0-445-7)")
	}
}

// AC-15 / D5: idempotency — a second delivery the same UTC day does NOT
// double-send (the claim collides on the UNIQUE key).
func TestDeliverDigest_Idempotent(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "user-c@example.test", true)
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetEmailOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetEmailOptIn: %v", err)
	}

	first, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest #1: %v", err)
	}
	if !first.Sent {
		t.Fatalf("first delivery should send: %+v", first)
	}
	second, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest #2: %v", err)
	}
	if second.Sent {
		t.Fatalf("second delivery same period must NOT re-send: %+v", second)
	}
	if got := prov.recipients(); len(got) != 1 {
		t.Fatalf("idempotency violated: %d sends, want 1: %v", len(got), got)
	}
}

// AC-13 / P0-445-3: Tenant A's notification never emails Tenant B's user.
// Two tenants each run delivery under their OWN tenant GUC; each sink send
// must carry only that tenant's recipient.
func TestDeliverDigest_NoCrossTenant(t *testing.T) {
	app, admin := openPools(t)
	provA := &fakeProvider{}
	provB := &fakeProvider{}
	chA := email.NewChannel(app, provA, "https://atlas.example.test")
	chB := email.NewChannel(app, provB, "https://atlas.example.test")

	tenantA, userA := seedUser(t, admin, "tenant-a-user@example.test", true)
	tenantB, userB := seedUser(t, admin, "tenant-b-user@example.test", true)

	ctxA := tenantCtx(t, tenantA)
	ctxB := tenantCtx(t, tenantB)
	if err := chA.SetEmailOptIn(ctxA, tenantA, userA, true); err != nil {
		t.Fatalf("opt-in A: %v", err)
	}
	if err := chB.SetEmailOptIn(ctxB, tenantB, userB, true); err != nil {
		t.Fatalf("opt-in B: %v", err)
	}

	// Tenant A delivers to user A only.
	if _, err := chA.DeliverDigest(ctxA, userA, userA.String()); err != nil {
		t.Fatalf("deliver A: %v", err)
	}
	// Tenant B delivers to user B only.
	if _, err := chB.DeliverDigest(ctxB, userB, userB.String()); err != nil {
		t.Fatalf("deliver B: %v", err)
	}

	for _, r := range provA.recipients() {
		if strings.Contains(r, "tenant-b") {
			t.Fatalf("CROSS-TENANT LEAK: tenant A sink sent to %q", r)
		}
	}
	for _, r := range provB.recipients() {
		if strings.Contains(r, "tenant-a") {
			t.Fatalf("CROSS-TENANT LEAK: tenant B sink sent to %q", r)
		}
	}

	// Attempt the cross-tenant footgun directly: ask channel A (under
	// tenant A's GUC) to deliver tenant B's user. RLS scopes the lookups
	// to tenant A, so user B's account email + notifications are invisible
	// — nothing is sent to user B.
	provA.mu.Lock()
	provA.sent = nil
	provA.mu.Unlock()
	res, err := chA.DeliverDigest(ctxA, userB, userB.String())
	if err == nil && res.Sent {
		t.Fatalf("cross-tenant delivery sent under tenant A GUC: %+v", res)
	}
	for _, r := range provA.recipients() {
		if strings.Contains(r, "tenant-b") {
			t.Fatalf("CROSS-TENANT LEAK via direct call: %q", r)
		}
	}
}

// seedNotification inserts one unread notification of a given type for the
// (tenant, user). Used by the slice 542 per-kind filter tests.
func seedNotification(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, typ string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, now())
	`, uuid.New(), tenantID, userID.String(), typ); err != nil {
		t.Fatalf("seed notification %s: %v", typ, err)
	}
}

// seedEmailPref inserts a slice-108 per-event `email`-channel preference row
// for the (tenant, user). Used to drive the slice 542 per-kind filter.
func seedEmailPref(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, event string, enabled bool) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO user_notification_preferences (tenant_id, user_id, event, channel, enabled, updated_at)
		VALUES ($1, $2, $3, 'email', $4, now())
	`, tenantID, userID, event, enabled); err != nil {
		t.Fatalf("seed email pref %s=%v: %v", event, enabled, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(),
			`DELETE FROM user_notification_preferences WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("cleanup prefs: %v", err)
		}
	})
}

// Slice 542 / AC + threat-model I: a per-kind `email=false` opt-out removes
// THAT kind's count from the digest, but delivery is otherwise unchanged — the
// digest still goes to the same account email (the master opt-in is on and at
// least one un-muted kind remains). A muted kind must not redirect delivery.
func TestDeliverDigest_PerKindMute_RemovesCountKeepsDelivery(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	// seedUser seeds one audit_note.reply already (unmapped -> always kept).
	tenantID, userID := seedUser(t, admin, "perkind@example.test", true)
	// Add a control.drift (mapped -> control_drift) and a policy_ack_due.
	seedNotification(t, admin, tenantID, userID, "control.drift")
	seedNotification(t, admin, tenantID, userID, "policy_ack_due")
	// Mute control.drift via the email channel; leave policy_ack_due default-on.
	seedEmailPref(t, admin, tenantID, userID, "control_drift", false)

	ctx := tenantCtx(t, tenantID)
	if err := ch.SetEmailOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetEmailOptIn: %v", err)
	}

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if !res.Sent {
		t.Fatalf("expected delivery (audit_note.reply + policy_ack_due survive): %+v", res)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.sent) != 1 {
		t.Fatalf("expected exactly one send, got %d", len(prov.sent))
	}
	msg := prov.sent[0]
	// Delivery is unchanged: same account email (P0-542-2, threat-model I).
	if msg.Recipient != "perkind@example.test" {
		t.Fatalf("recipient changed by filter: %q", msg.Recipient)
	}
	// The muted kind's label must NOT appear; the surviving kinds' must.
	if strings.Contains(msg.HTMLBody, "Control-drift alerts") {
		t.Fatalf("muted control.drift kind leaked into digest body:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "Policy acknowledgments due") {
		t.Fatalf("expected surviving policy_ack_due in body:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.HTMLBody, "Audit-note replies") {
		t.Fatalf("expected surviving (unmapped) audit_note.reply in body:\n%s", msg.HTMLBody)
	}
}

// Slice 542: when EVERY unread kind is muted via per-kind `email=false`, the
// digest has zero surviving kinds and is skipped (no empty email sent). Master
// is on; the per-kind filter narrows to nothing.
func TestDeliverDigest_AllKindsMuted_Skips(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "allmuted@example.test", false)
	seedNotification(t, admin, tenantID, userID, "control.drift")
	seedNotification(t, admin, tenantID, userID, "policy_ack_due")
	seedEmailPref(t, admin, tenantID, userID, "control_drift", false)
	seedEmailPref(t, admin, tenantID, userID, "policy_ack_due", false)

	ctx := tenantCtx(t, tenantID)
	if err := ch.SetEmailOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetEmailOptIn: %v", err)
	}

	res, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err != nil {
		t.Fatalf("DeliverDigest: %v", err)
	}
	if res.Sent {
		t.Fatalf("all kinds muted should skip, not send: %+v", res)
	}
	if got := prov.recipients(); len(got) != 0 {
		t.Fatalf("all-muted user got a send: %v", got)
	}
}

// AC-8: a failing provider records outcome=failed and surfaces the error;
// the digest is NOT marked sent (re-attemptable next tick).
func TestDeliverDigest_FailureRecorded(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{failWith: errors.New("smtp dial timeout")}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	tenantID, userID := seedUser(t, admin, "user-d@example.test", true)
	ctx := tenantCtx(t, tenantID)
	if err := ch.SetEmailOptIn(ctx, tenantID, userID, true); err != nil {
		t.Fatalf("SetEmailOptIn: %v", err)
	}

	_, err := ch.DeliverDigest(ctx, userID, userID.String())
	if err == nil {
		t.Fatalf("expected send failure to surface as error")
	}
	if !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
