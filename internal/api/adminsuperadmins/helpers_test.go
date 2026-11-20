package adminsuperadmins

import "testing"

// #2684: a super admin from another tenant is named without its email.
func TestCrossTenantDisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   userName
		want string
	}{
		{"display name wins", userName{displayName: " Ada Admin ", email: "ada@example.invalid"}, "Ada Admin"},
		{"blank name falls back to local part", userName{displayName: "  ", email: "ada@example.invalid"}, "ada"},
		{"no at-sign keeps the whole value", userName{email: "ada"}, "ada"},
		{"nothing to show", userName{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := crossTenantDisplayName(tc.in); got != tc.want {
				t.Fatalf("crossTenantDisplayName(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
