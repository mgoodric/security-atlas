//go:build integration

// #2183 — 20261021000000_normalize_user_principal_ids rewrites
// "user:<uuid>" principal ids to the bare users(id) in
// notifications.recipient_user_id and auditor_assignments.user_id, leaves
// every other value alone, survives a person holding one grant in both forms,
// and is idempotent.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const normalizeUserPrincipalsUp = "../../migrations/sql/20261021000000_normalize_user_principal_ids.sql"

func TestNormalizeUserPrincipalIDsMigration(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(normalizeUserPrincipalsUp)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	ctx := context.Background()
	tenant := uuid.New()
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM notifications WHERE tenant_id = $1`,
			`DELETE FROM auditor_assignments WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := adminPool.Exec(context.Background(), stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	// One audit period to hang assignments on.
	fwID, versionID, periodID := uuid.New(), uuid.New(), uuid.New()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := adminPool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	mustExec(`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, '#2183', $2, 'test')`,
		fwID, "fw-2183-mig-"+uuid.NewString()[:8])
	mustExec(`INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`,
		versionID, fwID)
	mustExec(`INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end,
			status, created_by, created_at, updated_at)
		VALUES ($1, $2, '#2183 mig', $3, $4, $5, 'open', 'test-seed', now(), now())`,
		periodID, tenant, versionID,
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	alice, bob, carol := uuid.New(), uuid.New(), uuid.New()
	nilPrefixed := "user:" + uuid.Nil.String()

	// notifications: input recipient -> expected recipient after the rewrite.
	notifCases := map[string]string{
		"user:" + strings.ToUpper(alice.String()): alice.String(), // case folded
		"user:" + bob.String():                    bob.String(),
		carol.String():                            carol.String(), // already bare
		"key_2183":                                "key_2183",
		"bootstrap-admin":                         "bootstrap-admin",
		"oauth_client:ci-runner":                  "oauth_client:ci-runner",
		"user:not-a-uuid":                         "user:not-a-uuid",
		nilPrefixed:                               nilPrefixed,
	}
	notifIDs := map[string]uuid.UUID{}
	for in := range notifCases {
		id := uuid.New()
		notifIDs[in] = id
		mustExec(`INSERT INTO notifications (id, tenant_id, recipient_user_id, type) VALUES ($1, $2, $3, 'test')`,
			id, tenant, in)
	}

	// auditor_assignments: Alice prefixed only; Bob in both forms on the same
	// period (the PK-collision case); an email id from the e2e fixture shape.
	for _, uid := range []string{
		"user:" + alice.String(),
		"user:" + bob.String(), bob.String(),
		"demo-operator@example.invalid",
		"key_2183",
	} {
		mustExec(`INSERT INTO auditor_assignments (tenant_id, user_id, audit_period_id, granted_by) VALUES ($1, $2, $3, 'test')`,
			tenant, uid, periodID)
	}

	// Apply twice: the second run must be a no-op, not an error.
	for i := 0; i < 2; i++ {
		if _, err := adminPool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("apply migration (run %d): %v", i+1, err)
		}
	}

	for in, want := range notifCases {
		var got string
		if err := adminPool.QueryRow(ctx, `SELECT recipient_user_id FROM notifications WHERE id = $1`, notifIDs[in]).Scan(&got); err != nil {
			t.Fatalf("read notification %q: %v", in, err)
		}
		if got != want {
			t.Errorf("recipient %q -> %q; want %q", in, got, want)
		}
	}

	rows, err := adminPool.Query(ctx,
		`SELECT user_id FROM auditor_assignments WHERE tenant_id = $1 ORDER BY user_id`, tenant)
	if err != nil {
		t.Fatalf("read assignments: %v", err)
	}
	got := map[string]int{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			t.Fatalf("scan assignment: %v", err)
		}
		got[uid]++
	}
	rows.Close()
	want := map[string]int{
		alice.String():                  1,
		bob.String():                    1, // the two forms collapsed to one grant
		"demo-operator@example.invalid": 1,
		"key_2183":                      1,
	}
	if len(got) != len(want) {
		t.Fatalf("assignments = %v; want %v", got, want)
	}
	for uid, n := range want {
		if got[uid] != n {
			t.Errorf("assignments[%q] = %d; want %d (all: %v)", uid, got[uid], n, got)
		}
	}
}
