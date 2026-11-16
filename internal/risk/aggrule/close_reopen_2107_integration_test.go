//go:build integration

// #2107 — closing a child takes it out of its rule's meta-risk at once,
// as deleting one does (#1861); reopening it puts it back. A closed risk
// is not an aggregation candidate. The engine never closes the meta-risk.

package aggrule_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

func TestEngine_CloseAndReopenRefreshMetaRisk_2107(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, rules, ctx, makeRule("ownership-stale", "ownership", 2, 1, 90, "max"))

	now := time.Now().UTC().Add(5 * time.Second)
	r1 := seedRisk(t, admin, tenant, "r1", 2, 2, nil, &team1, now) // sev 4
	r2 := seedRisk(t, admin, tenant, "r2", 3, 3, nil, &team1, now) // sev 9
	r3 := seedRisk(t, admin, tenant, "r3", 5, 5, nil, &team1, now) // sev 25
	for _, id := range []uuid.UUID{r1, r2, r3} {
		if _, err := risks.AssignThemes(ctx, id, []string{"ownership"}); err != nil {
			t.Fatalf("AssignThemes %s: %v", id, err)
		}
	}
	var meta uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT id FROM risks WHERE tenant_id=$1 AND (inherent_score->>'rule_generated')::bool`,
		tenant).Scan(&meta); err != nil {
		t.Fatalf("find meta-risk: %v", err)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 25 {
		t.Fatalf("meta-risk severity with 3 children = %d, want 25", got)
	}

	if _, err := risks.Close(ctx, r3, risk.CloseInput{
		Reason: dbx.RiskClosureReasonRealisedAndHandled, Actor: "test-actor",
	}); err != nil {
		t.Fatalf("Close r3: %v", err)
	}
	if children := metaChildIDs(t, admin, tenant, meta); children[r3] || len(children) != 2 {
		t.Fatalf("after close: children %v, want r1, r2 only", children)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 9 {
		t.Fatalf("after close: severity %d, want 9 (max of 4, 9)", got)
	}

	if _, err := risks.Reopen(ctx, r3, risk.ReopenInput{Actor: "test-actor"}); err != nil {
		t.Fatalf("Reopen r3: %v", err)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 25 {
		t.Fatalf("after reopen: severity %d, want 25", got)
	}
	var status string
	if err := admin.QueryRow(context.Background(),
		`SELECT status::text FROM risks WHERE id = $1`, meta).Scan(&status); err != nil {
		t.Fatalf("meta-risk status: %v", err)
	}
	if status != "open" {
		t.Fatalf("meta-risk status = %s, want open", status)
	}
}
