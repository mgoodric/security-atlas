// Package moduleaccesstest gives handler tests a module switch store that
// is down, so each module-gated read can show it answers 503
// module_state_unavailable rather than a plain 500 (#2575, #2702).
package moduleaccesstest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// flagsDown fails every lookup as the flag store does when its DB read
// fails.
type flagsDown struct{}

func (flagsDown) Get(context.Context, string) (featureflag.Flag, error) {
	return featureflag.Flag{}, featureflag.ErrStoreUnavailable
}

// FlagsDown returns a module switch store whose every read fails with
// featureflag.ErrStoreUnavailable, for a handler that reads switches
// itself (featureflag.RouteOn) as well as through Views.
func FlagsDown() featureflag.Looker { return flagsDown{} }

// StoreDown returns Views over FlagsDown. Its access store has no
// database, so only a super-admin caller (SuperAdminRequest) reaches the
// switch read.
func StoreDown() *moduleaccess.Views {
	return moduleaccess.NewViews(moduleaccess.NewStore(nil), flagsDown{})
}

// SuperAdminRequest builds a request whose caller is a super-admin in a
// fresh tenant. A super-admin is exempt from module access, so no access
// row is read before the switch read.
func SuperAdminRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	tenantID := uuid.NewString()
	ctx := authctx.WithCredential(context.Background(), credstore.Credential{
		ID: "jwt:abc", UserID: "user:" + uuid.NewString(), TenantID: tenantID,
	})
	ctx = jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{SuperAdmin: true})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return httptest.NewRequest(method, target, nil).WithContext(ctx)
}

// AssertUnavailable fails t unless rec is a 503 module_state_unavailable.
func AssertUnavailable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 (body %s)", rec.Code, rec.Body)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v (%s)", err, rec.Body)
	}
	if body.Code != "module_state_unavailable" {
		t.Fatalf("code = %q; want module_state_unavailable (body %s)", body.Code, rec.Body)
	}
}
