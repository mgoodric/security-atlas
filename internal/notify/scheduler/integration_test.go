//go:build integration

// Integration tests for the slice 582 notification-channel digest scheduler
// (internal/notify/scheduler). Real Postgres + RLS; the enumeration query
// runs through the migrator (BYPASSRLS) pool and per-user delivery runs the
// REAL slice-445 email channel through the app pool with a fake SMTP sink.
//
// Load-bearing coverage:
//
//   - The tick enumerates ONLY opted-in users (enabled=true). An opted-out
//     user (enabled=false / no row) never appears and is never driven
//     (default opted-OUT honored end-to-end through the real SELECT).
//   - Each delivery runs under the user's OWN tenant context: a two-tenant
//     sweep delivers A's user under tenant A and B's user under tenant B,
//     never crossing (canvas invariant #6).
//   - Idempotency: a second sweep the same UTC day double-CALLS DeliverDigest
//     but does NOT double-SEND (the slice-445 claim-before-send collides).
//
// Run via: just test-integration (sets DATABASE_URL_APP + DATABASE_URL).
package scheduler_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/notify/scheduler"
)

type fakeProvider struct {
	mu   sync.Mutex
	sent []email.Message
}

func (f *fakeProvider) Send(_ context.Context, msg email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

// openPools returns the RLS-enforcing atlas_app pool and the privileged
// BYPASSRLS migrate pool from the shared internal/dbtest harness (slice 435 /
// 742). The app pool backs the per-user delivery (real slice-445 channel); the
// migrate pool runs the BYPASSRLS enumeration query and cross-tenant seeding.
func openPools(t *testing.T) (app, admin *pgxpool.Pool) {
	t.Helper()
	return dbtest.NewAppPool(t), dbtest.NewMigratePool(t)
}

// seedUser inserts a (tenant, user) pair with a known account email, an
// optional unread notification, and an optional email opt-in, all via the
// admin (BYPASSRLS) pool.
func seedUser(t *testing.T, admin *pgxpool.Pool, accountEmail string, withUnread, optIn bool) (tenantID, userID uuid.UUID) {
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
	if optIn {
		if _, err := admin.Exec(ctx, `
			INSERT INTO email_channel_optin (tenant_id, user_id, enabled, updated_at)
			VALUES ($1, $2, true, now())
		`, tenantID, userID); err != nil {
			t.Fatalf("seed opt-in: %v", err)
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

// The sweep enumerates opted-in users through the real SELECT, drives the
// real email channel per user, and honors default-opted-OUT end-to-end.
func TestSweepOnce_EnumeratesOptedInOnly(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	// optedIn: enabled=true + unread -> should be sent.
	tIn, _ := seedUser(t, admin, "in@example.test", true, true)
	// optedOut: no opt-in row -> never enumerated, never sent.
	seedUser(t, admin, "out@example.test", true, false)

	s := scheduler.New(admin, []scheduler.Channel{scheduler.EmailChannel(ch)}, nil)
	rep, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.Sent < 1 {
		t.Fatalf("expected at least 1 send, got %+v", rep)
	}
	recips := prov.recipients()
	var sawIn, sawOut bool
	for _, r := range recips {
		if r == "in@example.test" {
			sawIn = true
		}
		if r == "out@example.test" {
			sawOut = true
		}
	}
	if !sawIn {
		t.Fatalf("opted-in user was not delivered; recipients=%v", recips)
	}
	if sawOut {
		t.Fatalf("opted-OUT user must never be delivered; recipients=%v", recips)
	}
	_ = tIn
}

// A two-tenant sweep delivers each tenant's user under that tenant's own
// context — no cross-tenant leak.
func TestSweepOnce_TwoTenantsNoCross(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	seedUser(t, admin, "tenantA@example.test", true, true)
	seedUser(t, admin, "tenantB@example.test", true, true)

	s := scheduler.New(admin, []scheduler.Channel{scheduler.EmailChannel(ch)}, nil)
	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	recips := prov.recipients()
	var a, b bool
	for _, r := range recips {
		switch r {
		case "tenantA@example.test":
			a = true
		case "tenantB@example.test":
			b = true
		}
	}
	if !a || !b {
		t.Fatalf("both tenants' opted-in users should be delivered; recipients=%v", recips)
	}
}

// A second sweep the same UTC day double-CALLS DeliverDigest but does NOT
// double-SEND (the slice-445 claim-before-send is the idempotency guard).
func TestSweepOnce_IdempotentNoDoubleSend(t *testing.T) {
	app, admin := openPools(t)
	prov := &fakeProvider{}
	ch := email.NewChannel(app, prov, "https://atlas.example.test")

	seedUser(t, admin, "idem@example.test", true, true)

	s := scheduler.New(admin, []scheduler.Channel{scheduler.EmailChannel(ch)}, nil)
	first, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("first SweepOnce: %v", err)
	}
	second, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	if first.Sent < 1 {
		t.Fatalf("first sweep should send, got %+v", first)
	}
	if second.Sent != 0 {
		t.Fatalf("second sweep must not double-send, got %+v", second)
	}
	// Exactly one send to this recipient across both sweeps.
	n := 0
	for _, r := range prov.recipients() {
		if r == "idem@example.test" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 send to idem user, got %d", n)
	}
}
