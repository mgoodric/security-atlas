package posturetargets

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func f(v float64) *float64 { return &v }

func TestInputValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Input
		ok   bool
	}{
		{"higher ordered", Input{f(90), f(80), f(60), DirectionHigherIsBetter}, true},
		{"higher equal bounds", Input{f(90), f(90), f(90), DirectionHigherIsBetter}, true},
		{"higher warning above target", Input{f(90), f(95), f(60), DirectionHigherIsBetter}, false},
		{"higher critical above warning", Input{f(90), f(70), f(75), DirectionHigherIsBetter}, false},
		{"lower ordered", Input{f(5), f(10), f(20), DirectionLowerIsBetter}, true},
		{"lower reversed", Input{f(20), f(10), f(5), DirectionLowerIsBetter}, false},
		{"target band ordered", Input{f(50), f(55), f(30), DirectionTargetIsBetter}, true},
		{"target band inverted", Input{f(50), f(30), f(55), DirectionTargetIsBetter}, false},
		{"over 100", Input{f(101), f(80), f(60), DirectionHigherIsBetter}, false},
		{"negative", Input{f(90), f(80), f(-1), DirectionHigherIsBetter}, false},
		{"NaN", Input{f(math.NaN()), f(80), f(60), DirectionHigherIsBetter}, false},
		{"three decimals", Input{f(90.125), f(80), f(60), DirectionHigherIsBetter}, false},
		{"two decimals", Input{f(90.25), f(80.5), f(60), DirectionHigherIsBetter}, true},
		// Two-decimal values whose float64 * 100 is not a whole number.
		{"70.1 / 0.57 / 4.35 (float error)", Input{f(70.1), f(4.35), f(0.57), DirectionHigherIsBetter}, true},
		{"4.35 lower", Input{f(4.35), f(70.1), f(70.1), DirectionLowerIsBetter}, true},
		{"target band, equal distances", Input{f(70.1), f(70.2), f(70.0), DirectionTargetIsBetter}, true},
		{"target band, 0.01 further", Input{f(70.1), f(70.25), f(69.96), DirectionTargetIsBetter}, false},
		{"three decimals near a whole hundredth", Input{f(70.101), f(60), f(50), DirectionHigherIsBetter}, false},
		{"missing warning", Input{f(90), nil, f(60), DirectionHigherIsBetter}, false},
		{"unknown direction", Input{f(90), f(80), f(60), "up"}, false},
		{"empty direction", Input{f(90), f(80), f(60), ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := tc.in.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate() = %v; want ok", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("Validate() = nil; want an error")
				}
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Validate() = %v; want ErrInvalid", err)
				}
			}
		})
	}
}

// TestSet_Gate: the posture-target write gate is authz.IsTenantApprover
// (#2447). An admitted caller reaches the version parse (400 on a bad id);
// a refused one gets 403 first. A JWT role claim with no live tenant role
// is refused, so a revoked grant fails while its token is still live.
func TestSet_Gate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want int
	}{
		{"platform admin", credstore.Credential{IsAdmin: true}, http.StatusBadRequest},
		{"approver flag", credstore.Credential{IsApprover: true}, http.StatusBadRequest},
		{"live admin", credstore.Credential{ID: "jwt:a", TenantRoles: []string{"admin"}}, http.StatusBadRequest},
		{"live grc_engineer", credstore.Credential{ID: "jwt:g", TenantRoles: []string{"grc_engineer"}}, http.StatusBadRequest},
		{"live viewer", credstore.Credential{ID: "jwt:v", TenantRoles: []string{"viewer"}}, http.StatusForbidden},
		{"live auditor", credstore.Credential{ID: "jwt:u", TenantRoles: []string{"auditor"}}, http.StatusForbidden},
		{"live control_owner", credstore.Credential{ID: "jwt:c", TenantRoles: []string{"control_owner"}}, http.StatusForbidden},
		{"revoked admin, JWT claim live", credstore.Credential{ID: "jwt:r", OwnerRoles: []string{"admin"}}, http.StatusForbidden},
		{"revoked grc_engineer, JWT claim live", credstore.Credential{ID: "jwt:s", OwnerRoles: []string{"grc_engineer"}}, http.StatusForbidden},
		{"API key grc_engineer label", credstore.Credential{ID: "key_g", OwnerRoles: []string{"grc_engineer"}}, http.StatusBadRequest},
		{"no signal", credstore.Credential{}, http.StatusForbidden},
	}
	const tenant = "00000000-0000-4000-8000-000000002447"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cred := tc.cred
			cred.TenantID = tenant
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			r := httptest.NewRequest(http.MethodPut, "/v1/framework-scopes/posture-targets/not-a-uuid", nil)
			r = r.WithContext(authctx.WithCredential(ctx, cred))
			w := httptest.NewRecorder()
			New(nil).Set(w, r)
			if w.Code != tc.want {
				t.Fatalf("Set status = %d; want %d", w.Code, tc.want)
			}
		})
	}
}
