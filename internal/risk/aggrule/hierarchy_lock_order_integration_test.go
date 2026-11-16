//go:build integration

// #2004 — which paths wait on the tenant's risk-hierarchy lock, and that
// they take it before any other advisory lock.
//
// Every path that takes risk_hierarchy:<tenant> is listed in decisions D11
// (docs/audit-log/1748-risk-hierarchy-links-decisions.md): LinkChildrenTx
// (POST /v1/risks/{id}/children and the link_child_risks applier),
// Engine.LockRules (the LockOnRiskWrite hook) and Engine.Evaluate. Each is
// exercised here against a transaction that holds the lock.

package aggrule_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

// advisoryLocksHeld counts the advisory locks pid has been granted.
func advisoryLocksHeld(t *testing.T, admin *pgxpool.Pool, pid int32) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_locks WHERE pid = $1 AND locktype = 'advisory' AND granted`,
		pid).Scan(&n); err != nil {
		t.Fatalf("read pg_locks: %v", err)
	}
	return n
}

func TestHierarchyLock_PathsTakeItFirstAndOnlyWhenARuleMatches_2004(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	tenantID := uuid.MustParse(tenant)
	rules := aggrule.NewStore(app)
	createAndActivate(t, rules, ctx, makeRule("hierarchy-lock-order", "ownership", 3, 1, 90, "max"))

	paths := []struct {
		name   string
		blocks bool
		run    func(ctx context.Context, tx pgx.Tx) error
	}{
		{"LockOnRiskWrite, matching theme", true, func(ctx context.Context, tx pgx.Tx) error {
			return aggrule.LockOnRiskWrite(ctx, dbx.New(tx), tenantID, []string{"ownership"})
		}},
		{"LockOnRiskWrite, no rule matches", false, func(ctx context.Context, tx pgx.Tx) error {
			return aggrule.LockOnRiskWrite(ctx, dbx.New(tx), tenantID, []string{"unruled"})
		}},
		{"LockOnRiskWrite, no themes", false, func(ctx context.Context, tx pgx.Tx) error {
			return aggrule.LockOnRiskWrite(ctx, dbx.New(tx), tenantID, nil)
		}},
		{"Evaluate, matching theme", true, func(ctx context.Context, tx pgx.Tx) error {
			_, err := aggrule.NewEngine(dbx.New(tx)).Evaluate(ctx, tenantID, []string{"ownership"})
			return err
		}},
		{"Evaluate, no rule matches", false, func(ctx context.Context, tx pgx.Tx) error {
			_, err := aggrule.NewEngine(dbx.New(tx)).Evaluate(ctx, tenantID, []string{"unruled"})
			return err
		}},
		{"LinkChildrenTx", true, func(ctx context.Context, tx pgx.Tx) error {
			_, err := risk.LinkChildrenTx(ctx, dbx.New(tx), tenantID, uuid.New(), []uuid.UUID{uuid.New()})
			if errors.Is(err, risk.ErrNotFound) {
				return nil // reached the parent read, so it held the lock
			}
			return err
		}},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			holder, holderPID := openTenantTx(t, ctx, app)
			defer func() { _ = holder.Rollback(ctx) }()
			if err := dbx.New(holder).LockRiskHierarchy(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}); err != nil {
				t.Fatalf("holder: lock hierarchy: %v", err)
			}

			tx, pid := openTenantTx(t, ctx, app)
			defer func() { _ = tx.Rollback(ctx) }()
			done := make(chan error, 1)
			go func() { done <- p.run(ctx, tx) }()

			if !p.blocks {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("run: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("waited on the hierarchy lock with no rule to evaluate")
				}
				return
			}

			waitBlockedBy(t, admin, holderPID)
			// Waiting on the hierarchy lock, it holds no advisory lock yet:
			// no meta-risk key was taken ahead of it.
			if n := advisoryLocksHeld(t, admin, pid); n != 0 {
				t.Fatalf("holds %d advisory locks while waiting on the hierarchy lock, want 0", n)
			}
			select {
			case err := <-done:
				t.Fatalf("finished while the hierarchy lock was held: %v", err)
			default:
			}
			if err := holder.Rollback(ctx); err != nil {
				t.Fatalf("holder: release: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("run after release: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("did not finish after the hierarchy lock was released")
			}
		})
	}
}
