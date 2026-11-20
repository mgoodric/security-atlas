package exception

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2688: with a waitgroup attached, the detached Activate/Revoke
// re-evaluation is tracked by it, so a shutdown that waits on the group
// does not return while the evaluation is still running.
func TestAfterStateChange_TrackedByWaitGroup(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	finished := make(chan struct{})
	var wg sync.WaitGroup
	s := (&Store{}).WithReevaluate(func(context.Context, uuid.UUID, uuid.UUID) {
		<-release
		close(finished)
	}).WithWaitGroup(&wg)

	s.afterStateChange(context.Background(), Exception{})

	waited := make(chan struct{})
	go func() {
		wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("wg.Wait returned while the re-evaluation was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("wg.Wait did not return after the re-evaluation finished")
	}
	select {
	case <-finished:
	default:
		t.Fatal("wg.Wait returned before the re-evaluation finished")
	}
}

// Without a waitgroup the re-evaluation still runs, detached.
func TestAfterStateChange_RunsWithoutWaitGroup(t *testing.T) {
	t.Parallel()
	ran := make(chan struct{})
	s := (&Store{}).WithReevaluate(func(context.Context, uuid.UUID, uuid.UUID) { close(ran) })
	s.afterStateChange(context.Background(), Exception{})
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("re-evaluation did not run")
	}
}
