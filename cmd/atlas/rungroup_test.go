package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockUntilDone stands in for a subsystem loop: it runs until ctx ends.
func blockUntilDone(ctx context.Context) func() error {
	return func() error {
		<-ctx.Done()
		return nil
	}
}

// TestRunGroupFatalErrorEndsRunLoop: #2781. A fatal subsystem's error
// cancels the shared context, so the other loops drain and wait returns
// promptly with the error, and the context's cause names it.
func TestRunGroupFatalErrorEndsRunLoop(t *testing.T) {
	g, ctx := newRunGroup(context.Background())
	g.fatal("http", blockUntilDone(ctx))
	g.nonFatal("digest scheduler", blockUntilDone(ctx))
	g.add(func() { <-ctx.Done() })
	g.fatal("grpc", func() error { return errBoom })

	got := waitWithin(t, g, 5*time.Second)
	if len(got) != 1 || !errors.Is(got[0], errBoom) || got[0].Error() != "grpc: boom" {
		t.Fatalf("errors = %v, want [grpc: boom]", got)
	}
	if cause := context.Cause(ctx); cause == nil || cause.Error() != "grpc: boom" {
		t.Fatalf("context cause = %v, want grpc: boom", cause)
	}
}

// TestRunGroupNonFatalErrorKeepsRunning: a non-fatal subsystem's error is
// recorded but does not cancel the context; the other loops keep running
// until the parent (the signal context) ends.
func TestRunGroupNonFatalErrorKeepsRunning(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	g, ctx := newRunGroup(parent)
	nonFatalDone := make(chan struct{})
	g.nonFatal("roster sync", func() error {
		defer close(nonFatalDone)
		return errBoom
	})
	g.fatal("http", blockUntilDone(ctx))

	<-nonFatalDone
	select {
	case <-ctx.Done():
		t.Fatalf("context cancelled by a non-fatal error: %v", context.Cause(ctx))
	case <-time.After(200 * time.Millisecond):
	}

	stop()
	got := waitWithin(t, g, 5*time.Second)
	if len(got) != 1 || got[0].Error() != "roster sync: boom" {
		t.Fatalf("errors = %v, want [roster sync: boom]", got)
	}
}

// TestRunGroupCleanShutdownHasNoErrors: a signal-driven shutdown where every
// subsystem returns nil collects nothing, so main exits 0.
func TestRunGroupCleanShutdownHasNoErrors(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	g, ctx := newRunGroup(parent)
	g.fatal("grpc", blockUntilDone(ctx))
	g.nonFatal("expirer", blockUntilDone(ctx))
	stop()
	if got := waitWithin(t, g, 5*time.Second); len(got) != 0 {
		t.Fatalf("errors = %v, want none", got)
	}
}

func waitWithin(t *testing.T, g *runGroup, d time.Duration) []error {
	t.Helper()
	done := make(chan []error, 1)
	go func() { done <- g.wait() }()
	select {
	case got := <-done:
		return got
	case <-time.After(d):
		t.Fatal("wait did not return: the run loop did not drain")
		return nil
	}
}
