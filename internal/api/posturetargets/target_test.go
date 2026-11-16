package posturetargets

import (
	"errors"
	"math"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
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

func TestHasTargetAdmin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"platform admin", credstore.Credential{IsAdmin: true}, true},
		{"approver flag", credstore.Credential{IsApprover: true}, true},
		{"admin role", credstore.Credential{OwnerRoles: []string{"admin"}}, true},
		{"grc_engineer role", credstore.Credential{OwnerRoles: []string{"grc_engineer"}}, true},
		{"viewer", credstore.Credential{OwnerRoles: []string{"viewer"}}, false},
		{"auditor", credstore.Credential{OwnerRoles: []string{"auditor"}}, false},
		{"control_owner", credstore.Credential{OwnerRoles: []string{"control_owner"}}, false},
		{"no signal", credstore.Credential{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasTargetAdmin(tc.cred); got != tc.want {
				t.Fatalf("hasTargetAdmin = %v; want %v", got, tc.want)
			}
		})
	}
}
