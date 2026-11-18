package controlimport

import (
	"context"
	"testing"
	"time"
)

// queue starts a waiter for tenant and returns a channel that yields its
// release func once it is granted.
func queue(t *testing.T, r *runSlots, tenant string) <-chan func() {
	t.Helper()
	waiting := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.waiters)
	}
	before := waiting()
	got := make(chan func(), 1)
	go func() {
		if release, ok := r.acquire(context.Background(), tenant, 0); ok {
			got <- release
		}
	}()
	// Let the waiter register before the next one, so arrival order is fixed.
	deadline := time.Now().Add(2 * time.Second)
	for waiting() == before {
		if time.Now().After(deadline) {
			t.Fatalf("%s's waiter never queued", tenant)
		}
		time.Sleep(time.Millisecond)
	}
	return got
}

func granted(ch <-chan func()) (func(), bool) {
	select {
	case f := <-ch:
		return f, true
	case <-time.After(50 * time.Millisecond):
		return nil, false
	}
}

// #2329: tenant B, queued behind tenant A's backlog, takes the next slot
// that frees.
func TestRunSlotsNextSlotGoesToTheTenantHoldingFewest(t *testing.T) {
	t.Parallel()
	r := newRunSlots(2)
	a1, _ := r.acquire(context.Background(), "A", 0)
	a2, _ := r.acquire(context.Background(), "A", 0)
	a3 := queue(t, r, "A")
	a4 := queue(t, r, "A")
	b1 := queue(t, r, "B")

	a1()
	if _, ok := granted(a3); ok {
		t.Fatal("A's queued run took the slot ahead of B")
	}
	relB, ok := granted(b1)
	if !ok {
		t.Fatal("B was not granted the freed slot")
	}
	a2()
	relA3, ok := granted(a3)
	if !ok {
		t.Fatal("A's next run was not granted once B held a slot")
	}
	relB()
	if _, ok := granted(a4); !ok {
		t.Fatal("A's last run was not granted")
	}
	relA3()
}

// With one slot, the waiting tenants take turns.
func TestRunSlotsSingleSlotRoundRobin(t *testing.T) {
	t.Parallel()
	r := newRunSlots(1)
	held, _ := r.acquire(context.Background(), "A", 0)
	a2 := queue(t, r, "A")
	a3 := queue(t, r, "A")
	b1 := queue(t, r, "B")

	order := []struct {
		name string
		ch   <-chan func()
	}{{"B", b1}, {"A", a2}, {"A", a3}}
	for _, want := range order {
		held()
		var ok bool
		if held, ok = granted(want.ch); !ok {
			t.Fatalf("expected %s to be granted next", want.name)
		}
	}
	held()
}

// A waiter that gives up leaves the queue, and a tenant alone uses every slot.
func TestRunSlotsTimeoutAndWorkConserving(t *testing.T) {
	t.Parallel()
	r := newRunSlots(2)
	a1, ok1 := r.acquire(context.Background(), "A", 0)
	a2, ok2 := r.acquire(context.Background(), "A", 0)
	if !ok1 || !ok2 {
		t.Fatal("a tenant alone could not take both slots")
	}
	if _, ok := r.acquire(context.Background(), "B", 10*time.Millisecond); ok {
		t.Fatal("acquire succeeded with no slot free")
	}
	r.mu.Lock()
	n := len(r.waiters)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d waiters left after a timeout, want 0", n)
	}
	a1()
	a1() // a second release is a no-op
	a2()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inUse != 0 || len(r.held) != 0 {
		t.Fatalf("inUse %d held %v after every release, want none", r.inUse, r.held)
	}
}
