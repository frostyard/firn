// Package tui is firn's only frontend (ADR-0007,
// docs/adr/0007-tui-only-frontend-single-binary.md): a terminal wizard
// and install view built on the Charm stack, serving displays and
// serial consoles with the same code. It consumes pipeline progress
// events in-process over a channel (internal/progress) and must stay
// legible at 80×24 — ANSI-safe adaptive colors only, never
// truecolor-only styling.
package tui

import (
	"context"
	"fmt"
	"strings"

	pbar "github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/frostyard/firn/internal/progress"
)

// InstallResult is what the install view learned from the event
// stream by the time it exited.
type InstallResult struct {
	Done         bool
	Failed       bool
	FailedStep   string
	ErrorMessage string
	RecoveryKey  string
	Summary      []progress.SummaryItem
}

// RunInstall renders a running install from events until a terminal
// event (Done/Error) arrives or the channel closes. ctx bounds the
// UI's lifetime; cancel must abort the *pipeline's* context (a child
// of ctx, not ctx itself, or cancellation would kill the UI before
// cleanup finishes). Ctrl+C calls cancel and keeps the view alive —
// showing "cleaning up" — until the pipeline reports its terminal
// event; the install is never orphaned.
func RunInstall(ctx context.Context, events <-chan progress.Event, cancel context.CancelFunc) (InstallResult, error) {
	p := tea.NewProgram(newInstallModel(events, cancel), tea.WithContext(ctx))
	final, err := p.Run()
	m, ok := final.(installModel)
	if !ok {
		return InstallResult{}, err
	}
	return m.result, err
}

// ANSI-safe styles: indexed colors 0–15 and terminal attributes only,
// so serial consoles and 16-color terminals render them faithfully.
var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "3", Dark: "11"})
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "1", Dark: "9"})
	okStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "2", Dark: "10"})
)

// tailLen is how many recent Info/Warning lines the view keeps.
const tailLen = 5

type tailLine struct {
	warning bool
	text    string
}

// eventMsg delivers one pipeline event into the bubbletea loop.
type eventMsg struct{ event progress.Event }

// channelClosedMsg reports the event channel closing (pipeline gone).
type channelClosedMsg struct{}

// waitForEvent blocks on the channel and hands the next event (or its
// closing) to Update; each delivery re-arms the read.
func waitForEvent(ch <-chan progress.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return channelClosedMsg{}
		}
		return eventMsg{event: e}
	}
}

type installModel struct {
	events <-chan progress.Event
	cancel context.CancelFunc

	bar         pbar.Model
	width       int
	steps       []progress.Step
	totalWeight int
	// currentIndex is -1 until the first StepStart.
	currentIndex int
	currentName  string
	stepFraction float64
	tail         []tailLine

	gated     bool // recovery-key screen is blocking the view
	canceling bool // Ctrl+C seen; waiting for the pipeline to stop
	finished  bool // terminal event seen (or channel closed)
	result    InstallResult
}

func newInstallModel(events <-chan progress.Event, cancel context.CancelFunc) installModel {
	bar := pbar.New(pbar.WithSolidFill("6"), pbar.WithoutPercentage())
	bar.Width = 60
	return installModel{
		events:       events,
		cancel:       cancel,
		bar:          bar,
		width:        80,
		currentIndex: -1,
	}
}

func (m installModel) Init() tea.Cmd { return waitForEvent(m.events) }

func (m installModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.bar.Width = min(msg.Width-10, 60)
		if m.bar.Width < 10 {
			m.bar.Width = 10
		}
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.finished && !m.gated {
				return m, tea.Quit
			}
			// Abort the pipeline but never orphan it: stay alive
			// until its terminal event (or channel close) arrives.
			if !m.canceling {
				m.canceling = true
				if m.cancel != nil {
					m.cancel()
				}
			}
			return m, nil
		}
		if m.gated && msg.Type == tea.KeyEnter {
			m.gated = false
			if m.finished {
				return m, tea.Quit
			}
			return m, nil
		}
		return m, nil

	case eventMsg:
		return m.handleEvent(msg.event)

	case channelClosedMsg:
		m.finished = true
		if m.gated {
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m installModel) handleEvent(e progress.Event) (tea.Model, tea.Cmd) {
	switch e := e.(type) {
	case progress.Start:
		m.steps = e.Steps
		m.totalWeight = 0
		for _, s := range e.Steps {
			m.totalWeight += s.Weight
		}

	case progress.StepStart:
		m.currentIndex = e.Index
		m.currentName = e.Name
		m.stepFraction = 0

	case progress.StepProgress:
		if e.Index == m.currentIndex {
			switch {
			case e.Fraction > 0:
				m.stepFraction = min(e.Fraction, 1)
			case e.TotalBytes > 0:
				m.stepFraction = min(float64(e.Bytes)/float64(e.TotalBytes), 1)
			}
		}

	case progress.Info:
		m.tail = pushTail(m.tail, tailLine{text: e.Message})

	case progress.Warning:
		m.tail = pushTail(m.tail, tailLine{warning: true, text: e.Message})

	case progress.Summary:
		m.result.Summary = e.Items

	case progress.RecoveryKey:
		m.result.RecoveryKey = e.Key
		m.gated = true

	case progress.Done:
		m.result.Done = true
		return m.finish()

	case progress.Error:
		m.result.Failed = true
		m.result.FailedStep = e.Step
		m.result.ErrorMessage = e.Message
		return m.finish()
	}
	return m, waitForEvent(m.events)
}

// finish records the terminal event; if the recovery-key gate is up,
// quitting waits for its acknowledging keypress.
func (m installModel) finish() (tea.Model, tea.Cmd) {
	m.finished = true
	if m.gated {
		return m, nil
	}
	return m, tea.Quit
}

func pushTail(tail []tailLine, l tailLine) []tailLine {
	tail = append(tail, l)
	if len(tail) > tailLen {
		tail = tail[len(tail)-tailLen:]
	}
	return tail
}

// overallFraction folds Start's step weights with the current step's
// fine-grained fraction into one overall completion figure.
func (m installModel) overallFraction() float64 {
	if m.result.Done {
		return 1
	}
	if m.totalWeight == 0 || m.currentIndex < 0 {
		return 0
	}
	done := 0
	for i, s := range m.steps {
		if i >= m.currentIndex {
			break
		}
		done += s.Weight
	}
	current := 0.0
	if m.currentIndex < len(m.steps) {
		current = m.stepFraction * float64(m.steps[m.currentIndex].Weight)
	}
	return (float64(done) + current) / float64(m.totalWeight)
}

func (m installModel) View() string {
	if m.gated {
		return m.recoveryView()
	}
	if m.finished {
		return m.finalView()
	}
	return m.runningView()
}

func (m installModel) runningView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("firn install"))
	b.WriteString("\n\n")

	f := m.overallFraction()
	fmt.Fprintf(&b, "%s %3.0f%%\n", m.bar.ViewAs(f), f*100)

	if m.currentIndex >= 0 {
		fmt.Fprintf(&b, "step %d/%d: %s\n", m.currentIndex+1, len(m.steps), m.currentName)
	} else {
		b.WriteString(dimStyle.Render("waiting for pipeline…"))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for _, l := range m.tail {
		if l.warning {
			b.WriteString(warnStyle.Render("warning: " + l.text))
		} else {
			b.WriteString(dimStyle.Render(l.text))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if m.canceling {
		b.WriteString(warnStyle.Render("cancelled — cleaning up… (waiting for the install to stop safely)"))
	} else {
		b.WriteString(dimStyle.Render("ctrl+c to cancel"))
	}
	b.WriteString("\n")
	return b.String()
}

// recoveryView blocks everything else. The key itself is printed
// completely unstyled — no color, no borders — so a serial console
// can never garble it.
func (m installModel) recoveryView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("RECOVERY KEY — WRITE THIS DOWN"))
	b.WriteString("\n\n\n")
	b.WriteString("    " + m.result.RecoveryKey + "\n\n\n")
	b.WriteString("This key unlocks your encrypted disk if all other credentials are lost.\n")
	b.WriteString("It is shown only once and is not stored anywhere else.\n\n")
	b.WriteString(titleStyle.Render("Press Enter to confirm: I have saved it"))
	b.WriteString("\n")
	return b.String()
}

func (m installModel) finalView() string {
	var b strings.Builder
	if m.result.Failed {
		b.WriteString(errStyle.Render("install failed"))
		b.WriteString("\n\n")
		if m.result.FailedStep != "" {
			fmt.Fprintf(&b, "step: %s\n", m.result.FailedStep)
		}
		fmt.Fprintf(&b, "%s\n", m.result.ErrorMessage)
	} else if m.result.Done {
		b.WriteString(okStyle.Render("install complete"))
		b.WriteString("\n")
	} else {
		b.WriteString(warnStyle.Render("install did not finish"))
		b.WriteString("\n")
	}
	if len(m.result.Summary) > 0 {
		b.WriteString("\n")
		for _, it := range m.result.Summary {
			fmt.Fprintf(&b, "  %s: %s\n", it.Code, it.Detail)
		}
	}
	return b.String()
}
