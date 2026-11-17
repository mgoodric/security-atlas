//go:build integration

// #2481 — the catalog-delete guard cannot lose an overlay edge to a
// concurrent insert. Two connections, READ COMMITTED (the default):
//
//   - the insert holds its FK lock first and commits while the delete waits:
//     the delete is refused (23001), and the edge survives;
//   - the delete holds the row first and commits while the insert waits:
//     the insert fails its FK check (23503), so no edge exists to remove.
//
// Either way no active overlay edge is removed without an audit row.

package mappingoverlays_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const insertOverlaySQL = `
	INSERT INTO fw_to_scf_overlay_edges (tenant_id, framework_requirement_id, scf_anchor_id,
		relationship_type, strength, rationale, source_ref, created_by)
	VALUES ($1, $2, $3, 'equal', 1.0, 'race test', 'race test', 'test')
	RETURNING id`

// waitForLock blocks until the backend pid is waiting on a heavyweight lock,
// so the test knows the other session is parked behind the first one.
func waitForLock(t *testing.T, pool *pgxpool.Pool, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waitType *string
		if err := pool.QueryRow(context.Background(),
			`SELECT wait_event_type FROM pg_stat_activity WHERE pid = $1`, int32(pid)).Scan(&waitType); err != nil {
			t.Fatalf("pg_stat_activity for pid %d: %v", pid, err)
		}
		if waitType != nil && *waitType == "Lock" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d never waited on a lock", pid)
}

// The insert commits while the delete is waiting on it: the delete must see
// the committed edge and refuse.
func TestOverlay_CatalogDeleteRefusedWhenInsertCommitsMidDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	anchor := f.anchorID(t, "IAC-06")

	for name, del := range map[string]struct {
		sql string
		arg uuid.UUID
	}{
		"requirement": {`DELETE FROM framework_requirements WHERE id = $1`, f.requirement["C2"]},
		"anchor":      {`DELETE FROM scf_anchors WHERE id = $1`, anchor},
	} {
		t.Run(name, func(t *testing.T) {
			ins, err := f.admin.Begin(ctx)
			if err != nil {
				t.Fatalf("begin insert: %v", err)
			}
			var edgeID uuid.UUID
			if err := ins.QueryRow(ctx, insertOverlaySQL, f.tenantA.tenant, f.requirement["C2"], anchor).Scan(&edgeID); err != nil {
				_ = ins.Rollback(ctx)
				t.Fatalf("insert overlay edge: %v", err)
			}
			t.Cleanup(func() {
				_, _ = f.admin.Exec(ctx, `DELETE FROM fw_to_scf_overlay_edges WHERE id = $1`, edgeID)
			})

			conn, err := f.admin.Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			defer conn.Release()
			pid := conn.Conn().PgConn().PID()
			done := make(chan error, 1)
			go func() {
				// Rolled back whatever happens, so a regression cannot wipe
				// the shared catalog.
				done <- pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, del.sql, del.arg); err != nil {
						return err
					}
					return errors.New("rollback")
				})
			}()

			waitForLock(t, f.admin, pid)
			if err := ins.Commit(ctx); err != nil {
				t.Fatalf("commit insert: %v", err)
			}
			if err := <-done; sqlState(err) != "23001" {
				t.Fatalf("delete after a concurrent insert committed: err = %v; want 23001", err)
			}

			var status string
			if err := f.admin.QueryRow(ctx, `SELECT status FROM fw_to_scf_overlay_edges WHERE id = $1`, edgeID).Scan(&status); err != nil || status != "active" {
				t.Fatalf("overlay edge after refused delete: status %q, err %v; want active", status, err)
			}
		})
	}
}

// The delete holds the requirement while the insert waits: once the delete
// commits, the insert fails its FK check and leaves nothing behind.
func TestOverlay_InsertFailsWhenCatalogDeleteCommitsFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := f.requirement["C3"]

	del, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin delete: %v", err)
	}
	if _, err := del.Exec(ctx, `DELETE FROM framework_requirements WHERE id = $1`, req); err != nil {
		_ = del.Rollback(ctx)
		t.Fatalf("delete requirement: %v", err)
	}

	conn, err := f.admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	pid := conn.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() {
		var id uuid.UUID
		done <- conn.QueryRow(ctx, insertOverlaySQL, f.tenantA.tenant, req, f.anchorID(t, "CRY-03")).Scan(&id)
	}()

	waitForLock(t, f.admin, pid)
	if err := del.Commit(ctx); err != nil {
		t.Fatalf("commit delete: %v", err)
	}
	if err := <-done; sqlState(err) != "23503" {
		t.Fatalf("insert after the requirement's delete committed: err = %v; want 23503", err)
	}

	var edges, audits int
	if err := f.admin.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM fw_to_scf_overlay_edges WHERE framework_requirement_id = $1),
		       (SELECT count(*) FROM fw_to_scf_overlay_edge_audit_log
		        WHERE action = 'cascade_removed' AND after->>'removed_row_id' = $1::text)`,
		req).Scan(&edges, &audits); err != nil {
		t.Fatalf("count: %v", err)
	}
	if edges != 0 || audits != 0 {
		t.Errorf("after the race: %d overlay edges, %d cascade_removed rows on the requirement; want 0 and 0", edges, audits)
	}
}
