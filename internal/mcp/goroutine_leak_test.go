package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// TestNoGoroutineLeakAcrossToolCalls is the AC-14 goroutine-hygiene
// gate. We make N tool calls back-to-back; after the calls drain, the
// goroutine count should not grow unboundedly. We sample twice (after
// warm-up + after N calls) and assert the delta is bounded — Go's
// runtime keeps idle G's around for a while, so an exact equality is
// too strict, but a hard ceiling above the warm-up sample catches a
// leak that would compound over a long MCP session.
//
// The test also runs under `go test -race`; race detection here
// catches concurrent map / state mutations that goroutine count alone
// would miss.
func TestNoGoroutineLeakAcrossToolCalls(t *testing.T) {
	t.Parallel()

	// Echo server: drains the request, returns a constant body.
	// We don't care about correctness here, just that each call's
	// resources (body reader, transport state) close cleanly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"controls":[],"count":0}`))
	}))
	defer srv.Close()

	client, err := mcp.NewClient(srv.URL, "test-bearer", "v0.0.0-test")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tool := tools.NewListControls(client)

	// Warm-up: stabilize the goroutine count after the first few
	// HTTP transport setup goroutines have spawned.
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = tool.Handle(ctx, json.RawMessage(`{}`))
	}
	// Brief settle window — http.Transport keeps idle conns for
	// IdleConnTimeout; we don't need them to close fully, we just
	// need a stable baseline.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	// Hammer.
	const N = 100
	for i := 0; i < N; i++ {
		_, _ = tool.Handle(ctx, json.RawMessage(`{}`))
	}
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	// Allow a small headroom (transport keepalive goroutines, the
	// test runtime's own background G's). A leak would show as
	// linear-with-N growth; a 10-G ceiling rejects that with margin.
	if after-baseline > 10 {
		t.Errorf("goroutine leak suspected: baseline=%d after=%d delta=%d (cap=10)",
			baseline, after, after-baseline)
	}
}

// TestConcurrentToolCalls verifies the Client is safe under concurrent
// use by tool handlers (the *http.Client + transport are stdlib-safe;
// this is the lock-down test).
func TestConcurrentToolCalls(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client, _ := mcp.NewClient(srv.URL, "test-bearer", "v0.0.0-test")

	var wg sync.WaitGroup
	const N = 50
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = client.Get(context.Background(), "/v1/anything", url.Values{}, &struct{}{})
		}()
	}
	wg.Wait()
}

// TestServer_RunReturnsOnEOF verifies a clean stdin close (EOF) leads
// to a clean Server.Run return — the foundation of "Claude Desktop
// closes the subprocess" being a clean shutdown path.
func TestServer_RunReturnsOnEOF(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", nil, nil)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(context.Background(), strings.NewReader(""), &noopWriter{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run on empty stdin = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after EOF within 2s")
	}
}

// noopWriter satisfies io.Writer for tests that don't care about
// output.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
