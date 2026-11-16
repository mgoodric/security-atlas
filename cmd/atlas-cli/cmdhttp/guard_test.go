package cmdhttp

// #2257: Guard refuses plain http:// to a non-loopback host before any byte
// is sent. These tests are not t.Parallel: they flip the package-level
// --allow-insecure-http switch, and Go runs every sequential test before
// releasing the parallel ones, so the switch never changes under them.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer is a plain-http server that counts the requests reaching
// it and records whether any of them carried an Authorization header.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var hits atomic.Int32
	var sawBearer atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			sawBearer.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &sawBearer
}

// routeTo returns a transport whose every dial lands on addr, so a request
// to a non-loopback name (atlas.example.test) reaches the test server if,
// and only if, the guard lets it through.
func routeTo(addr string) *http.Transport {
	var d net.Dialer
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	}
}

func setAllow(t *testing.T, allow bool) {
	t.Helper()
	prev := allowInsecureHTTP.Load()
	SetAllowInsecureHTTP(allow)
	t.Cleanup(func() { SetAllowInsecureHTTP(prev) })
}

func doGet(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestGuardRefusesNonLoopbackHTTPWithoutSending(t *testing.T) {
	setAllow(t, false)
	srv, hits, sawBearer := countingServer(t)
	c := &http.Client{Timeout: 5 * time.Second, Transport: Guard(routeTo(srv.Listener.Addr().String()))}

	for _, url := range []string{
		"http://atlas.example.test/v1/controls",
		"http://localhost@atlas.example.test/v1/controls",
		"http://localhost.:8080/v1/controls",
		"HTTP://atlas.example.test/v1/controls",
	} {
		err := doGet(t, c, url)
		if !errors.Is(err, ErrInsecureHTTP) {
			t.Errorf("GET %s: err = %v, want ErrInsecureHTTP", url, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server saw %d requests; a refused request must never be sent", n)
	}
	if sawBearer.Load() {
		t.Fatal("server saw a bearer over plain http")
	}
}

func TestGuardAllowsLoopbackHTTP(t *testing.T) {
	setAllow(t, false)
	srv, hits, _ := countingServer(t)
	c := Client(5 * time.Second)

	if err := doGet(t, c, srv.URL+"/v1/controls"); err != nil {
		t.Fatalf("GET loopback %s: %v", srv.URL, err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("server saw %d requests, want 1", n)
	}
}

func TestGuardAllowInsecureHTTPOptOut(t *testing.T) {
	setAllow(t, true)
	srv, hits, _ := countingServer(t)
	c := &http.Client{Timeout: 5 * time.Second, Transport: Guard(routeTo(srv.Listener.Addr().String()))}

	if err := doGet(t, c, "http://atlas.example.test/v1/controls"); err != nil {
		t.Fatalf("GET with --allow-insecure-http: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("server saw %d requests, want 1", n)
	}
}

// A redirect from https:// to a non-loopback http:// is a downgrade; the
// guard sees the redirected hop and refuses it before it is sent.
func TestGuardRefusesRedirectDowngrade(t *testing.T) {
	setAllow(t, false)
	leak, leakHits, _ := countingServer(t)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://atlas.example.test/leak", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	base, ok := origin.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("TLS test server transport is %T", origin.Client().Transport)
	}
	tr := base.Clone()
	var d net.Dialer
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "atlas.example.test:") {
			return d.DialContext(ctx, network, leak.Listener.Addr().String())
		}
		return d.DialContext(ctx, network, addr)
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: Guard(tr)}

	err := doGet(t, c, origin.URL+"/start")
	if !errors.Is(err, ErrInsecureHTTP) {
		t.Fatalf("redirect downgrade: err = %v, want ErrInsecureHTTP", err)
	}
	if n := leakHits.Load(); n != 0 {
		t.Fatalf("downgraded target saw %d requests, want 0", n)
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func TestGuardClosesBodyOfRefusedRequest(t *testing.T) {
	setAllow(t, false)
	body := &closeTracker{Reader: strings.NewReader("payload")}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://atlas.example.test/v1/import", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := Guard(nil).RoundTrip(req); !errors.Is(err, ErrInsecureHTTP) {
		t.Fatalf("RoundTrip: err = %v, want ErrInsecureHTTP", err)
	}
	if !body.closed {
		t.Fatal("refused request's body was not closed")
	}
}

func TestGuardNilUsesDefaultTransport(t *testing.T) {
	g, ok := Guard(nil).(*guard)
	if !ok {
		t.Fatalf("Guard(nil) is %T", Guard(nil))
	}
	if g.next != http.DefaultTransport {
		t.Fatalf("Guard(nil).next = %T, want http.DefaultTransport", g.next)
	}
}
