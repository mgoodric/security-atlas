//go:build integration

// #2311 — integration tests for the risk-edit follow-ups: If-Match on
// PATCH /v1/risks/{id} (two editors from one version, one wins), and the
// ?limit= cap on GET /v1/risks/{id}/audit-log. Real Postgres + the
// assembled router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run 'IfMatch|AuditLimit'

package risks_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// patchIfMatch sends a PATCH with an If-Match header and returns the
// status. It does not touch t, so it is safe in a goroutine.
func patchIfMatch(env testEnv, path, raw, ifMatch string) (int, error) {
	req, err := http.NewRequest(http.MethodPatch, env.server.URL+path, bytes.NewBufferString(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	req.Header.Set("If-Match", ifMatch)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func riskETagOf(t *testing.T, env testEnv, id string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/risks/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET risk: %v", err)
	}
	_ = resp.Body.Close()
	tag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || tag == "" {
		t.Fatalf("GET risk: status %d, ETag %q", resp.StatusCode, tag)
	}
	return tag
}

// TestIfMatch_ConcurrentEditorsOneWins: two PATCHes sent at once from the
// same version both carry its ETag; the row lock orders them, the first
// writes, and the second is a 412 that writes nothing.
func TestIfMatch_ConcurrentEditorsOneWins(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)
	tag := riskETagOf(t, env, id)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	errs := make([]error, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], errs[i] = patchIfMatch(env, "/v1/risks/"+id,
				fmt.Sprintf(`{"title":"editor %d"}`, i), tag)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("PATCH: %v", err)
		}
	}
	sort.Ints(codes)
	if codes[0] != http.StatusOK || codes[1] != http.StatusPreconditionFailed {
		t.Fatalf("statuses %v, want one 200 and one 412", codes)
	}
	if n := len(auditEntries(t, env, id)); n != 1 {
		t.Fatalf("audit rows = %d, want 1 (the loser writes none)", n)
	}
}

// TestIfMatch_StaleAndCurrent: a stale ETag is a 412 even sequentially;
// the ETag after a write is current; no If-Match is last-write-wins.
func TestIfMatch_StaleAndCurrent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)
	stale := riskETagOf(t, env, id)

	if code, err := patchIfMatch(env, "/v1/risks/"+id, `{"title":"first"}`, stale); err != nil || code != http.StatusOK {
		t.Fatalf("first PATCH: %d %v", code, err)
	}
	if code, _ := patchIfMatch(env, "/v1/risks/"+id, `{"title":"second"}`, stale); code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: status %d, want 412", code)
	}
	current := riskETagOf(t, env, id)
	if current == stale {
		t.Fatal("ETag did not change after a write")
	}
	if code, _ := patchIfMatch(env, "/v1/risks/"+id, `{"title":"third"}`, current); code != http.StatusOK {
		t.Fatalf("current If-Match: status %d, want 200", code)
	}
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"title":"no precondition"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("no If-Match: status %d: %v", resp.StatusCode, body)
	}
	_, got := get(t, env, "/v1/risks/"+id)
	if v := riskField(t, got, "title"); v != "no precondition" {
		t.Fatalf("title = %v", v)
	}
}

// TestAuditLimit_CapsAndFlagsTruncation: ?limit= returns the newest rows
// and says when older ones exist; a limit past the max is a 400.
func TestAuditLimit_CapsAndFlagsTruncation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	id := createRecordRisk(t, env, nil)
	for i := 0; i < 3; i++ {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO risk_audit_log (tenant_id, risk_id, actor, action_type, before_state, after_state, created_at)
			VALUES ($1, $2, 'test', 'fields_changed', '{}', jsonb_build_object('n', $3::int), now() + make_interval(secs => $3::int))`,
			tenant, id, i); err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}

	resp, body := get(t, env, "/v1/risks/"+id+"/audit-log?limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("limit=2: status %d: %v", resp.StatusCode, body)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 2 || body["truncated"] != true || body["limit"] != float64(2) {
		t.Fatalf("limit=2: %d entries, truncated=%v, limit=%v", len(entries), body["truncated"], body["limit"])
	}
	if after := entries[0].(map[string]any)["after"].(map[string]any); after["n"] != float64(2) {
		t.Fatalf("newest row first: got %v", after)
	}

	_, body = get(t, env, "/v1/risks/"+id+"/audit-log?limit=3")
	if body["truncated"] != false || body["count"] != float64(3) {
		t.Fatalf("limit=3: truncated=%v count=%v", body["truncated"], body["count"])
	}
	_, body = get(t, env, "/v1/risks/"+id+"/audit-log")
	if body["limit"] != float64(200) || body["truncated"] != false {
		t.Fatalf("default: limit=%v truncated=%v", body["limit"], body["truncated"])
	}
	if resp, _ := get(t, env, "/v1/risks/"+id+"/audit-log?limit=501"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit=501: status %d, want 400", resp.StatusCode)
	}
}
