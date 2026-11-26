//go:build integration

// #2110 — POST /v1/controls/{id}/evaluate against real Postgres (RLS on the
// app role). The handler is the production one over a real eval.Engine:
// evaluate returns the new state and appends a manual-trigger evaluation;
// the assigned owner may evaluate (as a JWT session and as a user-bound
// key); a non-owner non-admin is refused and nothing is written. #2479:
// every row of a manual run records the caller's principal in
// triggered_by, and the CHECK keeps the column to manual rows.

package controlstate_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlstateapi "github.com/mgoodric/security-atlas/internal/api/controlstate"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
)

func evaluateServer(t *testing.T, app *pgxpool.Pool, cred credstore.Credential) *httptest.Server {
	t.Helper()
	h := controlstateapi.NewEvaluateHandler(eval.NewEngine(eval.NewStore(app), scope.NewStore(app)), app, 0)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authctx.WithCredential(req.Context(), cred)))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/controls/{id}/evaluate", h.Evaluate)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func postEvaluate(t *testing.T, ts *httptest.Server, ctrl uuid.UUID) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/controls/"+ctrl.String()+"/evaluate", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST evaluate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func manualEvaluations(t *testing.T, admin *pgxpool.Pool, ctrl uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_evaluations WHERE control_id = $1 AND trigger = 'manual'`, ctrl).Scan(&n); err != nil {
		t.Fatalf("count evaluations: %v", err)
	}
	return n
}

// latestTriggeredBy returns the triggered_by values of the control's newest
// eval run (one per row of that run).
func latestTriggeredBy(t *testing.T, admin *pgxpool.Pool, ctrl uuid.UUID) []*string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT triggered_by FROM control_evaluations
		WHERE eval_run_id = (
			SELECT eval_run_id FROM control_evaluations
			WHERE control_id = $1 ORDER BY evaluated_at DESC, id DESC LIMIT 1)`, ctrl)
	if err != nil {
		t.Fatalf("query triggered_by: %v", err)
	}
	defer rows.Close()
	var out []*string
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan triggered_by: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no evaluation rows")
	}
	return out
}

func assertTriggeredBy(t *testing.T, admin *pgxpool.Pool, ctrl uuid.UUID, want string) {
	t.Helper()
	for _, v := range latestTriggeredBy(t, admin, ctrl) {
		if v == nil || *v != want {
			t.Fatalf("triggered_by = %v; want %q", v, want)
		}
	}
}

func TestEvaluate2110_Integration(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin,
		"control_owner_assignments",
		"control_evaluations",
		"evidence_records",
		"scope_cells",
		"controls",
		"users",
	)
	ctrl := seedControl(t, admin, tenant, "automated", "daily")
	seedEvidence(t, admin, tenant, ctrl, "pass", time.Now().UTC().Add(-time.Hour))

	owner := uuid.New()
	other := uuid.New()
	for _, u := range []uuid.UUID{owner, other} {
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Evaluate 2110', 'active')`,
			u, tenant, u.String()[:8]+"@example.test"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO control_owner_assignments (tenant_id, control_id, owner_user_id, assigned_by) VALUES ($1, $2, $3, $4)`,
		tenant, ctrl, owner, other); err != nil {
		t.Fatalf("seed owner assignment: %v", err)
	}

	t.Run("admin evaluate returns the new state", func(t *testing.T) {
		before := manualEvaluations(t, admin, ctrl)
		ts := evaluateServer(t, app, credstore.Credential{ID: "jwt:admin", UserID: "user:" + other.String(), TenantID: tenant, TenantRoles: []string{"admin"}})
		code, body := postEvaluate(t, ts, ctrl)
		if code != http.StatusOK {
			t.Fatalf("status = %d; body %v", code, body)
		}
		states, _ := body["states"].([]any)
		if len(states) == 0 {
			t.Fatalf("no states in response: %v", body)
		}
		s0, _ := states[0].(map[string]any)
		if s0["result"] != "pass" || s0["trigger"] != "manual" {
			t.Fatalf("state = %v; want result=pass trigger=manual", s0)
		}
		if after := manualEvaluations(t, admin, ctrl); after <= before {
			t.Fatalf("manual evaluations %d -> %d; want an append", before, after)
		}
		assertTriggeredBy(t, admin, ctrl, other.String())
	})

	t.Run("owner via JWT session", func(t *testing.T) {
		ts := evaluateServer(t, app, credstore.Credential{ID: "jwt:owner", UserID: "user:" + owner.String(), TenantID: tenant, TenantRoles: []string{"control_owner"}})
		if code, body := postEvaluate(t, ts, ctrl); code != http.StatusOK {
			t.Fatalf("status = %d; body %v", code, body)
		}
		assertTriggeredBy(t, admin, ctrl, owner.String())
	})

	t.Run("owner via user-bound key", func(t *testing.T) {
		ts := evaluateServer(t, app, credstore.Credential{ID: "key_owner2110", UserID: owner.String(), TenantID: tenant, OwnerRoles: []string{"control_owner"}})
		if code, body := postEvaluate(t, ts, ctrl); code != http.StatusOK {
			t.Fatalf("status = %d; body %v", code, body)
		}
		assertTriggeredBy(t, admin, ctrl, owner.String())
	})

	for _, role := range []string{"grc_engineer", "control_owner", "viewer", "auditor"} {
		t.Run("non-owner "+role+" refused", func(t *testing.T) {
			before := manualEvaluations(t, admin, ctrl)
			ts := evaluateServer(t, app, credstore.Credential{ID: "jwt:" + role, UserID: "user:" + other.String(), TenantID: tenant, TenantRoles: []string{role}})
			if code, body := postEvaluate(t, ts, ctrl); code != http.StatusForbidden {
				t.Fatalf("status = %d; want 403; body %v", code, body)
			}
			if after := manualEvaluations(t, admin, ctrl); after != before {
				t.Fatalf("manual evaluations %d -> %d; a refused call wrote", before, after)
			}
		})
	}

	t.Run("a scheduled run records no actor", func(t *testing.T) {
		engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
		if _, err := engine.EvaluateControl(dbtest.WithTenantCtx(t, tenant), ctrl, eval.TriggerScheduled, eval.FarFuture); err != nil {
			t.Fatalf("EvaluateControl: %v", err)
		}
		for _, v := range latestTriggeredBy(t, admin, ctrl) {
			if v != nil {
				t.Fatalf("scheduled triggered_by = %q; want NULL", *v)
			}
		}
		if _, err := engine.EvaluateControlBy(dbtest.WithTenantCtx(t, tenant), ctrl, eval.TriggerScheduled, owner.String(), eval.FarFuture); !errors.Is(err, eval.ErrActorNotManual) {
			t.Fatalf("EvaluateControlBy(scheduled, actor) err = %v; want ErrActorNotManual", err)
		}
	})

	t.Run("CHECK refuses an actor on a non-manual row", func(t *testing.T) {
		_, err := admin.Exec(context.Background(), `
			INSERT INTO control_evaluations (id, tenant_id, control_id, eval_run_id, evaluated_at,
				result, freshness_status, evidence_count_in_window, trigger, triggered_by)
			VALUES ($1, $2, $3, $4, now(), 'inconclusive', 'no_evidence', 0, 'ingest', 'someone')`,
			uuid.New(), tenant, ctrl, uuid.New())
		if err == nil || !strings.Contains(err.Error(), "control_evaluations_triggered_by_manual_only") {
			t.Fatalf("insert err = %v; want the triggered_by_manual_only CHECK", err)
		}
	})
}
