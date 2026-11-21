//go:build integration

// #2839 — integration tests for the risk register import API. Real Postgres
// with RLS, the assembled platform router and the OPA engine as production
// wires it, so each request runs the full path: JWT, tenancy middleware,
// authz, the handler, riskimport.Store and risk.Store. The DB is never
// mocked.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/riskimports/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (migrate role).
//
// testdata/register.csv has a title row above the header, alias headers,
// two unmapped columns, and rows that stage (a category by name, one by
// slug, one without an ID, one whose ID the tenant already uses, one whose
// ID only another tenant uses) and rows that are rejected (no title, an ID
// repeated in another case, an unknown category, a bad review date).
package riskimports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// ----- harness -----

// env serves one tenant with bearers for a person who is a tenant admin
// (the approver), a person with a live grc_engineer grant, a person with a
// live viewer grant, and a machine credential with the admin role.
type env struct {
	admin    *pgxpool.Pool
	app      *pgxpool.Pool
	tenant   string
	server   *httptest.Server
	approver uuid.UUID
	apprJWT  string
	grcJWT   string
	viewJWT  string
	machine  string
	srv      *api.Server
}

// issue mints a further JWT for the env's server.
func (e env) issue(t *testing.T, claims jwt.AtlasClaims) string {
	t.Helper()
	return e.srv.IssueTestJWT(t, claims)
}

func cleanupTables() []string {
	return []string{
		"risk_import_rejected_rows",
		"risk_import_rows",
		"risk_import_batches",
		"risk_control_links",
		"risks",
		"user_roles",
		"users",
	}
}

func newEnv(t *testing.T) env {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, cleanupTables()...)
	return newEnvFor(t, admin, app, tenant)
}

func newEnvFor(t *testing.T, admin, app *pgxpool.Pool, tenant string) env {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))

	tenantID := uuid.MustParse(tenant)
	person := func(name, role string) (uuid.UUID, string) {
		id := seedUser(t, admin, tenant, name)
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2839')`,
			tenant, id.String(), role); err != nil {
			t.Fatalf("grant %s: %v", role, err)
		}
		claims := testjwt.OwnerFor(tenantID, []string{role})
		claims.Subject = "user:" + id.String()
		return id, srv.IssueTestJWT(t, claims)
	}
	e := env{admin: admin, app: app, tenant: tenant, srv: srv}
	e.approver, e.apprJWT = person("approver", "admin")
	_, e.grcJWT = person("grc", "grc_engineer")
	_, e.viewJWT = person("viewer", "viewer")
	machine := testjwt.AdminFor(tenantID)
	machine.Subject = "oauth_client:ci-runner"
	e.machine = srv.IssueTestJWT(t, machine)

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	e.server = httptest.NewServer(h)
	t.Cleanup(e.server.Close)
	return e
}

func seedUser(t *testing.T, admin *pgxpool.Pool, tenant, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := name + "-" + id.String()[:8] + "@2839.test"
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')`, id, tenant, email, name); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedRisk creates a risk in tenant through the risk store, carrying ref as
// its organisation risk ID.
func seedRisk(t *testing.T, app *pgxpool.Pool, tenant, ref, title string) string {
	t.Helper()
	r, err := risk.NewStore(app).Create(dbtest.WithTenantCtx(t, tenant), risk.CreateInput{
		Title:       title,
		CategoryRef: "operational",
		ExternalRef: ref,
		Actor:       "test-2839",
	})
	if err != nil {
		t.Fatalf("seed risk %s: %v", ref, err)
	}
	return r.ID.String()
}

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/register.csv")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func upload(t *testing.T, e env, bearer, filename string, payload []byte, columnMap ...string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write part: %v", err)
	}
	for _, m := range columnMap {
		_ = mw.WriteField("column_map", m)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/v1/risks/imports", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bearer)
	return do(t, req)
}

func call(t *testing.T, e env, method, bearer, path string, body any) (int, []byte) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.server.URL+path, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	return do(t, req)
}

func do(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %T: %v; body=%s", v, err, raw)
	}
	return v
}

func stage(t *testing.T, e env, bearer string) riskimport.Batch {
	t.Helper()
	status, raw := upload(t, e, bearer, "register.csv", fixture(t))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	return decode[riskimport.Batch](t, raw)
}

func approve(t *testing.T, e env, bearer, batchID string) (int, []byte) {
	t.Helper()
	return call(t, e, http.MethodPost, bearer, "/v1/risks/imports/"+batchID+"/approve", nil)
}

func stagedByRef(t *testing.T, b riskimport.Batch, ref string) riskimport.BatchRow {
	t.Helper()
	for _, r := range b.Staged {
		if r.ExternalRef == ref {
			return r
		}
	}
	t.Fatalf("batch has no staged row %q", ref)
	return riskimport.BatchRow{}
}

func countRisks(t *testing.T, e env) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil {
		t.Fatalf("count risks: %v", err)
	}
	return n
}

// ----- upload -> stage -> approve -> risks -----

func TestRiskImport_UploadStageApproveCreatesRisks(t *testing.T) {
	e := newEnv(t)
	existing := seedRisk(t, e.app, e.tenant, "r-exist", "Original title")

	// Another tenant holds R-OTHER: it must not make tenant A's row a
	// duplicate.
	other := dbtest.SeedTenant(t, e.admin, cleanupTables()...)
	seedRisk(t, e.app, other, "R-OTHER", "Other tenant's risk")

	// A GRC engineer stages the register.
	b := stage(t, e, e.grcJWT)
	if b.TotalRows != 9 || b.StagedCount != 5 || b.RejectedCount != 4 {
		t.Fatalf("counts total=%d staged=%d rejected=%d, want 9/5/4", b.TotalRows, b.StagedCount, b.RejectedCount)
	}
	if b.Status != riskimport.BatchStatusStaged || b.SourceFormat != "csv" || b.SourceFilename != "register.csv" {
		t.Errorf("batch status=%s format=%s filename=%s", b.Status, b.SourceFormat, b.SourceFilename)
	}
	if got := strings.Join(b.UnmappedColumns, "|"); got != "Risk Owner|Target Date" {
		t.Errorf("unmapped columns = %q", got)
	}
	gotRejected := map[int]string{}
	for _, r := range b.Rejected {
		gotRejected[r.RowNumber] = r.Reason
	}
	wantRejected := map[int]string{
		5: riskimport.RejectMissingField,
		6: riskimport.RejectDuplicateExternal,
		7: riskimport.RejectUnknownCategory,
		8: riskimport.RejectInvalidReviewDue,
	}
	for row, reason := range wantRejected {
		if gotRejected[row] != reason {
			t.Errorf("row %d rejected as %q, want %q (all: %v)", row, gotRejected[row], reason, gotRejected)
		}
	}
	r1 := stagedByRef(t, b, "R-001")
	if r1.Title != "Vendor outage" || r1.Category != "operational" || r1.ReviewDue != "2027-01-15" ||
		r1.Description != "The primary payments vendor is unavailable for a business day." {
		t.Errorf("R-001 staged as %+v", r1)
	}
	if r1.Unmapped["Risk Owner"] != "alice@corp.test" || r1.Unmapped["Target Date"] != "2027-03-01" || len(r1.Unmapped) != 2 {
		t.Errorf("R-001 unmapped = %v", r1.Unmapped)
	}
	if stagedByRef(t, b, "R-002").Category != "privacy" {
		t.Errorf("R-002 category = %q, want privacy", stagedByRef(t, b, "R-002").Category)
	}
	if countRisks(t, e) != 1 {
		t.Fatalf("staging created risks: %d in tenant, want the 1 seeded", countRisks(t, e))
	}

	// Reading the batch returns the same rows.
	status, raw := call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET batch = %d; body=%s", status, raw)
	}
	read := decode[riskimport.Batch](t, raw)
	if len(read.Staged) != 5 || len(read.Rejected) != 4 || stagedByRef(t, read, "R-001").Unmapped["Risk Owner"] != "alice@corp.test" {
		t.Fatalf("GET batch = %+v", read)
	}

	// The approver approves every pending row.
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	if res.ApprovedCount != 4 || len(res.Duplicates) != 1 || len(res.Skipped) != 0 {
		t.Fatalf("approve result = %+v", res)
	}
	if d := res.Duplicates[0]; d.ExternalRef != "R-EXIST" || d.RiskID != existing || d.Status != riskimport.RowStatusDuplicate {
		t.Errorf("duplicate = %+v, want R-EXIST on %s", d, existing)
	}
	// Re-importing never updates the existing risk.
	var title string
	if err := e.admin.QueryRow(context.Background(), `SELECT title FROM risks WHERE id = $1`, existing).Scan(&title); err != nil {
		t.Fatalf("read existing risk: %v", err)
	}
	if title != "Original title" {
		t.Errorf("existing risk title = %q, want it unchanged", title)
	}

	// Each approved row is a real risk with the row's fields, and a
	// `created` risk audit row naming the approver.
	byRef := map[string]riskimport.RowDecision{}
	for _, d := range res.Approved {
		byRef[d.ExternalRef] = d
		if d.DecidedBy != e.approver.String() || d.RiskID == "" {
			t.Errorf("decision %+v", d)
		}
	}
	type stored struct {
		title, desc, category, ref, review, treatment, createdBy string
	}
	readRisk := func(id string) stored {
		t.Helper()
		var s stored
		if err := e.admin.QueryRow(context.Background(), `
			SELECT r.title, r.description, c.slug, COALESCE(r.external_ref, ''),
			       COALESCE(to_char(r.review_due_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''), r.treatment::text,
			       COALESCE((SELECT actor FROM risk_audit_log a WHERE a.risk_id = r.id AND a.action_type = 'created'), '')
			FROM risks r JOIN org_risk_categories c ON c.id = r.category_id
			WHERE r.id = $1 AND r.tenant_id = $2`, id, e.tenant).
			Scan(&s.title, &s.desc, &s.category, &s.ref, &s.review, &s.treatment, &s.createdBy); err != nil {
			t.Fatalf("read risk %s: %v", id, err)
		}
		return s
	}
	got := readRisk(byRef["R-001"].RiskID)
	want := stored{
		title: "Vendor outage", desc: "The primary payments vendor is unavailable for a business day.",
		category: "operational", ref: "R-001", review: "2027-01-15", treatment: "untreated", createdBy: e.approver.String(),
	}
	if got != want {
		t.Errorf("R-001 risk = %+v, want %+v", got, want)
	}
	if strings.Contains(got.desc, "alice@corp.test") {
		t.Errorf("unmapped value written into the description: %q", got.desc)
	}
	if got := readRisk(byRef["R-OTHER"].RiskID); got.category != "regulatory" || got.ref != "R-OTHER" {
		t.Errorf("R-OTHER risk = %+v", got)
	}
	if got := readRisk(byRef[""].RiskID); got.title != "Risk without an ID" || got.ref != "" || got.category != "financial" {
		t.Errorf("row without an ID = %+v", got)
	}
	if n := countRisks(t, e); n != 5 {
		t.Errorf("risks in tenant = %d, want the seeded one plus 4", n)
	}

	// One import audit row per decision, naming the approver.
	rows, err := e.admin.Query(context.Background(), `
		SELECT action, actor, COALESCE(external_ref, ''), COALESCE(risk_id::text, '')
		FROM risk_import_audit_log WHERE tenant_id = $1 AND batch_id = $2`, e.tenant, b.ID)
	if err != nil {
		t.Fatalf("read import audit: %v", err)
	}
	var audit []string
	for rows.Next() {
		var action, actor, ref, riskID string
		if err := rows.Scan(&action, &actor, &ref, &riskID); err != nil {
			t.Fatalf("scan import audit: %v", err)
		}
		if actor != e.approver.String() || riskID == "" {
			t.Errorf("audit row %s %s: actor=%q risk=%q", action, ref, actor, riskID)
		}
		audit = append(audit, action+":"+ref)
	}
	rows.Close()
	sort.Strings(audit)
	if got := strings.Join(audit, ","); got != "approve:,approve:R-001,approve:R-002,approve:R-OTHER,duplicate:R-EXIST" {
		t.Errorf("import audit = %s", got)
	}

	// Every row is decided: the batch is completed and a second approval
	// approves nothing.
	status, raw = call(t, e, http.MethodGet, e.apprJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK || decode[riskimport.Batch](t, raw).Status != riskimport.BatchStatusCompleted {
		t.Errorf("batch after approval = %d %s", status, raw)
	}
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK || decode[riskimport.ApproveResult](t, raw).ApprovedCount != 0 {
		t.Errorf("second approval = %d %s", status, raw)
	}
}

// TestRiskImport_ReimportMarksDuplicates: uploading the same register again
// creates nothing for an ID already imported; each such row is a duplicate.
func TestRiskImport_ReimportMarksDuplicates(t *testing.T) {
	e := newEnv(t)
	csv := []byte("Risk ID,Title,Category\nR-100,First,operational\nR-101,Second,operational\n")
	status, raw := upload(t, e, e.grcJWT, "r.csv", csv)
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	first := decode[riskimport.Batch](t, raw)
	if status, raw := approve(t, e, e.apprJWT, first.ID); status != http.StatusOK || decode[riskimport.ApproveResult](t, raw).ApprovedCount != 2 {
		t.Fatalf("first approval = %d %s", status, raw)
	}
	reworded := []byte("Risk ID,Title,Category\nr-100,First renamed,operational\nR-101,Second renamed,privacy\n")
	_, raw = upload(t, e, e.grcJWT, "r.csv", reworded)
	second := decode[riskimport.Batch](t, raw)
	status, raw = approve(t, e, e.apprJWT, second.ID)
	res := decode[riskimport.ApproveResult](t, raw)
	if status != http.StatusOK || res.ApprovedCount != 0 || len(res.Duplicates) != 2 {
		t.Fatalf("re-import approval = %d %+v", status, res)
	}
	if n := countRisks(t, e); n != 2 {
		t.Errorf("risks = %d, want 2", n)
	}
	var titles []string
	rows, _ := e.admin.Query(context.Background(), `SELECT title FROM risks WHERE tenant_id = $1 ORDER BY title`, e.tenant)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		titles = append(titles, s)
	}
	rows.Close()
	if strings.Join(titles, ",") != "First,Second" {
		t.Errorf("titles = %v, want the first import's", titles)
	}
}

func TestRiskImport_ColumnMapOverridesDetection(t *testing.T) {
	e := newEnv(t)
	csv := []byte("Ref No.,Headline,Bucket,Notes\nX-1,Mapped title,operational,kept aside\n")
	status, raw := upload(t, e, e.grcJWT, "r.csv", csv,
		"external_ref=Ref No.", "title=Headline", "category=Bucket")
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	r := stagedByRef(t, b, "X-1")
	if r.Title != "Mapped title" || r.Category != "operational" || r.Unmapped["Notes"] != "kept aside" {
		t.Errorf("row = %+v", r)
	}
	detectable := []byte("Risk ID,Title,Category\nX-2,A title,operational\n")
	if status, raw := upload(t, e, e.grcJWT, "r.csv", detectable, "title=Missing Header"); status != http.StatusBadRequest ||
		!strings.Contains(string(raw), "not in the file") {
		t.Errorf("absent mapped header = %d %s", status, raw)
	}
}

// ----- reject -----

func TestRiskImport_RejectRow(t *testing.T) {
	e := newEnv(t)
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-002")
	path := "/v1/risks/imports/" + b.ID + "/rows/" + row.ID + "/reject"

	if status, raw := call(t, e, http.MethodPost, e.apprJWT, path, map[string]string{"reason": " "}); status != http.StatusBadRequest {
		t.Errorf("reject without reason = %d %s", status, raw)
	}
	status, raw := call(t, e, http.MethodPost, e.apprJWT, path, map[string]string{"reason": "not our risk"})
	if status != http.StatusOK {
		t.Fatalf("reject = %d %s", status, raw)
	}
	d := decode[riskimport.RowDecision](t, raw)
	if d.Status != riskimport.RowStatusRejected || d.RejectionReason != "not our risk" || d.DecidedBy != e.approver.String() {
		t.Errorf("decision = %+v", d)
	}
	if status, _ := call(t, e, http.MethodPost, e.apprJWT, path, map[string]string{"reason": "again"}); status != http.StatusConflict {
		t.Errorf("second reject = %d, want 409", status)
	}
	var action, actor, reason string
	if err := e.admin.QueryRow(context.Background(), `
		SELECT action, actor, reason FROM risk_import_audit_log WHERE tenant_id = $1 AND row_id = $2`,
		e.tenant, row.ID).Scan(&action, &actor, &reason); err != nil {
		t.Fatalf("read reject audit: %v", err)
	}
	if action != "reject" || actor != e.approver.String() || reason != "not our risk" {
		t.Errorf("reject audit = %s %s %s", action, actor, reason)
	}

	// Approving the batch leaves the rejected row alone.
	_, raw = approve(t, e, e.apprJWT, b.ID)
	for _, a := range decode[riskimport.ApproveResult](t, raw).Approved {
		if a.RowID == row.ID {
			t.Errorf("rejected row was approved: %+v", a)
		}
	}
}

// ----- authz -----

func TestRiskImport_Access(t *testing.T) {
	e := newEnv(t)

	if status, raw := upload(t, e, e.viewJWT, "register.csv", fixture(t)); status != http.StatusForbidden {
		t.Errorf("viewer upload = %d, want 403; body=%s", status, raw)
	}
	b := stage(t, e, e.grcJWT)
	row := stagedByRef(t, b, "R-001")
	reject := "/v1/risks/imports/" + b.ID + "/rows/" + row.ID + "/reject"

	// grc_engineer carried only as a JWT role claim, with no live grant, is
	// not a tenant approver (#2381).
	claimOnly := e.issue(t, testjwt.OwnerFor(uuid.MustParse(e.tenant), []string{"grc_engineer"}))
	for name, bearer := range map[string]string{"viewer": e.viewJWT, "machine": e.machine, "grc_engineer claim only": claimOnly} {
		if status, raw := approve(t, e, bearer, b.ID); status != http.StatusForbidden {
			t.Errorf("%s approve = %d, want 403; body=%s", name, status, raw)
		}
		if status, raw := call(t, e, http.MethodPost, bearer, reject, map[string]string{"reason": "no"}); status != http.StatusForbidden {
			t.Errorf("%s reject = %d, want 403; body=%s", name, status, raw)
		}
	}
	if n := countRisks(t, e); n != 0 {
		t.Fatalf("refused approvals created %d risks", n)
	}
	var decided int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_rows WHERE batch_id = $1 AND status <> 'pending'`, b.ID).Scan(&decided); err != nil {
		t.Fatalf("count decided: %v", err)
	}
	if decided != 0 {
		t.Errorf("refused decisions decided %d rows", decided)
	}

	// Another tenant's approver cannot see or decide the batch.
	other := newEnvFor(t, e.admin, e.app, dbtest.SeedTenant(t, e.admin, cleanupTables()...))
	if status, _ := call(t, other, http.MethodGet, other.apprJWT, "/v1/risks/imports/"+b.ID, nil); status != http.StatusNotFound {
		t.Errorf("other tenant GET = %d, want 404", status)
	}
	if status, _ := approve(t, other, other.apprJWT, b.ID); status != http.StatusNotFound {
		t.Errorf("other tenant approve = %d, want 404", status)
	}
}

// ----- caps -----

func TestRiskImport_Caps(t *testing.T) {
	e := newEnv(t)

	big := bytes.Repeat([]byte("a"), riskimport.MaxUploadBytes+1)
	if status, raw := upload(t, e, e.grcJWT, "big.csv", big); status != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize upload = %d %s", status, raw)
	}

	var rows strings.Builder
	rows.WriteString("Risk ID,Title,Category\n")
	for i := 0; i < riskimport.MaxRows; i++ {
		fmt.Fprintf(&rows, "R-%d,Risk %d,operational\n", i, i)
	}
	if status, raw := upload(t, e, e.grcJWT, "rows.csv", []byte(rows.String())); status != http.StatusBadRequest ||
		!strings.Contains(string(raw), "5000-row cap") {
		t.Errorf("row cap = %d %s", status, raw)
	}

	header := []string{"Risk ID", "Title", "Category"}
	for len(header) <= riskimport.MaxColumns {
		header = append(header, fmt.Sprintf("Extra %d", len(header)))
	}
	wide := strings.Join(header, ",") + "\n"
	if status, raw := upload(t, e, e.grcJWT, "wide.csv", []byte(wide)); status != http.StatusBadRequest ||
		!strings.Contains(string(raw), "column 200") {
		t.Errorf("column cap = %d %s", status, raw)
	}

	var n int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_batches WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if n != 0 {
		t.Errorf("refused uploads staged %d batches", n)
	}
}
