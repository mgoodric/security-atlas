// Pure-Go unit tests for the slice-508 admin SCIM-credential surface (no
// Postgres, no build tag). Covers the admin-gate deny branches (missing
// credential → 401, non-admin → 403) and the user-id prefix strip.
package adminscim

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestRequireAdmin_DenyBranches(t *testing.T) {
	t.Parallel()

	t.Run("missing credential → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials", nil)
		if _, ok := requireAdmin(rec, req); ok {
			t.Fatal("requireAdmin should fail with no credential")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", rec.Code)
		}
	})

	t.Run("non-admin → 403", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials", nil)
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
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials", nil)
		tid := uuid.New().String()
		ctx := authctx.WithCredential(req.Context(), credstore.Credential{
			TenantID: tid,
			IsAdmin:  true,
			UserID:   "user:" + uuid.New().String(),
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

func TestParseUserID(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	cases := []struct {
		name    string
		in      string
		want    uuid.UUID
		wantErr bool
	}{
		{"bare uuid", id.String(), id, false},
		{"user: prefix stripped", "user:" + id.String(), id, false},
		{"garbage", "not-a-uuid", uuid.Nil, true},
		{"credential id form rejected", "key_" + id.String(), uuid.Nil, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseUserID(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseUserID(%q) = %s; want %s", tc.in, got, tc.want)
			}
		})
	}
}
