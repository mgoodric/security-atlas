package main

import (
	"context"
	"fmt"
	"sync"
)

// runGroup drives the run loop's subsystem goroutines. #2781: before it, a
// subsystem that failed (gRPC failing to bind, say) recorded its error and
// returned while every other loop kept running, so the process stayed up
// half-dead until SIGTERM. A fatal subsystem's first error now cancels the
// shared context, everything drains, and main exits 1 so the supervisor
// restarts it. A non-fatal subsystem's error is only recorded; those loops
// log each failed sweep and retry on their next tick on their own.
//
// Which subsystem is which is Matt's decision of 2026-11-20, recorded in
// docs/audit-log/2781-fatal-subsystems-decisions.md.
type runGroup struct {
	wg     sync.WaitGroup
	errs   errCollector
	cancel context.CancelCauseFunc
}

// newRunGroup derives the run loop's context from parent. The returned
// context is cancelled, with the error as its cause, by the first fatal
// subsystem to fail.
func newRunGroup(parent context.Context) (*runGroup, context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	return &runGroup{cancel: cancel}, ctx
}

// fatal runs fn in its own goroutine. A non-nil error is recorded as
// "name: err" and cancels the group's context.
func (g *runGroup) fatal(name string, fn func() error) {
	g.goRun(name, true, fn)
}

// nonFatal runs fn in its own goroutine. A non-nil error is recorded as
// "name: err" and leaves the other subsystems running.
func (g *runGroup) nonFatal(name string, fn func() error) {
	g.goRun(name, false, fn)
}

func (g *runGroup) goRun(name string, fatal bool, fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := fn(); err != nil {
			err = fmt.Errorf("%s: %w", name, err)
			g.errs.add(err)
			if fatal {
				g.cancel(err)
			}
		}
	}()
}

// add tracks a goroutine that reports no error (the backup loops), so wait
// still waits for it.
func (g *runGroup) add(fn func()) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		fn()
	}()
}

// wait blocks until every subsystem has returned, releases the context, and
// returns the collected errors in the order they were recorded.
func (g *runGroup) wait() []error {
	g.wg.Wait()
	g.cancel(nil)
	return g.errs.all()
}
