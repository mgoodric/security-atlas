//go:build integration

// Integration tests for #1965: activated and superseded revisions are
// immutable. A predicate edit on one is refused (409 at the API, a
// check_violation at the DB), a new revision is made from it with
// POST /v1/framework-scopes {"from_scope_id": ...}, and an as_of read
// returns the predicate that was in force then.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const prodPredicate = `{"op":"eq","dim":"environment","value":"prod"}`

type scopeResp struct {
	FrameworkScope struct {
		ID                 string          `json:"id"`
		FrameworkVersionID string          `json:"framework_version_id"`
		Name               string          `json:"name"`
		State              string          `json:"state"`
		Predicate          json.RawMessage `json:"predicate"`
	} `json:"framework_scope"`
}

func patchPredicate(c soaClient, scopeID, predicate string) (int, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPatch, c.base+"/v1/framework-scopes/"+scopeID, c.bearer,
		fmt.Sprintf(`{"predicate":%s}`, predicate))
	return resp.StatusCode, payload
}

func createRevision(c soaClient, body string) (int, scopeResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPost, c.base+"/v1/framework-scopes", c.bearer, body)
	var out scopeResp
	if resp.StatusCode == http.StatusCreated {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal create: %v", err)
		}
	}
	return resp.StatusCode, out, payload
}

// sameJSON reports whether two JSON documents are equal after decoding.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	return string(xs) == string(ys)
}

func TestHTTP_PredicateFrozen_NewRevisionAndAsOf(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}

	now := time.Now().UTC().Truncate(time.Second)
	activate := func(id string, at time.Time) {
		t.Helper()
		c.transition(id, "submit", bearer, "")
		c.transition(id, "approve", approver, "")
		c.transition(id, "activate", bearer, fmt.Sprintf(`{"effective_from":%q}`, at.Format(time.RFC3339)))
	}

	// Revision A: predicate true plus the fixture decisions, in force from
	// two hours ago.
	a := c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, a)
	activate(a, now.Add(-2*time.Hour))

	// The activated predicate is frozen.
	if code, payload := patchPredicate(c, a, prodPredicate); code != http.StatusConflict {
		t.Fatalf("PATCH activated predicate: status %d, want 409; body %s", code, payload)
	}

	// A new revision from A: same version and name, the new predicate, A's
	// decisions copied.
	code, b, payload := createRevision(c, fmt.Sprintf(`{"from_scope_id":%q,"predicate":%s}`, a, prodPredicate))
	if code != http.StatusCreated {
		t.Fatalf("create revision from A: status %d; body %s", code, payload)
	}
	if b.FrameworkScope.State != "draft" || b.FrameworkScope.Name != "ISMS" ||
		b.FrameworkScope.FrameworkVersionID != f.FrameworkVersionID.String() ||
		!sameJSON(t, b.FrameworkScope.Predicate, []byte(prodPredicate)) {
		t.Fatalf("revision B = %+v", b.FrameworkScope)
	}
	bID := b.FrameworkScope.ID
	bList := c.mustList(bID)
	if bList.Counts.Excluded != 2 || bList.Counts.Applicable != 5 || bList.Counts.Undecided != 1 {
		t.Fatalf("B decisions not copied from A: counts %+v", bList.Counts)
	}
	// Justification and implementation reference are copied with the decision.
	wantCopied := map[string][2]string{
		"R-01": {"No in-house software development", ""},
		"R-02": {"No cardholder data processed", ""},
		"R-04": {"", "https://wiki.example/isms/A"},
		"R-08": {"", "https://wiki.example/isms/A"},
	}
	for _, r := range bList.Requirements {
		want, ok := wantCopied[r.Code]
		if !ok {
			continue
		}
		if r.Justification != want[0] || r.ImplementationRef != want[1] {
			t.Fatalf("B %s: justification %q implementation_ref %q, want %q %q",
				r.Code, r.Justification, r.ImplementationRef, want[0], want[1])
		}
		delete(wantCopied, r.Code)
	}
	if len(wantCopied) != 0 {
		t.Fatalf("B missing requirements %v", wantCopied)
	}
	rows := carryOverRows(t, tenant, bID)
	if len(rows) != 7 {
		t.Fatalf("B carry_over log rows = %d, want 7", len(rows))
	}
	for _, r := range rows {
		src, ok := r.After["carried_over_from"].(map[string]any)
		if !ok || src["framework_scope_id"] != a || src["framework_version_id"] != f.FrameworkVersionID.String() {
			t.Fatalf("carried_over_from = %v", r.After["carried_over_from"])
		}
		if _, has := src["migration_id"]; has {
			t.Fatalf("same-version copy names a migration: %v", src)
		}
	}
	// A is untouched by the copy.
	if aList := c.mustList(a); aList.FrameworkScope.State != "activated" || aList.Counts.Excluded != 2 {
		t.Fatalf("A after copy: state %s counts %+v", aList.FrameworkScope.State, aList.Counts)
	}

	// Activating B supersedes A: the trigger lets the state change through.
	activate(bID, now.Add(-time.Hour))
	got := c.mustList(a)
	if got.FrameworkScope.State != "superseded" {
		t.Fatalf("A after B activated: state %s, want superseded", got.FrameworkScope.State)
	}

	// The superseded predicate is frozen too.
	if code, payload := patchPredicate(c, a, `{"op":"true"}`); code != http.StatusConflict {
		t.Fatalf("PATCH superseded predicate: status %d, want 409; body %s", code, payload)
	}

	asOf := func(at time.Time) (string, json.RawMessage) {
		t.Helper()
		q := url.Values{"framework_version": {f.FrameworkVersionID.String()}, "as_of": {at.Format(time.RFC3339)}}
		resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/framework-scopes?"+q.Encode(), bearer, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("as_of %s: status %d; body %s", at, resp.StatusCode, payload)
		}
		var out struct {
			FrameworkScopes []struct {
				ID        string          `json:"id"`
				Predicate json.RawMessage `json:"predicate"`
			} `json:"framework_scopes"`
		}
		if err := json.Unmarshal(payload, &out); err != nil || len(out.FrameworkScopes) != 1 {
			t.Fatalf("as_of %s: %v; body %s", at, err, payload)
		}
		return out.FrameworkScopes[0].ID, out.FrameworkScopes[0].Predicate
	}

	// While A was in force, its predicate; now, B's.
	if id, pred := asOf(now.Add(-90 * time.Minute)); id != a || !sameJSON(t, pred, []byte(`{"op":"true"}`)) {
		t.Fatalf("as_of during A: id %s predicate %s", id, pred)
	}
	if id, pred := asOf(now.Add(time.Minute)); id != bID || !sameJSON(t, pred, []byte(prodPredicate)) {
		t.Fatalf("as_of now: id %s predicate %s", id, pred)
	}

	// The DB trigger freezes the row off the API path, for both the
	// activated and the superseded revision: the predicate, effective_from,
	// and the state (so a step back to draft cannot reopen the predicate or
	// the decisions). Each write runs in its own tx, as one statement, and
	// the two-step bypass as two statements in one tx.
	app := dbtest.NewAppPool(t)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	writes := []struct {
		name  string
		stmts []string
	}{
		{"predicate", []string{`UPDATE framework_scopes SET predicate = '{"op":"false"}'::jsonb, predicate_hash = 'x' WHERE id = $1`}},
		{"effective_from", []string{`UPDATE framework_scopes SET effective_from = effective_from - interval '1 day' WHERE id = $1`}},
		{"back to draft", []string{`UPDATE framework_scopes SET state = 'draft' WHERE id = $1`}},
		{"back to approved", []string{`UPDATE framework_scopes SET state = 'approved' WHERE id = $1`}},
		{"draft then predicate", []string{
			`UPDATE framework_scopes SET state = 'draft' WHERE id = $1`,
			`UPDATE framework_scopes SET predicate = '{"op":"false"}'::jsonb, predicate_hash = 'x' WHERE id = $1`,
		}},
		{"draft then decision", []string{
			`UPDATE framework_scopes SET state = 'draft' WHERE id = $1`,
			`UPDATE framework_scope_applicability SET decision = 'applicable', justification = '' WHERE framework_scope_id = $1`,
		}},
	}
	for _, id := range []string{a, bID} {
		for _, w := range writes {
			err := withAppTenant(ctx, app, func(tx pgx.Tx) error {
				for _, s := range w.stmts {
					if _, err := tx.Exec(ctx, s, id); err != nil {
						return err
					}
				}
				return nil
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != "framework_scopes_predicate_frozen" {
				t.Errorf("DB %s on %s: err = %v, want framework_scopes_predicate_frozen", w.name, id, err)
			}
		}
	}

	// superseded_by and superseded_at stay writable on a frozen row: the
	// supersede path writes them.
	if err := withAppTenant(ctx, app, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE framework_scopes SET superseded_by = $2, superseded_at = now() WHERE id = $1`, a, bID)
		return err
	}); err != nil {
		t.Fatalf("superseded_by write on superseded A: %v", err)
	}

	// Nothing above changed A or B.
	if got := c.mustList(a); got.FrameworkScope.State != "superseded" || got.Counts.Excluded != 2 {
		t.Fatalf("A after refused writes: state %s counts %+v", got.FrameworkScope.State, got.Counts)
	}
	if got := c.mustList(bID); got.FrameworkScope.State != "activated" || got.Counts.Excluded != 2 {
		t.Fatalf("B after refused writes: state %s counts %+v", got.FrameworkScope.State, got.Counts)
	}
}

func TestHTTP_NewRevision_Validation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	other := seedSoAFixtureAs(t, admin, tenant, "", "Other framework (#1965)")
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	a := c.createScope(f.FrameworkVersionID, "ISMS")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"unknown source", fmt.Sprintf(`{"from_scope_id":%q}`, uuid.New()), http.StatusNotFound},
		{"malformed source", `{"from_scope_id":"nope"}`, http.StatusBadRequest},
		{"other version", fmt.Sprintf(`{"from_scope_id":%q,"framework_version_id":%q}`, a, other.FrameworkVersionID), http.StatusBadRequest},
		{"no source, no name", fmt.Sprintf(`{"framework_version_id":%q,"predicate":{"op":"true"}}`, f.FrameworkVersionID), http.StatusBadRequest},
	}
	for _, tc := range cases {
		if code, _, payload := createRevision(c, tc.body); code != tc.want {
			t.Errorf("%s: status %d, want %d; body %s", tc.name, code, tc.want, payload)
		}
	}

	// A draft source works too, and an explicit name overrides the source's.
	code, b, payload := createRevision(c, fmt.Sprintf(`{"from_scope_id":%q,"name":"ISMS v2"}`, a))
	if code != http.StatusCreated || b.FrameworkScope.Name != "ISMS v2" ||
		!sameJSON(t, b.FrameworkScope.Predicate, []byte(`{"op":"true"}`)) {
		t.Fatalf("create from draft: status %d; body %s", code, payload)
	}
}
