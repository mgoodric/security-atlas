//go:build integration

// Integration test for issue #1738: a control whose SCF anchor reaches no
// requirement in an enabled framework gets a non-fatal warning that suggests
// the parent anchor when that one is mapped. Real Postgres with RLS.

package control_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedMappedRequirement creates a throwaway framework with a current version
// and one requirement mapped to anchorID, removing all of it on cleanup.
func seedMappedRequirement(t *testing.T, admin *pgxpool.Pool, anchorID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fwID, fvID, reqID := uuid.New(), uuid.New(), uuid.New()
	slug := "t1738-" + fwID.String()[:8]
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO frameworks (id, tenant_id, slug, name, issuer) VALUES ($1, NULL, $2, $2, 'test')`, []any{fwID, slug}},
		{`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status) VALUES ($1, NULL, $2, '1', 'current')`, []any{fvID, fwID}},
		{`INSERT INTO framework_requirements (id, framework_version_id, code, title) VALUES ($1, $2, 'R-1', 'test requirement')`, []any{reqID, fvID}},
		{`INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, mapping_tier)
		  VALUES ($1, $2, $3, 'equal', 1.0, 'org_internal', 'verified')`, []any{uuid.New(), reqID, anchorID}},
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed mapped requirement: %v", err)
		}
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, fwID); err != nil {
			t.Logf("cleanup framework: %v", err)
		}
	})
}

func TestAnchorCoverageWarning(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	suffix := strings.ToUpper(uuid.New().String()[:4])
	parentID, parentCode := seedSCFAnchor(t, admin, "TZA-"+suffix, "TZA")
	_, childCode := seedSCFAnchor(t, admin, parentCode+".1", "TZA")
	mappedID, mappedCode := seedSCFAnchor(t, admin, "TZB-"+suffix, "TZB")
	_, orphanCode := seedSCFAnchor(t, admin, "TZC-"+suffix, "TZC")
	seedMappedRequirement(t, admin, parentID)
	seedMappedRequirement(t, admin, mappedID)

	tenant := freshTenant(t, admin)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	store := control.NewStore(app)

	t.Run("mapped anchor has no warning", func(t *testing.T) {
		got, err := store.AnchorCoverageWarning(ctx, mappedCode)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want no warning", got, err)
		}
	})
	t.Run("unmapped sub-control suggests its mapped parent", func(t *testing.T) {
		got, err := store.AnchorCoverageWarning(ctx, childCode)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, childCode) || !strings.Contains(got, "parent anchor "+parentCode+" does") {
			t.Fatalf("warning = %q, want it to name %s and suggest parent %s", got, childCode, parentCode)
		}
	})
	t.Run("unmapped top-level anchor warns without a parent", func(t *testing.T) {
		got, err := store.AnchorCoverageWarning(ctx, orphanCode)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, orphanCode) || strings.Contains(got, "parent anchor") {
			t.Fatalf("warning = %q, want a warning with no parent suggestion", got)
		}
	})
	t.Run("unknown anchor is not this check's concern", func(t *testing.T) {
		got, err := store.AnchorCoverageWarning(ctx, "NOPE-"+suffix)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want empty", got, err)
		}
	})
}
