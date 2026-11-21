//go:build integration

// #2849 — the api_keys purpose CHECK accepts policy-sync, keeps accepting
// bootstrap-recovery, and still refuses any other value. The down migration
// refuses to run while a policy-sync key exists, and once applied refuses
// policy-sync again.

package db_test

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	policySyncPurposeUp   = "../../migrations/sql/20261275000000_api_keys_purpose_policy_sync.sql"
	policySyncPurposeDown = "../../migrations/sql/20261275000000_api_keys_purpose_policy_sync.down.sql"
)

// insertsPurpose reports whether an api_keys row with purpose (nil for
// NULL) inserts, inside a savepoint that always rolls back.
func insertsPurpose(ctx context.Context, t *testing.T, tx pgx.Tx, purpose *string) bool {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	defer func() { _ = sp.Rollback(ctx) }()
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatalf("rand: %v", err)
	}
	_, err = sp.Exec(ctx, `INSERT INTO api_keys (tenant_id, token_hash, purpose) VALUES ($1, $2, $3)`,
		uuid.New(), hash, purpose)
	return err == nil
}

func TestAPIKeysPurposePolicySyncMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policySyncPurposeUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(policySyncPurposeDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema
	// and data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	str := func(s string) *string { return &s }
	accepted := []*string{nil, str("bootstrap-recovery"), str("policy-sync")}
	refused := []*string{str("bogus"), str("Policy-Sync"), str("policy_sync"), str("")}

	for _, p := range accepted {
		if !insertsPurpose(ctx, t, tx, p) {
			t.Errorf("purpose %v refused after 20261275000000", deref(p))
		}
	}
	for _, p := range refused {
		if insertsPurpose(ctx, t, tx, p) {
			t.Errorf("purpose %q accepted after 20261275000000", *p)
		}
	}

	// The down migration refuses while a policy-sync key exists.
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		t.Fatalf("rand: %v", err)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, `INSERT INTO api_keys (tenant_id, token_hash, purpose) VALUES ($1, $2, 'policy-sync')`, uuid.New(), hash); err != nil {
		t.Fatalf("seed policy-sync key: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Errorf("down migration applied while a policy-sync key exists")
	}
	_ = sp.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM api_keys WHERE purpose = 'policy-sync'`); err != nil {
		t.Fatalf("clear policy-sync keys: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	if insertsPurpose(ctx, t, tx, str("policy-sync")) {
		t.Errorf("after the down migration api_keys still accepts policy-sync")
	}
	if !insertsPurpose(ctx, t, tx, str("bootstrap-recovery")) {
		t.Errorf("after the down migration api_keys refuses bootstrap-recovery")
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
	if !insertsPurpose(ctx, t, tx, str("policy-sync")) {
		t.Errorf("after re-applying the up migration api_keys refuses policy-sync")
	}
	if insertsPurpose(ctx, t, tx, str("bogus")) {
		t.Errorf("after re-applying the up migration api_keys accepts bogus")
	}
}

func deref(p *string) string {
	if p == nil {
		return "NULL"
	}
	return *p
}
