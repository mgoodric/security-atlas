//go:build integration

// #2740: two replicas revoking one key at once write exactly one
// api_key_revoke row. The race is forced, not hoped for: an outside
// transaction holds the key's row lock while both revokes start, and lets go
// only once both are waiting on it. Without the locking read, both have
// already seen revoked_at IS NULL and each writes a row.
package auth_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// raceRevokes runs both revokes while an outside transaction holds the
// row lock on api_keys row keyID, releases it once both are waiting on a
// lock, and returns their errors.
func raceRevokes(t *testing.T, keyID uuid.UUID, revokes [2]func() error) [2]error {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the lock holder needs the owner pool")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	holder, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	tag, err := holder.Exec(ctx, `UPDATE api_keys SET last_used_at = last_used_at WHERE id = $1`, keyID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("lock holder matched %d api_keys rows, want 1", tag.RowsAffected())
	}
	var holderPID int
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}

	var errs [2]error
	var wg sync.WaitGroup
	for i, fn := range revokes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn()
		}()
	}

	// Both revokers wait on a lock: the first on the holder, the second on
	// the first (tuple lock) or the holder.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := adminPool.QueryRow(ctx, `
			SELECT count(DISTINCT pid) FROM pg_locks
			WHERE NOT granted AND pid <> $1`, holderPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d revokes waiting on the row lock after 15s", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	return errs
}

func countRevokeRows(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	n := 0
	for _, row := range keyAuditRows(t, tenant) {
		if row.Action == apikeystore.AuditActionKeyRevoke {
			n++
		}
	}
	return n
}

// HTTP path (admincreds): apikeystore.Store.Revoke.
func TestAPIKeyAudit_ConcurrentRevokesWriteOneRow(t *testing.T) {
	tenant := uuid.New()
	cleanupKeys(t, tenant)
	ctx := withTenant(t, tenant)
	store := newAuditKeyStore()
	by := apikeystore.Actor{UserID: uuid.New(), Principal: "race"}

	cred, _, err := store.Issue(ctx, tenant.String(), apikeystore.IssueInput{}, by)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	keyID := uuid.MustParse(strings.TrimPrefix(cred.ID, "key_"))
	revoke := func() error { return store.Revoke(ctx, tenant.String(), keyID, by) }

	for i, err := range raceRevokes(t, keyID, [2]func() error{revoke, revoke}) {
		if err != nil {
			t.Errorf("revoke %d: %v", i, err)
		}
	}
	if n := countRevokeRows(t, tenant); n != 1 {
		t.Errorf("api_key_revoke rows = %d, want 1", n)
	}
}

// gRPC path (AdminCredentialsService): two credstores over one database,
// as two replicas are, each revoking through its apikeystore.Persister.
func TestGRPCAdminKeyAudit_ConcurrentRevokesWriteOneRow(t *testing.T) {
	tenant := uuid.New()
	cleanupKeys(t, tenant)
	ctx := adminCaller(tenant, uuid.NewString())
	svcA, _ := grpcAdmin(t, nil)
	issued, err := svcA.Issue(ctx, &adminv1.IssueRequest{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	id := issued.GetHandle().GetId()
	// Replica B loads the key from the database at attach.
	svcB, storeB := grpcAdmin(t, nil)
	if _, ok := storeB.TenantOf(id); !ok {
		t.Fatalf("replica B did not load %s", id)
	}

	keyID := uuid.MustParse(strings.TrimPrefix(id, "key_"))
	errs := raceRevokes(t, keyID, [2]func() error{
		func() error { _, err := svcA.Revoke(ctx, &adminv1.RevokeRequest{Id: id}); return err },
		func() error { _, err := svcB.Revoke(ctx, &adminv1.RevokeRequest{Id: id}); return err },
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("revoke %d: %v", i, err)
		}
	}
	if n := countRevokeRows(t, tenant); n != 1 {
		t.Errorf("api_key_revoke rows = %d, want 1", n)
	}
}
