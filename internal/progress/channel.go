package progress

import "sync"

// Channel is the in-process emitter feeding the TUI (ADR-0007): the
// pipeline goroutine calls Emit and Close, the TUI receives from
// Events. It satisfies Emitter alongside NDJSON; Emit never returns a
// non-nil error.
//
// Ownership: the sender (pipeline) side calls Emit and, when the run
// is over, Close. Protocol-order violations return errors rather than
// panicking, so callers can fail closed without crashing the installer.
type Channel struct {
	mu       sync.Mutex
	ch       chan Event
	closed   bool
	terminal bool
}

// NewChannel returns a Channel whose stream is buffered to buf events.
func NewChannel(buf int) *Channel {
	return &Channel{ch: make(chan Event, buf)}
}

// Emit delivers e to the receive side in order. It blocks when the
// buffer is full and the receiver has not caught up. Events after a terminal
// event or Close are rejected and never delivered.
func (c *Channel) Emit(e Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrEmitterClosed
	}
	if c.terminal {
		return ErrAfterTerminal
	}
	c.ch <- e
	if isTerminal(e) {
		c.terminal = true
	}
	return nil
}

// Events returns the receive side of the stream. It is closed by
// Close after any buffered events are drained by the receiver.
func (c *Channel) Events() <-chan Event { return c.ch }

// Close ends the stream. Safe to call more than once. It reports a truncated
// producer stream when no terminal event was emitted before the first close.
func (c *Channel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.ch)
	}
	if !c.terminal {
		return ErrStreamTruncated
	}
	return nil
}
