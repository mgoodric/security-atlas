package authz_test

// #3030 (decided on #3021): a viewer reads mapping overlays, the
// organisation's crosswalk edges onto the controls it already reads, and
// writes none of them. viewer.rego admits the `mapping-overlays` resource
// for reads only; every write route stays denied by is_read.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestViewerOverlays3030_ReadsButNeverWrites(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000003030"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c3030"
	routes := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/v1/mapping-overlays", true},
		{http.MethodGet, "/v1/mapping-overlays/export", true},
		{http.MethodGet, "/v1/mapping-overlays/audited-under", true},
		{http.MethodGet, "/v1/mapping-overlays/audited-under/export", true},
		{http.MethodGet, "/v1/mapping-overlays/remap-proposals", true},
		{http.MethodPost, "/v1/mapping-overlays", false},
		{http.MethodPost, "/v1/mapping-overlays/" + id + "/retire", false},
		{http.MethodPost, "/v1/mapping-overlays/import", false},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", false},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/reject", false},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/keep-legacy", false},
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:3030", TenantID: tenant, UserID: "user-3030"}))
		in := authz.BuildInput(r, nil)
		if in.Resource.Type != "mapping-overlays" {
			t.Fatalf("%s %s derives resource %q, want mapping-overlays", rt.method, rt.path, in.Resource.Type)
		}
		if isRead := in.Action == "read"; isRead != rt.want {
			t.Fatalf("%s %s derives action %q; the table expects read=%v", rt.method, rt.path, in.Action, rt.want)
		}
		in.User.Roles = []authz.Role{authz.RoleViewer}
		in.User.Attrs = map[string]any{"is_super_admin": false}
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("viewer %s %s: Decide: %v", rt.method, rt.path, err)
		}
		if d.Allow != rt.want {
			t.Errorf("viewer %s %s: allow=%v, want %v (reason=%s)", rt.method, rt.path, d.Allow, rt.want, d.Reason)
		}
	}
}
