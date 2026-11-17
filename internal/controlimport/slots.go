package controlimport

import (
	"context"
	"sync"
	"time"
)

// runSlots caps concurrent suggestion runs in one process and shares them
// fairly between tenants (#2329). When a slot frees it goes to the waiting
// run whose tenant holds the fewest slots; ties go to the tenant served least
// recently, then to the run that has waited longest. So a tenant with a
// backlog cannot keep another tenant waiting behind it: the other tenant's
// run takes the next slot that frees, whatever is queued ahead of it.
//
// It is work-conserving: there is no per-tenant cap, so a tenant alone on the
// process still uses every slot.
type runSlots struct {
	mu      sync.Mutex
	limit   int
	inUse   int
	held    map[string]int
	served  map[string]uint64 // tenant -> grant sequence of its last grant
	seq     uint64
	waiters []*slotWaiter // in arrival order
}

type slotWaiter struct {
	tenant  string
	ready   chan struct{}
	granted bool
}

func newRunSlots(n int) *runSlots {
	if n < 1 {
		n = 1
	}
	return &runSlots{limit: n, held: map[string]int{}, served: map[string]uint64{}}
}

// acquire waits for a slot for tenant's run. It returns the release func and
// true once a slot is granted, or false when ctx ends or, for a positive
// wait, when wait elapses first.
func (r *runSlots) acquire(ctx context.Context, tenant string, wait time.Duration) (func(), bool) {
	w := &slotWaiter{tenant: tenant, ready: make(chan struct{})}
	r.mu.Lock()
	r.waiters = append(r.waiters, w)
	r.dispatchLocked()
	r.mu.Unlock()

	var timeout <-chan time.Time
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-w.ready:
		return r.releaser(tenant), true
	case <-ctx.Done():
	case <-timeout:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if w.granted {
		// Granted while giving up: keep the slot rather than leak it.
		return r.releaser(tenant), true
	}
	for i, x := range r.waiters {
		if x == w {
			r.waiters = append(r.waiters[:i], r.waiters[i+1:]...)
			break
		}
	}
	return nil, false
}

func (r *runSlots) releaser(tenant string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.inUse--
			if r.held[tenant]--; r.held[tenant] <= 0 {
				delete(r.held, tenant)
			}
			r.dispatchLocked()
		})
	}
}

// dispatchLocked grants free slots to waiters in fairness order.
func (r *runSlots) dispatchLocked() {
	for r.inUse < r.limit && len(r.waiters) > 0 {
		best := 0
		for i := 1; i < len(r.waiters); i++ {
			if r.before(r.waiters[i], r.waiters[best]) {
				best = i
			}
		}
		w := r.waiters[best]
		r.waiters = append(r.waiters[:best], r.waiters[best+1:]...)
		r.inUse++
		r.held[w.tenant]++
		r.seq++
		r.served[w.tenant] = r.seq
		w.granted = true
		close(w.ready)
	}
}

// before reports whether a goes ahead of b. Waiters are scanned in arrival
// order, so equal ranks keep it.
func (r *runSlots) before(a, b *slotWaiter) bool {
	if ha, hb := r.held[a.tenant], r.held[b.tenant]; ha != hb {
		return ha < hb
	}
	return r.served[a.tenant] < r.served[b.tenant]
}
