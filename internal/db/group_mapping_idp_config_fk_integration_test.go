//go:build integration

// #2643 — the tenant-composite idp_config FK on oidc_idp_group_mappings.
// Asserts the migration's counts-only pre-check aborts when a cross-tenant
// or nonexistent idp_config_id already exists, names the counts, and
// re-applies cleanly when none does. Every case rolls back.

package db_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	groupMappingIDPFKUp   = "../../migrations/sql/20261271000000_group_mapping_idp_config_fk.sql"
	groupMappingIDPFKDown = "../../migrations/sql/20261271000000_group_mapping_idp_config_fk.down.sql"
)

func TestGroupMappingIDPConfigFKMigration_PreCheck(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the pre-check test needs the migrate role")
	}
	up, err := os.ReadFile(groupMappingIDPFKUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(groupMappingIDPFKDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	withAdminTx := func(fn func(context.Context, pgx.Tx)) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("admin Begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		fn(ctx, tx)
	}
	seedConfig := func(ctx context.Context, tx pgx.Tx, tenant string) string {
		id := uuid.NewString()
		if _, err := tx.Exec(ctx,
			`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
			 VALUES ($1, $2, 'okta', 'https://idp.example', 'cid', '\x00', 'https://app/cb')`,
			id, tenant); err != nil {
			t.Fatalf("seed idp config: %v", err)
		}
		return id
	}
	seedMapping := func(ctx context.Context, tx pgx.Tx, tenant, cfg, group string) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role)
			 VALUES ($1, $2, $3, 'viewer')`, tenant, cfg, group); err != nil {
			t.Fatalf("seed mapping: %v", err)
		}
	}

	t.Run("reapplies cleanly with only own-tenant references", func(t *testing.T) {
		withAdminTx(func(ctx context.Context, tx pgx.Tx) {
			tenant := uuid.NewString()
			seedMapping(ctx, tx, tenant, seedConfig(ctx, tx, tenant), "Engineering")
			if _, err := tx.Exec(ctx, string(up)); err != nil {
				t.Fatalf("re-apply up migration: %v", err)
			}
		})
	})

	t.Run("aborts on cross-tenant and nonexistent references", func(t *testing.T) {
		withAdminTx(func(ctx context.Context, tx pgx.Tx) {
			if _, err := tx.Exec(ctx, string(down)); err != nil {
				t.Fatalf("apply down migration: %v", err)
			}
			tenantA := uuid.NewString()
			tenantB := uuid.NewString()
			seedMapping(ctx, tx, tenantA, seedConfig(ctx, tx, tenantB), "Foreign")
			seedMapping(ctx, tx, tenantA, uuid.NewString(), "Missing")

			_, err := tx.Exec(ctx, string(up))
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgErrCheckViolation {
				t.Fatalf("up migration over bad references: want SQLSTATE %s, got %v", pgErrCheckViolation, err)
			}
			if !strings.Contains(pgErr.Message, "nonexistent=1") || !strings.Contains(pgErr.Message, "cross_tenant=1") {
				t.Errorf("pre-check message should carry both counts, got %q", pgErr.Message)
			}
		})
	})
}
