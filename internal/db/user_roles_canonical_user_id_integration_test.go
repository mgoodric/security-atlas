//go:build integration

// #2661 — 20261280000000_user_roles_canonical_user_id: a user_roles.user_id
// that reads as a uuid must be the canonical lowercase hyphenated form, so
// the u.id::text = ur.user_id deactivation guard can never miss it. Non-uuid
// principals are unaffected, and the migration's pre-check refuses to apply
// over a violating row.

package db_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	userRolesCanonicalUp   = "../../migrations/sql/20261280000000_user_roles_canonical_user_id.sql"
	userRolesCanonicalDown = "../../migrations/sql/20261280000000_user_roles_canonical_user_id.down.sql"
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
}
