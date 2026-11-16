package main

import (
	"strings"
	"testing"
)

// #2257: dialConnectorRegistry dials through sdk.Dial, so --insecure is
// loopback-only on the ConnectorRegistry path, as it is on the push path.
// grpc.NewClient is lazy, so the accepted loopback dial does no network IO.
func TestDialConnectorRegistry_InsecureIsLoopbackOnly(t *testing.T) {
	saved := common
	t.Cleanup(func() { common = saved })
	common.insecure = true

	common.endpoint = "atlas.example.test:443"
	if _, conn, err := dialConnectorRegistry(); err == nil {
		_ = conn.Close()
		t.Fatalf("dialConnectorRegistry(%q, --insecure) succeeded; want a non-loopback refusal", common.endpoint)
	} else if !strings.Contains(err.Error(), "refuses non-loopback endpoint") {
		t.Fatalf("dialConnectorRegistry(%q, --insecure) error = %v; want a non-loopback refusal", common.endpoint, err)
	}

	common.endpoint = "127.0.0.1:1"
	_, conn, err := dialConnectorRegistry()
	if err != nil {
		t.Fatalf("dialConnectorRegistry(%q, --insecure) = %v; want a conn", common.endpoint, err)
	}
	_ = conn.Close()
}
