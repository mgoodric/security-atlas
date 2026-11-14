package users

import "testing"

// #1944: an explicit claim always decides; an absent claim links only under
// LinkUnlessUnverified, the zero value.
func TestUnverifiedEmailPolicyAllowsLink(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	cases := []struct {
		name     string
		policy   UnverifiedEmailPolicy
		verified *bool
		want     bool
	}{
		{"default, verified", LinkUnlessUnverified, &yes, true},
		{"default, unverified", LinkUnlessUnverified, &no, false},
		{"default, absent", LinkUnlessUnverified, nil, true},
		{"strict, verified", LinkOnlyIfVerified, &yes, true},
		{"strict, unverified", LinkOnlyIfVerified, &no, false},
		{"strict, absent", LinkOnlyIfVerified, nil, false},
	}
	var zero UnverifiedEmailPolicy
	if zero != LinkUnlessUnverified {
		t.Fatalf("zero UnverifiedEmailPolicy = %d, want LinkUnlessUnverified", zero)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.policy.allowsLink(tc.verified); got != tc.want {
				t.Errorf("allowsLink = %v, want %v", got, tc.want)
			}
		})
	}
}
