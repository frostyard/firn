// Package progress defines firn's progress event model — the single
// source of truth consumed in-process by the TUI and serialized as
// NDJSON for external consumers, per docs/specs/progress-protocol.md
// (protocol version 1).
package progress

import "errors"

// Protocol is the progress protocol version this package implements.
const Protocol = 1

var (
	ErrEmitterClosed   = errors.New("progress: emitter is closed")
	ErrAfterTerminal   = errors.New("progress: event emitted after terminal event")
	ErrStreamTruncated = errors.New("progress: stream closed without a terminal event")
)

// Step describes one assembled pipeline step, announced in Start.
type Step struct {
	Name   string `json:"name"`
	Weight int    `json:"weight"`
}

// Event is implemented by every progress event. Kind returns the
// spec's `event` type tag.
type Event interface{ Kind() string }

// Start is the first event, exactly once per run.
type Start struct {
	Protocol int    `json:"protocol"`
	Firn     string `json:"firn"`
	Steps    []Step `json:"steps"`
}

// StepStart announces a step beginning; Index is 0-based into
// Start.Steps and strictly increasing.
type StepStart struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

// StepProgress is optional fine-grained progress within a step. TotalBytes > 0
// selects byte mode; otherwise Fraction is authoritative, including zero.
type StepProgress struct {
	Index      int     `json:"index"`
	Fraction   float64 `json:"fraction"`
	Bytes      int64   `json:"bytes"`
	TotalBytes int64   `json:"total_bytes"`
}

// Info is human-readable narration with no machine meaning.
type Info struct {
	Message string `json:"message"`
}

// Warning is a non-fatal degradation with a stable code.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SummaryItem is one thing the user must know before Done.
type SummaryItem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Summary is emitted once when non-empty, immediately before Done or Error.
type Summary struct {
	Items []SummaryItem `json:"items"`
}

// RecoveryKey is the deliberate disclosure of a generated recovery
// key — the only event ever permitted to contain a secret.
type RecoveryKey struct {
	Key string `json:"key"`
}

// Done is the successful terminal event.
type Done struct {
	OK bool `json:"ok"`
}

// Error is the fatal terminal event.
type Error struct {
	Step    string `json:"step"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (Start) Kind() string        { return "start" }
func (StepStart) Kind() string    { return "step_start" }
func (StepProgress) Kind() string { return "step_progress" }
func (Info) Kind() string         { return "info" }
func (Warning) Kind() string      { return "warning" }
func (Summary) Kind() string      { return "summary" }
func (RecoveryKey) Kind() string  { return "recovery_key" }
func (Done) Kind() string         { return "done" }
func (Error) Kind() string        { return "error" }

// Emitter receives events. Implementations are NDJSON for external consumers
// and Channel for the TUI's in-process view.
type Emitter interface {
	Emit(Event) error
}

// EmitterFunc adapts a function to Emitter. It is used by renderers that
// cannot fail while keeping every pipeline path on the same checked contract.
type EmitterFunc func(Event) error

func (f EmitterFunc) Emit(event Event) error { return f(event) }

// Warning and error codes stable enough to be part of the contract are
// collected here as they are introduced (spec rule 3).
const (
	CodeStepFailed         = "step_failed"
	CodeCleanupFailed      = "cleanup_failed"
	CodeImageVerifyFailed  = "image_verification_failed"
	CodeImageVerifyRetried = "image_verification_retried"
	CodeNoTPM              = "no_tpm"
	CodeFlatpakUnreachable = "flatpak_unreachable"
	CodeGroupMissing       = "group_missing"
	CodeNoCoreSet          = "no_core_set"
	CodeStoreUnmountFailed = "store_umount_failed"
	CodeStoreCleanupFailed = "store_cleanup_failed"
	CodeStreamTruncated    = "stream_truncated"
)

func isTerminal(e Event) bool {
	switch e.(type) {
	case Done, Error:
		return true
	default:
		return false
	}
}
