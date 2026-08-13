package tui

// The kiosk sends stdout to the physical TTY and stderr to the journal.
// Consequently, returning an error from an interactive path is not enough:
// systemd restarts firn and the user sees page one with no explanation.  This
// small view is the common last stop for failures that happen outside the
// install progress view.

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type errorModel struct {
	title string
	err   error
	width int
}

func (m errorModel) Init() tea.Cmd { return nil }

func (m errorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		return m, nil
	}
	if _, ok := msg.(tea.KeyMsg); ok {
		return m, tea.Quit
	}
	return m, nil
}

func (m errorModel) View() string {
	width := m.width
	if width == 0 {
		width = 80
	}
	textWidth := max(10, width-1)
	return fmt.Sprintf("%s\n\n%s\n\n%s\n",
		errStyle.Render(ansi.Wrap(m.title, textWidth, "/:.-")),
		ansi.Wrap(m.err.Error(), textWidth, "/:.-"),
		dimStyle.Render(ansi.Wrap("press any key to return to the installer", textWidth, "/:.-")))
}

// HoldError displays err on the TTY until the user acknowledges it, then
// returns the original error so the CLI still exits non-zero and journals it.
func HoldError(ctx context.Context, title string, err error) error {
	if err == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, _ = tea.NewProgram(errorModel{title: title, err: err, width: 80}, tea.WithContext(ctx)).Run()
	return err
}
