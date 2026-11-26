//go:build integration

// #2129 — a vendor's owner is a user. POST and PATCH /v1/vendors set
// owner_user_id; GET returns it, and the free text no user matched as
// owner_unlinked. An owner that is not an active user of the vendor's own
// tenant is a 422, and every owner change writes one vendor_audit_log
// 'owner_changed' row naming the caller's principal (#2450), and no
// me_audit_log row.
//
// Run with:
//
//	go test -tags=integration ./internal/api/vendors/ -run Owner

package vendors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func seedVendorOwner(t *testing.T, admin *pgxpool.Pool, tenant, name, status string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, id+"@owner-2129.test", name, status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func freshOwnerTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "vendor_scope_cells", "vendors", "vendor_audit_log", "me_audit_log", "users")
}

type vendorOwnerWire struct {
	ID            string  `json:"id"`
	OwnerUserID   *string `json:"owner_user_id"`
	OwnerUnlinked string  `json:"owner_unlinked"`
	OwnerUser     string  `json:"owner_user"`
}

func decodeVendor(t *testing.T, body []byte) vendorOwnerWire {
	t.Helper()
	var out struct {
		Vendor vendorOwnerWire `json:"vendor"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode vendor: %v: %s", err, body)
	}
	return out.Vendor
}

// ownerAuditRows returns the before and after of each owner_changed row of
// tenant, oldest first. It fails the test if a row names no actor or the
// nil uuid, or if any owner change still went to me_audit_log.
func ownerAuditRows(t *testing.T, admin *pgxpool.Pool, tenant string) [][2]map[string]any {
	t.Helper()
	var meRows int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM me_audit_log
		 WHERE tenant_id = $1 AND action = 'vendor_owner_update'`, tenant).Scan(&meRows); err != nil {
		t.Fatalf("count me_audit_log rows: %v", err)
	}
	if meRows != 0 {
		t.Errorf("%d owner changes went to me_audit_log, want 0", meRows)
	}
	rows, err := admin.Query(context.Background(), `
		SELECT actor, before_state, after_state FROM vendor_audit_log
		 WHERE tenant_id = $1 AND action_type = 'owner_changed'
		 ORDER BY created_at, id`, tenant)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out [][2]map[string]any
	for rows.Next() {
		var actor string
		var b, a []byte
		if err := rows.Scan(&actor, &b, &a); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		if actor == "" || actor == uuid.Nil.String() {
			t.Errorf("audit row actor = %q, want the caller's principal", actor)
		}
		var before, after map[string]any
		_ = json.Unmarshal(b, &before)
		_ = json.Unmarshal(a, &after)
		out = append(out, [2]map[string]any{before, after})
	}
	return out
}

func vendorBody(name, ownerField string) string {
	return `{"name":"` + name + `","criticality":"medium","review_cadence":"annual"` + ownerField + `}`
}

func TestOwner_VendorOwnerUserSetAndReturned(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshOwnerTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)
	alice := seedVendorOwner(t, admin, tenant, "Alice Owner", "active")
	bob := seedVendorOwner(t, admin, tenant, "Bob Owner", "active")

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer,
		vendorBody("Owned Co", `,"owner_user_id":"`+alice+`","owner_user":"alice"`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}
	v := decodeVendor(t, body)
	if v.OwnerUserID == nil || *v.OwnerUserID != alice || v.OwnerUnlinked != "" || v.OwnerUser != "" {
		t.Fatalf("create returned %+v, want owner %s and no text", v, alice)
	}

	resp, body = doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+v.ID, bearer,
		vendorBody("Owned Co", `,"owner_user_id":"`+bob+`"`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %s", resp.StatusCode, body)
	}
	if got := decodeVendor(t, body); got.OwnerUserID == nil || *got.OwnerUserID != bob {
		t.Fatalf("patch returned %+v, want owner %s", got, bob)
	}
	resp, body = doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+v.ID, bearer, "")
	if got := decodeVendor(t, body); resp.StatusCode != http.StatusOK || got.OwnerUserID == nil || *got.OwnerUserID != bob {
		t.Fatalf("GET reads back %+v (status %d), want owner %s", got, resp.StatusCode, bob)
	}

	// An edit that keeps the owner writes no row.
	resp, body = doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+v.ID, bearer,
		vendorBody("Owned Co renamed", `,"owner_user_id":"`+bob+`"`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status %d: %s", resp.StatusCode, body)
	}

	rows := ownerAuditRows(t, admin, tenant)
	if len(rows) != 2 {
		t.Fatalf("got %d owner_changed rows, want 2 (create, change): %v", len(rows), rows)
	}
	if rows[0][0]["owner_user_id"] != nil || rows[0][1]["owner_user_id"] != alice {
		t.Errorf("create row = %v", rows[0])
	}
	if rows[1][0]["owner_user_id"] != alice || rows[1][1]["owner_user_id"] != bob || rows[1][1]["vendor_id"] != v.ID {
		t.Errorf("change row = %v", rows[1])
	}
}

func TestOwner_VendorUnlinkedTextIsReturnedUntilAUserIsPicked(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshOwnerTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)
	alice := seedVendorOwner(t, admin, tenant, "Alice Owner", "active")

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer,
		vendorBody("Unlinked Co", `,"owner_user":"procurement team"`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}
	v := decodeVendor(t, body)
	if v.OwnerUserID != nil || v.OwnerUnlinked != "procurement team" {
		t.Fatalf("unlinked vendor = %+v", v)
	}
	resp, body = doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+v.ID, bearer,
		vendorBody("Unlinked Co", `,"owner_user":"procurement team","owner_user_id":"`+alice+`"`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %s", resp.StatusCode, body)
	}
	if got := decodeVendor(t, body); got.OwnerUserID == nil || *got.OwnerUserID != alice || got.OwnerUnlinked != "" {
		t.Fatalf("after picking: %+v", got)
	}
}

func TestOwner_VendorOwnerMustBeAnActiveUserOfTheTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshOwnerTenant(t, admin)
	other := freshOwnerTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)
	disabled := seedVendorOwner(t, admin, tenant, "Dee Disabled", "disabled")
	foreign := seedVendorOwner(t, admin, other, "Fay Foreign", "active")

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, vendorBody("Target Co", ""))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}
	id := decodeVendor(t, body).ID

	for name, owner := range map[string]string{
		"disabled user":       disabled,
		"another tenant user": foreign,
		"no such user":        uuid.NewString(),
	} {
		resp, body := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer,
			vendorBody("Refused "+name, `,"owner_user_id":"`+owner+`"`))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("create with %s: status %d, want 422: %s", name, resp.StatusCode, body)
		}
		resp, body = doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+id, bearer,
			vendorBody("Target Co", `,"owner_user_id":"`+owner+`"`))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("patch with %s: status %d, want 422: %s", name, resp.StatusCode, body)
		}
	}
	if resp, _ := doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+id, bearer,
		vendorBody("Target Co", `,"owner_user_id":"not-a-uuid"`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-UUID owner: status %d, want 400", resp.StatusCode)
	}
	if rows := ownerAuditRows(t, admin, tenant); len(rows) != 0 {
		t.Fatalf("refused owners wrote audit rows: %v", rows)
	}
}
