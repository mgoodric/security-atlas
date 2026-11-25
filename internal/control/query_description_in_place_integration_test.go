//go:build integration

// Issue #2891: #2864 reworded evidence_queries[].description on nine stock
// bundles. An existing tenant re-uploads every stock bundle on start
// (deploy/docker/bootstrap/bootstrap.sh), so that rewording must update the
// active control in place, not supersede it and leave its owner and
// evidence on the old row.

package control_test

import (
	"context"
	"maps"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestUpload_StockQueryDescriptionChangeUpdatesInPlace(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	seeded, _ := seedSCFAnchor(t, admin, "AST-22", "AST")
	rel := releaseOfAnchor(t, admin, seeded)
	tenant := dbtest.SeedTenant(t, admin,
		"control_manifest_revisions", "risk_control_links", "risks", "exceptions",
		"control_owner_assignments", "evidence_records", "controls",
	)
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	// testdata/cc6_5-2864 holds soc2_cc6_5_asset_disposal's control.yaml
	// before and after #2864 (434c74b2), byte for byte. They differ only in
	// the one query's description. Neither declares scf_release (both
	// predate #1773), so the seeded anchor's release is added at upload,
	// identically to both: they still differ only in the description.
	upload := func(path string) control.UploadResult {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		b, err := control.FinalizeBundleForHTTP([]byte(withSCFRelease(string(raw), rel)))
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		res, err := store.Upload(ctx, b, "key_admin")
		if err != nil {
			t.Fatalf("upload %s: %v", path, err)
		}
		return res
	}

	pre := upload("testdata/cc6_5-2864/pre.yaml")
	seedControlLinks(t, admin, tenant, pre.ControlID)
	want := map[string]int{"owner": 1, "evidence": 1, "exception": 1, "risk_link": 1}

	post := upload("testdata/cc6_5-2864/post.yaml")
	if !post.UpdatedInPlace || post.ControlID != pre.ControlID || post.Version != pre.Version ||
		post.SupersededID != (uuid.UUID{}) {
		t.Fatalf("query-description-only upload should update in place; got %+v (prior id %s)", post, pre.ControlID)
	}
	if got := linkCounts(t, admin, tenant, pre.ControlID); !maps.Equal(got, want) {
		t.Fatalf("links after upload: got %v, want %v", got, want)
	}

	var rows int
	var storedDesc string
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM controls WHERE tenant_id = $1 AND bundle_id = 'soc2_cc6_5_asset_disposal'
	`, tenant).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("upload inserted a version: %d controls rows", rows)
	}
	// The stored evidence_queries copy carries the new description.
	if err := admin.QueryRow(context.Background(), `
		SELECT evidence_queries->0->>'description' FROM controls WHERE id = $1
	`, pre.ControlID).Scan(&storedDesc); err != nil {
		t.Fatalf("read evidence_queries: %v", err)
	}
	if storedDesc != "A disposal-log upload exists within the last 120 days (quarterly plus 30-day grace)" {
		t.Fatalf("evidence_queries description not updated: %q", storedDesc)
	}
}
