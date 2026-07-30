package change

import "testing"

func TestAllowedTransition(t *testing.T) {
	cases := []struct {
		from string
		to   string
		ok   bool
	}{
		{StatusProposed, StatusApproved, true},
		{StatusApproved, StatusImplemented, true},
		{StatusImplemented, StatusVerified, true},
		{StatusProposed, StatusImplemented, false},
		{StatusApproved, StatusVerified, false},
		{StatusVerified, StatusImplemented, false},
		{StatusProposed, StatusProposed, true},
	}
	for _, tc := range cases {
		if got := AllowedTransition(tc.from, tc.to); got != tc.ok {
			t.Fatalf("AllowedTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}
