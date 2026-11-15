//go:build integration

// #2224: every change to the tenant's LLM routing writes exactly one
// me_audit_log row in the same transaction, naming the actor and carrying the
// before and after routing; a refused change (403, 400, 409) writes none. The
// row never carries the provider key or its ciphertext.

package llmrouting_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
)

// testActor is the user routerFor puts on every request's credential.
const testActor = "00000000-0000-0000-0000-000000000001"

type auditRow struct {
	userID, action string
	before, after  map[string]any
	raw            string
}

func routingAuditRows(t *testing.T, admin *pgxpool.Pool, tenant string) []auditRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT user_id::text, action, before, after, before::text || after::text
		  FROM me_audit_log
		 WHERE tenant_id = $1 AND action IN ($2, $3)
		 ORDER BY occurred_at`,
		tenant, cloud.AuditActionRoutingUpdate, cloud.AuditActionRoutingClear)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var (
			r             auditRow
			before, after []byte
		)
		if err := rows.Scan(&r.userID, &r.action, &before, &after, &r.raw); err != nil {
			t.Fatalf("scan audit row: %v", err)
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

// assertNoSecret fails when an audit row carries the key, its ciphertext, or
// any field outside the masked shape.
func assertNoSecret(t *testing.T, r auditRow, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(r.raw, s) {
			t.Errorf("%s audit row carries a secret: %s", r.action, r.raw)
		}
	}
	allowed := []string{"provider", "is_cloud", "has_api_key", "api_key_written"}
	for _, side := range []map[string]any{r.before, r.after} {
		for k := range side {
			if !slices.Contains(allowed, k) {
				t.Errorf("%s audit row has field %q outside %v", r.action, k, allowed)
			}
		}
	}
}

func storedCiphertext(t *testing.T, admin *pgxpool.Pool, tenant string) string {
	t.Helper()
	var ct *string
	if err := admin.QueryRow(context.Background(),
		`SELECT api_key_ciphertext FROM tenant_llm_routing WHERE tenant_id = $1`, tenant).Scan(&ct); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if ct == nil {
		return ""
	}
	return *ct
}

func TestHandler_EachChangeWritesOneAuditRow(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t)
	h := routerFor(cloud.NewStore(app, testCrypter(t)), tenant, []string{"admin"}, false)

	// Opt in to a cloud provider.
	if code, body := do(t, h, http.MethodPut, "/v1/admin/llm-routing",
		`{"provider":"anthropic","api_key":"`+fakeKey+`"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	ct1 := storedCiphertext(t, admin, tenant)
	rows := routingAuditRows(t, admin, tenant)
	if len(rows) != 1 {
		t.Fatalf("after PUT: %d audit rows, want 1", len(rows))
	}
	r := rows[0]
	if r.action != cloud.AuditActionRoutingUpdate || r.userID != testActor {
		t.Errorf("row = %s by %s, want %s by %s", r.action, r.userID, cloud.AuditActionRoutingUpdate, testActor)
	}
	if r.before["provider"] != "local-ollama" || r.before["has_api_key"] != false {
		t.Errorf("before = %v, want the local-ollama default", r.before)
	}
	if r.after["provider"] != "anthropic" || r.after["is_cloud"] != true || r.after["has_api_key"] != true || r.after["api_key_written"] != true {
		t.Errorf("after = %v, want anthropic with a key written", r.after)
	}
	assertNoSecret(t, r, fakeKey, ct1)

	// Rotate the key under the same provider: still a change, still one row.
	const rotated = "handler-fake-provider-key-11111"
	if code, body := do(t, h, http.MethodPut, "/v1/admin/llm-routing",
		`{"provider":"anthropic","api_key":"`+rotated+`"}`); code != http.StatusOK {
		t.Fatalf("rotate PUT = %d: %s", code, body)
	}
	ct2 := storedCiphertext(t, admin, tenant)
	rows = routingAuditRows(t, admin, tenant)
	if len(rows) != 2 {
		t.Fatalf("after rotation: %d audit rows, want 2", len(rows))
	}
	if r := rows[1]; r.before["provider"] != "anthropic" || r.after["api_key_written"] != true {
		t.Errorf("rotation row before=%v after=%v", r.before, r.after)
	}
	assertNoSecret(t, rows[1], fakeKey, rotated, ct1, ct2)

	// Clear: one clear row, back to local.
	if code, body := do(t, h, http.MethodDelete, "/v1/admin/llm-routing", ""); code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", code, body)
	}
	rows = routingAuditRows(t, admin, tenant)
	if len(rows) != 3 {
		t.Fatalf("after DELETE: %d audit rows, want 3", len(rows))
	}
	r = rows[2]
	if r.action != cloud.AuditActionRoutingClear || r.userID != testActor {
		t.Errorf("row = %s by %s, want %s by %s", r.action, r.userID, cloud.AuditActionRoutingClear, testActor)
	}
	if r.before["provider"] != "anthropic" || r.after["provider"] != "local-ollama" || r.after["has_api_key"] != false {
		t.Errorf("clear row before=%v after=%v", r.before, r.after)
	}
	assertNoSecret(t, r, fakeKey, rotated, ct1, ct2)

	// Clearing a tenant with no config changes nothing and writes nothing.
	if code, body := do(t, h, http.MethodDelete, "/v1/admin/llm-routing", ""); code != http.StatusOK {
		t.Fatalf("second DELETE = %d: %s", code, body)
	}
	if rows = routingAuditRows(t, admin, tenant); len(rows) != 3 {
		t.Fatalf("after a no-op DELETE: %d audit rows, want 3", len(rows))
	}
}

func TestHandler_RefusedChangeWritesNoAuditRow(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t)
	store := cloud.NewStore(app, testCrypter(t))
	viewer := routerFor(store, tenant, []string{"viewer"}, false)
	tenantAdmin := routerFor(store, tenant, []string{"admin"}, false)
	noCrypter := routerFor(cloud.NewStore(app, nil), tenant, []string{"admin"}, false)

	// Give the tenant a config so a refused DELETE has something to remove.
	if code, body := do(t, tenantAdmin, http.MethodPut, "/v1/admin/llm-routing", `{"provider":"local-ollama"}`); code != http.StatusOK {
		t.Fatalf("seed PUT = %d: %s", code, body)
	}
	base := len(routingAuditRows(t, admin, tenant))

	for _, tc := range []struct {
		name         string
		h            http.Handler
		method, body string
		want         int
	}{
		{"viewer PUT", viewer, http.MethodPut, `{"provider":"anthropic","api_key":"x"}`, http.StatusForbidden},
		{"viewer DELETE", viewer, http.MethodDelete, "", http.StatusForbidden},
		{"unknown provider", tenantAdmin, http.MethodPut, `{"provider":"custom","api_key":"x"}`, http.StatusBadRequest},
		{"invalid JSON", tenantAdmin, http.MethodPut, `{`, http.StatusBadRequest},
		{"local with a key", tenantAdmin, http.MethodPut, `{"provider":"local-ollama","api_key":"x"}`, http.StatusBadRequest},
		{"cloud without a key", tenantAdmin, http.MethodPut, `{"provider":"openai"}`, http.StatusBadRequest},
		{"no cloud key configured", noCrypter, http.MethodPut, `{"provider":"anthropic","api_key":"x"}`, http.StatusConflict},
	} {
		if code, body := do(t, tc.h, tc.method, "/v1/admin/llm-routing", tc.body); code != tc.want {
			t.Errorf("%s = %d, want %d: %s", tc.name, code, tc.want, body)
		}
		if got := len(routingAuditRows(t, admin, tenant)); got != base {
			t.Errorf("%s: %d audit rows, want %d (a refused change writes none)", tc.name, got, base)
		}
	}
}
