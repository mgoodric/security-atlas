package approvals

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

const tenant2134 = "00000000-0000-4000-8000-000000002134"

func listAs(t *testing.T, cred *credstore.Credential) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/approvals", nil)
	if cred != nil {
		r = r.WithContext(authctx.WithCredential(r.Context(), *cred))
	}
	w := httptest.NewRecorder()
	New(nil, nil).List(w, r)
	return w
}

// The pre-database paths of List: no credential, a caller who approves
// nothing (an empty list, never a refusal), and an approver on a server
// with no database. Pending (#2134) carries the role gate now, so these pin
// that List still answers as before.
func TestList_PreDatabasePaths(t *testing.T) {
	t.Parallel()
	if w := listAs(t, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no credential: %d, want 401", w.Code)
	}
	viewer := credstore.Credential{ID: "jwt:v", TenantID: tenant2134, UserID: requester, TenantRoles: []string{"viewer"}}
	w := listAs(t, &viewer)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer: %d, want 200", w.Code)
	}
	var out Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 0 || len(out.Items) != 0 || len(out.Counts) != len(Types) {
		t.Errorf("viewer: %+v, want an empty list with a zero count per type", out)
	}
	approver := credstore.Credential{ID: "jwt:a", TenantID: tenant2134, UserID: other, TenantRoles: []string{"grc_engineer"}}
	if w := listAs(t, &approver); w.Code != http.StatusServiceUnavailable {
		t.Errorf("approver, no database: %d, want 503", w.Code)
	}
	bad := credstore.Credential{ID: "jwt:b", TenantID: "not-a-uuid"}
	if w := listAs(t, &bad); w.Code != http.StatusInternalServerError {
		t.Errorf("bad tenant: %d, want 500", w.Code)
	}
}
