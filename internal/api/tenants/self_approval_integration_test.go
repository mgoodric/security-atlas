//go:build integration

// #2101: the organisation's Allow self-approval setting. Off by default;
// only an admin changes it; each change writes a me_audit_log row with the
// before and after values.

package tenants_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestPatchTenant_AllowSelfApproval_AdminOnlyAndAudited(t *testing.T) {
	tenantID := seedTenant(t, "Self Approval Co")
	path := "/v1/tenants/" + tenantID.String()

	var allow bool
	if err := adminPool.QueryRow(context.Background(),
		`SELECT allow_self_approval FROM tenants WHERE id = $1`, tenantID).Scan(&allow); err != nil {
		t.Fatalf("read default: %v", err)
	}
	if allow {
		t.Fatal("allow_self_approval defaults to true, want false")
	}

	// A non-admin is refused, and nothing changes or is logged.
	res, body := patch(t, newRouter(t, tenantID, uuid.NewString(), false), path, map[string]any{"allow_self_approval": true})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: status %d (want 403) body=%v", res.StatusCode, body)
	}
	if err := adminPool.QueryRow(context.Background(),
		`SELECT allow_self_approval FROM tenants WHERE id = $1`, tenantID).Scan(&allow); err != nil {
		t.Fatalf("read after refusal: %v", err)
	}
	if allow {
		t.Fatal("a non-admin turned self-approval on")
	}

	// An admin turns it on, then off: one audit row per change.
	admin := uuid.New()
	h := newRouter(t, tenantID, admin.String(), true)
	res, body = patch(t, h, path, map[string]any{"allow_self_approval": true})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin on: status %d body=%v", res.StatusCode, body)
	}
	tenant, _ := body["tenant"].(map[string]any)
	if tenant["allow_self_approval"] != true {
		t.Fatalf("response allow_self_approval = %v, want true", tenant["allow_self_approval"])
	}
	res, body = patch(t, h, path, map[string]any{"allow_self_approval": false})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin off: status %d body=%v", res.StatusCode, body)
	}

	rows, err := adminPool.Query(context.Background(),
		`SELECT user_id, before, after FROM me_audit_log
		  WHERE tenant_id = $1 AND action = 'tenant_self_approval_update'
		  ORDER BY occurred_at, id`, tenantID)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer rows.Close()
	type entry struct {
		user          uuid.UUID
		before, after []byte
	}
	var got []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.user, &e.before, &e.after); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows = %d, want 2 (on, off)", len(got))
	}
	want := []struct{ before, after string }{
		{`"allow_self_approval": false`, `"allow_self_approval": true`},
		{`"allow_self_approval": true`, `"allow_self_approval": false`},
	}
	for i, e := range got {
		if e.user != admin {
			t.Errorf("row %d: user_id %s, want the admin %s", i, e.user, admin)
		}
		if !bytes.Contains(e.before, []byte(want[i].before)) || !bytes.Contains(e.after, []byte(want[i].after)) {
			t.Errorf("row %d: before %s after %s, want %s -> %s", i, e.before, e.after, want[i].before, want[i].after)
		}
	}
}
