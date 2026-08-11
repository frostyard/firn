package progress

import (
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
	c.Close()

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
	c.Close()

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
	c.Close()
}

func TestChannelEmitAfterCloseDoesNotPanic(t *testing.T) {
	c := NewChannel(1)
	c.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Emit after Close panicked: %v", r)
		}
	}()
	if err := c.Emit(Info{Message: "late"}); err != nil {
		t.Fatalf("Emit after Close = %v, want nil", err)
	}
}

// Channel must satisfy the same Emitter contract as NDJSON.
var _ Emitter = (*Channel)(nil)
