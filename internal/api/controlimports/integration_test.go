//go:build integration

// #1779 — integration tests for the control import upload API. Real Postgres
// with RLS plus the assembled platform router, so each request runs the full
// path: JWT, tenancy middleware, authz, the handler, and controlimport.Store.
// The DB is never mocked.
//
// Run with:
//
//	go test -tags=integration ./internal/api/controlimports/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (migrate role).
//
// Fixtures: a small SCF catalog and a small framework whose requirements have
// crosswalk edges into it (seeded under per-test slugs, removed on cleanup),
// and testdata/controls.csv, which the XLSX case converts cell-for-cell. The
// sheet holds clean rows, a row missing its title, a row with an unknown
// tested-under code, a duplicate control ID, and a row with no tested-under
// data.
package controlimports_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"control_import_rejected_rows",
		"control_import_rows",
		"control_import_batches",
	)
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// seedCatalog writes a three-anchor SCF-shaped catalog and a framework with
// three requirements, two of which carry crosswalk edges into the catalog.
// Both are global catalog rows under per-test slugs so they never collide
// with the bundled catalogs; cleanup deletes them (cascading to versions,
// anchors, requirements and edges). Returns the framework slug.
func seedCatalog(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	scfSlug, fwSlug := "byoc-scf-"+suffix, "byoc-fw-"+suffix
	scfID, scfVer := uuid.New(), uuid.New()
	fwID, fwVer := uuid.New(), uuid.New()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed catalog: %v\nsql: %s", err, sql)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = ANY($1)`, []uuid.UUID{scfID, fwID})
	})
	for _, f := range []struct {
		id, ver    uuid.UUID
		slug, name string
	}{{scfID, scfVer, scfSlug, "Test SCF"}, {fwID, fwVer, fwSlug, "Test Framework"}} {
		exec(`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, $2, $3, 'test')`, f.id, f.name, f.slug)
		exec(`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status) VALUES ($1, NULL, $2, '2017', 'current')`, f.ver, f.id)
	}
	anchors := map[string]uuid.UUID{"IAC-06": uuid.New(), "IAC-15": uuid.New(), "CRY-05": uuid.New()}
	for scf, id := range anchors {
		exec(`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title) VALUES ($1, $2, $3, $4, $5)`,
			id, scfVer, scf, scf[:3], "Anchor "+scf)
	}
	reqs := map[string]uuid.UUID{"CC6.1": uuid.New(), "CC6.2": uuid.New(), "CC7.1": uuid.New()}
	for code, id := range reqs {
		exec(`INSERT INTO framework_requirements (id, framework_version_id, code, title) VALUES ($1, $2, $3, $4)`,
			id, fwVer, code, "Requirement "+code)
	}
	for code, scf := range map[string]string{"CC6.1": "IAC-06", "CC6.2": "IAC-15"} {
		exec(`INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution)
		      VALUES ($1, $2, $3, 'intersects_with', 0.8, 'scf_official')`, uuid.New(), reqs[code], anchors[scf])
	}
	return fwSlug
}

type upload struct {
	filename  string
	payload   []byte
	framework string
	columnMap []string
}

func doUpload(t *testing.T, env testEnv, u upload) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", u.filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(u.payload); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if u.framework != "" {
		_ = mw.WriteField("framework", u.framework)
	}
	for _, m := range u.columnMap {
		_ = mw.WriteField("column_map", m)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/v1/controls/imports", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	return do(t, req)
}

func doGet(t *testing.T, env testEnv, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
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

func decodeBatch(t *testing.T, raw []byte) controlimport.Batch {
	t.Helper()
	var b controlimport.Batch
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("decode batch: %v; body=%s", err, raw)
	}
	return b
}

func fixtureCSV(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/controls.csv")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// fixtureXLSX converts testdata/controls.csv cell-for-cell into a workbook.
func fixtureXLSX(t *testing.T) []byte {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(fixtureCSV(t))).ReadAll()
	if err != nil {
		t.Fatalf("parse fixture csv: %v", err)
	}
	return makeXLSX(t, rows)
}

func makeXLSX(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			_ = f.SetCellStr(sheet, cell, val)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

// padXLSX re-zips a workbook with an extra entry of n zero bytes: small on
// the wire, n bytes declared uncompressed.
func padXLSX(t *testing.T, raw []byte, n int) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read xlsx zip: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if err := zw.Copy(f); err != nil {
			t.Fatalf("copy %s: %v", f.Name, err)
		}
	}
	w, err := zw.Create("xl/media/pad.bin")
	if err != nil {
		t.Fatalf("create pad: %v", err)
	}
	chunk := make([]byte, 1<<20)
	for written := 0; written < n; written += len(chunk) {
		if _, err := w.Write(chunk[:min(len(chunk), n-written)]); err != nil {
			t.Fatalf("write pad: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return out.Bytes()
}

// ----- expected outcome of testdata/controls.csv -----

type wantStaged struct {
	row         int
	id          string
	testedUnder []string
	implType    string
}

var wantFixtureStaged = []wantStaged{
	{row: 2, id: "ORG-AC-01", testedUnder: []string{"CC6.1", "CC6.2"}, implType: "automated"},
	{row: 3, id: "ORG-AC-02", testedUnder: []string{"CC6.2"}, implType: "manual_periodic"},
	{row: 7, id: "ORG-HR-01", testedUnder: []string{}, implType: "manual_attested"},
}

var wantFixtureRejected = []controlimport.RejectedRow{
	{RowNumber: 4, ExternalID: "ORG-AC-03", Reason: controlimport.RejectMissingField},
	{RowNumber: 5, ExternalID: "ORG-AC-04", Reason: controlimport.RejectUnknownTestedUnder},
	{RowNumber: 6, ExternalID: "ORG-AC-01", Reason: controlimport.RejectDuplicateID},
}

const fixtureDataRows = 6

func assertFixtureBatch(t *testing.T, b controlimport.Batch, format, framework string) {
	t.Helper()
	if b.ID == "" || b.Status != controlimport.BatchStatusStaged || b.SourceFormat != format {
		t.Fatalf("batch header = id %q status %q format %q", b.ID, b.Status, b.SourceFormat)
	}
	if b.Framework.Slug != framework || b.Framework.Version != "2017" {
		t.Errorf("framework = %+v, want %s 2017", b.Framework, framework)
	}
	if b.TotalRows != fixtureDataRows || b.StagedCount+b.RejectedCount != b.TotalRows {
		t.Errorf("counts total=%d staged=%d rejected=%d; want total %d = staged + rejected",
			b.TotalRows, b.StagedCount, b.RejectedCount, fixtureDataRows)
	}
	if len(b.Staged) != b.StagedCount || len(b.Rejected) != b.RejectedCount {
		t.Errorf("rows staged=%d rejected=%d disagree with counts %d/%d",
			len(b.Staged), len(b.Rejected), b.StagedCount, b.RejectedCount)
	}
	if len(b.Staged) != len(wantFixtureStaged) {
		t.Fatalf("staged = %+v, want %d rows", b.Staged, len(wantFixtureStaged))
	}
	for i, want := range wantFixtureStaged {
		got := b.Staged[i]
		if got.RowNumber != want.row || got.ExternalID != want.id || got.Status != controlimport.RowStatusPending {
			t.Errorf("staged[%d] = row %d id %q status %q; want row %d id %q pending",
				i, got.RowNumber, got.ExternalID, got.Status, want.row, want.id)
		}
		if got.ImplementationType != want.implType {
			t.Errorf("staged[%d] implementation_type = %q, want %q", i, got.ImplementationType, want.implType)
		}
		codes := make([]string, 0, len(got.TestedUnder))
		for _, r := range got.TestedUnder {
			if r.RequirementID == "" {
				t.Errorf("staged[%d] tested-under %s has no requirement_id", i, r.Code)
			}
			codes = append(codes, r.Code)
		}
		if strings.Join(codes, ",") != strings.Join(want.testedUnder, ",") {
			t.Errorf("staged[%d] tested_under = %v, want %v", i, codes, want.testedUnder)
		}
	}
	if len(b.Rejected) != len(wantFixtureRejected) {
		t.Fatalf("rejected = %+v, want %d rows", b.Rejected, len(wantFixtureRejected))
	}
	for i, want := range wantFixtureRejected {
		got := b.Rejected[i]
		if got.RowNumber != want.RowNumber || got.ExternalID != want.ExternalID || got.Reason != want.Reason {
			t.Errorf("rejected[%d] = %+v, want row %d id %q reason %s", i, got, want.RowNumber, want.ExternalID, want.Reason)
		}
		if got.Detail == "" {
			t.Errorf("rejected[%d] has no detail", i)
		}
	}
	if !strings.Contains(b.Rejected[1].Detail, "CC9.9") {
		t.Errorf("unknown tested-under detail = %q, want it to name CC9.9", b.Rejected[1].Detail)
	}
	if len(b.UnmappedColumns) != 1 || b.UnmappedColumns[0] != "Owner" {
		t.Errorf("unmapped_columns = %v, want [Owner]", b.UnmappedColumns)
	}
}

// ----- AC: upload stages and rejects, CSV and XLSX -----

func TestUploadStagesAndRejectsRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	framework := seedCatalog(t, admin)

	cases := []struct {
		name, filename, format string
		payload                func(*testing.T) []byte
	}{
		{"csv", "controls.csv", "csv", fixtureCSV},
		{"xlsx", "controls.xlsx", "xlsx", fixtureXLSX},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := freshTenant(t, admin)
			env := testServer(t, app, tenant)

			status, raw := doUpload(t, env, upload{filename: tc.filename, payload: tc.payload(t), framework: framework})
			if status != http.StatusCreated {
				t.Fatalf("upload = %d, want 201; body=%s", status, raw)
			}
			created := decodeBatch(t, raw)
			assertFixtureBatch(t, created, tc.format, framework)

			// The GET read-back returns the same batch from storage.
			status, raw = doGet(t, env, "/v1/controls/imports/"+created.ID)
			if status != http.StatusOK {
				t.Fatalf("get = %d, want 200; body=%s", status, raw)
			}
			got := decodeBatch(t, raw)
			assertFixtureBatch(t, got, tc.format, framework)
			if got.Staged[0].ID != created.Staged[0].ID {
				t.Errorf("read-back staged id %q != created %q", got.Staged[0].ID, created.Staged[0].ID)
			}
		})
	}
}

// ----- AC: an explicit column map is honoured -----

func TestUploadHonoursColumnMap(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	framework := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	sheet := makeXLSX(t, [][]string{
		{"Ref No.", "Control Name", "What we do", "Audit Mapping"},
		{"SEC-1", "MFA", "Workforce accounts use MFA.", "CC6.1"},
		{"SEC-2", "Access review", "Access is reviewed quarterly.", "CC6.2, CC7.1"},
	})

	// Without a map the sheet's ID and description columns are unrecognised.
	status, raw := doUpload(t, env, upload{filename: "odd.xlsx", payload: sheet, framework: framework})
	if status != http.StatusBadRequest {
		t.Fatalf("unmapped upload = %d, want 400; body=%s", status, raw)
	}

	status, raw = doUpload(t, env, upload{
		filename: "odd.xlsx", payload: sheet, framework: framework,
		columnMap: []string{"id=Ref No.", "description=What we do", "tested_under=Audit Mapping"},
	})
	if status != http.StatusCreated {
		t.Fatalf("mapped upload = %d, want 201; body=%s", status, raw)
	}
	b := decodeBatch(t, raw)
	if b.StagedCount != 2 || b.RejectedCount != 0 || b.TotalRows != 2 {
		t.Fatalf("counts = %d staged / %d rejected / %d total, want 2/0/2; rejected=%+v",
			b.StagedCount, b.RejectedCount, b.TotalRows, b.Rejected)
	}
	second := b.Staged[1]
	if second.ExternalID != "SEC-2" || second.Description != "Access is reviewed quarterly." || len(second.TestedUnder) != 2 {
		t.Errorf("staged[1] = %+v, want SEC-2 with its description and two criteria", second)
	}

	// A map naming a header the sheet doesn't have is a clear error.
	status, raw = doUpload(t, env, upload{
		filename: "odd.xlsx", payload: sheet, framework: framework,
		columnMap: []string{"id=Reference Number", "description=What we do"},
	})
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "Reference Number") {
		t.Errorf("bad map = %d %s, want 400 naming the missing header", status, raw)
	}
}

// ----- AC: oversize and over-row-cap uploads fail clearly -----

func TestUploadCaps(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	framework := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	t.Run("oversize", func(t *testing.T) {
		big := []byte("Control ID,Title,Description\n" + strings.Repeat("x", controlimport.MaxUploadBytes))
		status, raw := doUpload(t, env, upload{filename: "big.csv", payload: big, framework: framework})
		if status != http.StatusRequestEntityTooLarge || !strings.Contains(string(raw), "size cap") {
			t.Errorf("oversize = %d %s, want 413 naming the size cap", status, raw)
		}
	})
	t.Run("over row cap", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("Control ID,Title,Description\n")
		for i := 0; i < controlimport.MaxRows; i++ {
			fmt.Fprintf(&b, "C-%d,Title %d,Description %d\n", i, i, i)
		}
		status, raw := doUpload(t, env, upload{filename: "many.csv", payload: []byte(b.String()), framework: framework})
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "row cap") {
			t.Errorf("over row cap = %d %s, want 400 naming the row cap", status, raw)
		}
	})
	t.Run("xlsx decompression bomb", func(t *testing.T) {
		bomb := padXLSX(t, fixtureXLSX(t), controlimport.MaxUnzipBytes+1)
		status, raw := doUpload(t, env, upload{filename: "bomb.xlsx", payload: bomb, framework: framework})
		if status != http.StatusRequestEntityTooLarge || !strings.Contains(string(raw), "uncompressed cap") {
			t.Errorf("bomb (%d bytes on the wire) = %d %s, want 413 naming the uncompressed cap", len(bomb), status, raw)
		}
	})
	t.Run("xlsx over row cap", func(t *testing.T) {
		rows := [][]string{{"Control ID", "Title", "Description"}}
		for i := 0; i < controlimport.MaxRows; i++ {
			rows = append(rows, []string{fmt.Sprintf("C-%d", i), "T", "D"})
		}
		status, raw := doUpload(t, env, upload{filename: "many.xlsx", payload: makeXLSX(t, rows), framework: framework})
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "row cap") {
			t.Errorf("xlsx over row cap = %d %s, want 400 naming the row cap", status, raw)
		}
	})
	t.Run("unknown framework", func(t *testing.T) {
		status, raw := doUpload(t, env, upload{filename: "controls.csv", payload: fixtureCSV(t), framework: "no-such-framework"})
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "no-such-framework") {
			t.Errorf("unknown framework = %d %s, want 400 naming it", status, raw)
		}
	})

	// None of the refused uploads left a batch behind.
	var n int
	ctx := dbtest.WithTenantCtx(t, tenant)
	if err := withTenantTx(ctx, app, func(q pgx.Tx) error {
		return q.QueryRow(ctx, `SELECT count(*) FROM control_import_batches`).Scan(&n)
	}); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if n != 0 {
		t.Errorf("refused uploads left %d batches", n)
	}
}

// ----- AC: another tenant can't see the batch or its rows (RLS) -----

func TestBatchInvisibleToOtherTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	framework := seedCatalog(t, admin)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	status, raw := doUpload(t, envA, upload{filename: "controls.csv", payload: fixtureCSV(t), framework: framework})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	batchID := decodeBatch(t, raw).ID

	status, raw = doGet(t, envB, "/v1/controls/imports/"+batchID)
	if status != http.StatusNotFound {
		t.Errorf("tenant B GET = %d %s, want 404", status, raw)
	}

	// Directly through the RLS-bound app role: tenant B reads nothing from
	// any of the three tables, tenant A reads its rows.
	counts := func(tenant string) [3]int {
		ctx := dbtest.WithTenantCtx(t, tenant)
		var out [3]int
		err := withTenantTx(ctx, app, func(q pgx.Tx) error {
			for i, table := range []string{"control_import_batches", "control_import_rows", "control_import_rejected_rows"} {
				col := "batch_id"
				if i == 0 {
					col = "id"
				}
				if err := q.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+col+" = $1::uuid", batchID).Scan(&out[i]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("count as %s: %v", tenant, err)
		}
		return out
	}
	if got := counts(tenantB); got != [3]int{} {
		t.Errorf("tenant B sees batch/rows/rejected = %v, want none", got)
	}
	if got := counts(tenantA); got != [3]int{1, 3, 3} {
		t.Errorf("tenant A sees batch/rows/rejected = %v, want [1 3 3]", got)
	}
}

// ----- helpers for direct RLS-bound reads -----

func withTenantTx(ctx context.Context, app *pgxpool.Pool, fn func(pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := app.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	return fn(tx)
}
