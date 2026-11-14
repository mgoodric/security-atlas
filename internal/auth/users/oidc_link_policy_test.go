package users

import "testing"

// #1944 D3: an explicit claim always decides; an absent claim links only
// when the IdP config trusts unverified email.
func TestEmailLinkAllowed(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	cases := []struct {
		name     string
		verified *bool
		trust    bool
		want     bool
	}{
		{"strict, verified", &yes, false, true},
		{"strict, unverified", &no, false, false},
		{"strict, absent", nil, false, false},
		{"trusting, verified", &yes, true, true},
		{"trusting, unverified", &no, true, false},
		{"trusting, absent", nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := emailLinkAllowed(tc.verified, tc.trust); got != tc.want {
				t.Errorf("emailLinkAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}
