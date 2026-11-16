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

// kindSourcePath is a route that reads each kind's subject. The mapped
// resource type must be the one the authz middleware derives from it, so
// the gate decides exactly what the source route would.
var kindSourcePath = map[unifiedlog.Kind]string{
	unifiedlog.KindDecision:        "/v1/admin/audit-log",
	unifiedlog.KindEvidence:        "/v1/evidence",
	unifiedlog.KindException:       "/v1/exceptions",
	unifiedlog.KindSample:          "/v1/audit-periods/00000000-0000-0000-0000-000000000001/samples",
	unifiedlog.KindAuditPeriod:     "/v1/audit-periods",
	unifiedlog.KindAggregationRule: "/v1/aggregation-rules",
	unifiedlog.KindFeatureFlag:     "/v1/admin/features",
	unifiedlog.KindWalkthrough:     "/v1/walkthroughs",
	unifiedlog.KindFrameworkScope:  "/v1/framework-scopes",
	unifiedlog.KindTaxonomy:        "/v1/risks/categories",
	unifiedlog.KindMappingOverlay:  "/v1/mapping-overlays",
	unifiedlog.KindControlImport:   "/v1/controls",
}

type grcRoles struct{}

func (grcRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return []authz.Role{authz.RoleGRCEngineer}, nil
}

// TestKindSourceResource_CoversEveryKind fails when a kind is added to
// unifiedlog.AllKinds without a source resource (#2375 review B1): an
// unmapped kind would show every non-admin reader a null payload. me rows
// are judged by owner, not by a resource. Each mapped type must be the one
// its source route resolves to, and one the policy knows (grc_engineer,
// which reads every tenant resource, is allowed it).
func TestKindSourceResource_CoversEveryKind(t *testing.T) {
	t.Parallel()
	engine, err := authz.NewEngine(context.Background(), grcRoles{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for _, kind := range unifiedlog.AllKinds {
		if kind == unifiedlog.KindMe {
			continue
		}
		resource, ok := kindSourceResource[kind]
		if !ok {
			t.Errorf("kind %q has no kindSourceResource entry; map it to its source route's OPA resource type", kind)
			continue
		}
		path, ok := kindSourcePath[kind]
		if !ok {
			t.Errorf("kind %q has no kindSourcePath entry in this test", kind)
			continue
		}
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{
			ID: "k", TenantID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
		}))
		in := authz.BuildInput(r, nil)
		if in.Resource.Type != resource {
			t.Errorf("kind %q: mapped to %q but its source route %s resolves to %q", kind, resource, path, in.Resource.Type)
		}
		d, err := engine.Decide(r.Context(), in)
		if err != nil || !d.Allow {
			t.Errorf("kind %q: grc_engineer read of %q denied (%v, %s)", kind, resource, err, d.Reason)
		}
	}
	for kind := range kindSourceResource {
		if !unifiedlog.IsCanonical(kind) || kind == unifiedlog.KindMe {
			t.Errorf("kindSourceResource maps %q, which is not a gated unified-log kind", kind)
		}
	}
}
