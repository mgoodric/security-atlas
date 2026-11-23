//go:build integration

// #2836 — the web mapping step's API: POST /v1/controls/imports/preview
// reads an upload's header row and first rows without staging it, and the
// upload honours the step's column map and ignore_column. Real Postgres
// with RLS and the assembled router; see integration_test.go.
package controlimports_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// previewServer serves one tenant with the authz engine attached, and
// returns a grc_engineer bearer and a viewer bearer, each a person holding
// that role in user_roles.
func previewServer(t *testing.T, admin, app *pgxpool.Pool, tenant string) (testEnv, string) {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	attachAuthz(t, srv, app)
	// Issue the tokens first: the first one attaches the JWT validator the
	// router is built with.
	env := testEnv{bearer: personBearer(t, admin, srv, tenant, authz.RoleGRCEngineer)}
	viewer := personBearer(t, admin, srv, tenant, authz.RoleViewer)
	env.server = httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(env.server.Close)
	return env, viewer
}

func decodePreview(t *testing.T, raw []byte) controlimport.Preview {
	t.Helper()
	var p controlimport.Preview
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode preview: %v; body=%s", err, raw)
	}
	return p
}

func batchCount(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_import_batches WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	return n
}

func TestControlImport_PreviewMapsWithoutStaging(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	framework := seedCatalog(t, admin).framework
	tenant := dbtest.SeedTenant(t, admin,
		"control_import_candidates",
		"control_import_rejected_rows",
		"control_import_rows",
		"control_import_batches",
		"user_roles",
		"users",
	)
	env, viewer := previewServer(t, admin, app, tenant)

	// A title row above headers no alias matches, and a "Type" column an
	// alias maps to implementation_type, holding a value it would reject.
	sheet := []byte("Our register\nKey,Headline,Body,Audit Mapping,Type\n" +
		"SEC-1,MFA,Workforce accounts use MFA.,CC6.1,not-a-type\n")
	const preview = "/v1/controls/imports/preview"

	status, raw := doUpload(t, env, upload{filename: "odd.csv", payload: sheet, path: preview})
	if status != http.StatusOK {
		t.Fatalf("preview = %d; body=%s", status, raw)
	}
	p := decodePreview(t, raw)
	if p.HeaderDetected || p.HeaderRow != 2 || p.DataRows != 1 || len(p.Rows) != 1 {
		t.Fatalf("preview = %+v, want row 2 offered with one data row", p)
	}
	fields := map[string]string{}
	for _, c := range p.Columns {
		fields[c.Header] = c.Field
		if !c.Mappable {
			t.Errorf("column %q not mappable", c.Header)
		}
	}
	want := map[string]string{"Key": "", "Headline": "", "Body": "", "Audit Mapping": "", "Type": controlimport.FieldImplementationType}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}

	mapped := []string{"id=Key", "title=Headline", "description=Body", "tested_under=Audit Mapping"}
	status, raw = doUpload(t, env, upload{filename: "odd.csv", payload: sheet, path: preview, columnMap: mapped, ignore: []string{"Type"}})
	if status != http.StatusOK {
		t.Fatalf("mapped preview = %d; body=%s", status, raw)
	}
	if p := decodePreview(t, raw); !p.HeaderDetected || p.Columns[4].Field != "" {
		t.Fatalf("mapped preview = %+v, want detected with Type ignored", p)
	}
	if n := batchCount(t, admin, tenant); n != 0 {
		t.Fatalf("previews staged %d batches, want 0", n)
	}

	status, raw = doUpload(t, env, upload{
		filename: "odd.csv", payload: sheet, framework: framework,
		columnMap: mapped, ignore: []string{"Type"},
	})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	b := decodeBatch(t, raw)
	if b.StagedCount != 1 || b.RejectedCount != 0 {
		t.Fatalf("counts = %d staged / %d rejected; rejected=%+v", b.StagedCount, b.RejectedCount, b.Rejected)
	}
	if s := b.Staged[0]; s.ExternalID != "SEC-1" || s.Title != "MFA" || s.ImplementationType != "" || len(s.TestedUnder) != 1 {
		t.Fatalf("staged = %+v", s)
	}
	if !reflect.DeepEqual(b.UnmappedColumns, []string{"Type"}) {
		t.Fatalf("unmapped = %v, want the ignored alias column", b.UnmappedColumns)
	}

	// A viewer cannot preview, and a header both mapped and ignored is a 400.
	if status, raw := doUpload(t, env, upload{filename: "odd.csv", payload: sheet, framework: framework, columnMap: mapped, bearer: viewer}); status != http.StatusForbidden {
		t.Errorf("viewer upload = %d, want 403; body=%.200s", status, raw)
	}
	if status, raw := doUpload(t, env, upload{filename: "odd.csv", payload: sheet, path: preview, bearer: viewer}); status != http.StatusForbidden {
		t.Errorf("viewer preview = %d, want 403; body=%s", status, raw)
	}
	if status, raw := doUpload(t, env, upload{filename: "odd.csv", payload: sheet, path: preview, columnMap: mapped, ignore: []string{"Key"}}); status != http.StatusBadRequest {
		t.Errorf("mapped and ignored = %d, want 400; body=%s", status, raw)
	}
	// A row past the column cap is the same 400, with the same message, on
	// preview and upload.
	wide := []byte("ID,Title,Description\nA,B,C" + strings.Repeat(",x", controlimport.MaxColumns) + "\n")
	pStatus, pRaw := doUpload(t, env, upload{filename: "wide.csv", payload: wide, path: preview})
	uStatus, uRaw := doUpload(t, env, upload{filename: "wide.csv", payload: wide, framework: framework})
	if pStatus != http.StatusBadRequest || uStatus != http.StatusBadRequest || string(pRaw) != string(uRaw) ||
		!strings.Contains(string(pRaw), "past column 200") {
		t.Errorf("column cap: preview %d %s, upload %d %s; want the same 400", pStatus, pRaw, uStatus, uRaw)
	}

	if n := batchCount(t, admin, tenant); n != 1 {
		t.Fatalf("batches = %d, want the one upload", n)
	}
}
