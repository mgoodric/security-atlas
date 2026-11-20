package main

import "sync"

// errCollector gathers the errors returned by the run loop's goroutines.
// #2770: it replaced a fixed-buffer channel (cap 2) that ~18 goroutines sent
// to unconditionally while nothing read it until wg.Wait returned; a third
// failure blocked its sender before wg.Done, so wg.Wait hung forever. add
// never blocks, so there is no buffer size to keep in step with the senders.
type errCollector struct {
	mu   sync.Mutex
	errs []error
}

// add records err; a nil err is ignored.
func (c *errCollector) add(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs = append(c.errs, err)
}

// all returns a copy of the collected errors, in the order they were added.
func (c *errCollector) all() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.errs...)
}
