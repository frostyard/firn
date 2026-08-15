package progress

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// NDJSON serializes events per the progress protocol spec: one JSON
// object per line with an `event` type tag, nothing else on the
// stream. Safe for concurrent use.
type NDJSON struct {
	mu       sync.Mutex
	w        io.Writer
	terminal bool
}

// NewNDJSON returns an emitter writing to w (stdout in --json-progress
// mode; the caller owns keeping other output off that stream).
func NewNDJSON(w io.Writer) *NDJSON { return &NDJSON{w: w} }

// maxLine is the spec's per-line ceiling (rule 5).
const maxLine = 64 * 1024

func (n *NDJSON) Emit(e Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.terminal {
		return ErrAfterTerminal
	}
	// Tag-then-payload: marshal the event struct, then splice in the
	// `event` tag so each type's fields stay declared on the type.
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("progress: marshal %s: %w", e.Kind(), err)
	}
	tag, _ := json.Marshal(e.Kind())
	line := make([]byte, 0, len(payload)+len(tag)+11)
	line = append(line, `{"event":`...)
	line = append(line, tag...)
	if string(payload) != "{}" {
		line = append(line, ',')
		line = append(line, payload[1:]...)
	} else {
		line = append(line, '}')
	}
	line = append(line, '\n')
	if len(line) > maxLine {
		return fmt.Errorf("progress: %s event exceeds %d-byte line limit", e.Kind(), maxLine)
	}

	if _, err := n.w.Write(line); err != nil {
		return fmt.Errorf("progress: write: %w", err)
	}
	if isTerminal(e) {
		n.terminal = true
	}
	return nil
}
