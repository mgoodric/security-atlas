//go:build integration

// #2843 — owners arrive matched by email to an active user of the
// uploading tenant, or as the unlinked free-text owner with the row
// flagged. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// seedUserWithEmail inserts a user of tenant with the given email and status.
func seedUserWithEmail(t *testing.T, admin *pgxpool.Pool, tenant, email, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $3, $4)`, id, tenant, email, status); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// riskOwner reads a created risk's linked owner ("" when none) and its
// free-text owner.
func riskOwner(t *testing.T, e env, id string) (string, string) {
	t.Helper()
	var user, text string
	if err := e.admin.QueryRow(context.Background(), `
		SELECT COALESCE(owner_user_id::text, ''), treatment_owner FROM risks WHERE id = $1`, id).Scan(&user, &text); err != nil {
		t.Fatalf("read owner of risk %s: %v", id, err)
	}
	return user, text
}

func TestRiskImport_Owners(t *testing.T) {
	e := newEnv(t)
	other := dbtest.SeedTenant(t, e.admin, cleanupTables()...)
	sfx := e.tenant[:8]
	alice := seedUserWithEmail(t, e.admin, e.tenant, "alice-"+sfx+"@2843.test", "active")
	seedUserWithEmail(t, e.admin, e.tenant, "dave-"+sfx+"@2843.test", "disabled")
	erin := seedUserWithEmail(t, e.admin, e.tenant, "erin-"+sfx+"@2843.test", "active")
	// The same address in both tenants matches this tenant's user only;
	// an address only another tenant holds matches no one.
	shared := seedUserWithEmail(t, e.admin, e.tenant, "shared-"+sfx+"@2843.test", "active")
	foreignShared := seedUserWithEmail(t, e.admin, other, "shared-"+sfx+"@2843.test", "active")
	foreignOnly := seedUserWithEmail(t, e.admin, other, "carol-"+sfx+"@2843.test", "active")

	csv := fmt.Sprintf("Risk ID,Title,Category,Risk Owner\n"+
		"R-ALICE,Matched owner,operational,  ALICE-%[1]s@2843.test \n"+
		"R-SHARED,Owner in both tenants,operational,shared-%[1]s@2843.test\n"+
		"R-FOREIGN,Owner only in another tenant,operational,carol-%[1]s@2843.test\n"+
		"R-UNKNOWN,Owner nobody has,operational,frank-%[1]s@2843.test\n"+
		"R-DISABLED,Disabled owner,operational,dave-%[1]s@2843.test\n"+
		"R-NAME,Owner by name,operational,Platform team\n"+
		"R-ERIN,Owner disabled after upload,operational,erin-%[1]s@2843.test\n"+
		"R-NONE,No owner,operational,\n", sfx)

	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if b.StagedCount != 8 || b.RejectedCount != 0 || len(b.UnmappedColumns) != 0 {
		t.Fatalf("batch staged=%d rejected=%+v unmapped=%q", b.StagedCount, b.Rejected, b.UnmappedColumns)
	}

	type want struct {
		user, unlinked string
		flagged        bool
	}
	staged := map[string]want{
		"R-ALICE":    {user: alice.String()},
		"R-SHARED":   {user: shared.String()},
		"R-FOREIGN":  {unlinked: "carol-" + sfx + "@2843.test", flagged: true},
		"R-UNKNOWN":  {unlinked: "frank-" + sfx + "@2843.test", flagged: true},
		"R-DISABLED": {unlinked: "dave-" + sfx + "@2843.test", flagged: true},
		"R-NAME":     {unlinked: "Platform team", flagged: true},
		"R-ERIN":     {user: erin.String()},
		"R-NONE":     {},
	}
	// The upload response and a later read of the batch agree.
	status, raw = call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get batch = %d %s", status, raw)
	}
	for _, got := range []riskimport.Batch{b, decode[riskimport.Batch](t, raw)} {
		for ref, w := range staged {
			r := stagedByRef(t, got, ref)
			if (r.OwnerUserID == "") != (r.OwnerEmail == "") {
				t.Errorf("%s owner user=%q but email=%q", ref, r.OwnerUserID, r.OwnerEmail)
			}
			if r.OwnerUserID != w.user || r.OwnerUnlinked != w.unlinked ||
				slices.Contains(r.Flags, riskimport.FlagOwnerUnlinked) != w.flagged {
				t.Errorf("%s staged owner user=%q unlinked=%q flags=%v; want %+v", ref, r.OwnerUserID, r.OwnerUnlinked, r.Flags, w)
			}
			if r.OwnerUserID == foreignShared.String() || r.OwnerUserID == foreignOnly.String() {
				t.Errorf("%s linked to another tenant's user %s", ref, r.OwnerUserID)
			}
		}
	}

	// Erin is disabled between upload and approval: her risk is created
	// with her email as the unlinked owner, not linked to a disabled user.
	if _, err := e.admin.Exec(context.Background(), `UPDATE users SET status = 'disabled' WHERE id = $1`, erin); err != nil {
		t.Fatalf("disable erin: %v", err)
	}
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	if res.ApprovedCount != 8 || len(res.Skipped) != 0 {
		t.Fatalf("approve result = %+v", res)
	}
	created := map[string]want{
		"R-ALICE":    {user: alice.String()},
		"R-SHARED":   {user: shared.String()},
		"R-FOREIGN":  {unlinked: "carol-" + sfx + "@2843.test"},
		"R-UNKNOWN":  {unlinked: "frank-" + sfx + "@2843.test"},
		"R-DISABLED": {unlinked: "dave-" + sfx + "@2843.test"},
		"R-NAME":     {unlinked: "Platform team"},
		"R-ERIN":     {unlinked: "erin-" + sfx + "@2843.test"},
		"R-NONE":     {},
	}
	for _, d := range res.Approved {
		w, ok := created[d.ExternalRef]
		if !ok {
			t.Errorf("unexpected approved row %q", d.ExternalRef)
			continue
		}
		user, text := riskOwner(t, e, d.RiskID)
		if user != w.user || text != w.unlinked {
			t.Errorf("%s risk owner user=%q text=%q; want user=%q text=%q", d.ExternalRef, user, text, w.user, w.unlinked)
		}
	}
}
