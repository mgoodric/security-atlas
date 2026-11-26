//go:build integration

// Integration tests for the framework_scope_workflow_log (issue #1786): every
// predicate change and state transition writes one append-only row with
// actor, time, before and after; the app role cannot rewrite the log; and
// the unified admin audit log lists the rows.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type logRow struct {
	Action    string
	FromState *string
	ToState   string
	Before    map[string]any
	After     map[string]any
	Actor     string
	At        time.Time
}

// readWorkflowLog reads a scope's log rows through the app role under the
// tenant GUC, oldest first.
func readWorkflowLog(t *testing.T, tenant string, scopeID string) []logRow {
	t.Helper()
	app := dbtest.NewAppPool(t)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	rows, err := dbx.New(tx).ListFrameworkScopeWorkflowLog(ctx, dbx.ListFrameworkScopeWorkflowLogParams{
		TenantID:         pgUUIDFromString(t, tenant),
		FrameworkScopeID: pgUUIDFromString(t, scopeID),
	})
	if err != nil {
		t.Fatalf("list workflow log: %v", err)
	}
	out := make([]logRow, 0, len(rows))
	for _, r := range rows {
		lr := logRow{Action: r.Action, FromState: r.FromState, ToState: r.ToState, Actor: r.Actor, At: r.OccurredAt.Time}
		if r.Before != nil {
			if err := json.Unmarshal(r.Before, &lr.Before); err != nil {
				t.Fatalf("unmarshal before: %v", err)
			}
		}
		if err := json.Unmarshal(r.After, &lr.After); err != nil {
			t.Fatalf("unmarshal after: %v", err)
		}
		out = append(out, lr)
	}
	return out
}

func pgUUIDFromString(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true}
}

// withAppTenant runs fn in an app-role tx with the tenant GUC applied.
func withAppTenant(ctx context.Context, app *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := app.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TestHTTP_WorkflowLog_OneEntryPerChange drives two scopes through the HTTP
// API — create, predicate edit, submit, approve, activate, an approval reset
// and a supersession — and asserts each change wrote exactly one log row
// with actor, time, before and after.
func TestHTTP_WorkflowLog_OneEntryPerChange(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	start := time.Now().UTC().Add(-time.Minute)

	create := func(name string) string {
		t.Helper()
		body := fmt.Sprintf(`{"framework_version_id":%q,"name":%q,"predicate":{"op":"true"}}`, fvID.String(), name)
		resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes", bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status %d; body %s", name, resp.StatusCode, payload)
		}
		var out struct {
			FrameworkScope struct {
				ID string `json:"id"`
			} `json:"framework_scope"`
		}
		if err := json.Unmarshal(payload, &out); err != nil {
			t.Fatalf("unmarshal create: %v", err)
		}
		return out.FrameworkScope.ID
	}
	step := func(method, path, who, body string) {
		t.Helper()
		resp, payload := doJSON(t, method, ts.URL+path, who, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status %d; body %s", method, path, resp.StatusCode, payload)
		}
	}
	activateBody := fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Format(time.RFC3339))

	// Scope A: create, edit predicate in draft, submit, approve, activate.
	a := create("A")
	step(http.MethodPatch, "/v1/framework-scopes/"+a, bearer, `{"predicate":{"op":"eq","dim":"environment","value":"prod"}}`)
	step(http.MethodPatch, "/v1/framework-scopes/"+a+"/submit", bearer, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+a+"/approve", approver, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+a+"/activate", bearer, activateBody)

	// Scope B: approved, reset to draft by a predicate edit, re-approved,
	// then activated — which supersedes A.
	b := create("B")
	step(http.MethodPatch, "/v1/framework-scopes/"+b+"/submit", bearer, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+b+"/approve", approver, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+b, bearer, `{"predicate":{"op":"eq","dim":"environment","value":"staging"}}`)
	step(http.MethodPatch, "/v1/framework-scopes/"+b+"/submit", bearer, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+b+"/approve", approver, "")
	step(http.MethodPatch, "/v1/framework-scopes/"+b+"/activate", bearer, activateBody)

	type want struct{ action, from, to string }
	check := func(scopeID string, wants []want) {
		t.Helper()
		got := readWorkflowLog(t, tenant, scopeID)
		if len(got) != len(wants) {
			t.Fatalf("scope %s: %d log rows; want %d: %+v", scopeID, len(got), len(wants), got)
		}
		for i, w := range wants {
			r := got[i]
			if r.Action != w.action || r.ToState != w.to {
				t.Errorf("row %d: action=%q to=%q; want %q -> %q", i, r.Action, r.ToState, w.action, w.to)
			}
			if r.Actor == "" {
				t.Errorf("row %d (%s): empty actor", i, r.Action)
			}
			if r.At.Before(start) {
				t.Errorf("row %d (%s): occurred_at %v before test start", i, r.Action, r.At)
			}
			if r.After["state"] != w.to || r.After["id"] != scopeID {
				t.Errorf("row %d (%s): after image %v; want state %q id %s", i, r.Action, r.After, w.to, scopeID)
			}
			if w.from == "" {
				if r.FromState != nil || r.Before != nil {
					t.Errorf("row %d (%s): from=%v before=%v; want both nil", i, r.Action, r.FromState, r.Before)
				}
				continue
			}
			if r.FromState == nil || *r.FromState != w.from || r.Before["state"] != w.from {
				t.Errorf("row %d (%s): from=%v before.state=%v; want %q", i, r.Action, r.FromState, r.Before["state"], w.from)
			}
		}
	}

	check(a, []want{
		{"create", "", "draft"},
		{"predicate.update", "draft", "draft"},
		{"submit", "draft", "review"},
		{"approve", "review", "approved"},
		{"activate", "approved", "activated"},
		{"supersede", "activated", "superseded"},
	})
	check(b, []want{
		{"create", "", "draft"},
		{"submit", "draft", "review"},
		{"approve", "review", "approved"},
		{"predicate.update", "approved", "draft"},
		{"submit", "draft", "review"},
		{"approve", "review", "approved"},
		{"activate", "approved", "activated"},
	})

	// The predicate edit's before/after images carry the old and new predicate.
	aLog := readWorkflowLog(t, tenant, a)
	beforePred, _ := json.Marshal(aLog[1].Before["predicate"])
	afterPred, _ := json.Marshal(aLog[1].After["predicate"])
	if string(beforePred) != `{"op":"true"}` {
		t.Errorf("predicate.update before.predicate = %s; want {\"op\":\"true\"}", beforePred)
	}
	if string(afterPred) == string(beforePred) {
		t.Errorf("predicate.update after.predicate unchanged: %s", afterPred)
	}
}

// TestWorkflowLog_AppendOnlyForAppRole asserts atlas_app cannot UPDATE or
// DELETE log rows, even inside its own tenant.
func TestWorkflowLog_AppendOnlyForAppRole(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	scope := mustActivate(t, ctx, frameworkscope.NewStore(app), fvID, "append-only", `{"op":"true"}`)

	for _, stmt := range []string{
		`UPDATE framework_scope_workflow_log SET actor = 'tampered' WHERE framework_scope_id = $1`,
		`DELETE FROM framework_scope_workflow_log WHERE framework_scope_id = $1`,
	} {
		err := withAppTenant(ctx, app, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, scope.ID)
			return err
		})
		if err == nil {
			t.Errorf("app role ran %q; want permission denied", stmt)
		}
	}
	rows := readWorkflowLog(t, tenant, scope.ID.String())
	if len(rows) != 4 {
		t.Fatalf("log rows after tamper attempts = %d; want 4 (create, submit, approve, activate)", len(rows))
	}
	for _, r := range rows {
		if r.Actor == "tampered" {
			t.Fatalf("log row was updated by the app role: %+v", r)
		}
	}
}

// TestHTTP_WorkflowLog_InAdminAuditLog asserts the unified admin audit log
// lists framework-scope log rows under kind=framework_scope.
func TestHTTP_WorkflowLog_InAdminAuditLog(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	adminBearer := srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	httpSrv := ts.URL

	body := fmt.Sprintf(`{"framework_version_id":%q,"name":"listed","predicate":{"op":"true"}}`, fvID.String())
	resp, payload := doJSON(t, http.MethodPost, httpSrv+"/v1/framework-scopes", adminBearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d; body %s", resp.StatusCode, payload)
	}
	var created struct {
		FrameworkScope struct {
			ID string `json:"id"`
		} `json:"framework_scope"`
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	resp, payload = doJSON(t, http.MethodPatch, httpSrv+"/v1/framework-scopes/"+created.FrameworkScope.ID+"/submit", adminBearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit: status %d; body %s", resp.StatusCode, payload)
	}

	q := url.Values{}
	q.Set("from", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))
	q.Set("to", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	q.Set("kind", "framework_scope")
	resp, payload = doJSON(t, http.MethodGet, httpSrv+"/v1/admin/audit-log/unified?"+q.Encode(), adminBearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unified audit log: status %d; body %s", resp.StatusCode, payload)
	}
	var list struct {
		Entries []struct {
			Kind        string          `json:"kind"`
			TargetType  string          `json:"target_type"`
			TargetID    string          `json:"target_id"`
			Action      string          `json:"action"`
			ActorID     string          `json:"actor_id"`
			PayloadJSON json.RawMessage `json:"payload_json"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatalf("unmarshal unified: %v", err)
	}
	actions := map[string]bool{}
	for _, e := range list.Entries {
		if e.Kind != "framework_scope" || e.TargetType != "framework_scope" || e.TargetID != created.FrameworkScope.ID {
			t.Errorf("unexpected entry: %+v", e)
			continue
		}
		if e.ActorID == "" {
			t.Errorf("entry %s has empty actor_id", e.Action)
		}
		var p map[string]any
		if err := json.Unmarshal(e.PayloadJSON, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if _, ok := p["after"]; !ok {
			t.Errorf("entry %s payload has no after image: %s", e.Action, e.PayloadJSON)
		}
		actions[e.Action] = true
	}
	if len(list.Entries) != 2 || !actions["create"] || !actions["submit"] {
		t.Fatalf("unified entries = %+v; want create + submit", list.Entries)
	}
}
