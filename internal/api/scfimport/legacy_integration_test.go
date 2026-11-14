//go:build integration

package scfimport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// #1769: SCF catalogs carry legacy SCF IDs.

const legacyTestTenant = "11111111-1111-1111-1111-111111111111"

func legacyCatalog() *scfimport.Catalog {
	return &scfimport.Catalog{
		SchemaVersion:  scfimport.SchemaVersion,
		ReleaseVersion: "test-legacy-1769",
		ReleaseDate:    "2026-11-01",
		Controls: []scfimport.Control{
			{SCFID: "IAC-06", Family: "IAC", Title: "Multi-Factor Authentication (MFA)", Description: "MFA.", LegacySCFID: "IAC-06.1"},
			{SCFID: "IAC-07", Family: "IAC", Title: "User Provisioning & Lifecycle", Description: "Provisioning."},
		},
	}
}

func storedLegacy(t *testing.T, pool *pgxpool.Pool, release, scfID string) *string {
	t.Helper()
	var got *string
	err := pool.QueryRow(context.Background(), `
		SELECT a.legacy_scf_id
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = $2`, release, scfID).Scan(&got)
	if err != nil {
		t.Fatalf("read legacy_scf_id for %s: %v", scfID, err)
	}
	return got
}

// anchorAPI boots the HTTP server on the RLS-enforcing app role, the way
// the anchors package's own integration tests do.
func anchorAPI(t *testing.T) func(path string) map[string]any {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(dbtest.NewAppPool(t))
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(legacyTestTenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return func(path string) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d; body=%s", path, resp.StatusCode, body)
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("GET %s: unmarshal: %v", path, err)
		}
		return out
	}
}

func listedBySCFID(t *testing.T, resp map[string]any) map[string]map[string]any {
	t.Helper()
	list, ok := resp["anchors"].([]any)
	if !ok {
		t.Fatalf("response has no anchors array: %v", resp)
	}
	out := make(map[string]map[string]any, len(list))
	for _, item := range list {
		a := item.(map[string]any)
		out[a["scf_id"].(string)] = a
	}
	return out
}

func TestImport_LegacySCFIDStoredAndReturnedByAnchorAPI(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := legacyCatalog()

	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := storedLegacy(t, pool, cat.ReleaseVersion, "IAC-06"); got == nil || *got != "IAC-06.1" {
		t.Fatalf("IAC-06 legacy_scf_id = %v; want IAC-06.1", got)
	}
	if got := storedLegacy(t, pool, cat.ReleaseVersion, "IAC-07"); got != nil {
		t.Fatalf("IAC-07 legacy_scf_id = %q; want NULL", *got)
	}

	get := anchorAPI(t)

	one := get("/v1/anchors/IAC-06")["anchor"].(map[string]any)
	if one["legacy_scf_id"] != "IAC-06.1" {
		t.Fatalf("GET /v1/anchors/IAC-06 legacy_scf_id = %v; want IAC-06.1", one["legacy_scf_id"])
	}
	other := get("/v1/anchors/IAC-07")["anchor"].(map[string]any)
	if v, present := other["legacy_scf_id"]; present {
		t.Fatalf("GET /v1/anchors/IAC-07 carries legacy_scf_id %v; want it absent", v)
	}

	for _, path := range []string{"/v1/anchors", "/v1/anchors?include=state"} {
		listed := listedBySCFID(t, get(path))
		if listed["IAC-06"]["legacy_scf_id"] != "IAC-06.1" {
			t.Fatalf("%s IAC-06 legacy_scf_id = %v; want IAC-06.1", path, listed["IAC-06"]["legacy_scf_id"])
		}
		if v, present := listed["IAC-07"]["legacy_scf_id"]; present {
			t.Fatalf("%s IAC-07 carries legacy_scf_id %v; want it absent", path, v)
		}
	}
}

func TestImport_CatalogWithoutLegacySCFIDImportsUnchanged(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)

	report, err := scfimport.Import(context.Background(), pool, cat)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Created != len(cat.Controls) {
		t.Fatalf("created = %d; want %d", report.Created, len(cat.Controls))
	}
	var withLegacy int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_anchors WHERE legacy_scf_id IS NOT NULL`).Scan(&withLegacy); err != nil {
		t.Fatalf("count: %v", err)
	}
	if withLegacy != 0 {
		t.Fatalf("%d anchors carry a legacy_scf_id from a catalog without the field", withLegacy)
	}
}

// A release imported before its catalog carried legacy IDs is filled in by
// re-importing the same release label; a stored value is never rewritten.
func TestImport_ReimportBackfillsLegacySCFIDButNeverRewritesIt(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	ctx := context.Background()

	bare := legacyCatalog()
	bare.Controls[0].LegacySCFID = ""
	if _, err := scfimport.Import(ctx, pool, bare); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	report, err := scfimport.Import(ctx, pool, legacyCatalog())
	if err != nil {
		t.Fatalf("backfill Import: %v", err)
	}
	if report.Updated != 1 || report.Unchanged != 1 || report.IsNewVersion {
		t.Fatalf("backfill report = %+v; want 1 updated, 1 unchanged, not new", report)
	}
	if got := storedLegacy(t, pool, bare.ReleaseVersion, "IAC-06"); got == nil || *got != "IAC-06.1" {
		t.Fatalf("IAC-06 legacy_scf_id after backfill = %v; want IAC-06.1", got)
	}

	// Same catalog again is a no-op.
	report, err = scfimport.Import(ctx, pool, legacyCatalog())
	if err != nil {
		t.Fatalf("repeat Import: %v", err)
	}
	if report.Updated != 0 || report.Unchanged != 2 {
		t.Fatalf("repeat report = %+v; want 0 updated, 2 unchanged", report)
	}

	changed := legacyCatalog()
	changed.Controls[0].LegacySCFID = "IAC-99"
	_, err = scfimport.Import(ctx, pool, changed)
	var conflict *scfimport.ReleaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Import with a different legacy_scf_id: err = %v; want *ReleaseConflictError", err)
	}
	if len(conflict.Changed) != 1 || conflict.Changed[0] != "IAC-06" {
		t.Fatalf("conflict.Changed = %v; want [IAC-06]", conflict.Changed)
	}
	if got := storedLegacy(t, pool, bare.ReleaseVersion, "IAC-06"); got == nil || *got != "IAC-06.1" {
		t.Fatalf("IAC-06 legacy_scf_id after refused import = %v; want IAC-06.1", got)
	}
}
