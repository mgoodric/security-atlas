package ratekey

import "testing"

func TestIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"ipv4", "203.0.113.7", "203.0.113.7"},
		{"ipv4-mapped ipv6", "::ffff:203.0.113.7", "203.0.113.7"},
		{"ipv6 keys on /64", "2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::/64"},
		{"ipv6 /64 start", "2001:db8:1:2::", "2001:db8:1:2::/64"},
		{"ipv6 loopback", "::1", "::/64"},
		{"ipv6 zone dropped", "fe80::1%eth0", "fe80::/64"},
		{"empty unchanged", "", ""},
		{"garbage unchanged", "not-an-ip", "not-an-ip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IP(tc.in); got != tc.want {
				t.Fatalf("IP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIPSharesBucketWithinSlash64 is the #2047 property: two addresses in
// one /64 share a key, and addresses in neighbouring /64s do not.
func TestIPSharesBucketWithinSlash64(t *testing.T) {
	t.Parallel()
	a := IP("2001:db8:0:1::1")
	b := IP("2001:db8:0:1:ffff:ffff:ffff:ffff")
	c := IP("2001:db8:0:2::1")
	if a != b {
		t.Fatalf("same /64 split: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different /64s merged: %q", a)
	}
}
