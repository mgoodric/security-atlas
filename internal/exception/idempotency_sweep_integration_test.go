//go:build integration

// #2714: IdempotencySweeper deletes Idempotency-Keys past the 24h window
// and keeps the ones inside it, on the app role under RLS.

package exception_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// #2714: a key recorded by the #2618 build, whose hash is the legacy one,
// still replays after the upgrade instead of answering 422.
func TestHTTP_ExceptionCreate_LegacyHashedKeyReplays(t *testing.T) {
	a, _ := newExceptionAPI(t)
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	requester := a.bearer("user:" + uuid.New().String())
	key := uuid.New().String()
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Hour)
	body := fmt.Sprintf(`{"control_id":%q,"justification":"legacy","expires_at":%q}`,
		a.ctrl, expires.Format(time.RFC3339))

	code, _, raw := a.postWithKey(requester, key, body)
	if code != http.StatusCreated {
		t.Fatalf("first send: status %d body=%s", code, raw)
	}
	first := decodeException(t, raw).Exception.ID

	// Rewrite the recorded hash to what the #2618 build stored for this
	// body: predicate "" and compensating_controls null.
	shipped := fmt.Sprintf(`{"control_id":%q,"scope_cell_predicate":"","justification":"legacy",`+
		`"compensating_controls":null,"expires_at":%q}`, a.ctrl.String(), expires.Format(time.RFC3339Nano))
	sum := sha256.Sum256([]byte(shipped))
	if _, err := admin.Exec(ctx, `
		UPDATE exception_idempotency_keys SET request_hash = $3
		WHERE tenant_id = $1 AND idempotency_key = $2`,
		a.tenant, key, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("set legacy hash: %v", err)
	}

	code, replayed, raw := a.postWithKey(requester, key, body)
	if code != http.StatusCreated || replayed != "true" {
		t.Fatalf("retry of a legacy-hashed key: status %d replayed=%q body=%s, want a replay", code, replayed, raw)
	}
	if got := decodeException(t, raw).Exception.ID; got != first {
		t.Fatalf("replayed exception %s, want %s", got, first)
	}
	// A different body is still refused.
	code, _, raw = a.postWithKey(requester, key, a.createBody("another reason"))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy key, different body: status %d body=%s, want 422", code, raw)
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

// The DELETE policy admits only keys past the window: even in its own
// tenant the app role cannot delete a key that could still replay.
func TestIdempotencySweeper_PolicyKeepsKeysInsideTheWindow(t *testing.T) {
	a, _ := newExceptionAPI(t)
	app := dbtest.NewAppPool(t)
	requester := a.bearer("user:" + uuid.New().String())

	key := uuid.New().String()
	if code, _, raw := a.postWithKey(requester, key, a.createBody("window "+key)); code != http.StatusCreated {
		t.Fatalf("create with key: status %d body=%s", code, raw)
	}

	own := dbtest.WithTenantCtx(t, a.tenant)
	tx, err := app.Begin(own)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(own) }()
	if err := tenancy.ApplyTenant(own, tx); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(own, `DELETE FROM exception_idempotency_keys WHERE idempotency_key = $1`, key)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("the app role deleted a key inside the window (%d rows)", tag.RowsAffected())
	}
}
