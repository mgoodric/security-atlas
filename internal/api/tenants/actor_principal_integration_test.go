//go:build integration

// #2329: every tenant settings audit row names the acting credential in
// actor_principal, so a change made with a key that is not a user's (user_id
// uuid.Nil) still says which credential made it.

package tenants_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestPatchTenant_AuditRowNamesActorPrincipal(t *testing.T) {
	tenantID := seedTenant(t, "Actor Principal Co")
	path := "/v1/tenants/" + tenantID.String()

	// An unbound key: its UserID is its own credential id, so it is not a
	// user and user_id is uuid.Nil.
	machine := newRouter(t, tenantID, "key_test_tenants", true)
	if res, body := patch(t, machine, path, map[string]any{"allow_self_approval": true}); res.StatusCode != http.StatusOK {
		t.Fatalf("machine key: status %d body=%v", res.StatusCode, body)
	}
	// A user's key: user_id and actor_principal are both the user.
	user := uuid.New()
	if res, body := patch(t, newRouter(t, tenantID, user.String(), true), path, map[string]any{"allow_self_approval": false}); res.StatusCode != http.StatusOK {
		t.Fatalf("user key: status %d body=%v", res.StatusCode, body)
	}

	rows, err := adminPool.Query(context.Background(),
		`SELECT user_id, actor_principal FROM me_audit_log
		  WHERE tenant_id = $1 AND action = 'tenant_self_approval_update'
		  ORDER BY occurred_at, id`, tenantID)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer rows.Close()
	want := []struct {
		user      uuid.UUID
		principal string
	}{
		{uuid.Nil, "key_test_tenants"},
		{user, user.String()},
	}
	i := 0
	for rows.Next() {
		var (
			u uuid.UUID
			p *string
		)
		if err := rows.Scan(&u, &p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if i >= len(want) {
			t.Fatalf("more than %d audit rows", len(want))
		}
		if u != want[i].user || p == nil || *p != want[i].principal {
			t.Errorf("row %d: user_id %s actor_principal %v, want %s / %q", i, u, p, want[i].user, want[i].principal)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("audit rows = %d, want %d", i, len(want))
	}
}
