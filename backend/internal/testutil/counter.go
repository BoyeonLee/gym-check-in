package testutil

import "sync/atomic"

// counter is a tiny goroutine-safe sequence used by uniquePhone (and other
// future factories) to avoid collisions across parallel tests.
type counter struct {
	v atomic.Int64
}

func newCounter(start int64) *counter {
	c := &counter{}
	c.v.Store(start)
	return c
}

func (c *counter) next() int64 { return c.v.Add(1) }
