package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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

// #2575: a module switch the flag store cannot read answers 503
// module_state_unavailable, as ModuleGate does, not a plain 500. A
// super-admin caller is exempt from module access, so no DB read runs
// before the switch read.
func TestGetTarget_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := New(nil).WithModules(moduleaccess.NewViews(moduleaccess.NewStore(nil), flagsDown{}))

	tenantID := uuid.NewString()
	ctx := authctx.WithCredential(context.Background(), credstore.Credential{
		ID: "jwt:abc", UserID: "user:" + uuid.NewString(), TenantID: tenantID,
	})
	ctx = jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{SuperAdmin: true})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "control_pass_rate")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/control_pass_rate/target", nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetTarget(rec, req)

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
