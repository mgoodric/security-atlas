package sdk_test

import (
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
		{":50051", true}, // empty host dials the local system

		{"localhost.evil.com:50051", false},
		{"127.0.0.1.evil.com:50051", false},
		{"evil-localhost:50051", false},
		{"128.0.0.1:50051", false},
		{"0.0.0.0:50051", false},
		{"[::]:50051", false},
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
