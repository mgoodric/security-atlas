package ratelimit

import (
	"fmt"
	"math/rand"
	"net/http/httptest"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

func TestPerMinute_BurstThenRefill(t *testing.T) {
	t.Parallel()
	now := t0
	l := PerMinute(6, func() time.Time { return now })
	for i := 0; i < 6; i++ {
		if ok, wait := l.Allow("p"); !ok {
			t.Fatalf("request %d refused (wait %v); the whole minute's budget may arrive at once", i, wait)
		}
	}
	if ok, wait := l.Allow("p"); ok || wait != 10*time.Second {
		t.Fatalf("7th request = (%v, %v); want refused, 10s", ok, wait)
	}
	// A refused request does not push the next slot further out.
	if ok, wait := l.Allow("p"); ok || wait != 10*time.Second {
		t.Fatalf("8th request = (%v, %v); want refused, 10s", ok, wait)
	}
	now = now.Add(10 * time.Second)
	if ok, _ := l.Allow("p"); !ok {
		t.Fatal("after 10s: refused; want admitted")
	}
	if ok, _ := l.Allow("p"); ok {
		t.Fatal("second request after 10s admitted; only one token refilled")
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := PerMinute(1, func() time.Time { return t0 })
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a refused")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b refused")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a's second request admitted at 1/min")
	}
}

// A burst of one is a cooldown window: one request, then nothing until the
// interval has fully passed.
func TestNew_BurstOneIsACooldown(t *testing.T) {
	t.Parallel()
	now := t0
	l := New(time.Minute, 1, func() time.Time { return now })
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("first refused")
	}
	now = now.Add(time.Minute - time.Nanosecond)
	if ok, wait := l.Allow("ip"); ok || wait != time.Nanosecond {
		t.Fatalf("just inside the window = (%v, %v); want refused, 1ns", ok, wait)
	}
	now = now.Add(time.Nanosecond)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("at the window edge: refused; want admitted")
	}
}

// A bucket that never reaches one token admits nothing, and reports the
// time a full refill to one token would take.
func TestNew_FractionalBurstBelowOneAdmitsNothing(t *testing.T) {
	t.Parallel()
	l := PerSecond(0.25, 0.5, func() time.Time { return t0 })
	if ok, wait := l.Allow("k"); ok || wait != 2*time.Second {
		t.Fatalf("Allow = (%v, %v); want refused, 2s", ok, wait)
	}
}

// refBucket is the float token bucket the evidence and OAuth handlers used
// before #2497: start full, refill rate tokens/sec up to capacity, take one.
type refBucket struct {
	tokens float64
	last   time.Time
	seen   bool
}

func (b *refBucket) take(now time.Time, rate, capacity float64) (bool, time.Duration) {
	if !b.seen {
		b.tokens, b.last, b.seen = capacity, now, true
	}
	b.tokens = min(capacity, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// The GCRA limiter admits exactly what the old float token bucket admitted,
// on random traffic, for the rate/burst shapes the callers use.
func TestAllow_MatchesFloatTokenBucket(t *testing.T) {
	t.Parallel()
	shapes := []struct{ rate, burst float64 }{
		{1, 2},      // evidence push at 1/s
		{10, 20},    // evidence push at 10/s
		{1, 60},     // OAuth 60/min
		{0.1, 6},    // control evaluate 6/min
		{0.25, 0.5}, // a bucket that never reaches one token
	}
	for _, s := range shapes {
		t.Run(fmt.Sprintf("rate=%v,burst=%v", s.rate, s.burst), func(t *testing.T) {
			rng := rand.New(rand.NewSource(1))
			now := t0
			l := PerSecond(s.rate, s.burst, func() time.Time { return now })
			ref := map[string]*refBucket{}
			for i := 0; i < 5000; i++ {
				// Gaps on a 1ms grid keep float drift well under the
				// grid, so the two must agree exactly on admission.
				now = now.Add(time.Duration(rng.Intn(int(2000/s.rate))) * time.Millisecond)
				key := fmt.Sprintf("k%d", rng.Intn(3))
				if ref[key] == nil {
					ref[key] = &refBucket{}
				}
				wantOK, wantWait := ref[key].take(now, s.rate, s.burst)
				gotOK, gotWait := l.Allow(key)
				if gotOK != wantOK {
					t.Fatalf("step %d key %s: admitted=%v, reference %v", i, key, gotOK, wantOK)
				}
				if d := gotWait - wantWait; d > time.Microsecond || d < -time.Microsecond {
					t.Fatalf("step %d key %s: wait=%v, reference %v", i, key, gotWait, wantWait)
				}
			}
		})
	}
}

func TestAllow_PrunesIdleKeysPastTheFloor(t *testing.T) {
	t.Parallel()
	now := t0
	l := PerMinute(60, func() time.Time { return now })
	for i := 0; i < PruneFloor; i++ {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	// Still draining: nothing is dropped.
	l.Allow("live")
	if got := l.Len(); got != PruneFloor+1 {
		t.Fatalf("len before refill = %d, want %d", got, PruneFloor+1)
	}
	// After a full refill every existing key is idle. Add keys until the
	// next sweep fires; only the post-refill keys survive it.
	now = now.Add(61 * time.Second)
	for added := 1; added <= 4*PruneFloor; added++ {
		prev := l.Len()
		l.Allow(fmt.Sprintf("n%d", added))
		if got := l.Len(); got < prev {
			if got != added {
				t.Fatalf("len after prune = %d, want %d (only post-refill keys)", got, added)
			}
			return
		}
	}
	t.Fatalf("no prune after %d new keys", 4*PruneFloor)
}

func TestAllow_RefusedKeyIsNotStored(t *testing.T) {
	t.Parallel()
	l := PerSecond(0.25, 0.5, func() time.Time { return t0 })
	l.Allow("k")
	if got := l.Len(); got != 0 {
		t.Fatalf("Len = %d; want 0 (a refused new key stores nothing)", got)
	}
}

func TestConstructors_PanicOnNonPositive(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"New interval": func() { New(0, 1, nil) },
		"New burst":    func() { New(time.Second, 0, nil) },
		"PerMinute":    func() { PerMinute(0, nil) },
		"PerSecond":    func() { PerSecond(-1, 1, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			f()
		})
	}
}

func TestNew_NilClockUsesWallClock(t *testing.T) {
	t.Parallel()
	if ok, _ := New(time.Hour, 1, nil).Allow("k"); !ok {
		t.Fatal("first request refused")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]int{
		0:                             1,
		time.Millisecond:              1,
		time.Second:                   1,
		time.Second + time.Nanosecond: 2,
		10 * time.Second:              10,
	}
	for in, want := range cases {
		if got := RetryAfterSeconds(in); got != want {
			t.Fatalf("RetryAfterSeconds(%v) = %d; want %d", in, got, want)
		}
	}
}

func TestSetRetryAfter(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	SetRetryAfter(w, 30)
	if got := w.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q; want 30", got)
	}
}
