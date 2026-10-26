package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/progress"
)

type installPhase int

const (
	phaseIdle installPhase = iota
	phaseRunning
	phaseDone
	phaseFailed
)

// logLine is one narration or warning line of the activity log.
type logLine struct {
	warning bool
	code    string
	text    string
}

// installState is what the install page knows from the event stream. apply
// is a pure reducer so the page's behavior is testable without a pipeline.
type installState struct {
	phase       installPhase
	steps       []progress.Step
	totalWeight int
	// current is the running step's index, -1 before the first StepStart.
	current      int
	stepFraction float64
	stepBytes    int64
	stepTotal    int64
	log          []logLine
	summary      []progress.SummaryItem
	// recoveryKey is shown on a blocking acknowledgement and cleared once
	// acknowledged (progress protocol, recovery-key disclosure).
	recoveryKey string
	failedStep  string
	errCode     string
	errMessage  string
	canceling   bool
	started     time.Time
	finished    time.Time
	recipePath  string
}

// logLen is how many recent narration and warning lines the page keeps.
const logLen = 200

func newInstallState() installState { return installState{current: -1} }

func (s *installState) apply(e progress.Event) {
	switch ev := e.(type) {
	case progress.Start:
		s.steps = ev.Steps
		s.totalWeight = 0
		for _, st := range ev.Steps {
			s.totalWeight += st.Weight
		}
	case progress.StepStart:
		s.current = ev.Index
		s.stepFraction, s.stepBytes, s.stepTotal = 0, 0, 0
	case progress.StepProgress:
		if ev.Index == s.current {
			s.stepBytes, s.stepTotal = ev.Bytes, ev.TotalBytes
			if ev.TotalBytes > 0 {
				s.stepFraction = float64(ev.Bytes) / float64(ev.TotalBytes)
			} else {
				s.stepFraction = ev.Fraction
			}
			s.stepFraction = min(max(s.stepFraction, 0), 1)
		}
	case progress.Info:
		s.addLog(logLine{text: ev.Message})
	case progress.Warning:
		s.addLog(logLine{warning: true, code: ev.Code, text: warningText(ev.Code, ev.Message)})
	case progress.Summary:
		s.summary = append(s.summary, ev.Items...)
	case progress.RecoveryKey:
		s.recoveryKey = ev.Key
	case progress.Done:
		s.phase = phaseDone
		s.current = len(s.steps)
		s.finished = time.Now()
	case progress.Error:
		s.phase = phaseFailed
		s.failedStep, s.errCode, s.errMessage = ev.Step, ev.Code, ev.Message
		s.finished = time.Now()
	}
}

// truncated records a stream that ended without a terminal event: firn died,
// which the protocol says consumers render as failure, never cancellation.
func (s *installState) truncated() {
	if s.phase == phaseRunning {
		s.apply(progress.Error{Code: progress.CodeStreamTruncated, Message: "the install stopped without reporting an outcome"})
	}
}

func (s *installState) addLog(l logLine) {
	s.log = append(s.log, l)
	if len(s.log) > logLen {
		s.log = s.log[len(s.log)-logLen:]
	}
}

// overall is the weighted fraction of the whole install.
func (s *installState) overall() float64 {
	if s.phase == phaseDone {
		return 1
	}
	if s.totalWeight == 0 || s.current < 0 {
		return 0
	}
	done := 0
	for i := 0; i < s.current && i < len(s.steps); i++ {
		done += s.steps[i].Weight
	}
	f := float64(done)
	if s.current < len(s.steps) {
		f += s.stepFraction * float64(s.steps[s.current].Weight)
	}
	return min(f/float64(s.totalWeight), 1)
}

// currentName is the running step's name, or "".
func (s *installState) currentName() string {
	if s.current >= 0 && s.current < len(s.steps) {
		return s.steps[s.current].Name
	}
	return ""
}

// stepTitles are the steps as people would say them.
var stepTitles = map[string]string{
	"preflight-uefi":  "Checking the firmware",
	"preflight-tools": "Checking installer tools",
	"preflight-image": "Verifying the system image",
	"preflight-disk":  "Checking the target disk",
	"partition":       "Partitioning the disk",
	"format-boot":     "Creating the boot partition",
	"luks-format":     "Encrypting the disk",
	"format-root":     "Creating the root filesystem",
	"mount-target":    "Mounting the new system",
	"bootc-install":   "Installing the system image",
	"tpm-enroll":      "Binding the key to the TPM",
	"esp-stage":       "Staging the Secure Boot chain",
	"retag-root":      "Preparing the root partition",
	"flatpaks":        "Installing apps",
	"sysconfig":       "Applying your settings",
	"finalize":        "Finishing up",
	"mok-stage":       "Preparing key enrollment",
}

func stepTitle(name string) string {
	if t, ok := stepTitles[name]; ok {
		return t
	}
	return name
}

// warningText expands codes whose message alone is terse, keeping the code
// visible beside it (progress protocol, derived artifacts).
func warningText(code, message string) string {
	switch code {
	case progress.CodeFlatpakUnreachable:
		return "Could not reach a Flatpak app, so it was not installed: " + message
	case progress.CodeGroupMissing:
		return "A requested group does not exist in this image and was skipped: " + message
	case progress.CodeNoCoreSet:
		return "This image publishes no core app set: " + message
	}
	return message
}

// summaryTitle names a summary item's code for the outcome screen.
func summaryTitle(code string) string {
	switch code {
	case progress.CodeFlatpakUnreachable:
		return "App not installed"
	case progress.CodeGroupMissing:
		return "Group skipped"
	case progress.CodeNoCoreSet:
		return "No core apps"
	}
	return code
}

// humanBytes renders bytes in IEC units with one decimal.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// humanDuration renders an elapsed time as "4 min 12 s".
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m, s := int(d/time.Minute), int(d%time.Minute/time.Second)
	if m == 0 {
		return fmt.Sprintf("%d s", s)
	}
	return fmt.Sprintf("%d min %d s", m, s)
}

// reproduceCommand is the headless command that repeats this install.
func reproduceCommand(disk, path string) string {
	return strings.Join([]string{"firn install --confirm", disk, path}, " ")
}
