package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestErrorModelHoldsMessageUntilKey(t *testing.T) {
	m := errorModel{title: "installer setup failed", err: errors.New("cannot list disks")}
	view := m.View()
	for _, want := range []string{"installer setup failed", "cannot list disks", "press any key"} {
		if !strings.Contains(view, want) {
			t.Errorf("error view missing %q:\n%s", want, view)
		}
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Fatal("non-key input must not dismiss error")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	requireQuit(t, cmd)
}

func TestErrorModelWrapsToTerminalWidth(t *testing.T) {
	m := errorModel{
		title: "installer recipe validation failed",
		err:   errors.New("security.mok: Secure Boot is active: mok must be enroll or skip"),
		width: 32,
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if got := ansi.StringWidth(line); got > 31 {
			t.Errorf("rendered line width = %d, want <= 31: %q", got, line)
		}
	}
}
