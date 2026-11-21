//go:build integration

// #2511: an unscored risk created through the MCP create_risk tool and one
// created through POST /v1/risks's store are the same row.
//
// Run with:  go test -tags=integration -run TestCreateRisk_UnscoredSameRowAsREST ./internal/mcp/writeproposals/...

package writeproposals_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestCreateRisk_UnscoredSameRowAsREST(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := treatmentTenant(t, admin)
	store := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(app), nil)
	ctx := ctxFor(t, tenant)

	applied, err := proposeAndConfirm(t, store, ctx, writeproposals.ToolCreateRisk,
		`{"title":"Unscored","category":"operational"}`)
	if err != nil {
		t.Fatalf("create_risk with no score: %v", err)
	}
	created, err := risk.NewStore(app).Create(ctx, risk.CreateInput{Actor: "test-actor", Title: "Unscored", CategoryRef: "operational"})
	if err != nil {
		t.Fatalf("risk.Store.Create with no score: %v", err)
	}

	type row struct{ methodology, inherent, residual, treatment, level string }
	read := func(id string) row {
		t.Helper()
		var r row
		if err := admin.QueryRow(context.Background(), `
			SELECT methodology::text, inherent_score::text, residual_score::text, treatment::text, level::text
			FROM risks WHERE id = $1`, id,
		).Scan(&r.methodology, &r.inherent, &r.residual, &r.treatment, &r.level); err != nil {
			t.Fatalf("read risk %s: %v", id, err)
		}
		return r
	}
	mcpRow, restRow := read(*applied.AppliedSubject), read(created.ID.String())
	if mcpRow != restRow {
		t.Fatalf("MCP row %+v differs from REST row %+v", mcpRow, restRow)
	}
	if mcpRow.inherent != "{}" || mcpRow.residual != "{}" {
		t.Fatalf("unscored row stores %s / %s, want {} / {}", mcpRow.inherent, mcpRow.residual)
	}
}
