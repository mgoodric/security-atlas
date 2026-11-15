package sdk_test

import (
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// #2165: IsLoopbackEndpoint parses the host; it never prefix-matches, so a
// look-alike hostname is refused.
func TestIsLoopbackEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		endpoint string
		want     bool
	}{
		{"localhost:50051", true},
		{"LOCALHOST:50051", true},
		{"localhost", true},
		{"127.0.0.1:50051", true},
		{"127.8.9.10:50051", true}, // anywhere in 127.0.0.0/8
		{"127.255.255.254", true},
		{"[::1]:50051", true},
		{"::1", true},
		{"[::1]", true},
		{":50051", true},                     // empty host dials the local system
		{"[::ffff:127.0.0.1]:50051", true},   // IPv4-mapped loopback, bracketed with a port
		{"::ffff:127.0.0.1", true},           // IPv4-mapped loopback, bare
		{"[0:0:0:0:0:0:0:1]:50051", true},    // ::1 written out
		{"localhost.:50051", false},          // FQDN form goes to the resolver
		{"localhost.evil.com:50051", false},  // look-alike suffix
		{"127.0.0.1.evil.com:50051", false},  // look-alike suffix
		{"127.0.0.1.nip.io:50051", false},    // resolves to loopback; DNS is never consulted
		{"evil-localhost:50051", false},      // look-alike prefix
		{"http://localhost:8443", false},     // a URL is not a host:port endpoint
		{"http://localhost@evil.com", false}, // userinfo is not the host
		{"dns:///localhost:50051", false},    // resolver-scheme targets fail closed
		{"[::1%lo0]:50051", false},           // zoned address does not parse as an IP
		{"128.0.0.1:50051", false},
		{"0.0.0.0:50051", false},
		{"[::]:50051", false},
		{"[::ffff:10.0.0.1]:50051", false},
		{"[2001:db8::1]:50051", false},
		{"203.0.113.10:50051", false},
		{"atlas.example.test:443", false},
	}
	for _, tc := range cases {
		if got := sdk.IsLoopbackEndpoint(tc.endpoint); got != tc.want {
			t.Errorf("IsLoopbackEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.want)
		}
	}
}

// #2257: IsLoopbackURL takes the host from the parsed URL, so userinfo,
// look-alike names and DNS names that resolve to loopback are all refused.
func TestIsLoopbackURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost", true},
		{"http://localhost:8443", true},
		{"http://LocalHost:8443/v1/controls", true},
		{"http://127.0.0.1:8080", true},
		{"http://127.3.2.1", true},
		{"http://[::1]:8080/path", true},
		{"http://[::ffff:127.0.0.1]:8080", true},
		{"http://user:pass@localhost:8080", true}, // userinfo is not the host; localhost is

		{"http://localhost@evil.com", false},
		{"http://localhost:8443@evil.com", false},
		{"http://localhost.:8443", false},
		{"http://localhost.evil.com", false},
		{"http://127.0.0.1.nip.io:8080", false},
		{"http://evil.com/localhost", false},
		{"http://evil.com?h=localhost", false},
		{"http://0.0.0.0:8080", false},
		{"http://[::]:8080", false},
		{"http://atlas.example.test", false},
		{"http://:8080", false},   // no host
		{"localhost:8080", false}, // no scheme: "localhost" parses as the scheme
		{"", false},
		{"http://[::1", false}, // unparseable
	}
	for _, tc := range cases {
		if got := sdk.IsLoopbackURL(tc.url); got != tc.want {
			t.Errorf("IsLoopbackURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// #2257: Dial is the one gRPC dial for every connector's ConnectorRegistry
// client, so its WithInsecure refusal is what makes --insecure loopback-only
// there. grpc.NewClient is lazy, so the accepted cases do no network IO.
func TestDialInsecureIsLoopbackOnly(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"127.0.0.1:1", "localhost:1", "[::1]:1"} {
		conn, err := sdk.Dial(endpoint, sdk.WithInsecure())
		if err != nil {
			t.Errorf("Dial(%q, WithInsecure) = %v, want a conn", endpoint, err)
			continue
		}
		_ = conn.Close()
	}

	for _, endpoint := range []string{"atlas.example.test:443", "localhost.evil.com:1", "10.0.0.1:1"} {
		conn, err := sdk.Dial(endpoint, sdk.WithInsecure())
		if err == nil {
			_ = conn.Close()
			t.Errorf("Dial(%q, WithInsecure) succeeded, want a refusal", endpoint)
			continue
		}
		if !strings.Contains(err.Error(), "refuses non-loopback endpoint") {
			t.Errorf("Dial(%q, WithInsecure) error = %v, want a non-loopback refusal", endpoint, err)
		}
	}

	// TLS is not restricted to loopback.
	conn, err := sdk.Dial("atlas.example.test:443")
	if err != nil {
		t.Fatalf("Dial(TLS) = %v, want a conn", err)
	}
	_ = conn.Close()
}
