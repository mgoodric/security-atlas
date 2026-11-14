//go:build integration

// #1780 — integration tests for deterministic SCF anchor candidates on
// staged control import rows: crosswalk candidates from the framework's
// mappings of each row's tested criteria, keyword candidates from the SCF
// catalog, the no-candidate-reaches flag and its filter. Real Postgres with
// RLS and the assembled platform router; no LLM is configured anywhere.
package controlimports_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func decodeRows(t *testing.T, raw []byte) controlimport.RowList {
	t.Helper()
	var l controlimport.RowList
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatalf("decode rows: %v; body=%s", err, raw)
	}
	return l
}

func rowByID(t *testing.T, rows []controlimport.BatchControl, externalID string) controlimport.BatchControl {
	t.Helper()
	for _, r := range rows {
		if r.ExternalID == externalID {
			return r
		}
	}
	t.Fatalf("no staged row %s in %+v", externalID, rows)
	return controlimport.BatchControl{}
}

func codesOf(reqs []controlimport.Requirement) string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Code
	}
	return strings.Join(out, ",")
}

// uploadFixture stages testdata/controls.csv (or its XLSX twin) and returns
// the batch ID.
func uploadFixture(t *testing.T, env testEnv, framework, format string) string {
	t.Helper()
	u := upload{filename: "controls.csv", payload: fixtureCSV(t), framework: framework}
	if format == "xlsx" {
		u = upload{filename: "controls.xlsx", payload: fixtureXLSX(t), framework: framework}
	}
	status, raw := doUpload(t, env, u)
	if status != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", status, raw)
	}
	b := decodeBatch(t, raw)
	if b.SuggestionsStatus != controlimport.SuggestionsReady {
		t.Fatalf("suggestions_status = %q, want ready", b.SuggestionsStatus)
	}
	return b.ID
}

func listRows(t *testing.T, env testEnv, batchID, query string) controlimport.RowList {
	t.Helper()
	path := "/v1/controls/imports/" + batchID + "/rows"
	if query != "" {
		path += "?" + query
	}
	status, raw := doGet(t, env, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200; body=%s", path, status, raw)
	}
	return decodeRows(t, raw)
}

// ----- AC: the crosswalk candidate reaching both criteria ranks first -----

func TestCrosswalkCandidateReachingBothCriteriaRanksFirst(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)

	for _, format := range []string{"csv", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			tenant := freshTenant(t, admin)
			env := testServer(t, app, tenant)
			batchID := uploadFixture(t, env, cat.framework, format)

			list := listRows(t, env, batchID, "")
			if list.SCFRelease == nil || list.SCFRelease.Version != cat.scfRelease {
				t.Errorf("scf_release = %+v, want %s", list.SCFRelease, cat.scfRelease)
			}
			row := rowByID(t, list.Rows, "ORG-AC-01")
			if len(row.Candidates) < 2 {
				t.Fatalf("ORG-AC-01 candidates = %+v, want at least two", row.Candidates)
			}
			top := row.Candidates[0]
			if top.Rank != 1 || top.SCFID != cat.anchors["mfa"] || top.Source != controlimport.SourceCrosswalk {
				t.Errorf("top candidate = %+v, want rank 1 %s from crosswalk", top, cat.anchors["mfa"])
			}
			if codesOf(top.Reaches) != "CC6.1,CC6.2" || top.TestedCount != 2 || top.Fit() != "reaches 2 of 2: CC6.1, CC6.2" {
				t.Errorf("top fit = %q (reaches %s of %d), want reaches 2 of 2: CC6.1, CC6.2",
					top.Fit(), codesOf(top.Reaches), top.TestedCount)
			}
			second := row.Candidates[1]
			if second.SCFID != cat.anchors["review"] || second.Source != controlimport.SourceCrosswalk || second.Fit() != "reaches 1 of 2: CC6.2" {
				t.Errorf("second candidate = %+v fit %q, want %s reaching 1 of 2", second, second.Fit(), cat.anchors["review"])
			}
			if row.NoCandidateReaches {
				t.Error("ORG-AC-01 flagged although candidates reach its criteria")
			}

			// A single-criterion row ranks its crosswalk anchors by strength.
			ac02 := rowByID(t, list.Rows, "ORG-AC-02")
			if len(ac02.Candidates) < 2 || ac02.Candidates[0].SCFID != cat.anchors["review"] || ac02.Candidates[1].SCFID != cat.anchors["mfa"] {
				t.Errorf("ORG-AC-02 candidates = %+v, want review (0.9) then mfa (0.7)", ac02.Candidates)
			}
		})
	}
}

// ----- AC: no tested-under data gets keyword candidates only -----

func TestRowWithoutTestedUnderGetsKeywordCandidatesOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)
	batchID := uploadFixture(t, env, cat.framework, "csv")

	row := rowByID(t, listRows(t, env, batchID, "").Rows, "ORG-HR-01")
	if len(row.TestedUnder) != 0 {
		t.Fatalf("ORG-HR-01 tested_under = %+v, want none", row.TestedUnder)
	}
	if len(row.Candidates) == 0 {
		t.Fatal("ORG-HR-01 has no candidates, want keyword candidates")
	}
	for _, c := range row.Candidates {
		if c.Source != controlimport.SourceKeyword || len(c.Reaches) != 0 || c.TestedCount != 0 {
			t.Errorf("candidate %+v, want a keyword candidate reaching nothing", c)
		}
	}
	if row.NoCandidateReaches {
		t.Error("a row with no tested criteria is flagged")
	}
}

// ----- AC: a row whose criteria no anchor reaches is flagged and filterable -----

func TestUnreachedRowIsFlaggedAndFiltered(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)
	batchID := uploadFixture(t, env, cat.framework, "csv")

	row := rowByID(t, listRows(t, env, batchID, "").Rows, "ORG-OP-01")
	if !row.NoCandidateReaches {
		t.Errorf("ORG-OP-01 (tested under CC7.1, which nothing reaches) is not flagged")
	}
	for _, c := range row.Candidates {
		if c.Source != controlimport.SourceKeyword || c.Fit() != "reaches 0 of 1" {
			t.Errorf("ORG-OP-01 candidate %+v fit %q, want keyword reaching 0 of 1", c, c.Fit())
		}
	}

	flagged := listRows(t, env, batchID, "no_candidate_reaches=true").Rows
	if len(flagged) != 1 || flagged[0].ExternalID != "ORG-OP-01" {
		t.Errorf("no_candidate_reaches=true rows = %+v, want only ORG-OP-01", flagged)
	}
	if got := listRows(t, env, batchID, "status=pending").Rows; len(got) != len(wantFixtureStaged) {
		t.Errorf("status=pending rows = %d, want %d", len(got), len(wantFixtureStaged))
	}
	if got := listRows(t, env, batchID, "status=approved&no_candidate_reaches=true").Rows; len(got) != 0 {
		t.Errorf("status=approved rows = %+v, want none", got)
	}
	for _, q := range []string{"status=bogus", "no_candidate_reaches=maybe"} {
		if status, raw := doGet(t, env, "/v1/controls/imports/"+batchID+"/rows?"+q); status != http.StatusBadRequest {
			t.Errorf("?%s = %d %s, want 400", q, status, raw)
		}
	}
}

// ----- AC: no LLM configured, every row still has candidates -----

func TestNoLLMEveryRowHasCandidates(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant) // api.Config{}: no LLM provider

	status, raw := doUpload(t, env, upload{filename: "controls.csv", payload: fixtureCSV(t), framework: cat.framework})
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	if strings.Contains(string(raw), "suppressed") {
		t.Errorf("upload response mentions suppression: %s", raw)
	}
	created := decodeBatch(t, raw)
	list := listRows(t, env, created.ID, "")
	if len(list.Rows) != len(wantFixtureStaged) {
		t.Fatalf("rows = %d, want %d", len(list.Rows), len(wantFixtureStaged))
	}
	for _, r := range list.Rows {
		if len(r.Candidates) == 0 {
			t.Errorf("row %d %s has no candidates", r.RowNumber, r.ExternalID)
		}
		for _, c := range r.Candidates {
			if c.Source != controlimport.SourceCrosswalk && c.Source != controlimport.SourceKeyword {
				t.Errorf("row %s candidate source %q", r.ExternalID, c.Source)
			}
		}
	}
	// The upload response and the batch read-back carry the same candidates.
	status, raw = doGet(t, env, "/v1/controls/imports/"+created.ID)
	if status != http.StatusOK {
		t.Fatalf("get = %d; body=%s", status, raw)
	}
	got := decodeBatch(t, raw)
	for i := range got.Staged {
		if len(got.Staged[i].Candidates) != len(created.Staged[i].Candidates) {
			t.Errorf("row %s: read-back %d candidates, upload %d",
				got.Staged[i].ExternalID, len(got.Staged[i].Candidates), len(created.Staged[i].Candidates))
		}
	}
}

// ----- RLS: another tenant sees neither the rows nor the candidates -----

func TestCandidatesInvisibleToOtherTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)
	batchID := uploadFixture(t, envA, cat.framework, "csv")

	if status, raw := doGet(t, envB, "/v1/controls/imports/"+batchID+"/rows"); status != http.StatusNotFound {
		t.Errorf("tenant B rows = %d %s, want 404", status, raw)
	}
	count := func(tenant string) int {
		ctx := dbtest.WithTenantCtx(t, tenant)
		var n int
		if err := withTenantTx(ctx, app, func(q pgx.Tx) error {
			return q.QueryRow(ctx, `SELECT count(*) FROM control_import_candidates WHERE batch_id = $1::uuid`, batchID).Scan(&n)
		}); err != nil {
			t.Fatalf("count candidates as %s: %v", tenant, err)
		}
		return n
	}
	if n := count(tenantB); n != 0 {
		t.Errorf("tenant B sees %d candidates, want none", n)
	}
	if n := count(tenantA); n == 0 {
		t.Error("tenant A sees no candidates")
	}
}
