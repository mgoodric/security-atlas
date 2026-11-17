//go:build integration

// #2329: concurrent routing writes record exact before values. Set and Clear
// take the tenant's routing lock before reading the before value, so two
// writes that race each record the state the other committed. The tests hold
// that lock themselves until both writes are queued on it, so the race is
// forced on every run rather than left to timing.

package cloud_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/advisorylock"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
)

type raceRow struct {
	action        string
	before, after string // provider
}

func routingRows(t *testing.T, admin *pgxpool.Pool, tenant string) []raceRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT action, before, after FROM me_audit_log
		 WHERE tenant_id = $1 AND action IN ($2, $3)
		   AND actor_principal IS DISTINCT FROM 'seed'`,
		tenant, cloud.AuditActionRoutingUpdate, cloud.AuditActionRoutingClear)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var out []raceRow
	for rows.Next() {
		var (
			r             raceRow
			before, after []byte
		)
		if err := rows.Scan(&r.action, &before, &after); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var b, a struct {
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal(before, &b); err != nil {
			t.Fatalf("before: %v", err)
		}
		if err := json.Unmarshal(after, &a); err != nil {
			t.Fatalf("after: %v", err)
		}
		r.before, r.after = b.Provider, a.Provider
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

// assertChained fails unless the rows, in some order, form one history from
// start: the first row's before is start and each later row's before is the
// previous row's after.
func assertChained(t *testing.T, rows []raceRow, start string) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("%d audit rows, want 2: %+v", len(rows), rows)
	}
	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		a, b := rows[order[0]], rows[order[1]]
		if a.before == start && b.before == a.after {
			return
		}
	}
	t.Fatalf("audit rows do not chain from %q: %+v", start, rows)
}

// raceWrites holds the tenant's routing lock, starts both writes, waits
// until both are blocked on the lock, then releases it.
func raceWrites(t *testing.T, admin *pgxpool.Pool, tenant string, writes ...func() error) {
	t.Helper()
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := advisorylock.LockText(ctx, tx, advisorylock.TenantLLMRouting, tenant); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	errs := make(chan error, len(writes))
	for _, w := range writes {
		go func() { errs <- w() }()
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := admin.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks
			 WHERE locktype = 'advisory' AND NOT granted
			   AND classid = $1::int4::oid AND objid = hashtext($2)::oid`,
			int32(advisorylock.TenantLLMRouting), tenant).Scan(&waiting); err != nil {
			t.Fatalf("read pg_locks: %v", err)
		}
		if waiting == len(writes) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d writes waiting on the routing lock", waiting, len(writes))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	for range writes {
		if err := <-errs; err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestStore_ConcurrentFirstPutsChainBeforeValues(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	raceWrites(t, admin, tenant,
		func() error {
			_, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA), uuid.Nil, "key_a")
			return err
		},
		func() error {
			_, err := store.Set(ctx, cloud.ProviderOpenAI, cloud.Secret(fakeProviderKeyB), uuid.Nil, "key_b")
			return err
		},
	)
	assertChained(t, routingRows(t, admin, tenant), "local-ollama")
}

func TestStore_PutRacingDeleteChainsBeforeValues(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	// The seed's own audit row is left out of routingRows by its principal.
	if _, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA), uuid.Nil, "seed"); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	raceWrites(t, admin, tenant,
		func() error {
			_, err := store.Set(ctx, cloud.ProviderOpenAI, cloud.Secret(fakeProviderKeyB), uuid.Nil, "key_b")
			return err
		},
		func() error {
			removed, err := store.Clear(ctx, uuid.Nil, "key_c")
			if err == nil && !removed {
				t.Error("Clear removed nothing; the seeded or racing config should be there")
			}
			return err
		},
	)
	assertChained(t, routingRows(t, admin, tenant), "anthropic")
}

func TestStore_AuditRowNamesPrincipal(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	if _, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA), uuid.Nil, "bootstrap-admin"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := store.Clear(ctx, uuid.Nil, "oauth_client:ci"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	rows, err := admin.Query(context.Background(), `
		SELECT action, user_id::text, actor_principal FROM me_audit_log
		 WHERE tenant_id = $1 ORDER BY action DESC`, tenant)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer rows.Close()
	want := map[string]string{
		cloud.AuditActionRoutingUpdate: "bootstrap-admin",
		cloud.AuditActionRoutingClear:  "oauth_client:ci",
	}
	n := 0
	for rows.Next() {
		var action, user string
		var principal *string
		if err := rows.Scan(&action, &user, &principal); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		if user != uuid.Nil.String() {
			t.Errorf("%s user_id = %s, want uuid.Nil", action, user)
		}
		if principal == nil || *principal != want[action] {
			t.Errorf("%s actor_principal = %v, want %q", action, principal, want[action])
		}
	}
	if n != 2 {
		t.Fatalf("%d audit rows, want 2", n)
	}
}
