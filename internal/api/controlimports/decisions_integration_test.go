//go:build integration

// #1782 — integration tests for approving and rejecting staged import rows.
// Real Postgres with RLS and the assembled router, as in integration_test.go,
// whose fixtures (seedCatalog, testdata/controls.csv) these reuse. Approval
// must create a control visible through the controls API, in the caller's
// tenant, bound in the batch's SCF release.
package controlimports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// decisionEnv serves one tenant with three bearers: a person (a "user:"
// subject), a machine token, and an owner credential that names no user.
type decisionEnv struct {
	testEnv
	user    uuid.UUID
	machine string
	nouser  string
}

func decisionServer(t *testing.T, app *pgxpool.Pool, tenant string) decisionEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	tenantID := uuid.MustParse(tenant)
	user := uuid.New()
	person := testjwt.AdminFor(tenantID)
	person.Subject = "user:" + user.String()
	machine := testjwt.AdminFor(tenantID)
	machine.Subject = "oauth_client:ci-runner"
	// Issue the tokens first: the first one attaches the JWT validator the
	// router is built with.
	env := decisionEnv{
		testEnv: testEnv{bearer: srv.IssueTestJWT(t, person)},
		user:    user,
		machine: srv.IssueTestJWT(t, machine),
		nouser:  srv.IssueTestJWT(t, testjwt.AdminFor(tenantID)),
	}
	env.server = httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(env.server.Close)
	return env
}

func doPost(t *testing.T, env testEnv, bearer, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	return do(t, req)
}

// stageFixture uploads testdata/controls.csv and returns the batch with its
// candidates computed (inline for a batch this small).
func stageFixture(t *testing.T, env testEnv, framework string) controlimport.Batch {
	t.Helper()
	status, raw := doUpload(t, env, upload{filename: "controls.csv", payload: fixtureCSV(t), framework: framework})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	b := decodeBatch(t, raw)
	if b.SuggestionsStatus != controlimport.SuggestionsReady {
		t.Fatalf("suggestions = %s, want ready", b.SuggestionsStatus)
	}
	return b
}

func rowByOrgID(t *testing.T, b controlimport.Batch, orgID string) controlimport.BatchControl {
	t.Helper()
	for _, r := range b.Staged {
		if r.ExternalID == orgID {
			return r
		}
	}
	t.Fatalf("batch has no staged row %s", orgID)
	return controlimport.BatchControl{}
}

func decisionPath(batch, row, verb string) string {
	return "/v1/controls/imports/" + batch + "/rows/" + row + "/" + verb
}

func decodeDecision(t *testing.T, raw []byte) controlimport.RowDecision {
	t.Helper()
	var d controlimport.RowDecision
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decode decision: %v; body=%s", err, raw)
	}
	return d
}

// listedControl is a row of GET /v1/controls?include=state.
type listedControl struct {
	ID          string  `json:"id"`
	BundleID    string  `json:"bundle_id"`
	SCFID       string  `json:"scf_id"`
	SCFAnchorID *string `json:"scf_anchor_id"`
}

func listControls(t *testing.T, env testEnv, bearer string) []listedControl {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/controls?include=state", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	status, raw := do(t, req)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/controls = %d; body=%s", status, raw)
	}
	var body struct {
		Controls []listedControl `json:"controls"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode controls: %v", err)
	}
	return body.Controls
}

// anchorRelease returns the SCF release (framework version label) an anchor
// id belongs to.
func anchorRelease(t *testing.T, admin *pgxpool.Pool, anchorID string) string {
	t.Helper()
	var release string
	if err := admin.QueryRow(context.Background(), `
		SELECT fv.version FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE a.id = $1::uuid`, anchorID).Scan(&release); err != nil {
		t.Fatalf("read anchor release: %v", err)
	}
	return release
}

// ----- AC: approving a row creates a control in the caller's tenant -----

func TestApproveCreatesControlInCallersTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	other := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)
	otherEnv := decisionServer(t, app, other)

	b := stageFixture(t, env.testEnv, cat.framework)
	row := rowByOrgID(t, b, "ORG-AC-01")
	if len(row.Candidates) == 0 || row.Candidates[0].SCFID != cat.anchors["mfa"] {
		t.Fatalf("ORG-AC-01 candidates = %+v, want the mfa anchor first", row.Candidates)
	}

	status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "approve"), map[string]any{"candidate_rank": 1})
	if status != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", status, raw)
	}
	d := decodeDecision(t, raw)
	if d.Status != controlimport.RowStatusApproved || d.SCFID != cat.anchors["mfa"] || d.SCFRelease != cat.scfRelease {
		t.Errorf("decision = %+v, want approved with %s in release %s", d, cat.anchors["mfa"], cat.scfRelease)
	}
	if d.BundleID != "org-ac-01" || d.DecidedBy != env.user.String() {
		t.Errorf("bundle %q decided by %q, want org-ac-01 by %s", d.BundleID, d.DecidedBy, env.user)
	}

	// Visible in GET /v1/controls for the caller's tenant, anchored in the
	// batch's SCF release.
	var got *listedControl
	for _, c := range listControls(t, env.testEnv, env.bearer) {
		if c.ID == d.ControlID {
			c := c
			got = &c
		}
	}
	if got == nil {
		t.Fatalf("control %s not in GET /v1/controls", d.ControlID)
	}
	if got.BundleID != "org-ac-01" || got.SCFID != cat.anchors["mfa"] || got.SCFAnchorID == nil || *got.SCFAnchorID != d.SCFAnchorID {
		t.Errorf("listed control = %+v, want org-ac-01 anchored at %s (%s)", got, cat.anchors["mfa"], d.SCFAnchorID)
	}
	if rel := anchorRelease(t, admin, d.SCFAnchorID); rel != cat.scfRelease {
		t.Errorf("anchor is in SCF release %s, want the batch's %s", rel, cat.scfRelease)
	}

	// The control carries the organisation ID and both tested criteria.
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/controls/"+d.ControlID+"/coverage", nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	status, raw = do(t, req)
	if status != http.StatusOK {
		t.Fatalf("GET coverage = %d; body=%s", status, raw)
	}
	var cov struct {
		Control struct {
			Title              string                `json:"title"`
			ImplementationType string                `json:"implementation_type"`
			ExternalRefs       []control.ExternalRef `json:"external_refs"`
		} `json:"control"`
	}
	if err := json.Unmarshal(raw, &cov); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if cov.Control.Title != "MFA for workforce" || cov.Control.ImplementationType != "automated" {
		t.Errorf("control = %q %q, want the row's title and implementation type", cov.Control.Title, cov.Control.ImplementationType)
	}
	wantRefs := []control.ExternalRef{
		{System: controlimport.OrgControlRefSystem, Kind: "reference", ID: "ORG-AC-01"},
		{System: cat.framework, Kind: "tested_under", ID: "CC6.1"},
		{System: cat.framework, Kind: "tested_under", ID: "CC6.2"},
	}
	if len(cov.Control.ExternalRefs) != len(wantRefs) {
		t.Fatalf("external_refs = %+v, want %+v", cov.Control.ExternalRefs, wantRefs)
	}
	for i, w := range wantRefs {
		if cov.Control.ExternalRefs[i] != w {
			t.Errorf("external_refs[%d] = %+v, want %+v", i, cov.Control.ExternalRefs[i], w)
		}
	}

	// The row records the decision, and a second approve is refused.
	status, raw = doGet(t, env.testEnv, "/v1/controls/imports/"+b.ID+"/rows?status=approved")
	if status != http.StatusOK || !strings.Contains(string(raw), d.ControlID) {
		t.Errorf("approved rows = %d %s, want the row with its control", status, raw)
	}
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "approve"), map[string]any{"candidate_rank": 1})
	if status != http.StatusConflict || !strings.Contains(string(raw), "already decided") {
		t.Errorf("second approve = %d %s, want 409", status, raw)
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1 AND bundle_id = 'org-ac-01'`, tenant).Scan(&n); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if n != 1 {
		t.Errorf("%d org-ac-01 controls, want 1", n)
	}

	// Another tenant sees neither the control nor the row.
	for _, c := range listControls(t, otherEnv.testEnv, otherEnv.bearer) {
		if c.ID == d.ControlID {
			t.Errorf("tenant B lists tenant A's control %s", c.ID)
		}
	}
	status, _ = doPost(t, otherEnv.testEnv, otherEnv.bearer, decisionPath(b.ID, rowByOrgID(t, b, "ORG-AC-02").ID, "approve"),
		map[string]any{"candidate_rank": 1})
	if status != http.StatusNotFound {
		t.Errorf("tenant B approving tenant A's row = %d, want 404", status)
	}
}

// ----- AC: approving with a searched anchor (not a candidate) -----

func TestApproveWithSearchedAnchor(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	b := stageFixture(t, env.testEnv, cat.framework)
	row := rowByOrgID(t, b, "ORG-HR-01")
	searched := cat.anchors["monitoring"]
	for _, c := range row.Candidates {
		if c.SCFID == searched {
			t.Fatalf("fixture: %s is already a candidate of ORG-HR-01", searched)
		}
	}

	// Refusals first: an unknown code, a rank the row lacks, both choices.
	for _, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"scf_id": "NOPE-99"}, "not in SCF release"},
		{map[string]any{"candidate_rank": 99}, "no candidate with that rank"},
		{map[string]any{"candidate_rank": 1, "scf_id": searched}, "exactly one of"},
		{map[string]any{"scf_id": searched, "implementation_type": "manual"}, "implementation_type"},
	} {
		status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "approve"), tc.body)
		if status != http.StatusBadRequest || !strings.Contains(string(raw), tc.want) {
			t.Errorf("approve %v = %d %s, want 400 naming %q", tc.body, status, raw, tc.want)
		}
	}

	status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "approve"), map[string]any{
		"scf_id":              searched,
		"implementation_type": "manual_periodic",
		"applicability_expr":  map[string]any{"op": "eq", "dim": "env", "value": "prod"},
	})
	if status != http.StatusOK {
		t.Fatalf("approve with searched anchor = %d; body=%s", status, raw)
	}
	d := decodeDecision(t, raw)
	if d.SCFID != searched || d.SCFRelease != cat.scfRelease {
		t.Errorf("decision = %+v, want %s in %s", d, searched, cat.scfRelease)
	}
	// No tested-under data: the organisation ID is the only reference.
	if len(d.ExternalRefs) != 1 || d.ExternalRefs[0].ID != "ORG-HR-01" {
		t.Errorf("external_refs = %+v, want only ORG-HR-01", d.ExternalRefs)
	}
	var implType, appl string
	if err := admin.QueryRow(context.Background(),
		`SELECT implementation_type::text, applicability_expr FROM controls WHERE id = $1::uuid`, d.ControlID).Scan(&implType, &appl); err != nil {
		t.Fatalf("read control: %v", err)
	}
	if implType != "manual_periodic" || !strings.Contains(appl, `"env"`) {
		t.Errorf("control implementation_type=%q applicability=%s, want the approver's choices", implType, appl)
	}
}

// ----- AC: approval without an approver is refused; reject records the reason -----

func TestApprovalNeedsAPersonAndRejectKeepsReason(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	b := stageFixture(t, env.testEnv, cat.framework)
	row := rowByOrgID(t, b, "ORG-AC-02")

	for name, bearer := range map[string]string{"machine token": env.machine, "credential naming no user": env.nouser} {
		for _, verb := range []string{"approve", "reject"} {
			status, raw := doPost(t, env.testEnv, bearer, decisionPath(b.ID, row.ID, verb),
				map[string]any{"candidate_rank": 1, "reason": "x"})
			if status != http.StatusForbidden || !strings.Contains(string(raw), "a person must approve") {
				t.Errorf("%s %s = %d %s, want 403", name, verb, status, raw)
			}
		}
	}
	status, raw := doGet(t, env.testEnv, "/v1/controls/imports/"+b.ID+"/rows?status=pending")
	if status != http.StatusOK || !strings.Contains(string(raw), row.ID) {
		t.Fatalf("row left pending? %d %s", status, raw)
	}

	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "reject"), map[string]any{"reason": "  "})
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "reason") {
		t.Errorf("blank reason = %d %s, want 400", status, raw)
	}
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "reject"),
		map[string]any{"reason": "covered by ORG-AC-01"})
	if status != http.StatusOK {
		t.Fatalf("reject = %d; body=%s", status, raw)
	}
	d := decodeDecision(t, raw)
	if d.Status != controlimport.RowStatusRejected || d.RejectionReason != "covered by ORG-AC-01" || d.DecidedBy != env.user.String() {
		t.Errorf("decision = %+v", d)
	}

	status, raw = doGet(t, env.testEnv, "/v1/controls/imports/"+b.ID+"/rows?status=rejected")
	if status != http.StatusOK {
		t.Fatalf("rejected rows = %d %s", status, raw)
	}
	var list controlimport.RowList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(list.Rows) != 1 || list.Rows[0].RejectionReason != "covered by ORG-AC-01" || list.Rows[0].DecidedBy != env.user.String() {
		t.Errorf("rejected rows = %+v", list.Rows)
	}
	status, _ = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, row.ID, "approve"), map[string]any{"candidate_rank": 1})
	if status != http.StatusConflict {
		t.Errorf("approve after reject = %d, want 409", status)
	}
}

// ----- AC: a row duplicating an existing control's organisation ID -----

func TestApproveDuplicateOrgIDMarksDuplicate(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	first := stageFixture(t, env.testEnv, cat.framework)
	status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(first.ID, rowByOrgID(t, first, "ORG-AC-01").ID, "approve"),
		map[string]any{"candidate_rank": 1})
	if status != http.StatusOK {
		t.Fatalf("first approve = %d; body=%s", status, raw)
	}
	existing := decodeDecision(t, raw).ControlID

	// The same sheet again: ORG-AC-01 is now an existing control's reference.
	second := stageFixture(t, env.testEnv, cat.framework)
	dup := rowByOrgID(t, second, "ORG-AC-01")
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(second.ID, dup.ID, "approve"), map[string]any{"candidate_rank": 1})
	if status != http.StatusConflict {
		t.Fatalf("duplicate approve = %d, want 409; body=%s", status, raw)
	}
	d := decodeDecision(t, raw)
	if d.Status != controlimport.RowStatusDuplicate || d.DuplicateOf != existing || d.ControlID != "" {
		t.Errorf("decision = %+v, want duplicate of %s and no new control", d, existing)
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1 AND external_refs @> '[{"id":"ORG-AC-01"}]'`, tenant).Scan(&n); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if n != 1 {
		t.Errorf("%d controls carry ORG-AC-01, want 1", n)
	}
	status, raw = doGet(t, env.testEnv, "/v1/controls/imports/"+second.ID+"/rows?status=duplicate")
	if status != http.StatusOK || !strings.Contains(string(raw), dup.ID) || !strings.Contains(string(raw), existing) {
		t.Errorf("duplicate rows = %d %s, want the row naming %s", status, raw, existing)
	}
}

// ----- AC: approvals and rejections appear in the audit log -----

func TestDecisionsAppearInAuditLog(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	b := stageFixture(t, env.testEnv, cat.framework)
	approved := rowByOrgID(t, b, "ORG-AC-01")
	rejected := rowByOrgID(t, b, "ORG-OP-01")
	status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, approved.ID, "approve"), map[string]any{"candidate_rank": 1})
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	d := decodeDecision(t, raw)
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, rejected.ID, "reject"), map[string]any{"reason": "retired"})
	if status != http.StatusOK {
		t.Fatalf("reject = %d %s", status, raw)
	}

	q := url.Values{}
	q.Set("from", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))
	q.Set("to", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	q.Set("kind", "control_import")
	status, raw = doGet(t, env.testEnv, "/v1/admin/audit-log/unified?"+q.Encode())
	if status != http.StatusOK {
		t.Fatalf("unified audit log = %d %s", status, raw)
	}
	var list struct {
		Entries []struct {
			Kind        string         `json:"kind"`
			TargetType  string         `json:"target_type"`
			TargetID    string         `json:"target_id"`
			Action      string         `json:"action"`
			ActorID     string         `json:"actor_id"`
			PayloadJSON map[string]any `json:"payload_json"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode audit log: %v", err)
	}
	byRow := map[string]int{}
	for i, e := range list.Entries {
		byRow[e.TargetID] = i
		if e.Kind != "control_import" || e.TargetType != "control_import_row" || e.ActorID != env.user.String() {
			t.Errorf("entry = %+v, want a control_import row by %s", e, env.user)
		}
		if e.PayloadJSON["batch_id"] != b.ID {
			t.Errorf("entry %s payload batch_id = %v, want %s", e.Action, e.PayloadJSON["batch_id"], b.ID)
		}
	}
	if len(list.Entries) != 2 {
		t.Fatalf("audit entries = %+v, want approve and reject", list.Entries)
	}
	a := list.Entries[byRow[approved.ID]]
	if a.Action != "approve" || a.PayloadJSON["scf_anchor_id"] != d.SCFAnchorID || a.PayloadJSON["scf_id"] != cat.anchors["mfa"] ||
		a.PayloadJSON["scf_release"] != cat.scfRelease || a.PayloadJSON["control_id"] != d.ControlID || a.PayloadJSON["org_control_id"] != "ORG-AC-01" {
		t.Errorf("approve entry = %+v", a)
	}
	r := list.Entries[byRow[rejected.ID]]
	if r.Action != "reject" || r.PayloadJSON["reason"] != "retired" || r.PayloadJSON["org_control_id"] != "ORG-OP-01" {
		t.Errorf("reject entry = %+v", r)
	}

	// The audit table is append-only, even for the migrate role.
	if _, err := admin.Exec(context.Background(), `DELETE FROM control_import_audit_log WHERE tenant_id = $1`, tenant); err == nil {
		t.Error("DELETE on control_import_audit_log succeeded, want the append-only trigger to refuse")
	}
}

// ----- bundle-ID collision, anchors outside the batch's release, anchor ids -----

// legacyRelease adds a second, non-current SCF release holding one anchor and
// returns that anchor's code and id. Cleanup removes both.
func legacyRelease(t *testing.T, admin *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	verID, anchorID := uuid.New(), uuid.New()
	code := "LEG" + strings.ToUpper(uuid.NewString()[:8]) + "-01"
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
		_, _ = admin.Exec(context.Background(), `DELETE FROM framework_versions WHERE id = $1`, verID)
	})
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		SELECT $1, NULL, fv.framework_id, $2, 'legacy'
		FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'
		ORDER BY fv.created_at DESC LIMIT 1`,
		verID, "legacy-"+code); err != nil {
		t.Fatalf("insert legacy release: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, $3, 'IAC', 'Legacy anchor', 'Only in the legacy release.')`, anchorID, verID, code); err != nil {
		t.Fatalf("insert legacy anchor: %v", err)
	}
	return code, anchorID.String()
}

func anchorIDOf(t *testing.T, admin *pgxpool.Pool, scfID string) string {
	t.Helper()
	var id string
	if err := admin.QueryRow(context.Background(), `SELECT id::text FROM scf_anchors WHERE scf_id = $1`, scfID).Scan(&id); err != nil {
		t.Fatalf("read anchor %s: %v", scfID, err)
	}
	return id
}

func TestApproveBundleIDCollisionAndReleaseBinding(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	legacyCode, legacyID := legacyRelease(t, admin)
	tenant := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)

	// "AC 01" and "AC-01" are distinct organisation IDs with one slug.
	status, raw := doUpload(t, env.testEnv, upload{filename: "slugs.csv", framework: cat.framework,
		payload: []byte("Control ID,Title,Description\nAC 01,First,First control.\nAC-01,Second,Second control.\n")})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decodeBatch(t, raw)
	first, second := rowByOrgID(t, b, "AC 01"), rowByOrgID(t, b, "AC-01")

	// An anchor of another SCF release is refused by code and by id: never
	// re-bound into the batch's release.
	for _, ref := range []string{legacyCode, legacyID} {
		status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, first.ID, "approve"), map[string]any{"scf_id": ref})
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "not in") {
			t.Errorf("approve with legacy anchor %s = %d %s, want 400", ref, status, raw)
		}
	}

	// An anchor id of the batch's release binds, and is reported by code.
	mfaID := anchorIDOf(t, admin, cat.anchors["mfa"])
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, first.ID, "approve"), map[string]any{"scf_id": mfaID})
	if status != http.StatusOK {
		t.Fatalf("approve by anchor id = %d %s", status, raw)
	}
	d := decodeDecision(t, raw)
	if d.SCFID != cat.anchors["mfa"] || d.SCFAnchorID != mfaID || d.BundleID != "ac-01" {
		t.Errorf("decision = %+v, want code %s, anchor %s, bundle ac-01", d, cat.anchors["mfa"], mfaID)
	}
	var stored string
	if err := admin.QueryRow(context.Background(), `SELECT scf_id FROM control_import_rows WHERE id = $1`, first.ID).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != cat.anchors["mfa"] {
		t.Errorf("row scf_id = %q, want the code %s", stored, cat.anchors["mfa"])
	}

	// The second slug collides: 409, no new version of the first's control,
	// and the row stays pending.
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, second.ID, "approve"), map[string]any{"scf_id": cat.anchors["review"]})
	if status != http.StatusConflict || !strings.Contains(string(raw), "bundle ID") {
		t.Errorf("colliding approve = %d %s, want 409 naming the bundle ID", status, raw)
	}
	var n, version int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*), max(version) FROM controls WHERE tenant_id = $1 AND bundle_id = 'ac-01'`, tenant).Scan(&n, &version); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if n != 1 || version != 1 {
		t.Errorf("ac-01 controls = %d (max version %d), want one at v1", n, version)
	}
	status, raw = doGet(t, env.testEnv, "/v1/controls/imports/"+b.ID+"/rows?status=pending")
	if status != http.StatusOK || !strings.Contains(string(raw), second.ID) {
		t.Errorf("colliding row not pending: %d %s", status, raw)
	}
}
