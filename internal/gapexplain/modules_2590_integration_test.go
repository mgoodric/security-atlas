//go:build integration

package gapexplain_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/gapexplain"
)

// seedException inserts an active exception on the control.
func seedException(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate,
			justification, requested_by, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb, '#2590 test exception', 'requester',
		        now() + INTERVAL '15 days', 'active')
	`, id, tenant, ctrlID); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
	return id
}

// #2590: the gap explanation cites only the control and its evidence. An
// exception on the control cannot be cited, whether the Exceptions module
// is switched off (first tenant) or on (second): the citation does not
// resolve, so a draft naming it is suppressed, and the rollup the model
// sees carries no exception. A draft citing only the control and evidence
// renders in both tenants, so the switch changes nothing here.
func TestExplain2590_NeverCitesAnException(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	flags := featureflag.NewStore(app)

	for _, on := range []bool{false, true} {
		tenant := dbtest.SeedTenant(t, admin,
			"exceptions", "evidence_freshness", "evidence_records", "controls",
			"feature_flags", "feature_flag_audit_log")
		ctx := tenantCtx(t, tenant)
		if _, err := flags.Set(ctx, "exceptions.enabled", on, "test-2590", "test"); err != nil {
			t.Fatalf("switch exceptions.enabled=%v: %v", on, err)
		}
		if got, err := featureflag.EnabledStrict(ctx, flags, "exceptions.enabled"); err != nil || got != on {
			t.Fatalf("exceptions.enabled = %v, %v; want %v", got, err, on)
		}

		observed := time.Now().UTC().Add(-120 * 24 * time.Hour)
		ctrlID := seedControl(t, admin, tenant)
		evID := seedEvidence(t, admin, tenant, ctrlID, observed)
		seedStaleFreshness(t, admin, tenant, ctrlID, observed)
		exID := seedException(t, admin, tenant, ctrlID)
		store := gapexplain.NewStore(app)

		if _, ok, err := store.Resolve(ctx, exID); err != nil || ok {
			t.Errorf("exceptions on=%v: Resolve(exception) = %v, %v; want unresolved", on, ok, err)
		}
		rollup, err := store.Rollup(ctx, ctrlID)
		if err != nil {
			t.Fatalf("Rollup: %v", err)
		}
		for _, ev := range rollup.Evidence {
			if ev.EvidenceID == exID {
				t.Errorf("exceptions on=%v: rollup carries the exception", on)
			}
		}

		citing := fmt.Sprintf("Control (%s) is excepted (%s); its evidence (%s) is stale.", ctrlID, exID, evID)
		exp, err := stubWith(store, citing).Explain(ctx, ctrlID)
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		if !exp.Suppressed {
			t.Errorf("exceptions on=%v: a draft citing an exception was shown (citations %+v)", on, exp.Citations)
		}
		clean := fmt.Sprintf("Control (%s) is stale; its evidence (%s) is past the window.", ctrlID, evID)
		exp, err = stubWith(store, clean).Explain(ctx, ctrlID)
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		if exp.Suppressed {
			t.Errorf("exceptions on=%v: a draft citing control and evidence was suppressed (%q)", on, exp.Reason)
		}
	}
}
