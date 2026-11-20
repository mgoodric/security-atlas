package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestErrCollectorManyConcurrentFailuresDoNotHang mirrors main's run loop:
// a WaitGroup of goroutines that each report a failure into the collector
// before wg.Done. #2770: with the old cap-2 channel the third failure
// blocked its sender, so wg.Wait never returned. Every goroutine is released
// at once so all senders race; the test fails on timeout or a lost error.
func TestErrCollectorManyConcurrentFailuresDoNotHang(t *testing.T) {
	const senders = 18
	var (
		wg    sync.WaitGroup
		errs  errCollector
		start = make(chan struct{})
	)
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs.add(fmt.Errorf("worker %d: %w", i, errBoom))
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wg.Wait did not return: a goroutine is blocked reporting its error")
	}

	got := errs.all()
	if len(got) != senders {
		t.Fatalf("collected %d errors, want %d: %v", len(got), senders, got)
	}
	seen := make(map[string]bool, senders)
	for _, err := range got {
		if !errors.Is(err, errBoom) {
			t.Errorf("error %q does not wrap errBoom", err)
		}
		seen[err.Error()] = true
	}
	for i := range senders {
		if msg := fmt.Sprintf("worker %d: %v", i, errBoom); !seen[msg] {
			t.Errorf("missing error %q", msg)
		}
	}
}

// TestErrCollectorIgnoresNil: a nil add is not a failure, and all on an
// empty collector is empty (main's exit-1 path keys off its length).
func TestErrCollectorIgnoresNil(t *testing.T) {
	var errs errCollector
	errs.add(nil)
	if got := errs.all(); len(got) != 0 {
		t.Fatalf("all() = %v, want empty", got)
	}
}

var errBoom = errors.New("boom")
