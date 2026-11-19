package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDenyReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip     string
		strict string // reason under the zero Policy; "" = allowed
		priv   string // reason with AllowLoopback + AllowPrivate (the IdP opt-in)
	}{
		// Public destinations, including real Slack / webhook style hosts.
		{"93.184.216.34", "", ""},
		{"1.1.1.1", "", ""},
		{"34.226.36.48", "", ""},
		{"2606:4700:4700::1111", "", ""},
		{"::ffff:93.184.216.34", "", ""},
		// NAT64 / 6to4 wrapping a public IPv4 (DNS64 networks) stay usable.
		{"64:ff9b::5db8:d822", "", ""}, // 93.184.216.34
		{"2002:5db8:d822::1", "", ""},  // 93.184.216.34

		{"127.0.0.1", "loopback", ""},
		{"::1", "loopback", ""},
		{"10.0.0.5", "private", ""},
		{"172.16.0.1", "private", ""},
		{"192.168.1.1", "private", ""},
		{"fd00::1", "private", ""},
		{"100.64.0.1", "cgnat", ""},
		{"100.127.255.254", "cgnat", ""},
		{"::ffff:10.0.0.1", "private", ""},

		// Denied even with the opt-in.
		{"169.254.169.254", "link-local", "link-local"},
		{"fe80::1", "link-local", "link-local"},
		{"0.0.0.0", "unspecified", "unspecified"},
		{"::", "unspecified", "unspecified"},
		{"224.0.0.1", "link-local", "link-local"}, // link-local multicast
		{"239.1.1.1", "multicast", "multicast"},
		{"ff02::1", "link-local", "link-local"},
		{"ff05::1", "multicast", "multicast"},

		// Ranges added by the #2536 review (#2534 comment).
		{"0.1.2.3", "this-network", "this-network"},
		{"0.255.255.255", "this-network", "this-network"},
		{"255.255.255.255", "reserved", "reserved"},
		{"240.0.0.1", "reserved", "reserved"},
		{"198.18.0.1", "benchmarking", "benchmarking"},
		{"198.19.255.255", "benchmarking", "benchmarking"},
		{"64:ff9b::a9fe:a9fe", "nat64-embedded link-local", "nat64-embedded link-local"}, // 169.254.169.254
		{"64:ff9b::a00:1", "nat64-embedded private", ""},                                 // 10.0.0.1
		{"64:ff9b::7f00:1", "nat64-embedded loopback", ""},                               // 127.0.0.1
		{"2002:a9fe:a9fe::1", "6to4-embedded link-local", "6to4-embedded link-local"},    // 169.254.169.254
		{"2002:c0a8:101::1", "6to4-embedded private", ""},                                // 192.168.1.1
		{"2002:7f00:1::1", "6to4-embedded loopback", ""},                                 // 127.0.0.1

		// #2549: RFC 8215 local-use NAT64, read at every RFC 6052 layout
		// the address fits.
		{"64:ff9b:1::5db8:d822", "", ""},     // /96 93.184.216.34
		{"64:ff9b:1:5db8:d8:2200::", "", ""}, // /48 93.184.216.34
		{"64:ff9b:1::a9fe:a9fe", "nat64-local-embedded link-local", "nat64-local-embedded link-local"},       // /96 169.254.169.254
		{"64:ff9b:1::a00:1", "nat64-local-embedded private", ""},                                             // /96 10.0.0.1
		{"64:ff9b:1:0:a9:fea9:fe00:0", "nat64-local-embedded link-local", "nat64-local-embedded link-local"}, // /64 169.254.169.254
		{"64:ff9b:1:a9fe:a9:fe00::", "nat64-local-embedded link-local", "nat64-local-embedded link-local"},   // /48 169.254.169.254
		// /48 10.0.0.1; its /56 reading 0.0.1.0 is 0/8 padding, dropped.
		{"64:ff9b:1:a00:0:100::", "nat64-local-embedded private", ""},
		// /64 10.0.0.1 with a non-zero suffix that reads as /96 1.1.1.1:
		// RFC 6052's zero suffix is only a SHOULD, so every layout counts.
		{"64:ff9b:1:0:a:0:101:101", "nat64-local-embedded private", ""},
		{"64:ff9b:1:0:a9:fe00:101:101", "nat64-local-embedded link-local", "nat64-local-embedded link-local"}, // /64 169.254.0.1, /96 1.1.1.1
		{"64:ff9b:1:0:5d:b8d8:2200:0", "", ""},                                                                // /64 93.184.216.34
		{"64:ff9b:1::", "nat64-local without an embedded address", "nat64-local without an embedded address"},
		{"64:ff9b:1:0:ff00::1", "nat64-local without an embedded address", "nat64-local without an embedded address"}, // u octet set

		// #2549: Teredo, server IPv4 in clear and client IPv4 inverted.
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", "", ""},                                                                   // 65.54.227.120 / 192.0.2.45
		{"2001:0:a00:1:8000:63bf:3fff:fdd2", "teredo-server-embedded private", ""},                                         // server 10.0.0.1
		{"2001:0:4136:e378:8000:63bf:f5ff:fffe", "teredo-client-embedded private", ""},                                     // client 10.0.0.1
		{"2001:0:4136:e378:8000:63bf:5601:5601", "teredo-client-embedded link-local", "teredo-client-embedded link-local"}, // client 169.254.169.254
		{"2001:0:7f00:1:8000:63bf:3fff:fdd2", "teredo-server-embedded loopback", ""},                                       // server 127.0.0.1

		// #2549: deprecated IPv4-compatible ::a.b.c.d.
		{"::5db8:d822", "", ""}, // 93.184.216.34
		{"::a9fe:a9fe", "ipv4-compatible-embedded link-local", "ipv4-compatible-embedded link-local"},
		{"::a00:1", "ipv4-compatible-embedded private", ""},
		{"::7f00:1", "ipv4-compatible-embedded loopback", ""},
		{"::2", "ipv4-compatible-embedded this-network", "ipv4-compatible-embedded this-network"},
	}
	optIn := Policy{AllowLoopback: true, AllowPrivate: true}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tc.ip)
		}
		if got := (Policy{}).DenyReason(ip); got != tc.strict {
			t.Errorf("strict DenyReason(%s) = %q, want %q", tc.ip, got, tc.strict)
		}
		if got := optIn.DenyReason(ip); got != tc.priv {
			t.Errorf("opt-in DenyReason(%s) = %q, want %q", tc.ip, got, tc.priv)
		}
	}
}

func TestDenyReasonAllowancesAreIndependent(t *testing.T) {
	t.Parallel()
	if r := (Policy{AllowLoopback: true}).DenyReason(net.ParseIP("10.0.0.1")); r != "private" {
		t.Errorf("AllowLoopback alone permitted a private address (%q)", r)
	}
	if r := (Policy{AllowPrivate: true}).DenyReason(net.ParseIP("127.0.0.1")); r != "loopback" {
		t.Errorf("AllowPrivate alone permitted loopback (%q)", r)
	}
}

func TestDialControl(t *testing.T) {
	t.Parallel()
	strict := Policy{}
	for _, addr := range []string{"127.0.0.1:443", "169.254.169.254:80", "[::1]:443", "10.1.2.3:443", "255.255.255.255:443", "not-an-address", "example.com:443"} {
		if err := strict.DialControl("tcp", addr, nil); err == nil {
			t.Errorf("DialControl(%q) = nil, want refusal", addr)
		}
	}
	if err := strict.DialControl("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("DialControl(public) = %v, want nil", err)
	}
}

func TestNewHTTPClientHardened(t *testing.T) {
	t.Parallel()
	c := Policy{}.NewHTTPClient(7 * time.Second)
	if c.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", c.Transport)
	}
	if tr == http.DefaultTransport {
		t.Error("client shares http.DefaultTransport")
	}
	if tr.Proxy != nil {
		t.Error("Proxy is set; an environment proxy would bypass DialControl")
	}
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want ErrUseLastResponse", err)
	}
}

func TestNewHTTPClientRefusesLoopbackDial(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, err := Policy{}.NewHTTPClient(5 * time.Second).Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "ssrf guard") {
		t.Fatalf("Get(loopback) err = %v, want ssrf guard refusal", err)
	}
	if hits.Load() != 0 {
		t.Fatal("strict client reached the loopback server")
	}
	resp, err := Policy{AllowLoopback: true}.NewHTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get(loopback) with AllowLoopback: %v", err)
	}
	_ = resp.Body.Close()
}
