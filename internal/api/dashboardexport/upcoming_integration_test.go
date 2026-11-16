//go:build integration

// #2130 review — the export's upcoming panel reads the calendar's next 14
// days, so a policy acknowledgement lapsing in that window is still in it.

package dashboardexport_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/api/dashboardexport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
)

func TestUpcomingPanel_CarriesLapsingPolicyAck(t *testing.T) {
	tenant := dbtest.SeedTenant(t, dbtest.NewMigratePool(t), "policy_acknowledgments", "policies", "users")
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()

	userID, policyID, ackID := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		what string
		sql  string
		args []any
	}{
		{"user", `INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Export Ack', 'active')`,
			[]any{userID, tenant, "export-ack-" + userID.String()[:8] + "@test.example"}},
		{"policy", `INSERT INTO policies (id, tenant_id, title, version, status, effective_date, body_md, owner_role, approver_role, created_by, published_at, published_by)
			VALUES ($1, $2, 'Export ack policy', '1.0.0', 'published', now()::date, '# body', 'security_lead', 'ciso', 'tester', now(), 'tester')`,
			[]any{policyID, tenant}},
		{"ack", `INSERT INTO policy_acknowledgments (id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token)
			VALUES ($1, $2, $3, $3, $4, now() - INTERVAL '355 days', $5)`,
			[]any{ackID, tenant, policyID, userID, "export-" + ackID.String()}},
	} {
		if _, err := admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed %s: %v", q.what, err)
		}
	}

	app := dbtest.NewAppPool(t)
	src := dashboardexport.NewLivePanelSource(
		dashboard.NewStore(app), risk.NewStore(app), freshness.NewStore(app), drift.NewStore(app),
		calendar.NewStore(app),
	)
	snap, err := src.Snapshot(dbtest.WithTenantCtx(t, tenant), dashboardexport.UpcomingTypes{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, row := range snap.Panels.Upcoming {
		if row.Category == "policy_ack" && row.ResourceID == policyID.String() {
			if !strings.HasPrefix(row.Title, "Export ack policy: 1 acknowledgement lapses on ") || row.ResourceType != "policy" {
				t.Errorf("ack row: title=%q resource_type=%q", row.Title, row.ResourceType)
			}
			return
		}
	}
	t.Fatalf("the acknowledgement lapsing in 10 days is missing from the export's upcoming panel: %+v", snap.Panels.Upcoming)
}
