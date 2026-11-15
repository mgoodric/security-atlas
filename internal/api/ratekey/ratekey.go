// Package ratekey turns a resolved client IP into the key a per-IP rate
// limiter buckets on (#2047).
//
// Keying IPv6 on the full address lets one client mint a fresh bucket per
// request: a single customer allocation is a /64 (often a /56 or /48), so
// the 2^64 addresses inside it are one source. IP collapses every address
// in a /64 to one key. IPv4, and IPv4-mapped IPv6 (::ffff:a.b.c.d), key on
// the full IPv4 address.
//
// Every per-IP limiter goes through IP: the login throttle
// (internal/api/auth), the OAuth client-auth limiter (internal/api/oauth)
// and the admin demo limiter (internal/api/admindemo). It is a leaf
// package because internal/api/auth imports internal/api/oauth, so the
// helper cannot live in either.
package ratekey

import "net/netip"

// ipv6PrefixBits is the IPv6 prefix whose addresses share one bucket.
const ipv6PrefixBits = 64

// IP returns the rate-limit key for ip, a textual address as returned by
// auth.ClientIP. An IPv4 or IPv4-mapped address keys on its IPv4 form; any
// other IPv6 address keys on its /64 prefix in CIDR form ("2001:db8::/64").
// An unparseable value, including "", is returned unchanged, so a limiter
// keys on it exactly as it did before.
func IP(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	// WithZone("") so a link-local zone (fe80::1%eth0) cannot split a /64;
	// Prefix refuses zoned addresses.
	prefix, err := addr.WithZone("").Prefix(ipv6PrefixBits)
	if err != nil {
		return ip
	}
	return prefix.String()
}
