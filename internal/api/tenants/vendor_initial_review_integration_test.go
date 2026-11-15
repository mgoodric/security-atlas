//go:build integration

// #2201: the vendor initial-review window is settable through
// PATCH /v1/tenants/{id}. Setting it moves the first review date of a vendor
// that has never been reviewed; each change writes a me_audit_log row with
// the before and after values; a value outside 1..365 is a 400.

package tenants_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

func TestPatchTenant_VendorInitialReviewDays_MovesFirstReviewDue(t *testing.T) {
	tenantID := seedTenant(t, "Vendor Window Co")
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM vendors WHERE tenant_id = $1`, tenantID)
	})
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	store := vendor.NewStore(appPool)

	// A vendor added under the default window, never reviewed.
	v, err := store.Create(ctx, vendor.CreateVendorInput{
		Name:          "Unreviewed Vendor",
		Criticality:   vendor.CriticalityLow,
		ReviewCadence: vendor.CadenceAnnual,
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if want := vendor.FirstReviewDueDate(v.CreatedAt, vendor.DefaultInitialReviewDays); v.FirstReviewDue == nil || !v.FirstReviewDue.Equal(want) {
		t.Fatalf("default FirstReviewDue = %v; want %s", v.FirstReviewDue, want.Format(time.DateOnly))
	}

	admin := uuid.New()
	res, body := patch(t, newRouter(t, tenantID, admin.String(), true),
		"/v1/tenants/"+tenantID.String(), map[string]any{"vendor_initial_review_days": 7})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH: status %d body=%v", res.StatusCode, body)
	}
	tenant, _ := body["tenant"].(map[string]any)
	if got, _ := tenant["vendor_initial_review_days"].(float64); got != 7 {
		t.Fatalf("response vendor_initial_review_days = %v, want 7", tenant["vendor_initial_review_days"])
	}

	// The existing never-reviewed vendor now falls due 7 days after creation.
	got, err := store.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("get vendor: %v", err)
	}
	if want := vendor.FirstReviewDueDate(v.CreatedAt, 7); got.FirstReviewDue == nil || !got.FirstReviewDue.Equal(want) {
		t.Fatalf("FirstReviewDue after PATCH = %v; want %s", got.FirstReviewDue, want.Format(time.DateOnly))
	}
	if days, err := store.InitialReviewDays(ctx); err != nil || days != 7 {
		t.Fatalf("InitialReviewDays = %d, %v; want 7", days, err)
	}

	// One audit row, with the before and after values and the actor.
	var (
		user          uuid.UUID
		before, after []byte
	)
	if err := adminPool.QueryRow(context.Background(),
		`SELECT user_id, before, after FROM me_audit_log
		  WHERE tenant_id = $1 AND action = 'tenant_vendor_initial_review_update'`,
		tenantID).Scan(&user, &before, &after); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if user != admin {
		t.Errorf("audit user_id = %s, want %s", user, admin)
	}
	var b, a map[string]int
	_ = json.Unmarshal(before, &b)
	_ = json.Unmarshal(after, &a)
	if b["vendor_initial_review_days"] != 30 || a["vendor_initial_review_days"] != 7 {
		t.Errorf("audit before=%s after=%s; want 30 -> 7", before, after)
	}
}

func TestPatchTenant_VendorInitialReviewDays_OutOfRange(t *testing.T) {
	tenantID := seedTenant(t, "Vendor Window Range Co")
	h := newRouter(t, tenantID, uuid.NewString(), true)
	for _, days := range []int{0, -1, 366} {
		res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"vendor_initial_review_days": days})
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("days=%d: status %d (want 400) body=%v", days, res.StatusCode, body)
		}
	}
	var (
		stored int
		n      int
	)
	if err := adminPool.QueryRow(context.Background(),
		`SELECT vendor_initial_review_days FROM tenants WHERE id = $1`, tenantID).Scan(&stored); err != nil {
		t.Fatalf("read window: %v", err)
	}
	if stored != 30 {
		t.Errorf("window = %d after rejected PATCHes, want 30", stored)
	}
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 0 {
		t.Errorf("%d audit rows after rejected PATCHes, want 0", n)
	}
}

func TestPatchTenant_VendorInitialReviewDays_NonAdminForbidden(t *testing.T) {
	tenantID := seedTenant(t, "Vendor Window Member Co")
	res, body := patch(t, newRouter(t, tenantID, uuid.NewString(), false),
		"/v1/tenants/"+tenantID.String(), map[string]any{"vendor_initial_review_days": 10})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: status %d (want 403) body=%v", res.StatusCode, body)
	}
}
