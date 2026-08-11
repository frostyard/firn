package progress

import "sync"

// Channel is the in-process emitter feeding the TUI (ADR-0007): the
// pipeline goroutine calls Emit and Close, the TUI receives from
// Events. It satisfies Emitter alongside NDJSON; Emit never returns a
// non-nil error.
//
// Ownership: the sender (pipeline) side calls Emit and, when the run
// is over, Close. Emit after Close is a silent no-op rather than a
// panic, so a straggling emit during teardown cannot crash the
// installer.
type Channel struct {
	mu     sync.Mutex
	ch     chan Event
	closed bool
}

// NewChannel returns a Channel whose stream is buffered to buf events.
func NewChannel(buf int) *Channel {
	return &Channel{ch: make(chan Event, buf)}
}

// Emit delivers e to the receive side in order. It blocks when the
// buffer is full and the receiver has not caught up. After Close it
// drops the event. The returned error is always nil (Emitter shape).
func (c *Channel) Emit(e Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.ch <- e
	return nil
}

// Events returns the receive side of the stream. It is closed by
// Close after any buffered events are drained by the receiver.
func (c *Channel) Events() <-chan Event { return c.ch }

// Close ends the stream. Safe to call more than once.
func (c *Channel) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.ch)
	}
}
