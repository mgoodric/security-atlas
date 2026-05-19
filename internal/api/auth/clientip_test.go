// Slice 162 — unit tests for the clientIP + userAgent helpers.
//
// The big lift is exercising the TRUST_FORWARDED_HEADERS gate without
// leaking env state across tests. Each test that flips the env var sets
// it via t.Setenv (which auto-cleans).

package auth

import (
	"net/http"
	"testing"
)

func newReq(t *testing.T, remoteAddr string, headers map[string]string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIP_DefaultIgnoresForwardedHeader(t *testing.T) {
	// No TRUST_FORWARDED_HEADERS env. X-Forwarded-For is set but MUST be ignored.
	r := newReq(t, "203.0.113.4:55320", map[string]string{
		"X-Forwarded-For": "198.51.100.42",
	})
	got := clientIP(r)
	if got != "203.0.113.4" {
		t.Errorf("clientIP default = %q; want 203.0.113.4 (RemoteAddr port stripped, XFF ignored)", got)
	}
}

func TestClientIP_TrustsForwardedWhenEnvSet(t *testing.T) {
	t.Setenv(trustForwardedHeadersEnv, "1")
	r := newReq(t, "10.0.0.1:443", map[string]string{
		"X-Forwarded-For": "198.51.100.42, 10.0.0.2",
	})
	got := clientIP(r)
	if got != "198.51.100.42" {
		t.Errorf("clientIP trusted = %q; want 198.51.100.42 (leftmost forwarded IP)", got)
	}
}

func TestClientIP_FalsyEnvValueDoesNotTrust(t *testing.T) {
	// Anything other than the literal "1" leaves the header ignored.
	t.Setenv(trustForwardedHeadersEnv, "true")
	r := newReq(t, "203.0.113.4:55320", map[string]string{
		"X-Forwarded-For": "198.51.100.42",
	})
	got := clientIP(r)
	if got != "203.0.113.4" {
		t.Errorf("clientIP env=%q = %q; want 203.0.113.4 (only literal \"1\" opts in)", "true", got)
	}
}

func TestClientIP_MalformedForwardedFallsBack(t *testing.T) {
	t.Setenv(trustForwardedHeadersEnv, "1")
	r := newReq(t, "203.0.113.4:55320", map[string]string{
		"X-Forwarded-For": "not-an-ip",
	})
	got := clientIP(r)
	if got != "203.0.113.4" {
		t.Errorf("clientIP malformed XFF = %q; want fallback to RemoteAddr 203.0.113.4", got)
	}
}

func TestClientIP_IPv6RemoteAddr(t *testing.T) {
	r := newReq(t, "[2001:db8::1]:55320", nil)
	got := clientIP(r)
	if got != "2001:db8::1" {
		t.Errorf("clientIP IPv6 = %q; want 2001:db8::1", got)
	}
}

func TestClientIP_NoPortRemoteAddr(t *testing.T) {
	// Some test rigs set RemoteAddr to a bare host with no port (unix socket
	// adaptors, mock servers). The helper should still recover the IP.
	r := newReq(t, "203.0.113.4", nil)
	got := clientIP(r)
	if got != "203.0.113.4" {
		t.Errorf("clientIP no-port = %q; want 203.0.113.4", got)
	}
}

func TestClientIP_EmptyRemoteAddrReturnsEmpty(t *testing.T) {
	r := newReq(t, "", nil)
	got := clientIP(r)
	if got != "" {
		t.Errorf("clientIP empty RemoteAddr = %q; want \"\" (store layer converts to SQL NULL)", got)
	}
}

func TestClientIP_NilRequestReturnsEmpty(t *testing.T) {
	if got := clientIP(nil); got != "" {
		t.Errorf("clientIP(nil) = %q; want \"\"", got)
	}
}

func TestUserAgent_SurfacesHeader(t *testing.T) {
	r := newReq(t, "203.0.113.4:443", map[string]string{
		"User-Agent": "Mozilla/5.0 example",
	})
	if got := userAgent(r); got != "Mozilla/5.0 example" {
		t.Errorf("userAgent = %q; want \"Mozilla/5.0 example\"", got)
	}
}

func TestUserAgent_EmptyHeaderReturnsEmpty(t *testing.T) {
	r := newReq(t, "203.0.113.4:443", nil)
	if got := userAgent(r); got != "" {
		t.Errorf("userAgent (no header) = %q; want \"\"", got)
	}
}

func TestUserAgent_NilRequestReturnsEmpty(t *testing.T) {
	if got := userAgent(nil); got != "" {
		t.Errorf("userAgent(nil) = %q; want \"\"", got)
	}
}

func TestFirstForwardedIP_TrimsWhitespaceAndComma(t *testing.T) {
	cases := map[string]string{
		"198.51.100.42":                   "198.51.100.42",
		"  198.51.100.42  ":               "198.51.100.42",
		"198.51.100.42, 10.0.0.2":         "198.51.100.42",
		"  198.51.100.42 , 10.0.0.2":      "198.51.100.42",
		"\t198.51.100.42\t":               "198.51.100.42",
		"2001:db8::1, ::1":                "2001:db8::1",
		"":                                "",
		"not-an-ip":                       "",
		"   ":                             "",
		"198.51.100.42-malformed":         "",
		"198.51.100.42, garbage, 1.2.3.4": "198.51.100.42",
	}
	for in, want := range cases {
		if got := firstForwardedIP(in); got != want {
			t.Errorf("firstForwardedIP(%q) = %q; want %q", in, got, want)
		}
	}
}
