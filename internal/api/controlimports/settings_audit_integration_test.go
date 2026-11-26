//go:build integration

// #2224: every change to the control import settings writes exactly one
// me_audit_log row in the same transaction, naming the actor and carrying the
// before and after values; a refused change (403, 400) writes none. Real
// Postgres with RLS and the real controlimport.Store.

package controlimports_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/controlimports"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const settingsActor = "00000000-0000-0000-0000-0000000022a4"

// settingsRouter serves the settings routes over the real store with a
// credential holding roles for actor, under tenant.
func settingsRouter(t *testing.T, app *pgxpool.Pool, tenant string, roles []string) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "cred-" + settingsActor,
				TenantID:   tenant,
				UserID:     "user:" + settingsActor,
				OwnerRoles: roles,
			})
			ctx, err := tenancy.WithTenant(ctx, tenant)
			if err != nil {
				t.Errorf("WithTenant: %v", err)
				return
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	controlimports.New(nil).WithSettings(controlimport.NewStore(app)).RegisterRoutes(r)
	return r
}

func putSettings(t *testing.T, h http.Handler, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/v1/controls/imports/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

type settingsAuditRow struct {
	userID        string
	principal     string
	before, after map[string]any
}

func settingsAuditRows(t *testing.T, admin *pgxpool.Pool, tenant string) []settingsAuditRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT user_id::text, coalesce(actor_principal, ''), before, after FROM me_audit_log
		 WHERE tenant_id = $1 AND action = $2
		 ORDER BY occurred_at`, tenant, controlimport.AuditActionSettingsUpdate)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var out []settingsAuditRow
	for rows.Next() {
		var (
			r             settingsAuditRow
			before, after []byte
		)
		if err := rows.Scan(&r.userID, &r.principal, &before, &after); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if err := json.Unmarshal(before, &r.before); err != nil {
			t.Fatalf("before: %v", err)
		}
		if err := json.Unmarshal(after, &r.after); err != nil {
			t.Fatalf("after: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

func TestPutSettings_EachChangeWritesOneAuditRow(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "control_import_settings", "me_audit_log")
	h := settingsRouter(t, app, tenant, []string{"admin"})

	for i, want := range []struct{ before, after bool }{{false, true}, {true, false}} {
		body := `{"llm_rerank_local":false}`
		if want.after {
			body = `{"llm_rerank_local":true}`
		}
		if code := putSettings(t, h, body); code != http.StatusOK {
			t.Fatalf("PUT %s = %d", body, code)
		}
		rows := settingsAuditRows(t, admin, tenant)
		if len(rows) != i+1 {
			t.Fatalf("after PUT %d: %d audit rows, want %d", i+1, len(rows), i+1)
		}
		r := rows[i]
		if r.userID != settingsActor || r.principal != settingsActor {
			t.Errorf("row %d actor = %s principal = %q, want %s for both", i, r.userID, r.principal, settingsActor)
		}
		if r.before["llm_rerank_local"] != want.before || r.after["llm_rerank_local"] != want.after {
			t.Errorf("row %d before=%v after=%v, want %v -> %v", i, r.before, r.after, want.before, want.after)
		}
	}
}

func TestPutSettings_RefusedChangeWritesNoAuditRow(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "control_import_settings", "me_audit_log")
	engineer := settingsRouter(t, app, tenant, []string{"grc_engineer"})
	tenantAdmin := settingsRouter(t, app, tenant, []string{"admin"})

	for _, tc := range []struct {
		name string
		h    http.Handler
		body string
		want int
	}{
		{"non-admin", engineer, `{"llm_rerank_local":true}`, http.StatusForbidden},
		{"missing field", tenantAdmin, `{}`, http.StatusBadRequest},
		{"unknown field", tenantAdmin, `{"llm_rerank_local":true,"provider":"x"}`, http.StatusBadRequest},
		{"invalid JSON", tenantAdmin, `{`, http.StatusBadRequest},
	} {
		if code := putSettings(t, tc.h, tc.body); code != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, code, tc.want)
		}
		if n := len(settingsAuditRows(t, admin, tenant)); n != 0 {
			t.Errorf("%s: %d audit rows, want 0 (a refused change writes none)", tc.name, n)
		}
	}
}
