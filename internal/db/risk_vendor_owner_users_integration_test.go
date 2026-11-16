//go:build integration

// #2129 — 20261226000000_risk_vendor_owner_users.sql links free-text risk and
// vendor owners to users. Asserts against a live Postgres:
//
//  1. Text equal to one active user's name or email, ignoring case and
//     surrounding spaces, links to that user.
//  2. Text naming two users (a shared name) stays unlinked, as does text no
//     user matches, text naming a disabled user, and text naming a user of
//     another tenant only.
//  3. Nothing is lost: the up migration leaves every free-text owner as it
//     was, and the down migration restores each record's text exactly.
//  4. The me_audit_log CHECK keeps every action of 20261120000000 and adds
//     'vendor_owner_update'; the down migration refuses while such a row
//     exists.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ownerUsersPrior = "../../migrations/sql/20261120000000_llm_settings_audit.sql"
	ownerUsersUp    = "../../migrations/sql/20261226000000_risk_vendor_owner_users.sql"
	ownerUsersDown  = "../../migrations/sql/20261226000000_risk_vendor_owner_users.down.sql"
)

type ownerCase struct {
	name string
	text string
	// want is the key of the user the text links to, "" for unlinked.
	want string
}

// ownerCases are run against risks and vendors alike. Users (tenant A):
// alice "Alice Example" <alice@example.test>, two users named "Sam Same",
// disabled "Dee Disabled". Tenant B has "Bea Other" <bea@example.test>.
var ownerCases = []ownerCase{
	{"exact name", "Alice Example", "alice"},
	{"exact email", "alice@example.test", "alice"},
	{"name, case and spaces differ", "  aLiCe EXAMPLE ", "alice"},
	{"email, case differs", "ALICE@Example.TEST", "alice"},
	{"two users share the name", "Sam Same", ""},
	{"unknown text", "the platform team", ""},
	{"disabled user", "Dee Disabled", ""},
	{"another tenant's user", "Bea Other", ""},
	{"another tenant's email", "bea@example.test", ""},
	{"empty", "", ""},
}

func seedMigrationUser(ctx context.Context, t *testing.T, tx pgx.Tx, tenant, email, name, status string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, $5)`, id, tenant, email, name, status); err != nil {
		t.Fatalf("INSERT users: %v", err)
	}
	return id
}

func TestRiskVendorOwnerUsersMigration_LinksUnambiguousOwnersAndLosesNothing(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(ownerUsersUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(ownerUsersDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Back to the pre-#2129 schema: owners are free text only. Rows other
	// tests wrote with the new action are cleared first (rolled back with
	// the transaction), or the down migration refuses.
	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE action = 'vendor_owner_update'`); err != nil {
		t.Fatalf("clear vendor_owner_update rows: %v", err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}

	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	for _, tn := range []string{tenantA, tenantB} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
			tn, "owners-2129-"+tn[:8]); err != nil {
			t.Fatalf("INSERT tenants: %v", err)
		}
	}
	users := map[string]string{
		"alice": seedMigrationUser(ctx, t, tx, tenantA, "alice@example.test", "Alice Example", "active"),
	}
	seedMigrationUser(ctx, t, tx, tenantA, "sam1@example.test", "Sam Same", "active")
	seedMigrationUser(ctx, t, tx, tenantA, "sam2@example.test", "Sam Same", "active")
	seedMigrationUser(ctx, t, tx, tenantA, "dee@example.test", "Dee Disabled", "disabled")
	seedMigrationUser(ctx, t, tx, tenantB, "bea@example.test", "Bea Other", "active")

	riskIDs := map[string]string{}
	vendorIDs := map[string]string{}
	for i, c := range ownerCases {
		rid, vid := uuid.NewString(), uuid.NewString()
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment, treatment_owner)
			VALUES ($1, $2, 'owners 2129', risk_category_id($2, 'operational'), 'avoid', $3)`,
			rid, tenantA, c.text); err != nil {
			t.Fatalf("INSERT risks (%s): %v", c.name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO vendors (id, tenant_id, name, owner_user)
			VALUES ($1, $2, $3, $4)`,
			vid, tenantA, "owners 2129 vendor "+string(rune('a'+i)), c.text); err != nil {
			t.Fatalf("INSERT vendors (%s): %v", c.name, err)
		}
		riskIDs[c.name], vendorIDs[c.name] = rid, vid
	}

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}

	check := func(kind, query, id string, c ownerCase) {
		t.Helper()
		var owner *string
		var text string
		if err := tx.QueryRow(ctx, query, id).Scan(&owner, &text); err != nil {
			t.Fatalf("%s %s: read: %v", kind, c.name, err)
		}
		want := users[c.want]
		switch {
		case c.want == "" && owner != nil:
			t.Errorf("%s %s: linked to %s, want unlinked", kind, c.name, *owner)
		case c.want != "" && (owner == nil || *owner != want):
			t.Errorf("%s %s: owner %v, want %s", kind, c.name, owner, want)
		}
		if text != c.text {
			t.Errorf("%s %s: free text %q changed, want %q kept", kind, c.name, text, c.text)
		}
	}
	for _, c := range ownerCases {
		check("risk", `SELECT owner_user_id::text, treatment_owner FROM risks WHERE id = $1`, riskIDs[c.name], c)
		check("vendor", `SELECT owner_user_id::text, owner_user FROM vendors WHERE id = $1`, vendorIDs[c.name], c)
	}

	// The composite FK refuses another tenant's user, and deleting a user
	// nulls only owner_user_id (savepoints, rolled back).
	fk, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	var bea string
	if err := fk.QueryRow(ctx, `SELECT id::text FROM users WHERE tenant_id = $1`, tenantB).Scan(&bea); err != nil {
		t.Fatalf("read tenant B user: %v", err)
	}
	for _, table := range []string{"risks", "vendors"} {
		id := riskIDs["unknown text"]
		if table == "vendors" {
			id = vendorIDs["unknown text"]
		}
		inner, err := fk.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := inner.Exec(ctx, `UPDATE `+table+` SET owner_user_id = $1 WHERE id = $2`, bea, id); err == nil {
			t.Errorf("%s: another tenant's user was accepted as owner", table)
		}
		_ = inner.Rollback(ctx)
	}
	if _, err := fk.Exec(ctx, `DELETE FROM users WHERE id = $1`, users["alice"]); err != nil {
		t.Fatalf("delete owner user: %v", err)
	}
	var owner *string
	var tenantOf string
	if err := fk.QueryRow(ctx, `SELECT owner_user_id::text, tenant_id::text FROM risks WHERE id = $1`,
		riskIDs["exact name"]).Scan(&owner, &tenantOf); err != nil {
		t.Fatalf("read risk after user delete: %v", err)
	}
	if owner != nil || tenantOf != tenantA {
		t.Errorf("after deleting the owner: owner %v, tenant %s; want NULL owner, tenant kept", owner, tenantOf)
	}
	_ = fk.Rollback(ctx)

	// The CHECK takes the new action and keeps every earlier one.
	for _, a := range append(meAuditActions(t, ownerUsersPrior), "vendor_owner_update") {
		if !insertsAction(ctx, t, tx, a) {
			t.Errorf("me_audit_log refuses action %q after 20261226000000", a)
		}
	}
	if got, want := len(meAuditActions(t, ownerUsersUp)), len(meAuditActions(t, ownerUsersPrior))+1; got != want {
		t.Errorf("20261226000000 lists %d actions, want %d", got, want)
	}

	// The down migration refuses while a vendor_owner_update row exists.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := sp.Exec(ctx, `
		INSERT INTO me_audit_log (tenant_id, user_id, action) VALUES ($1, $2, 'vendor_owner_update')`,
		tenantA, users["alice"]); err != nil {
		t.Fatalf("insert vendor_owner_update row: %v", err)
	}
	if _, err := sp.Exec(ctx, string(down)); err == nil {
		t.Error("down migration applied while a vendor_owner_update row exists")
	}
	_ = sp.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM me_audit_log WHERE action = 'vendor_owner_update'`); err != nil {
		t.Fatalf("clear vendor_owner_update rows: %v", err)
	}

	// A risk whose owner was picked after the migration has no text; the
	// down migration names the owner in it rather than losing them.
	picked := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO risks (id, tenant_id, title, category_id, treatment, owner_user_id)
		VALUES ($1, $2, 'owners 2129 picked', risk_category_id($2, 'operational'), 'avoid', $3)`,
		picked, tenantA, users["alice"]); err != nil {
		t.Fatalf("INSERT picked risk: %v", err)
	}

	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	for _, c := range ownerCases {
		var text string
		if err := tx.QueryRow(ctx, `SELECT treatment_owner FROM risks WHERE id = $1`, riskIDs[c.name]).Scan(&text); err != nil {
			t.Fatalf("read risk after down: %v", err)
		}
		if text != c.text {
			t.Errorf("risk %s after down: %q, want %q", c.name, text, c.text)
		}
		if err := tx.QueryRow(ctx, `SELECT owner_user FROM vendors WHERE id = $1`, vendorIDs[c.name]).Scan(&text); err != nil {
			t.Fatalf("read vendor after down: %v", err)
		}
		if text != c.text {
			t.Errorf("vendor %s after down: %q, want %q", c.name, text, c.text)
		}
	}
	var pickedText string
	if err := tx.QueryRow(ctx, `SELECT treatment_owner FROM risks WHERE id = $1`, picked).Scan(&pickedText); err != nil {
		t.Fatalf("read picked risk after down: %v", err)
	}
	if pickedText != "Alice Example" {
		t.Errorf("picked risk after down: %q, want the owner's name", pickedText)
	}
	if insertsAction(ctx, t, tx, "vendor_owner_update") {
		t.Error("after the down migration me_audit_log still accepts vendor_owner_update")
	}

	// Up again re-applies cleanly.
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-apply up migration: %v", err)
	}
}
