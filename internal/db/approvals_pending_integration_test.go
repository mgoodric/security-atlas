//go:build integration

// #2457 — the four filtered reads behind GET /v1/approvals and its sidebar
// badge. Asserts against a live Postgres, as the non-owner app role:
//
//  1. Each query returns only the rows in the state its approve move
//     leaves from (requested exception, open risk with a proposed
//     acceptance, policy under review, framework scope in review).
//  2. Under RLS, tenant B's identical rows never appear in tenant A's
//     answer, and the other way round.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// pendingFixture is one tenant's seeded rows: the id of each pending row,
// by type.
type pendingFixture struct {
	tenant         string
	exception      string
	riskAcceptance string
	policy         string
	frameworkScope string
}

// seedPendingFixture inserts, for one tenant, a pending row of each type
// next to rows of the same type in every state the query must skip.
func seedPendingFixture(t *testing.T) pendingFixture {
	t.Helper()
	f := pendingFixture{
		tenant:         uuid.NewString(),
		exception:      uuid.NewString(),
		riskAcceptance: uuid.NewString(),
		policy:         uuid.NewString(),
		frameworkScope: uuid.NewString(),
	}
	controlID := uuid.NewString()
	frameworkID := uuid.NewString()
	versionID := uuid.NewString()

	t.Cleanup(func() {
		withTenantTx(t, f.tenant, true, func(ctx context.Context, tx pgx.Tx) {
			for _, stmt := range []string{
				`DELETE FROM exceptions WHERE tenant_id = $1`,
				`DELETE FROM framework_scopes WHERE tenant_id = $1`,
				`DELETE FROM policies WHERE tenant_id = $1`,
				`DELETE FROM risks WHERE tenant_id = $1`,
				`DELETE FROM controls WHERE tenant_id = $1`,
				`DELETE FROM framework_versions WHERE tenant_id = $1`,
				`DELETE FROM frameworks WHERE tenant_id = $1`,
			} {
				if _, err := tx.Exec(ctx, stmt, f.tenant); err != nil {
					t.Logf("cleanup %s: %v", stmt, err)
				}
			}
		})
	})

	withTenantTx(t, f.tenant, true, func(ctx context.Context, tx pgx.Tx) {
		exec := func(what, sql string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				t.Fatalf("INSERT %s: %v", what, err)
			}
		}
		mustInsertControl(ctx, t, tx, f.tenant, controlID, "IAC-01")
		exec("frameworks", `
			INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			VALUES ($1, $2, 'Approvals 2457', 'approvals-2457', 'Acme')
		`, frameworkID, f.tenant)
		exec("framework_versions", `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version)
			VALUES ($1, $2, $3, '2026.1')
		`, versionID, f.tenant, frameworkID)

		const exceptionSQL = `
			INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification,
				requested_by, expires_at, status, approved_by, approved_at)
			VALUES ($1, $2, $3, '{}'::jsonb, 'awaiting a vendor fix', 'requester',
				now() + interval '30 days', $4, $5, $6)`
		exec("exceptions (requested)", exceptionSQL, f.exception, f.tenant, controlID, "requested", nil, nil)
		exec("exceptions (approved)", exceptionSQL, uuid.NewString(), f.tenant, controlID, "approved", "approver", time.Now())

		exec("risks (proposed acceptance)", `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment, acceptance_status)
			VALUES ($1, $2, 'proposed', risk_category_id($2, 'operational'), 'accept', 'proposed')
		`, f.riskAcceptance, f.tenant)
		exec("risks (approved acceptance)", `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment, acceptance_status,
				accepted_until, accepter)
			VALUES ($1, $2, 'approved', risk_category_id($2, 'operational'), 'accept', 'approved',
				now() + interval '90 days', 'approver')
		`, uuid.NewString(), f.tenant)
		exec("risks (closed, proposed acceptance)", `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment, acceptance_status,
				status, closure_reason, closed_at, closed_by)
			VALUES ($1, $2, 'closed', risk_category_id($2, 'operational'), 'accept', 'proposed',
				'closed', 'not_applicable', now(), 'closer')
		`, uuid.NewString(), f.tenant)
		exec("risks (no acceptance)", `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment)
			VALUES ($1, $2, 'avoided', risk_category_id($2, 'operational'), 'avoid')
		`, uuid.NewString(), f.tenant)

		const policySQL = `
			INSERT INTO policies (id, tenant_id, title, version, body_md, owner_role, approver_role, status, created_by)
			VALUES ($1, $2, $3, '1.0.0', '# Policy', 'tenant_admin', 'security_lead', $3, 'author')`
		exec("policies (under_review)", policySQL, f.policy, f.tenant, "under_review")
		exec("policies (draft)", policySQL, uuid.NewString(), f.tenant, "draft")

		const scopeSQL = `
			INSERT INTO framework_scopes (id, tenant_id, framework_version_id, name, state, predicate, predicate_hash)
			VALUES ($1, $2, $3, $4, $4, '{"op":"true"}'::jsonb, encode(sha256('{"op":"true"}'::bytea), 'hex'))`
		exec("framework_scopes (review)", scopeSQL, f.frameworkScope, f.tenant, versionID, "review")
		exec("framework_scopes (draft)", scopeSQL, uuid.NewString(), f.tenant, versionID, "draft")
	})
	return f
}

// pendingIDs runs the four queries under tenant and returns each one's ids.
func pendingIDs(t *testing.T, tenant string) map[string][]string {
	t.Helper()
	got := map[string][]string{}
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		q := dbx.New(tx)
		tid := pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true}
		exceptions, err := q.ListRequestedExceptions(ctx, tid)
		if err != nil {
			t.Fatalf("ListRequestedExceptions: %v", err)
		}
		for _, r := range exceptions {
			got["exception"] = append(got["exception"], uuid.UUID(r.ID.Bytes).String())
		}
		risks, err := q.ListRisksWithProposedAcceptance(ctx, tid)
		if err != nil {
			t.Fatalf("ListRisksWithProposedAcceptance: %v", err)
		}
		for _, r := range risks {
			got["risk_acceptance"] = append(got["risk_acceptance"], uuid.UUID(r.ID.Bytes).String())
		}
		policies, err := q.ListPoliciesUnderReview(ctx, tid)
		if err != nil {
			t.Fatalf("ListPoliciesUnderReview: %v", err)
		}
		for _, r := range policies {
			got["policy"] = append(got["policy"], uuid.UUID(r.ID.Bytes).String())
		}
		scopes, err := q.ListFrameworkScopesInReview(ctx, tid)
		if err != nil {
			t.Fatalf("ListFrameworkScopesInReview: %v", err)
		}
		for _, r := range scopes {
			got["framework_scope"] = append(got["framework_scope"], uuid.UUID(r.ID.Bytes).String())
		}
	})
	return got
}

func TestApprovalsPendingQueries_FilterByStateAndTenant(t *testing.T) {
	a := seedPendingFixture(t)
	b := seedPendingFixture(t)

	for _, tc := range []struct {
		name string
		f    pendingFixture
	}{{"tenant A", a}, {"tenant B", b}} {
		got := pendingIDs(t, tc.f.tenant)
		want := map[string]string{
			"exception":       tc.f.exception,
			"risk_acceptance": tc.f.riskAcceptance,
			"policy":          tc.f.policy,
			"framework_scope": tc.f.frameworkScope,
		}
		for typ, id := range want {
			if len(got[typ]) != 1 || got[typ][0] != id {
				t.Errorf("%s %s: got %v, want only its pending row %s", tc.name, typ, got[typ], id)
			}
		}
	}
}
