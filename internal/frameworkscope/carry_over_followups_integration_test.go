//go:build integration

// Integration tests for the carry-over follow-ups (#2179, from the review of
// #2158 / #1793): the source revision's state is recorded under
// carried_over_from, the D5 fallback to the newest revision, D6's 409 on
// more than one prior version, the carry_over_rejected flag, a 409 for a
// source version the tenant cannot see, and the target row lock.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// D5 fallback: with no activated revision on N the newest revision is the
// source, and each carried row records that it came from a draft. D8: a
// rejected carry-over is flagged carry_over_rejected and not copied.
func TestHTTP_Applicability_CarryOverDraftSourceAndRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	if _, err := admin.Exec(context.Background(),
		`UPDATE framework_version_migrations SET status = 'rejected'
		  WHERE to_version_id = $1 AND requirement_code = 'C-02'`, f.ToVersionID); err != nil {
		t.Fatalf("reject C-02: %v", err)
	}
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}

	older := c.createScope(f.FromVersionID, "ISMS 2017 first draft")
	c.mustSet(older, f.From["C-01"], `{"decision":"applicable","implementation_ref":"older"}`)
	time.Sleep(10 * time.Millisecond) // created_at orders the revisions
	newer := c.createScope(f.FromVersionID, "ISMS 2017 second draft")
	c.mustSet(newer, f.From["C-01"], `{"decision":"applicable","implementation_ref":"newer"}`)
	c.mustSet(newer, f.From["C-02"], applicableBody)

	draft := c.createScope(f.ToVersionID, "ISMS 2022")
	res := c.mustCarryOver(draft)
	if res.SourceScopeID == nil || *res.SourceScopeID != newer {
		t.Fatalf("source scope = %v, want the newest revision %s", res.SourceScopeID, newer)
	}
	if len(res.Copied) != 1 {
		t.Fatalf("copied = %d, want 1 (C-01 only); %+v", len(res.Copied), res)
	}
	if got := reasonsByCode(res.Flagged)["C-02"]; got != "carry_over_rejected" {
		t.Fatalf("C-02 flag = %q, want carry_over_rejected; flagged %v", got, res.Flagged)
	}
	after := decisionsByCode(c, draft)
	if d := after["C-01"]; d == nil || d.ImplementationRef != "newer" {
		t.Fatalf("C-01 on N+1 = %+v, want the newest revision's decision", d)
	}
	if after["C-02"] != nil {
		t.Fatalf("C-02 (rejected carry-over) on N+1 = %+v, want undecided", after["C-02"])
	}
	rows := carryOverRows(t, tenant, draft)
	if len(rows) != 1 {
		t.Fatalf("carry_over rows = %d, want 1", len(rows))
	}
	src, _ := rows[0].After["carried_over_from"].(map[string]any)
	if src["framework_scope_id"] != newer || src["framework_scope_state"] != "draft" {
		t.Fatalf("carried_over_from = %v, want scope %s in state draft", src, newer)
	}
}

// D6: queue rows into N+1 from more than one prior version is a 409, and
// nothing is written.
func TestHTTP_Applicability_CarryOverAmbiguousSource(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	ctx := context.Background()
	var fwID uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_id FROM framework_versions WHERE id = $1`, f.ToVersionID).Scan(&fwID); err != nil {
		t.Fatalf("framework id: %v", err)
	}
	other := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2013', 'legacy')`, other, fwID); err != nil {
		t.Fatalf("seed version 2013: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO framework_version_migrations
		(framework_id, from_version_id, to_version_id, to_requirement_id, requirement_code, match_kind, status)
		VALUES ($1, $2, $3, $4, 'C-05', 'added', 'pending')`, fwID, other, f.ToVersionID, f.To["C-05"]); err != nil {
		t.Fatalf("seed migration from 2013: %v", err)
	}
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	oldRev := c.createScope(f.FromVersionID, "ISMS 2017")
	c.mustSet(oldRev, f.From["C-01"], applicableBody)
	draft := c.createScope(f.ToVersionID, "ISMS 2022")

	if code, _, payload := c.carryOver(draft); code != http.StatusConflict {
		t.Fatalf("carry-over with two prior versions: status %d, want 409; body %s", code, payload)
	}
	if n := len(carryOverRows(t, tenant, draft)); n != 0 {
		t.Fatalf("carry_over rows after 409 = %d, want 0", n)
	}
}

// A queue that leads from another tenant's custom version is a 409, not a
// 500, and the body does not name the version or its tenant.
func TestHTTP_Applicability_CarryOverInvisibleSource(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	owner := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	if _, err := admin.Exec(context.Background(),
		`UPDATE framework_versions SET tenant_id = $1 WHERE id = $2`, owner, f.FromVersionID); err != nil {
		t.Fatalf("make version N tenant-custom: %v", err)
	}
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	draft := c.createScope(f.ToVersionID, "ISMS 2022")

	code, _, payload := c.carryOver(draft)
	if code != http.StatusConflict {
		t.Fatalf("carry-over from an invisible version: status %d, want 409; body %s", code, payload)
	}
	for _, leak := range []string{f.FromVersionID.String(), owner, "2017"} {
		if strings.Contains(string(payload), leak) {
			t.Fatalf("409 body leaks %q: %s", leak, payload)
		}
	}
}

// The carry-over holds the target row FOR UPDATE: while a hand edit holds it
// (as the decision guard trigger does, FOR SHARE) the carry-over waits, and
// once the edit commits the carry-over sees it and leaves it alone.
func TestHTTP_Applicability_CarryOverLocksTarget(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedCarryFixture(t, admin)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	oldRev := c.createScope(f.FromVersionID, "ISMS 2017")
	c.mustSet(oldRev, f.From["C-01"], applicableBody)
	c.mustSet(oldRev, f.From["C-02"], applicableBody)
	draft := c.createScope(f.ToVersionID, "ISMS 2022")

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
	if _, err := tx.Exec(ctx, `SELECT 1 FROM framework_scopes WHERE id = $1 FOR SHARE`, draft); err != nil {
		t.Fatalf("share-lock draft: %v", err)
	}

	// The request runs off the test goroutine, so it reports errors on the
	// channel rather than through t.
	type result struct {
		code    int
		payload []byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost,
			c.base+"/v1/framework-scopes/"+draft+"/applicability:carry-over", nil)
		if err != nil {
			done <- result{err: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+c.bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(resp.Body)
		done <- result{resp.StatusCode, payload, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("carry-over returned (status %d) while the draft was locked by a hand edit", r.code)
	case <-time.After(500 * time.Millisecond):
	}

	if _, err := tx.Exec(ctx, `INSERT INTO framework_scope_applicability
		(tenant_id, framework_scope_id, framework_requirement_id, decision, justification, updated_by)
		VALUES ($1, $2, $3, 'excluded', 'hand edit', 'tester')`, tenant, draft, f.To["C-01"]); err != nil {
		t.Fatalf("hand edit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit hand edit: %v", err)
	}

	var r result
	select {
	case r = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("carry-over did not return after the hand edit committed")
	}
	if r.err != nil {
		t.Fatalf("carry-over request: %v", r.err)
	}
	if r.code != http.StatusOK {
		t.Fatalf("carry-over: status %d; body %s", r.code, r.payload)
	}
	var res carryResp
	if err := json.Unmarshal(r.payload, &res); err != nil {
		t.Fatalf("unmarshal carry-over: %v; body %s", err, r.payload)
	}
	if got := reasonsByCode(res.Skipped)["C-01"]; got != "already_decided" {
		t.Fatalf("C-01 = %q, want already_decided; skipped %v", got, res.Skipped)
	}
	if len(res.Copied) != 1 {
		t.Fatalf("copied = %d, want 1 (C-02)", len(res.Copied))
	}
	if d := decisionsByCode(c, draft)["C-01"]; d == nil || d.Justification != "hand edit" {
		t.Fatalf("hand edit overwritten: C-01 = %+v", d)
	}
}
