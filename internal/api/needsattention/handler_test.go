package needsattention

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

const tenant2134 = "00000000-0000-4000-8000-000000002134"

func listAs(t *testing.T, cred *credstore.Credential, query string) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/needs-attention"+query, nil)
	if cred != nil {
		r = r.WithContext(authctx.WithCredential(r.Context(), *cred))
	}
	w := httptest.NewRecorder()
	New(nil, nil, nil).List(w, r)
	return w.Code
}

// The checks List makes before reading anything: who may ask for which
// view, and the query's shape.
func TestList_Refusals(t *testing.T) {
	t.Parallel()
	viewer := credstore.Credential{ID: "jwt:v", TenantID: tenant2134, UserID: "u1", TenantRoles: []string{"viewer"}}
	owner := credstore.Credential{ID: "jwt:o", TenantID: tenant2134, UserID: "u2", TenantRoles: []string{"control_owner"}}
	// A JWT role claim is not a live role (#2381): it does not open for=all.
	claim := credstore.Credential{ID: "jwt:c", TenantID: tenant2134, UserID: "u3", OwnerRoles: []string{"admin"}}
	lead := credstore.Credential{ID: "jwt:g", TenantID: tenant2134, UserID: "u4", TenantRoles: []string{"grc_engineer"}}
	admin := credstore.Credential{ID: "jwt:a", TenantID: tenant2134, UserID: "u5", TenantRoles: []string{"admin"}}
	for _, c := range []struct {
		name  string
		cred  *credstore.Credential
		query string
		want  int
	}{
		{"no credential", nil, "", http.StatusUnauthorized},
		{"bad tenant", &credstore.Credential{ID: "jwt:x", TenantID: "nope"}, "", http.StatusInternalServerError},
		{"unknown view", &lead, "?for=team", http.StatusBadRequest},
		{"viewer asks for all", &viewer, "?for=all", http.StatusForbidden},
		{"control owner asks for all", &owner, "?for=all", http.StatusForbidden},
		{"admin claim only asks for all", &claim, "?for=all", http.StatusForbidden},
		{"limit too large", &lead, "?limit=101", http.StatusBadRequest},
		{"negative offset", &lead, "?offset=-1", http.StatusBadRequest},
		// Past the gates, a server with no database says so.
		{"GRC lead, all, no database", &lead, "?for=all", http.StatusServiceUnavailable},
		{"admin, all, no database", &admin, "?for=all", http.StatusServiceUnavailable},
		{"viewer, me, no database", &viewer, "?for=me", http.StatusServiceUnavailable},
	} {
		if got := listAs(t, c.cred, c.query); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
