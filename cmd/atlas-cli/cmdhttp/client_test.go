// Slice 088 — unit tests for cmdhttp.Client.
//
// Coverage:
//
//   - TestClientTimeoutIsHonored: point Client.Do at an httptest.Server
//     that sleeps longer than the configured timeout. Assert that Do
//     returns within the timeout window (plus a generous margin) and
//     the error chain reports a timeout / deadline-exceeded condition.
//   - TestClientReturnsDistinctInstances: assert two consecutive
//     Client() calls produce different *http.Client pointers AND
//     different *http.Transport pointers — no hidden package-level
//     shared state.
//   - TestClientTimeoutFieldIsSet: assert the returned client's
//     Timeout field equals the duration passed in (the construction
//     contract).
package cmdhttp

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientTimeoutIsHonored(t *testing.T) {
	t.Parallel()

	// Server that intentionally sleeps far longer than the client's
	// timeout. We don't want the server's sleep to be the limiting
	// factor; we want the client's Timeout to fire and abort.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
			_, _ = io.WriteString(w, "should never see this")
		case <-r.Context().Done():
			// Client cancelled; just return.
		}
	}))
	t.Cleanup(srv.Close)

	const timeout = 200 * time.Millisecond
	// Margin accounts for: scheduler jitter on a loaded CI runner,
	// the Client.Timeout grace before Go's net package returns control,
	// and the small post-timeout teardown window. 2s is intentionally
	// generous — the test asserts "fires within budget", not "fires
	// at exactly 200ms".
	const maxWait = 2 * time.Second

	client := Client(timeout)

	start := time.Now()
	resp, err := client.Get(srv.URL) //nolint:noctx // CLI-style call; explicit timeout via Client.
	elapsed := time.Since(start)
	if resp != nil {
		_ = resp.Body.Close()
	}

	if err == nil {
		t.Fatalf("expected timeout error, got nil response after %v", elapsed)
	}

	if elapsed > maxWait {
		t.Fatalf("client.Get did not abort in time: elapsed=%v, maxWait=%v, err=%v", elapsed, maxWait, err)
	}

	// The error should look like a timeout. Go reports this as a
	// *url.Error whose underlying err is either a net.Error with
	// Timeout()=true OR wraps os.ErrDeadlineExceeded. Either form
	// satisfies the contract; we check both to be portable across
	// stdlib refactors.
	if !isTimeout(err) {
		t.Fatalf("expected timeout-shaped error, got %T: %v", err, err)
	}
}

func TestClientReturnsDistinctInstances(t *testing.T) {
	t.Parallel()

	c1 := Client(10 * time.Second)
	c2 := Client(10 * time.Second)

	if c1 == c2 {
		t.Fatalf("expected distinct *http.Client instances, got the same pointer: %p", c1)
	}

	t1, ok1 := c1.Transport.(*http.Transport)
	t2, ok2 := c2.Transport.(*http.Transport)
	if !ok1 || !ok2 {
		t.Fatalf("expected *http.Transport on both clients (got %T and %T)", c1.Transport, c2.Transport)
	}
	if t1 == t2 {
		t.Fatalf("expected distinct *http.Transport instances, got the same pointer: %p", t1)
	}
}

func TestClientTimeoutFieldIsSet(t *testing.T) {
	t.Parallel()

	cases := []time.Duration{
		1 * time.Second,
		10 * time.Second,
		30 * time.Second,
		2 * time.Minute,
	}
	for _, want := range cases {
		want := want
		t.Run(want.String(), func(t *testing.T) {
			t.Parallel()
			c := Client(want)
			if c.Timeout != want {
				t.Fatalf("Client(%v).Timeout = %v, want %v", want, c.Timeout, want)
			}
		})
	}
}

// isTimeout returns true if err looks like a wall-clock deadline / I/O
// timeout from net/http. Covers both the net.Error{Timeout()} idiom
// and the modern errors.Is(err, os.ErrDeadlineExceeded) form.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	// Fallback: net/http occasionally surfaces "context deadline
	// exceeded" wrapped in url.Error without the Timeout() method
	// being preserved through every wrap layer.
	return strings.Contains(err.Error(), "deadline exceeded") ||
		strings.Contains(err.Error(), "Client.Timeout")
}
