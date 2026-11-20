//go:build integration

// #2588 — 20261272000000_policy_audience_generic_to_everyone maps the
// generic free-text audiences ('employee', 'all', 'all_staff',
// 'contractor') to 'everyone', leaves every other value alone, writes one
// 'audience_mapped' policy_audit_log row per changed policy with the before
// and after arrays, and is a no-op on a second run.
//
// The whole test runs in one transaction that is rolled back, so the
// migration's rewrite of any other test's rows never commits. Lives in
// internal/db/ so the local `just test-integration` package set runs it as
// well as CI.

package db_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
)

const policyAudienceGenericUp = "../../migrations/sql/20261272000000_policy_audience_generic_to_everyone.sql"

func TestPolicyAudienceGenericToEveryoneMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(policyAudienceGenericUp)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	ctx := context.Background()
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenant := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
		tenant, "aud-2588-"+tenant.String()[:8]); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	type tc struct {
		name   string
		status string
		in     []string
		want   []string
		mapped bool
	}
	cases := []tc{
		{"employee and contractor collapse", "published", []string{"employee", "contractor"}, []string{"everyone"}, true},
		{"team name kept, all mapped in place", "draft", []string{"engineer", "all"}, []string{"engineer", "everyone"}, true},
		{"existing everyone not repeated", "superseded", []string{"all_staff", "everyone", "viewer"}, []string{"everyone", "viewer"}, true},
		{"team names only", "published", []string{"engineer", "sre"}, []string{"engineer", "sre"}, false},
		{"untargeted", "draft", []string{}, []string{}, false},
		{"match is exact", "published", []string{"Employee", "all staff"}, []string{"Employee", "all staff"}, false},
		{"enum only", "published", []string{"admin", "everyone"}, []string{"admin", "everyone"}, false},
	}
	ids := make([]uuid.UUID, len(cases))
	for i, c := range cases {
		ids[i] = uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO policies
				(id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by,
				 acknowledgment_required_roles, effective_date)
			VALUES ($1, $2, $3, '1.0.0', 'Body.', $4, 'grc_engineer', 'ciso', 'key_seed', $5,
				CASE WHEN $4::text IN ('published', 'superseded') THEN current_date END)`,
			ids[i], tenant, c.name, c.status, c.in); err != nil {
			t.Fatalf("seed %q: %v", c.name, err)
		}
	}

	// Apply twice: the second run must change nothing and audit nothing.
	for run := 1; run <= 2; run++ {
		if _, err := tx.Exec(ctx, string(up)); err != nil {
			t.Fatalf("apply migration (run %d): %v", run, err)
		}
	}

	for i, c := range cases {
		var got []string
		if err := tx.QueryRow(ctx,
			`SELECT acknowledgment_required_roles FROM policies WHERE id = $1`, ids[i]).Scan(&got); err != nil {
			t.Fatalf("read %q: %v", c.name, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: audience %v -> %v; want %v", c.name, c.in, got, c.want)
		}

		rows, err := tx.Query(ctx, `
			SELECT action, actor, policy_title, policy_version, previous_audience, new_audience
			FROM policy_audit_log WHERE tenant_id = $1 AND policy_id = $2`, tenant, ids[i])
		if err != nil {
			t.Fatalf("read audit %q: %v", c.name, err)
		}
		n := 0
		for rows.Next() {
			n++
			var action, actor string
			var title, version *string
			var before, after []string
			if err := rows.Scan(&action, &actor, &title, &version, &before, &after); err != nil {
				t.Fatalf("scan audit %q: %v", c.name, err)
			}
			if action != "audience_mapped" || actor != "system:migration-2588" {
				t.Errorf("%s: audit action/actor = %q/%q", c.name, action, actor)
			}
			if title == nil || *title != c.name || version == nil || *version != "1.0.0" {
				t.Errorf("%s: audit title/version = %v/%v", c.name, title, version)
			}
			if !slices.Equal(before, c.in) || !slices.Equal(after, c.want) {
				t.Errorf("%s: audit before/after = %v/%v; want %v/%v", c.name, before, after, c.in, c.want)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("audit rows %q: %v", c.name, err)
		}
		wantRows := 0
		if c.mapped {
			wantRows = 1
		}
		if n != wantRows {
			t.Errorf("%s: %d audit row(s); want %d", c.name, n, wantRows)
		}
	}
}
