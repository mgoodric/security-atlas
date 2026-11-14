package auth

// Unit tests for #1913: /auth/local/login is throttled per IP and per
// account, answering 429 with Retry-After before any argon2id work, and the
// account bucket does not depend on whether the account exists.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func TestLoginLimiter_BurstThenRetryAfterThenRefill(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	l := newLoginLimiter(10, 5, clk.Now) // one token per 12s

	for i := range 10 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d refused within burst", i+1)
		}
	}
	ok, retry := l.Allow("k")
	if ok || retry != 12 {
		t.Fatalf("over burst: ok=%v retry=%d, want false 12", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("a different key shares the bucket")
	}
	clk.Advance(11 * time.Second)
	if ok, retry := l.Allow("k"); ok || retry != 1 {
		t.Fatalf("after 11s: ok=%v retry=%d, want false 1", ok, retry)
	}
	clk.Advance(1 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("refused after a full refill interval")
	}
}

func TestLoginLimiter_PrunesRefilledBuckets(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	l := newLoginLimiter(1, 60, clk.Now)
	for i := range loginBucketPruneFloor {
		l.Allow(fmt.Sprint(i))
	}
	clk.Advance(time.Second) // every bucket is full again
	l.Allow("fresh")
	if n := len(l.buckets); n != 1 {
		t.Fatalf("buckets after prune = %d, want 1", n)
	}
}

func TestLoginAccountKey_CaseAndSpaceInsensitive(t *testing.T) {
	t.Parallel()
	tid := uuid.New()
	if loginAccountKey(tid, " Alice@Example.COM ") != loginAccountKey(tid, "alice@example.com") {
		t.Fatal("case or whitespace variants get separate buckets")
	}
	if loginAccountKey(tid, "a@x.io") == loginAccountKey(uuid.New(), "a@x.io") {
		t.Fatal("same email in two tenants shares a bucket")
	}
}

func throttledHandler(clk *fakeClock) *Handler {
	return &Handler{loginThrottle: newLoginThrottle(clk.Now)}
}

func postLogin(h *Handler, remote, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(body))
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.LocalLogin(rec, req)
	return rec
}

func TestLocalLogin_PerIPThrottle429(t *testing.T) {
	t.Parallel()
	h := throttledHandler(newFakeClock())
	// Malformed bodies stop at 400 after the IP bucket, so no store is needed.
	for i := range loginIPBurst {
		if rec := postLogin(h, "198.51.100.7:1234", "{"); rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: status %d, want 400", i+1, rec.Code)
		}
	}
	rec := postLogin(h, "198.51.100.7:1234", "{")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("over IP burst: status %d Retry-After %q, want 429 1", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := postLogin(h, "203.0.113.9:1234", "{"); rec.Code != http.StatusBadRequest {
		t.Fatalf("another IP: status %d, want 400", rec.Code)
	}
}

// A drained account bucket answers 429 before VerifyLocalLogin (the handler
// has no user store, so reaching it would panic). The key is built from the
// request, so the answer is the same whether or not the email exists.
func TestLocalLogin_PerAccountThrottle429BeforeVerify(t *testing.T) {
	t.Parallel()
	h := throttledHandler(newFakeClock())
	tid := uuid.New()
	for range loginAccountBurst {
		h.loginThrottle.account.Allow(loginAccountKey(tid, "victim@example.com"))
	}
	body := fmt.Sprintf(`{"tenant_id":%q,"email":"VICTIM@example.com","password":"x"}`, tid)
	rec := postLogin(h, "198.51.100.7:1234", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "12" {
		t.Fatalf("Retry-After %q, want 12", got)
	}
	if !strings.Contains(rec.Body.String(), "too many login attempts") {
		t.Fatalf("body %q", rec.Body.String())
	}
}
