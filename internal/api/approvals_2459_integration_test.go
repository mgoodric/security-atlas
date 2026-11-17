//go:build integration

// #2459 — GET /v1/approvals agrees with the policy approve route when a
// draft editor names no person (an unbound API key, an OAuth client, the
// bootstrap admin). Such an editor is an unknown author: with Allow
// self-approval off, an uninvolved approver neither sees the policy nor
// can approve it; with it on, they see it marked self_approval and the
// approve route accepts it. Production router on real Postgres under RLS.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestApprovals2459_NonPersonEditorMatchesApproveGate(t *testing.T) {
	for _, kind := range []struct{ name, principal string }{
		{"unbound key", "key_" + uuid.NewString()},
		{"OAuth client", "oauth_client:ci-2459"},
		{"bootstrap admin", "bootstrap-admin"},
	} {
		t.Run(kind.name, func(t *testing.T) {
			migrate := dbtest.NewMigratePool(t)
			app := dbtest.NewAppPool(t)
			tenant := seed2119Tenant(t, migrate)
			tid := uuid.MustParse(tenant)

			submitter := seedPeriodUser(t, migrate, tenant, "active", "grc_engineer")
			approver := seedPeriodUser(t, migrate, tenant, "active", "grc_engineer")
			policyID := uuid.New()
			if _, err := migrate.Exec(context.Background(),
				`INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role,
				                       created_by, submitted_by, submitted_at, draft_editors)
				 VALUES ($1, $2, '#2459 policy', '1.0', '# Access', 'under_review', 'grc_engineer', 'admin',
				         $3, $3, now(), ARRAY[$3, $4]::text[])`,
				policyID, tenant, submitter.String(), kind.principal); err != nil {
				t.Fatalf("seed policy: %v", err)
			}

			engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
			if err != nil {
				t.Fatalf("authz.NewEngine: %v", err)
			}
			srv := api.New(api.Config{})
			srv.AttachDB(app)
			srv.AttachAuthz(engine, authz.NewAuditWriter(app))
			c := testjwt.OwnerFor(tid, []string{"grc_engineer"})
			c.Subject = "user:" + approver.String()
			asApprover := periodAuditorsCall{t, "", srv.IssueTestJWT(t, c)}
			ts := httptest.NewServer(srv.HTTPHandlerForTests())
			t.Cleanup(ts.Close)
			asApprover.url = ts.URL
			approvePath := "/v1/policies/" + policyID.String() + "/approve"

			// Setting off: not listed, and the route refuses.
			if got, out := list2119(t, asApprover); len(got) != 0 {
				t.Fatalf("approver sees a policy with an unknown author: %v", out)
			}
			if st, body := asApprover.do(http.MethodPatch, approvePath, nil); st != http.StatusForbidden {
				t.Fatalf("approve with self-approval off: %d %v, want 403", st, body)
			}

			// Setting on: listed and marked, and the route accepts it.
			set2119SelfApproval(t, migrate, tenant, true)
			got, out := list2119(t, asApprover)
			if got["policy"] == nil || got["policy"]["self_approval"] != true {
				t.Fatalf("approver with self-approval on: %v, want the policy marked self_approval", out)
			}
			if st, body := asApprover.do(http.MethodPatch, approvePath, nil); st != http.StatusOK {
				t.Fatalf("approve with self-approval on: %d %v, want 200", st, body)
			}
		})
	}
}
