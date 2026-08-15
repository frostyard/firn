package progress

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestChannelPreservesOrder(t *testing.T) {
	c := NewChannel(8)
	sent := []Event{
		Start{Protocol: Protocol, Firn: "test", Steps: []Step{{Name: "a", Weight: 1}}},
		StepStart{Index: 0, Name: "a"},
		Info{Message: "one"},
		Warning{Code: CodeNoTPM, Message: "two"},
		Done{OK: true},
	}
	for _, e := range sent {
		if err := c.Emit(e); err != nil {
			t.Fatalf("Emit(%s) = %v, want nil", e.Kind(), err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v, want terminal stream", err)
	}

	var got []Event
	for e := range c.Events() {
		got = append(got, e)
	}
	if len(got) != len(sent) {
		t.Fatalf("received %d events, want %d", len(got), len(sent))
	}
	for i := range sent {
		if !reflect.DeepEqual(got[i], sent[i]) {
			t.Errorf("event %d = %#v, want %#v", i, got[i], sent[i])
		}
	}
}

func TestChannelCloseSemantics(t *testing.T) {
	c := NewChannel(4)
	if err := c.Emit(Info{Message: "buffered"}); err != nil {
		t.Fatalf("Emit = %v, want nil", err)
	}
	if err := c.Close(); !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("Close = %v, want ErrStreamTruncated", err)
	}

	// Buffered events are still drainable after Close, then the
	// channel reports closed.
	e, ok := <-c.Events()
	if !ok {
		t.Fatal("channel closed before buffered event was drained")
	}
	if inf, isInfo := e.(Info); !isInfo || inf.Message != "buffered" {
		t.Fatalf("drained %#v, want Info{buffered}", e)
	}
	select {
	case _, ok := <-c.Events():
		if ok {
			t.Fatal("received unexpected extra event")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after Close")
	}

	// Close is idempotent.
	if err := c.Close(); !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("second Close = %v, want ErrStreamTruncated", err)
	}
}

func TestChannelEmitAfterCloseDoesNotPanic(t *testing.T) {
	c := NewChannel(1)
	_ = c.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Emit after Close panicked: %v", r)
		}
	}()
	if err := c.Emit(Info{Message: "late"}); !errors.Is(err, ErrEmitterClosed) {
		t.Fatalf("Emit after Close = %v, want ErrEmitterClosed", err)
	}
}

func TestChannelRejectsEventsAfterTerminal(t *testing.T) {
	c := NewChannel(2)
	if err := c.Emit(Done{OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Emit(Error{Code: CodeStepFailed, Message: "duplicate"}); !errors.Is(err, ErrAfterTerminal) {
		t.Fatalf("duplicate terminal error = %v, want ErrAfterTerminal", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Event
	for event := range c.Events() {
		got = append(got, event)
	}
	if len(got) != 1 {
		t.Fatalf("delivered %d events, want only terminal", len(got))
	}
}

// Channel must satisfy the same Emitter contract as NDJSON.
var _ Emitter = (*Channel)(nil)
