package adminauditlog

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/authz"
)

type viewerRoles struct{}

func (viewerRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return []authz.Role{authz.RoleViewer}, nil
}

func entry(kind unifiedlog.Kind, actor string) unifiedlog.Entry {
	return unifiedlog.Entry{Kind: kind, ActorID: actor, PayloadJSON: json.RawMessage(`{"k":1}`)}
}

// TestPayloadGate_Unit covers the #2375 gate without a database: an
// admin credential keeps everything, a nil engine keeps nothing but the
// caller's own me rows, an unmapped kind fails closed, and a real
// decision follows the role's policy.
func TestPayloadGate_Unit(t *testing.T) {
	t.Parallel()
	const tenant = "11111111-1111-1111-1111-111111111111"
	const self = "22222222-2222-2222-2222-222222222222"
	engine, err := authz.NewEngine(context.Background(), viewerRoles{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name   string
		cred   credstore.Credential
		engine *authz.Engine
		e      unifiedlog.Entry
		want   bool
	}{
		{"admin flag keeps any kind", credstore.Credential{TenantID: tenant, IsAdmin: true}, nil, entry(unifiedlog.KindFeatureFlag, "x"), true},
		{"admin flag keeps another user's me row", credstore.Credential{TenantID: tenant, IsAdmin: true}, nil, entry(unifiedlog.KindMe, "other"), true},
		{"nil engine withholds", credstore.Credential{TenantID: tenant, UserID: self}, nil, entry(unifiedlog.KindControlImport, "x"), false},
		{"own me row kept", credstore.Credential{TenantID: tenant, UserID: "user:" + self}, nil, entry(unifiedlog.KindMe, self), true},
		{"another user's me row withheld", credstore.Credential{TenantID: tenant, UserID: self}, engine, entry(unifiedlog.KindMe, "other"), false},
		{"unmapped kind fails closed", credstore.Credential{TenantID: tenant, UserID: self, OwnerRoles: []string{"viewer"}}, engine, entry("future_kind", "x"), false},
		{"viewer reads risks: taxonomy kept", credstore.Credential{TenantID: tenant, UserID: self, OwnerRoles: []string{"viewer"}}, engine, entry(unifiedlog.KindTaxonomy, "x"), true},
		{"viewer cannot read overlays: withheld", credstore.Credential{TenantID: tenant, UserID: self, OwnerRoles: []string{"viewer"}}, engine, entry(unifiedlog.KindMappingOverlay, "x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/v1/activity/unified", nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), tc.cred))
			g := (&Handler{engine: tc.engine}).newPayloadGate(r, tc.cred.UserID)
			entries := []unifiedlog.Entry{tc.e}
			g.apply(entries)
			if got := entries[0].PayloadJSON != nil; got != tc.want {
				t.Fatalf("payload kept = %v; want %v", got, tc.want)
			}
		})
	}
}
