// Slice 466 — AC-6 wire-level coverage for the trusted-proxy walk.
//
// The docker-compose self-host seed ships NO reverse-proxy container (it is
// a single-VM bundle where atlas binds the public port directly — see
// deploy/docker/docker-compose.yml), so an e2e that fronts atlas with a
// header-overwriting proxy cannot be wired from the seed today. Rather than
// relax the AC-6 assertion silently, this test drives the resolver over a
// REAL TCP connection via httptest.Server: the connecting peer is genuine
// loopback (set by the net stack, not a synthetic RemoteAddr string), and we
// vary the X-Forwarded-For header a "proxy" or a "forging client" would send.
// The seed-harness e2e (a real proxy container in front of atlas) is filed as
// the slice 470 spillover.
//
// This exercises exactly the resolution path that internal/api/auth/http.go
// runs for session creation, with a true remote address.

package auth

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP_OverRealTCP_TrustedLoopbackProxy(t *testing.T) {
	// Trust loopback as the "proxy" range so a forwarded header from a real
	// loopback connection is honored — modelling a header-overwriting proxy
	// co-located with atlas.
	withTrustedProxies(t, "127.0.0.0/8,::1/128")

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = clientIP(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	// The "proxy" overwrites X-Forwarded-For with the real client address.
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if got != "198.51.100.77" {
		t.Errorf("clientIP over real TCP (trusted loopback proxy) = %q; want 198.51.100.77", got)
	}
}

func TestClientIP_OverRealTCP_UntrustedPeerForgesHeader(t *testing.T) {
	// Trust ONLY a CIDR that loopback is NOT in. The httptest client connects
	// from loopback (untrusted), so a forged X-Forwarded-For must be ignored
	// and the real loopback peer returned.
	withTrustedProxies(t, "10.0.0.0/8")

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = clientIP(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("X-Forwarded-For", "1.2.3.4") // forged
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// The peer is loopback; assert the forged 1.2.3.4 did NOT win.
	if got == "1.2.3.4" {
		t.Fatalf("clientIP returned the forged header 1.2.3.4; spoofing not rejected")
	}
	if ip := net.ParseIP(got); ip == nil || !ip.IsLoopback() {
		t.Errorf("clientIP over real TCP (untrusted peer) = %q; want the loopback peer address", got)
	}
}
