//go:build integration

// #2639 — the audit trail the #2434 verify owner rule reads orders a row
// by its write, not by its transaction's start, so an owner change that
// waited on a completion sorts after it.
//
// Run with:
//
//	go test -tags=integration ./internal/actionplan/ -run '2639'

package actionplan_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/actionplan"
)

// TestAuditLog_2639_OrdersByWrite: a transaction that began before the
// plan's completion and writes its audit row after the completion commits
// sorts after it. With created_at DEFAULT now() the row took the
// transaction's start and sorted first. The colleague the late row names
// as owner is refused as verifier.
func TestAuditLog_2639_OrdersByWrite(t *testing.T) {
	env := newVerifyEnv(t)
	code, body := env.do(t, http.MethodPost, "/v1/action-plans", env.ownerJWT, map[string]any{
		"title": "Audit ordering plan", "owner_id": env.owner.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d: %v", code, body)
	}
	id := body["action_plan"].(map[string]any)["id"].(string)
	if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.ownerJWT, map[string]any{"status": actionplan.StatusInProgress}); code != http.StatusOK {
		t.Fatalf("PATCH in_progress: %d: %v", code, body)
	}

	ctx := context.Background()
	tx, err := env.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("start tx: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if code, body := env.do(t, http.MethodPatch, "/v1/action-plans/"+id, env.ownerJWT, map[string]any{"status": actionplan.StatusCompleted}); code != http.StatusOK {
		t.Fatalf("PATCH completed: %d: %v", code, body)
	}
	time.Sleep(20 * time.Millisecond)

	late := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO action_plan_audit_log (id, tenant_id, action_plan_id, actor_id, action_type, before_state, after_state)
		VALUES ($1, $2, $3, $4::uuid, 'updated',
		        jsonb_build_object('status', 'completed', 'owner_id', $4::text),
		        jsonb_build_object('status', 'completed', 'owner_id', $5::text))`,
		late, env.tenant, id, env.owner.String(), env.approver.String()); err != nil {
		t.Fatalf("late audit row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	trail, err := env.store.ListAuditLog(ctxFor(t, env.tenant), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	completion, lateAt := -1, -1
	for i, e := range trail {
		if completion < 0 && e.ActionType == actionplan.ActionStatusChanged &&
			jsonStatus(t, e.AfterState) == actionplan.StatusCompleted {
			completion = i
		}
		if e.ID == late {
			lateAt = i
		}
	}
	if completion < 0 || lateAt < 0 {
		t.Fatalf("completion row %d, late row %d: want both in the trail", completion, lateAt)
	}
	if lateAt < completion {
		t.Fatalf("late owner change sorts at %d, before the completion at %d; want after", lateAt, completion)
	}

	if code := env.verify(t, id, env.apprJWT); code != http.StatusForbidden {
		t.Fatalf("owner named on the late row verifies: %d, want 403", code)
	}
}

func jsonStatus(t *testing.T, raw []byte) string {
	t.Helper()
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("audit state: %v", err)
	}
	return st.Status
}
