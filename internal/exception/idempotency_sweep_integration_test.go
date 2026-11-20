//go:build integration

// #2714: IdempotencySweeper deletes Idempotency-Keys past the 24h window
// and keeps the ones inside it, on the app role under RLS.

package exception_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestIdempotencySweeper_DeletesExpiredKeysOnly(t *testing.T) {
	a, _ := newExceptionAPI(t)
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	requester := a.bearer("user:" + uuid.New().String())

	oldKey, freshKey := uuid.New().String(), uuid.New().String()
	for _, key := range []string{oldKey, freshKey} {
		if code, _, raw := a.postWithKey(requester, key, a.createBody("sweep "+key)); code != http.StatusCreated {
			t.Fatalf("create with key: status %d body=%s", code, raw)
		}
	}
	// Age one key past the window.
	if _, err := admin.Exec(ctx, `
		UPDATE exception_idempotency_keys
		SET created_at = now() - $3::interval
		WHERE tenant_id = $1 AND idempotency_key = $2`,
		a.tenant, oldKey, (exception.IdempotencyWindow + time.Hour).String()); err != nil {
		t.Fatalf("age key: %v", err)
	}

	keyExists := func(key string) bool {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx, `
			SELECT count(*) FROM exception_idempotency_keys
			WHERE tenant_id = $1 AND idempotency_key = $2`, a.tenant, key).Scan(&n); err != nil {
			t.Fatalf("count key: %v", err)
		}
		return n == 1
	}

	sweeper := exception.NewIdempotencySweeper(admin, dbtest.NewAppPool(t), nil)
	n, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("SweepOnce deleted %d rows, want at least the expired key", n)
	}
	if keyExists(oldKey) {
		t.Error("the key past the window survived the sweep")
	}
	if !keyExists(freshKey) {
		t.Error("the sweep deleted a key inside the window")
	}

	// The key inside the window still replays after the sweep.
	code, replayed, raw := a.postWithKey(requester, freshKey, a.createBody("sweep "+freshKey))
	if code != http.StatusCreated || replayed != "true" {
		t.Fatalf("fresh key after sweep: status %d replayed=%q body=%s, want a replay", code, replayed, raw)
	}
}

// The app role deletes only its own tenant's keys: under another tenant's
// context the sweep's DELETE matches nothing.
func TestIdempotencySweeper_DeleteIsTenantScoped(t *testing.T) {
	a, _ := newExceptionAPI(t)
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	requester := a.bearer("user:" + uuid.New().String())

	key := uuid.New().String()
	if code, _, raw := a.postWithKey(requester, key, a.createBody("scoped "+key)); code != http.StatusCreated {
		t.Fatalf("create with key: status %d body=%s", code, raw)
	}
	if _, err := admin.Exec(ctx, `
		UPDATE exception_idempotency_keys SET created_at = now() - interval '48 hours'
		WHERE tenant_id = $1 AND idempotency_key = $2`, a.tenant, key); err != nil {
		t.Fatalf("age key: %v", err)
	}

	other := dbtest.WithTenantCtx(t, uuid.NewString())
	tx, err := app.Begin(other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(other) }()
	if err := tenancy.ApplyTenant(other, tx); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(other, `DELETE FROM exception_idempotency_keys WHERE tenant_id = $1`, a.tenant)
	if err != nil {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("a delete under another tenant removed %d rows, want 0", tag.RowsAffected())
	}
}
