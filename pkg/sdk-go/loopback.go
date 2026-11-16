package sdk

import (
	"net"
	"net/url"
	"strings"
)

// IsLoopbackEndpoint reports whether a host:port gRPC endpoint names a
// loopback host (see IsLoopbackHost). An empty host (":50051") dials the
// local system and is accepted too. Uses net.SplitHostPort so IPv6 brackets
// are handled correctly. A URL ("http://localhost:8443") is not a host:port
// endpoint and is refused. WithInsecure, and so every --insecure gRPC dial,
// accepts only endpoints for which this is true.
func IsLoopbackEndpoint(endpoint string) bool {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = strings.TrimSuffix(strings.TrimPrefix(endpoint, "["), "]")
	}
	return host == "" || IsLoopbackHost(host)
}

// IsLoopbackURL reports whether rawURL's host is a loopback host (see
// IsLoopbackHost). The host comes from url.URL.Hostname, so userinfo is not
// mistaken for the host: "http://localhost@evil.com" names evil.com and is
// refused. A URL with no host is refused. atlas-cli refuses to send a request
// over plain http:// unless this is true of its URL (#2257).
func IsLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host != "" && IsLoopbackHost(host)
}

// IsLoopbackHost reports whether host, without a port or brackets, is a
// loopback host: "localhost" (exactly, case-insensitive), any address in
// 127.0.0.0/8 (including its IPv4-mapped IPv6 form, ::ffff:127.0.0.1), or
// ::1.
//
// The check is literal and never consults DNS. A name that happens to
// resolve to 127.0.0.1 ("127.0.0.1.nip.io") is refused, because what it
// resolves to is up to whoever controls the zone and can change between the
// check and the dial. For the same reason the fully qualified "localhost."
// is refused: the trailing dot sends it to the resolver rather than to the
// hosts file. The host is parsed, never prefix-matched, so
// "localhost.example.com" and "127.0.0.1.example.com" are refused too.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
