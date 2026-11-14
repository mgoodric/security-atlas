//go:build integration

// Integration tests for Statement of Applicability decisions on a
// framework-scope revision (issue #1787, spec #1744): set and list over the
// HTTP API, the exclusion-justification rule (domain + DB CHECK), the
// approval reset, activate/supersede with as_of reads, the workflow log,
// and RLS isolation.

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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// soaFixture is the spec's test fixture: a small framework version with
// eight requirements and crosswalk edges, and three tenant controls (two
// passing, one failing). The intended decisions are:
//
//	R-01 excluded, with passing control C1 behind it (a future contradiction)
//	R-02 excluded, with no control behind it
//	R-03 undecided
//	R-04..R-08 applicable
type soaFixture struct {
	FrameworkVersionID uuid.UUID
	Req                map[string]uuid.UUID // code -> requirement id
	Codes              []string
	Controls           map[string]uuid.UUID // C1, C2 (passing), C3 (failing)
}

func seedSoAFixture(t *testing.T, admin *pgxpool.Pool, tenant string) soaFixture {
	t.Helper()
	ctx := context.Background()
	uniq := uuid.NewString()[:8]
	fwID, scfFVID, fvID := uuid.New(), uuid.New(), uuid.New()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("framework", `INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		VALUES ($1, NULL, $2, 'SoA test framework (#1787)', 'tester')`, fwID, "soa-1787-"+uniq)
	exec("scf version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-1787', 'legacy')`, scfFVID, fwID)
	exec("framework version", `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2022', 'current')`, fvID, fwID)

	anchors := map[string]uuid.UUID{}
	for _, a := range []string{"A1", "A2", "A3"} {
		id := uuid.New()
		exec("anchor", `INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
			VALUES ($1, $2, $3, 'IAC', $4, '')`, id, scfFVID, "SOA-"+uniq+"-"+a, "anchor "+a)
		anchors[a] = id
	}

	f := soaFixture{FrameworkVersionID: fvID, Req: map[string]uuid.UUID{}, Controls: map[string]uuid.UUID{}}
	// Crosswalk: R-01 -> A1 (C1 passing), R-04 -> A2 (C2 passing),
	// R-05 -> A3 (C3 failing), R-06 -> A1. R-02 has no edge.
	edges := map[string]string{"R-01": "A1", "R-04": "A2", "R-05": "A3", "R-06": "A1"}
	for i := 1; i <= 8; i++ {
		code := fmt.Sprintf("R-%02d", i)
		id := uuid.New()
		exec("requirement", `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, $4, '')`, id, fvID, code, "requirement "+code)
		f.Req[code] = id
		f.Codes = append(f.Codes, code)
		if a, ok := edges[code]; ok {
			exec("edge", `INSERT INTO fw_to_scf_edges
				(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
				VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', '')`, uuid.New(), id, anchors[a])
		}
	}

	for _, c := range []struct{ name, anchor, result string }{
		{"C1", "A1", "pass"}, {"C2", "A2", "pass"}, {"C3", "A3", "fail"},
	} {
		id := uuid.New()
		exec("control", `INSERT INTO controls (
				id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
				control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
				evidence_queries, manual_evidence_schema, linked_policy_ids,
				freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
				bundle_uploaded_at, bundle_uploaded_by
			) VALUES (
				$1, $2, $3, 1, $4, $5, $6, '',
				'access_control', 'automated', 'security', 'active', '',
				'[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'test')`,
			id, tenant, "soa_1787_"+uniq+"_"+c.name, "SOA-"+uniq+"-"+c.anchor, anchors[c.anchor], "control "+c.name)
		exec("evaluation", `INSERT INTO control_evaluations (
				id, tenant_id, control_id, scope_cell_id, eval_run_id,
				evaluated_at, result, freshness_status, evidence_count_in_window, trigger
			) VALUES ($1, $2, $3, NULL, $4, now(), $5, 'fresh', 1, 'manual')`,
			uuid.New(), tenant, id, uuid.New(), c.result)
		f.Controls[c.name] = id
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM framework_scopes WHERE framework_version_id = $1`, fvID},
			{`DELETE FROM control_evaluations WHERE tenant_id = $1`, tenant},
			{`DELETE FROM controls WHERE tenant_id = $1`, tenant},
			{`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (SELECT id FROM framework_requirements WHERE framework_version_id = $1)`, fvID},
			{`DELETE FROM framework_requirements WHERE framework_version_id = $1`, fvID},
			{`DELETE FROM scf_anchors WHERE framework_version_id = $1`, scfFVID},
			{`UPDATE frameworks SET latest_version_id = NULL WHERE id = $1`, fwID},
			{`DELETE FROM framework_versions WHERE framework_id = $1`, fwID},
			{`DELETE FROM frameworks WHERE id = $1`, fwID},
		} {
			if _, err := admin.Exec(c, q.sql, q.arg); err != nil {
				t.Logf("cleanup %q: %v", q.sql, err)
			}
		}
	})
	return f
}

// soaTenant is freshTenant plus the tables the SoA fixture writes.
func soaTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"framework_scope_applicability",
		"framework_scope_workflow_log",
		"framework_scopes",
		"control_evaluations",
		"controls",
	)
}

type applicabilityResp struct {
	FrameworkScope *struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"framework_scope"`
	Requirements []struct {
		RequirementID     string  `json:"requirement_id"`
		Code              string  `json:"code"`
		Decision          *string `json:"decision"`
		Undecided         bool    `json:"undecided"`
		Justification     string  `json:"justification"`
		ImplementationRef string  `json:"implementation_ref"`
	} `json:"requirements"`
	Counts struct {
		Total      int `json:"total"`
		Applicable int `json:"applicable"`
		Excluded   int `json:"excluded"`
		Undecided  int `json:"undecided"`
	} `json:"counts"`
}

type setResp struct {
	Decision struct {
		Decision      string `json:"decision"`
		Justification string `json:"justification"`
	} `json:"decision"`
	FrameworkScope struct {
		State      string     `json:"state"`
		ApprovedAt *time.Time `json:"approved_at"`
	} `json:"framework_scope"`
	ApprovalInvalidated bool `json:"approval_invalidated"`
}

type soaClient struct {
	t      *testing.T
	base   string
	bearer string
}

func (c soaClient) createScope(fvID uuid.UUID, name string) string {
	c.t.Helper()
	body := fmt.Sprintf(`{"framework_version_id":%q,"name":%q,"predicate":{"op":"true"}}`, fvID, name)
	resp, payload := doJSON(c.t, http.MethodPost, c.base+"/v1/framework-scopes", c.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("create scope: status %d; body %s", resp.StatusCode, payload)
	}
	var out struct {
		FrameworkScope struct {
			ID string `json:"id"`
		} `json:"framework_scope"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		c.t.Fatalf("unmarshal create: %v", err)
	}
	return out.FrameworkScope.ID
}

func (c soaClient) set(scopeID string, reqID uuid.UUID, body string) (int, setResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPut,
		c.base+"/v1/framework-scopes/"+scopeID+"/applicability/"+reqID.String(), c.bearer, body)
	var out setResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal set: %v", err)
		}
	}
	return resp.StatusCode, out, payload
}

func (c soaClient) mustSet(scopeID string, reqID uuid.UUID, body string) setResp {
	c.t.Helper()
	code, out, payload := c.set(scopeID, reqID, body)
	if code != http.StatusOK {
		c.t.Fatalf("set decision: status %d; body %s", code, payload)
	}
	return out
}

func (c soaClient) get(path string) (int, applicabilityResp, []byte) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodGet, c.base+path, c.bearer, "")
	var out applicabilityResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(payload, &out); err != nil {
			c.t.Fatalf("unmarshal list: %v", err)
		}
	}
	return resp.StatusCode, out, payload
}

func (c soaClient) mustList(scopeID string) applicabilityResp {
	c.t.Helper()
	code, out, payload := c.get("/v1/framework-scopes/" + scopeID + "/applicability")
	if code != http.StatusOK {
		c.t.Fatalf("list applicability: status %d; body %s", code, payload)
	}
	return out
}

func (c soaClient) transition(scopeID, verb, bearer, body string) {
	c.t.Helper()
	resp, payload := doJSON(c.t, http.MethodPatch, c.base+"/v1/framework-scopes/"+scopeID+"/"+verb, bearer, body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("%s: status %d; body %s", verb, resp.StatusCode, payload)
	}
}

const (
	applicableBody = `{"decision":"applicable","implementation_ref":"https://wiki.example/isms/A"}`
	excludedBody   = `{"decision":"excluded","justification":"No in-house software development"}`
)

// applyFixtureDecisions records the fixture's intended decisions on a scope.
func applyFixtureDecisions(c soaClient, f soaFixture, scopeID string) {
	c.t.Helper()
	c.mustSet(scopeID, f.Req["R-01"], excludedBody)
	c.mustSet(scopeID, f.Req["R-02"], `{"decision":"excluded","justification":"No cardholder data processed"}`)
	for _, code := range []string{"R-04", "R-05", "R-06", "R-07", "R-08"} {
		c.mustSet(scopeID, f.Req[code], applicableBody)
	}
}

// Setting `excluded` without a justification fails in domain validation
// (HTTP 400) and at the DB CHECK; with one it succeeds.
func TestHTTP_Applicability_ExclusionRequiresJustification(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")

	for _, body := range []string{
		`{"decision":"excluded"}`,
		`{"decision":"excluded","justification":"   "}`,
	} {
		if code, _, payload := c.set(scopeID, f.Req["R-02"], body); code != http.StatusBadRequest {
			t.Fatalf("exclude without justification %s: status %d, want 400; body %s", body, code, payload)
		}
	}
	if code, _, _ := c.set(scopeID, f.Req["R-02"], `{"decision":"maybe"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid decision: status %d, want 400", code)
	}

	// The DB CHECK refuses the same row when the domain check is bypassed.
	app := dbtest.NewAppPool(t)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	err := withAppTenant(ctx, app, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO framework_scope_applicability
			(tenant_id, framework_scope_id, framework_requirement_id, decision, justification, updated_by)
			VALUES ($1, $2, $3, 'excluded', ' ', 'test')`, tenant, scopeID, f.Req["R-02"])
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "framework_scope_applicability_excluded_justified" {
		t.Fatalf("DB insert of unjustified exclusion: err = %v, want CHECK framework_scope_applicability_excluded_justified", err)
	}

	got := c.mustSet(scopeID, f.Req["R-02"], `{"decision":"excluded","justification":"No cardholder data processed"}`)
	if got.Decision.Decision != "excluded" || got.Decision.Justification != "No cardholder data processed" {
		t.Fatalf("set excluded: got %+v", got.Decision)
	}
	if list := c.mustList(scopeID); list.Counts.Excluded != 1 {
		t.Fatalf("after one exclusion: counts %+v", list.Counts)
	}
}

// Listing returns every requirement of the version, with undecided ones
// marked and counted.
func TestHTTP_Applicability_ListIncludesUndecided(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, _ := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")

	// Before any decision: all eight undecided.
	if list := c.mustList(scopeID); list.Counts.Total != 8 || list.Counts.Undecided != 8 {
		t.Fatalf("fresh scope counts = %+v, want total 8 undecided 8", list.Counts)
	}

	applyFixtureDecisions(c, f, scopeID)
	list := c.mustList(scopeID)
	if list.FrameworkScope == nil || list.FrameworkScope.ID != scopeID {
		t.Fatalf("framework_scope = %+v, want %s", list.FrameworkScope, scopeID)
	}
	if want := (struct{ Total, Applicable, Excluded, Undecided int }{8, 5, 2, 1}); list.Counts.Total != want.Total ||
		list.Counts.Applicable != want.Applicable || list.Counts.Excluded != want.Excluded || list.Counts.Undecided != want.Undecided {
		t.Fatalf("counts = %+v, want %+v", list.Counts, want)
	}
	if len(list.Requirements) != 8 {
		t.Fatalf("requirements = %d, want 8", len(list.Requirements))
	}
	for i, r := range list.Requirements {
		if r.Code != f.Codes[i] || r.RequirementID != f.Req[r.Code].String() {
			t.Fatalf("requirement %d = %s/%s, want %s", i, r.Code, r.RequirementID, f.Codes[i])
		}
		switch r.Code {
		case "R-03":
			if !r.Undecided || r.Decision != nil {
				t.Fatalf("R-03 should be undecided: %+v", r)
			}
		case "R-01", "R-02":
			if r.Undecided || r.Decision == nil || *r.Decision != "excluded" || r.Justification == "" {
				t.Fatalf("%s should be excluded with a justification: %+v", r.Code, r)
			}
		default:
			if r.Undecided || r.Decision == nil || *r.Decision != "applicable" || r.ImplementationRef == "" {
				t.Fatalf("%s should be applicable with an implementation ref: %+v", r.Code, r)
			}
		}
	}

	// A requirement outside the scope's framework version is refused.
	if code, _, _ := c.set(scopeID, uuid.New(), applicableBody); code != http.StatusNotFound {
		t.Fatalf("unknown requirement: status %d, want 404", code)
	}
}

// Changing a decision on an approved (or in-review) revision returns it to
// draft and clears the approval; on a draft it doesn't change state.
func TestHTTP_Applicability_EditResetsApproval(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")

	// Draft: no state change.
	got := c.mustSet(scopeID, f.Req["R-04"], applicableBody)
	if got.FrameworkScope.State != "draft" || got.ApprovalInvalidated {
		t.Fatalf("draft edit: state %s invalidated %v, want draft/false", got.FrameworkScope.State, got.ApprovalInvalidated)
	}

	// Review: back to draft.
	c.transition(scopeID, "submit", bearer, "")
	got = c.mustSet(scopeID, f.Req["R-05"], applicableBody)
	if got.FrameworkScope.State != "draft" || !got.ApprovalInvalidated {
		t.Fatalf("review edit: state %s invalidated %v, want draft/true", got.FrameworkScope.State, got.ApprovalInvalidated)
	}

	// Approved: back to draft, approval cleared.
	c.transition(scopeID, "submit", bearer, "")
	c.transition(scopeID, "approve", approver, "")
	got = c.mustSet(scopeID, f.Req["R-01"], excludedBody)
	if got.FrameworkScope.State != "draft" || !got.ApprovalInvalidated || got.FrameworkScope.ApprovedAt != nil {
		t.Fatalf("approved edit: state %s invalidated %v approved_at %v, want draft/true/nil",
			got.FrameworkScope.State, got.ApprovalInvalidated, got.FrameworkScope.ApprovedAt)
	}
	var approver2 *string
	if err := admin.QueryRow(context.Background(),
		`SELECT approver_user_id::text FROM framework_scopes WHERE id = $1`, scopeID).Scan(&approver2); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	if approver2 != nil {
		t.Fatalf("approver_user_id = %v after reset, want NULL", *approver2)
	}
}

// Activate, then supersede: the old revision's decisions stay readable and
// frozen, and as_of returns the decisions in force at that time.
func TestHTTP_Applicability_ActivateSupersedeAsOf(t *testing.T) {
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

	// Revision A: the fixture decisions, effective two hours ago.
	a := c.createScope(f.FrameworkVersionID, "ISMS rev A")
	applyFixtureDecisions(c, f, a)
	activate(a, now.Add(-2*time.Hour))

	// An activated revision's decisions are frozen.
	if code, _, payload := c.set(a, f.Req["R-03"], applicableBody); code != http.StatusConflict {
		t.Fatalf("edit activated revision: status %d, want 409; body %s", code, payload)
	}

	// Revision B: R-01 brought back into scope, effective one hour ago.
	b := c.createScope(f.FrameworkVersionID, "ISMS rev B")
	applyFixtureDecisions(c, f, b)
	c.mustSet(b, f.Req["R-01"], applicableBody)
	c.mustSet(b, f.Req["R-03"], applicableBody)
	activate(b, now.Add(-time.Hour))

	// A is superseded; its decisions are still readable, unchanged, frozen.
	oldList := c.mustList(a)
	if oldList.FrameworkScope.State != "superseded" || oldList.Counts.Excluded != 2 || oldList.Counts.Undecided != 1 {
		t.Fatalf("superseded A: state %s counts %+v", oldList.FrameworkScope.State, oldList.Counts)
	}
	if code, _, _ := c.set(a, f.Req["R-03"], applicableBody); code != http.StatusConflict {
		t.Fatalf("edit superseded revision: status %d, want 409", code)
	}

	asOf := func(at time.Time) applicabilityResp {
		t.Helper()
		q := url.Values{"framework_version": {f.FrameworkVersionID.String()}, "as_of": {at.Format(time.RFC3339)}}
		code, out, payload := c.get("/v1/framework-scopes/applicability?" + q.Encode())
		if code != http.StatusOK {
			t.Fatalf("as_of %s: status %d; body %s", at, code, payload)
		}
		return out
	}
	decisionOf := func(r applicabilityResp, code string) string {
		for _, x := range r.Requirements {
			if x.Code == code {
				if x.Decision == nil {
					return "undecided"
				}
				return *x.Decision
			}
		}
		t.Fatalf("requirement %s missing", code)
		return ""
	}

	// Before A took effect: no revision in force, everything undecided.
	before := asOf(now.Add(-3 * time.Hour))
	if before.FrameworkScope != nil || before.Counts.Undecided != 8 {
		t.Fatalf("as_of before A: scope %+v counts %+v", before.FrameworkScope, before.Counts)
	}
	// While A was in force: A's decisions.
	mid := asOf(now.Add(-90 * time.Minute))
	if mid.FrameworkScope == nil || mid.FrameworkScope.ID != a || decisionOf(mid, "R-01") != "excluded" || decisionOf(mid, "R-03") != "undecided" {
		t.Fatalf("as_of during A: scope %+v R-01 %s R-03 %s", mid.FrameworkScope, decisionOf(mid, "R-01"), decisionOf(mid, "R-03"))
	}
	// Now: B's decisions.
	cur := asOf(now.Add(time.Minute))
	if cur.FrameworkScope == nil || cur.FrameworkScope.ID != b || decisionOf(cur, "R-01") != "applicable" || cur.Counts.Undecided != 0 {
		t.Fatalf("as_of now: scope %+v R-01 %s counts %+v", cur.FrameworkScope, decisionOf(cur, "R-01"), cur.Counts)
	}

	// The DB guard freezes the activated revision even off the API path.
	app := dbtest.NewAppPool(t)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	err := withAppTenant(ctx, app, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE framework_scope_applicability SET decision = 'applicable', justification = ''
			WHERE framework_scope_id = $1 AND framework_requirement_id = $2`, b, f.Req["R-02"])
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("DB update on activated revision: err = %v, want check_violation", err)
	}
}

// Each decision change appears in the workflow log, with before and after
// decisions and the reset to draft in from_state/to_state.
func TestHTTP_Applicability_HistoryLog(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID := c.createScope(f.FrameworkVersionID, "ISMS")

	c.mustSet(scopeID, f.Req["R-01"], applicableBody)
	c.mustSet(scopeID, f.Req["R-01"], excludedBody)
	c.transition(scopeID, "submit", bearer, "")
	c.transition(scopeID, "approve", approver, "")
	c.mustSet(scopeID, f.Req["R-02"], excludedBody)

	var changes []logRow
	for _, r := range readWorkflowLog(t, tenant, scopeID) {
		if r.Action == "applicability.update" {
			changes = append(changes, r)
		}
	}
	if len(changes) != 3 {
		t.Fatalf("applicability.update rows = %d, want 3", len(changes))
	}
	decisionIn := func(img map[string]any) any {
		a, ok := img["applicability"].(map[string]any)
		if !ok {
			return nil
		}
		return a["decision"]
	}
	first, second, third := changes[0], changes[1], changes[2]
	if first.Before["applicability"] != nil || decisionIn(first.After) != "applicable" {
		t.Fatalf("first change before/after = %v / %v", first.Before["applicability"], first.After["applicability"])
	}
	if decisionIn(second.Before) != "applicable" || decisionIn(second.After) != "excluded" {
		t.Fatalf("second change before/after = %v / %v", second.Before["applicability"], second.After["applicability"])
	}
	if third.FromState == nil || *third.FromState != "approved" || third.ToState != "draft" {
		t.Fatalf("third change states = %v -> %s, want approved -> draft", third.FromState, third.ToState)
	}
	if req := third.After["applicability"].(map[string]any)["framework_requirement_id"]; req != f.Req["R-02"].String() {
		t.Fatalf("third change requirement = %v, want R-02", req)
	}
	for _, r := range changes {
		if r.Actor == "" || r.At.IsZero() {
			t.Fatalf("log row missing actor/time: %+v", r)
		}
	}
}

// Another tenant can neither read nor write the decisions.
func TestHTTP_Applicability_RLSIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenantA := soaTenant(t, admin)
	tenantB := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenantA)
	tsA, bearerA, _ := setupHTTPServer(t, tenantA)
	tsB, bearerB, _ := setupHTTPServer(t, tenantB)
	a := soaClient{t: t, base: tsA.URL, bearer: bearerA}
	b := soaClient{t: t, base: tsB.URL, bearer: bearerB}

	scopeID := a.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(a, f, scopeID)

	if code, _, _ := b.get("/v1/framework-scopes/" + scopeID + "/applicability"); code != http.StatusNotFound {
		t.Fatalf("tenant B list A's decisions: status %d, want 404", code)
	}
	if code, _, _ := b.set(scopeID, f.Req["R-03"], applicableBody); code != http.StatusNotFound {
		t.Fatalf("tenant B set on A's scope: status %d, want 404", code)
	}

	app := dbtest.NewAppPool(t)
	ctxB, _ := tenancy.WithTenant(context.Background(), tenantB)
	var visible int
	if err := withAppTenant(ctxB, app, func(tx pgx.Tx) error {
		return tx.QueryRow(ctxB, `SELECT count(*) FROM framework_scope_applicability WHERE framework_scope_id = $1`, scopeID).Scan(&visible)
	}); err != nil {
		t.Fatalf("tenant B count: %v", err)
	}
	if visible != 0 {
		t.Fatalf("tenant B sees %d of A's decisions, want 0", visible)
	}
	// A direct insert under B's GUC pointing at A's scope is refused.
	err := withAppTenant(ctxB, app, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctxB, `INSERT INTO framework_scope_applicability
			(tenant_id, framework_scope_id, framework_requirement_id, decision, updated_by)
			VALUES ($1, $2, $3, 'applicable', 'test')`, tenantB, scopeID, f.Req["R-03"])
		return err
	})
	if err == nil {
		t.Fatal("tenant B inserted a decision on tenant A's scope")
	}
	// And A's own view is untouched.
	if list := a.mustList(scopeID); list.Counts.Undecided != 1 || list.Counts.Excluded != 2 {
		t.Fatalf("tenant A counts after B's attempts = %+v", list.Counts)
	}
}
