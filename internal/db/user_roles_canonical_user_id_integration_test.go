//go:build integration

// #2661 — 20261282000000_user_roles_canonical_user_id: a user_roles.user_id
// that reads as a uuid must be the canonical lowercase hyphenated form, so
// the u.id::text = ur.user_id deactivation guard can never miss it. Non-uuid
// principals are unaffected, and the migration's pre-check refuses to apply
// over a violating row.

package db_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	userRolesCanonicalUp   = "../../migrations/sql/20261282000000_user_roles_canonical_user_id.sql"
	userRolesCanonicalDown = "../../migrations/sql/20261282000000_user_roles_canonical_user_id.down.sql"
)

func TestUserRolesCanonicalUserID(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	ctx := context.Background()
	u := uuid.New()
	hex32 := strings.ReplaceAll(u.String(), "-", "")

	cases := []struct {
		name   string
		userID string
		ok     bool
	}{
		{"canonical", u.String(), true},
		{"nil uuid canonical", uuid.Nil.String(), true},
		{"api key", "key_2661", true},
		{"bootstrap admin", "bootstrap-admin", true},
		{"oauth client", "oauth_client:ci-runner", true},
		{"prefixed subject", "user:" + u.String(), true},
		{"email fixture", "demo-operator@example.invalid", true},
		{"not quite a uuid", u.String()[:35], true},
		{"upper case", strings.ToUpper(u.String()), false},
		{"mixed case", "A0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", false},
		{"braced", "{" + u.String() + "}", false},
		{"urn", "urn:uuid:" + u.String(), false},
		{"upper urn", "URN:UUID:" + u.String(), false},
		{"32 hex", hex32, false},
		{"odd hyphens", hex32[:8] + "-" + hex32[8:16] + "-" + hex32[16:24] + "-" + hex32[24:], false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := adminPool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			tenant := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2)`,
				tenant, "urcu-2661-"+tenant.String()[:8]); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO user_roles (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`,
				tenant, tc.userID)
			switch {
			case tc.ok && err != nil:
				t.Fatalf("insert %q refused: %v", tc.userID, err)
			case !tc.ok && err == nil:
				t.Fatalf("insert %q accepted; want the canonical-uuid CHECK to refuse it", tc.userID)
			case !tc.ok && !strings.Contains(err.Error(), "user_roles_user_id_canonical_uuid"):
				t.Fatalf("insert %q refused by the wrong check: %v", tc.userID, err)
			}
		})
	}

	t.Run("pre-check refuses a violating row", func(t *testing.T) {
		up, err := os.ReadFile(userRolesCanonicalUp)
		if err != nil {
			t.Fatal(err)
		}
		down, err := os.ReadFile(userRolesCanonicalDown)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, string(down)); err != nil {
			t.Fatalf("apply down: %v", err)
		}
		tenant := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2)`,
			tenant, "urcu-2661-"+tenant.String()[:8]); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`,
			tenant, strings.ToUpper(u.String())); err != nil {
			t.Fatalf("seed violating row with the constraint dropped: %v", err)
		}
		_, err = tx.Exec(ctx, string(up))
		if err == nil || !strings.Contains(err.Error(), "non-canonical uuid user_id") {
			t.Fatalf("apply up over a violating row: err = %v; want the pre-check to refuse", err)
		}
	})

	// The runbook the pre-check's error names: its SQL fixes the rows, and
	// the migration then applies. The snippet is read from the runbook, so
	// the published text is the tested text. Its BEGIN/COMMIT are dropped so
	// it runs inside this test's rolled-back transaction.
	t.Run("runbook snippet fixes the rows, then the migration applies", func(t *testing.T) {
		up, err := os.ReadFile(userRolesCanonicalUp)
		if err != nil {
			t.Fatal(err)
		}
		down, err := os.ReadFile(userRolesCanonicalDown)
		if err != nil {
			t.Fatal(err)
		}
		snippet := runbookSQL(t, userRolesCanonicalRunbook)

		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		exec := func(sql string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				t.Fatalf("%v\n%s", err, sql)
			}
		}
		exec(string(down))

		tenant := uuid.New()
		exec(`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "urcu-2661-"+tenant.String()[:8])
		alone, twin, dup := uuid.New(), uuid.New(), uuid.New()
		grant := func(userID, role, by string, minutesAgo int) {
			t.Helper()
			exec(`INSERT INTO user_roles (tenant_id, user_id, role, granted_by, granted_at)
			      VALUES ($1, $2, $3, $4, now() - make_interval(mins => $5))`,
				tenant, userID, role, by, minutesAgo)
		}
		// Upper-case only: rewritten.
		grant(strings.ToUpper(alone.String()), "admin", "alone", 5)
		// Canonical plus a braced twin: the canonical row is kept as it was.
		grant(twin.String(), "viewer", "canonical", 5)
		grant("{"+twin.String()+"}", "viewer", "braced", 10)
		// Two spellings, no canonical row: one row, the earliest grant's.
		grant("urn:uuid:"+dup.String(), "auditor", "urn", 20)
		grant(strings.ReplaceAll(dup.String(), "-", ""), "auditor", "hex32", 10)
		// A SCIM source on the urn row moves to the canonical row.
		exec(`INSERT INTO user_role_group_sources (tenant_id, user_id, role, idp_config_id)
		      VALUES ($1, $2, 'auditor', NULL)`, tenant, "urn:uuid:"+dup.String())
		// Not a uuid: untouched.
		grant("key_2661", "viewer", "key", 5)

		exec(snippet)
		exec(string(up))

		rows, err := tx.Query(ctx,
			`SELECT user_id || ' ' || role || ' ' || granted_by FROM user_roles WHERE tenant_id = $1 ORDER BY 1`, tenant)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		want := []string{
			alone.String() + " admin alone",
			dup.String() + " auditor urn",
			twin.String() + " viewer canonical",
			"key_2661 viewer key",
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("user_roles after the snippet:\n got %q\nwant %q", got, want)
		}

		var sources int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM user_role_group_sources WHERE tenant_id = $1 AND user_id = $2 AND role = 'auditor'`,
			tenant, dup.String()).Scan(&sources); err != nil {
			t.Fatal(err)
		}
		if sources != 1 {
			t.Errorf("group sources on the canonical auditor row = %d; want 1", sources)
		}
	})
}

const userRolesCanonicalRunbook = "../../docs/runbooks/user-roles-canonical-user-id.md"

// runbookSQL returns the runbook's one ```sql block without its BEGIN; and
// COMMIT; lines.
func runbookSQL(t *testing.T, path string) string {
	t.Helper()
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(doc), "```sql\n")
	if len(parts) != 2 {
		t.Fatalf("%s: want exactly one ```sql block, found %d", path, len(parts)-1)
	}
	block, _, ok := strings.Cut(parts[1], "\n```")
	if !ok {
		t.Fatalf("%s: unterminated ```sql block", path)
	}
	var kept []string
	for _, line := range strings.Split(block, "\n") {
		if line == "BEGIN;" || line == "COMMIT;" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
