// Package progress defines firn's progress event model — the single
// source of truth consumed in-process by the TUI and serialized as
// NDJSON for external consumers, per docs/specs/progress-protocol.md
// (protocol version 1).
package progress

// Protocol is the progress protocol version this package implements.
const Protocol = 1

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

// StepProgress is optional fine-grained progress within a step.
type StepProgress struct {
	Index      int     `json:"index"`
	Fraction   float64 `json:"fraction,omitempty"`
	Bytes      int64   `json:"bytes,omitempty"`
	TotalBytes int64   `json:"total_bytes,omitempty"`
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
	OK        bool   `json:"ok"`
	BootEntry string `json:"boot_entry,omitempty"`
}

// Error is the fatal terminal event.
type Error struct {
	Step    string `json:"step,omitempty"`
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

// Emitter receives events. Implementations: NDJSON (external
// consumers) and, later, the TUI's channel-backed view.
type Emitter interface {
	Emit(Event) error
}

// Warning and error codes stable enough to be part of the contract are
// collected here as they are introduced (spec rule 3).
const (
	CodeStepFailed         = "step_failed"
	CodeCleanupFailed      = "cleanup_failed"
	CodeImageVerifyFailed  = "image_verification_failed"
	CodeNoTPM              = "no_tpm"
	CodeFlatpakUnreachable = "flatpak_unreachable"
)
