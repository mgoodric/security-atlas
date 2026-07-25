// Pure-Go unit tests for the slice-509 admin group-role-mapping surface (no
// Postgres, no build tag). Covers the admin-gate deny branches (missing
// credential → 401, non-admin → 403, AC-8) and the P0-509-4 role-validation +
// bad-input branches of Create that short-circuit before any DB call.
package admingroupmappings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

// mappingWith builds a grouprole.Mapping for the toResponse test.
func mappingWith(id uuid.UUID, cfg *uuid.UUID, group, role string) grouprole.Mapping {
	return grouprole.Mapping{ID: id, IDPConfigID: cfg, GroupRef: group, Role: role}
}

func TestRequireAdmin_DenyBranches(t *testing.T) {
	t.Parallel()

	t.Run("missing credential → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/group-role-mappings", nil)
		if _, ok := requireAdmin(rec, req); ok {
			t.Fatal("requireAdmin should fail with no credential")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", rec.Code)
		}
	})

	t.Run("non-admin → 403 (AC-8)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings", nil)
		ctx := authctx.WithCredential(req.Context(), credstore.Credential{
			TenantID: uuid.New().String(),
			IsAdmin:  false,
		})
		if _, ok := requireAdmin(rec, req.WithContext(ctx)); ok {
			t.Fatal("requireAdmin should fail for non-admin")
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d; want 403", rec.Code)
		}
	})

	t.Run("admin → ok", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/group-role-mappings", nil)
		tid := uuid.New().String()
		ctx := authctx.WithCredential(req.Context(), credstore.Credential{
			TenantID: tid, IsAdmin: true, UserID: "user:" + uuid.New().String(),
		})
		cred, ok := requireAdmin(rec, req.WithContext(ctx))
		if !ok {
			t.Fatal("requireAdmin should succeed for admin")
		}
		if cred.TenantID != tid {
			t.Errorf("tenant = %q; want %q", cred.TenantID, tid)
		}
	})
}

// TestCreate_NonAdminForbidden proves the Create endpoint itself returns 403 to
// a non-admin BEFORE any store call (defense in depth on the privilege-granting
// write — AC-8 / STRIDE-T).
func TestCreate_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	h := New(nil) // store is never reached on the 403 path
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings",
		strings.NewReader(`{"group_ref":"SecTeam","role":"admin"}`))
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		TenantID: uuid.New().String(), IsAdmin: false,
	})
	h.Create(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
}

// TestCreate_RejectsUnknownRole proves a mapping to a non-existent atlas role is
// a 400 BEFORE any store call (P0-509-4 — never auto-create a role).
func TestCreate_RejectsUnknownRole(t *testing.T) {
	t.Parallel()
	h := New(nil) // store must NOT be reached — rejection is pre-DB
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings",
		strings.NewReader(`{"group_ref":"SecTeam","role":"superuser"}`))
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		TenantID: uuid.New().String(), IsAdmin: true, UserID: "user:" + uuid.New().String(),
	})
	h.Create(rec, req.WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown role status = %d; want 400 (P0-509-4)", rec.Code)
	}
}

// TestCreate_RejectsMissingGroupRef proves an empty group_ref is a 400 pre-DB.
func TestCreate_RejectsMissingGroupRef(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings",
		strings.NewReader(`{"role":"viewer"}`))
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		TenantID: uuid.New().String(), IsAdmin: true, UserID: "user:" + uuid.New().String(),
	})
	h.Create(rec, req.WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing group_ref status = %d; want 400", rec.Code)
	}
}

func TestParseUserID(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	cases := []struct {
		in      string
		want    uuid.UUID
		wantErr bool
	}{
		{id.String(), id, false},
		{"user:" + id.String(), id, false},
		{"not-a-uuid", uuid.Nil, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseUserID(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseUserID(%q) = %s,%v; want %s", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestToResponse(t *testing.T) {
	t.Parallel()
	cfg := uuid.New()
	id := uuid.New()
	withCfg := toResponse(mappingWith(id, &cfg, "G", "viewer"))
	if withCfg.IDPConfigID == nil || *withCfg.IDPConfigID != cfg.String() {
		t.Fatalf("idp_config_id not surfaced: %+v", withCfg)
	}
	noCfg := toResponse(mappingWith(id, nil, "G", "viewer"))
	if noCfg.IDPConfigID != nil {
		t.Fatalf("nil idp_config_id should serialize null, got %v", *noCfg.IDPConfigID)
	}
}
