package evidence

// #2497 — pins the push limiter's behaviour across the move onto
// internal/api/ratelimit: per credential, rate r/s with burst 2r, 429 with
// code rate_limited, and Retry-After rounded to the nearest second.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestPushHTTP_RateLimitPerCredential(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	h := NewHTTPHandler(nil, 0.5)
	h.limiter = newPushLimiter(0.5, func() time.Time { return now })

	// An admitted request with an empty body stops at 400 before
	// touching the ingest service, which is nil here.
	push := func(credID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/evidence:push", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{ID: credID}))
		w := httptest.NewRecorder()
		h.PushHTTP(w, r)
		return w
	}

	// 0.5/s with burst 2 x 0.5 = 1: one push, then a 2s wait.
	if got := push("a").Code; got != http.StatusBadRequest {
		t.Fatalf("first push = %d; want 400 (admitted)", got)
	}
	w := push("a")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second push = %d; want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q; want 2", got)
	}
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != "rate_limited" {
		t.Fatalf("429 body = %s (%v); want code rate_limited", w.Body.String(), err)
	}
	if got := push("b").Code; got != http.StatusBadRequest {
		t.Fatalf("other credential = %d; want 400 (own bucket)", got)
	}

	// Retry-After rounds to the nearest second, not up: at +0.75s the
	// wait is 1.25s, advertised as 1.
	now = now.Add(750 * time.Millisecond)
	if got := push("a").Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After at +0.75s = %q; want 1", got)
	}
	now = now.Add(1250 * time.Millisecond)
	if got := push("a").Code; got != http.StatusBadRequest {
		t.Fatalf("push at +2s = %d; want 400 (admitted)", got)
	}
}

// The burst is twice the rate: at 2/s, four pushes arrive at once.
func TestPushHTTP_BurstIsTwiceTheRate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	l := newPushLimiter(2, func() time.Time { return now })
	for i := 0; i < 4; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("push %d refused; burst is 4", i)
		}
	}
	if ok, wait := l.Allow("a"); ok || wait != 500*time.Millisecond {
		t.Fatalf("fifth push = (%v, %v); want refused, 500ms", ok, wait)
	}
}

func TestNewPushLimiter_NonPositiveRateDisables(t *testing.T) {
	t.Parallel()
	for _, rate := range []float64{0, -1} {
		if l := newPushLimiter(rate, nil); l != nil {
			t.Fatalf("rate %v: limiter = %v; want nil (disabled)", rate, l)
		}
		if h := NewHTTPHandler(nil, rate); h.limiter != nil {
			t.Fatalf("rate %v: handler limiter set; want disabled", rate)
		}
	}
}
