package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/flatpak"
)

// corePreviewTimeout bounds the flatpaks page's image inspection, so an
// unreachable registry delays the page instead of hanging it.
const corePreviewTimeout = 30 * time.Second

// corePreviewState is what the flatpaks page knows about the chosen image's
// core Flatpak set (ADR-0018). The zero value, unavailable, keeps the
// toggle: install-time preflight reads the label again either way.
type corePreviewState int

const (
	corePreviewUnavailable corePreviewState = iota // inspect failed or not attempted
	corePreviewNone                                // image publishes no core set
	corePreviewInvalid                             // label present but malformed
	corePreviewAvailable                           // valid, non-empty set
)

// corePreview describes the core set of the image whose ref it records. It
// is display-only: the recipe carries just core_flatpaks, and preflight
// reads the label from the image it selects at install time.
type corePreview struct {
	ref    string
	state  corePreviewState
	apps   []flatpak.CoreApp
	detail string
}

// refreshCorePreview inspects the chosen image when the preview describes a
// different one (first visit, a new image, or Start over), using the same
// local-first selection preflight uses, without signature verification.
// quit reports that the user aborted while it ran.
func (w *wizard) refreshCorePreview(ctx context.Context) (quit bool, err error) {
	ref := w.c.entry.Ref
	if w.preview.ref == ref && ref != "" {
		return false, nil
	}
	inspect := func(ctx context.Context) corePreview { return inspectCorePreview(ctx, w, ref) }
	if w.showProgress == nil {
		w.preview = inspect(ctx)
		return false, nil
	}
	preview, quit, err := w.showProgress(ctx, "Reading "+w.c.entry.Name+"'s core app list…", inspect)
	if quit || err != nil {
		return quit, err
	}
	w.preview = preview
	return false, nil
}

// progressFunc runs inspect while showing title, returning its result.
// quit reports a user abort.
type progressFunc func(ctx context.Context, title string, inspect func(context.Context) corePreview) (preview corePreview, quit bool, err error)

// showInspecting draws a spinner while inspect runs, so a slow registry
// reads as progress rather than a hung installer, and keystrokes typed
// meanwhile are consumed here instead of reaching the flatpaks form. Like
// page, it renders to stdout (the kiosk routes stderr to the journal).
func showInspecting(ctx context.Context, title string, inspect func(context.Context) corePreview) (corePreview, bool, error) {
	ictx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := &inspectingModel{
		spin:    spinner.New(spinner.WithSpinner(spinner.Line)),
		title:   title,
		inspect: func() corePreview { return inspect(ictx) },
	}
	_, err := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	).Run()
	if ctx.Err() != nil {
		return corePreview{}, false, ctx.Err()
	}
	if errors.Is(err, tea.ErrInterrupted) {
		return corePreview{}, true, nil
	}
	if err != nil {
		return corePreview{}, false, err
	}
	return m.result, false, nil
}

// inspectingModel is the spinner shown while the preview inspection runs.
type inspectingModel struct {
	spin    spinner.Model
	title   string
	inspect func() corePreview
	done    bool
	result  corePreview
}

type previewDoneMsg struct{ preview corePreview }

func (m *inspectingModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg { return previewDoneMsg{m.inspect()} })
}

func (m *inspectingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case previewDoneMsg:
		m.result, m.done = msg.preview, true
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc {
			return m, tea.Interrupt
		}
		return m, nil // swallow keys typed while waiting
	}
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

func (m *inspectingModel) View() string {
	if m.done {
		return ""
	}
	return m.spin.View() + " " + m.title + "\n"
}

func inspectCorePreview(ctx context.Context, w *wizard, ref string) corePreview {
	p := corePreview{ref: ref}
	if w.opts.Runner == nil || ref == "" {
		p.detail = "no image inspector"
		return p
	}
	ictx, cancel := context.WithTimeout(ctx, corePreviewTimeout)
	defer cancel()
	source, err := bootcimg.CheckAndPinImage(ictx, w.opts.Runner, ref, "", nil)
	if err == nil && !source.Inspected {
		err = errors.New("image inspection returned no readable metadata")
	}
	if err != nil {
		// Keep the next visit retrying rather than caching a transient
		// failure such as a slow or offline registry.
		p.ref = ""
		p.detail = firstLine(err.Error())
		return p
	}
	value, present := source.Labels[flatpak.CoreLabel]
	apps, err := flatpak.ParseCoreLabelApps(value, present)
	switch {
	case errors.Is(err, flatpak.ErrMalformedCoreLabel):
		p.state = corePreviewInvalid
		p.detail = firstLine(err.Error())
	case err != nil:
		p.ref = ""
		p.detail = firstLine(err.Error())
	case len(apps) == 0:
		p.state = corePreviewNone
	default:
		p.state = corePreviewAvailable
		p.apps = apps
	}
	return p
}

// firstLine returns s's first line with non-printable runes replaced, so
// registry or skopeo text cannot put escape sequences on the console.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, line)
}

// wrapNames joins app names into lines no wider than width display cells.
func wrapNames(apps []flatpak.CoreApp, width int) string {
	var lines []string
	var line string
	for i, app := range apps {
		item := app.Name
		if i < len(apps)-1 {
			item += ","
		}
		switch {
		case line == "":
			line = item
		case lipgloss.Width(line)+1+lipgloss.Width(item) > width:
			lines = append(lines, line)
			line = item
		default:
			line += " " + item
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
