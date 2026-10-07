package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/runner"
)

const previewDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// previewRunner serves skopeo inspections whose core label depends on the
// image ref; refs missing from labels fail as unreachable. inspected counts
// skopeo invocations.
func previewRunner(labels map[string]string, inspected *int) *runner.Runner {
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "skopeo" {
				return nil, errors.New("unexpected " + name)
			}
			*inspected++
			ref := args[1][strings.Index(args[1], ":")+1:]
			ref = strings.TrimPrefix(ref, "//")
			value, ok := labels[ref]
			if !ok {
				return nil, errors.New("network unreachable")
			}
			body := `{"Digest":"` + previewDigest + `","Labels":{"containers.bootc":"1"`
			if value != "ABSENT" {
				body += `,"` + flatpak.CoreLabel + `":"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
			}
			return []byte(body + `}}`), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

const (
	snowRef   = "ghcr.io/frostyard/snow:latest"
	sundogRef = "ghcr.io/frostyard/sundog:latest"
	floeRef   = "ghcr.io/frostyard/floe:latest"
	brokenRef = "ghcr.io/frostyard/broken:latest"
	emptyRef  = "ghcr.io/frostyard/empty:latest"
)

var previewLabels = map[string]string{
	snowRef:   `{"version":1,"flatpaks":[{"id":"org.gnome.Loupe","name":"Image Viewer"},{"id":"org.mozilla.firefox","name":"Firefox"}]}`,
	sundogRef: `{"version":1,"flatpaks":[{"id":"org.kde.okular","name":"Okular"}]}`,
	floeRef:   "ABSENT",
	brokenRef: `{"version":2,"flatpaks":[]}`,
	emptyRef:  `{"version":1,"flatpaks":[]}`,
}

func previewWizard(inspected *int) *wizard {
	return &wizard{opts: WizardOpts{Runner: previewRunner(previewLabels, inspected)}}
}

func TestCorePreviewStates(t *testing.T) {
	for _, tc := range []struct {
		ref    string
		state  corePreviewState
		names  []string
		cached bool
	}{
		{ref: snowRef, state: corePreviewAvailable, names: []string{"Image Viewer", "Firefox"}, cached: true},
		{ref: floeRef, state: corePreviewNone, cached: true},
		{ref: emptyRef, state: corePreviewNone, cached: true},
		{ref: brokenRef, state: corePreviewInvalid, cached: true},
		{ref: "ghcr.io/frostyard/offline:latest", state: corePreviewUnavailable, cached: false},
	} {
		var inspected int
		w := previewWizard(&inspected)
		w.c.entry = CatalogEntry{Name: "x", Ref: tc.ref}
		mustRefresh(t, w)
		if w.preview.state != tc.state {
			t.Fatalf("%s: state = %v, want %v (detail %q)", tc.ref, w.preview.state, tc.state, w.preview.detail)
		}
		var names []string
		for _, app := range w.preview.apps {
			names = append(names, app.Name)
		}
		if strings.Join(names, ",") != strings.Join(tc.names, ",") {
			t.Fatalf("%s: names = %v, want %v", tc.ref, names, tc.names)
		}
		before := inspected
		mustRefresh(t, w)
		if reused := inspected == before; reused != tc.cached {
			t.Fatalf("%s: second visit reused the preview = %v, want %v", tc.ref, reused, tc.cached)
		}
	}
}

// Choosing another image (going back, or Start over) re-inspects, and a
// hidden toggle never leaves core_flatpaks set for the new image.
func TestCorePreviewFollowsImageChangesAndClearsHiddenToggle(t *testing.T) {
	var inspected int
	w := previewWizard(&inspected)

	w.c.entry = CatalogEntry{Name: "snow", Ref: snowRef}
	mustRefresh(t, w)
	w.flatpaksForm()
	w.c.coreFlatpaks = true // the user accepts Snow's set

	w.c.entry = CatalogEntry{Name: "floe", Ref: floeRef}
	mustRefresh(t, w)
	w.flatpaksForm()
	if w.preview.state != corePreviewNone || w.c.coreFlatpaks {
		t.Fatalf("after switching to floe: state = %v, coreFlatpaks = %v, want none and false",
			w.preview.state, w.c.coreFlatpaks)
	}

	w.c.entry = CatalogEntry{Name: "broken", Ref: brokenRef}
	w.c.coreFlatpaks = true
	mustRefresh(t, w)
	w.flatpaksForm()
	if w.preview.state != corePreviewInvalid || w.c.coreFlatpaks {
		t.Fatalf("after switching to a malformed label: state = %v, coreFlatpaks = %v",
			w.preview.state, w.c.coreFlatpaks)
	}

	w.c.entry = CatalogEntry{Name: "sundog", Ref: sundogRef}
	mustRefresh(t, w)
	w.c.coreFlatpaks = true
	w.flatpaksForm()
	if w.preview.state != corePreviewAvailable || !w.c.coreFlatpaks {
		t.Fatalf("an offered toggle must keep its answer: state = %v, coreFlatpaks = %v",
			w.preview.state, w.c.coreFlatpaks)
	}
}

func TestFlatpaksFormRendersEachPreviewState(t *testing.T) {
	for _, tc := range []struct {
		ref     string
		want    []string
		notWant []string
	}{
		{ref: snowRef, want: []string{"core app set?", "Image Viewer, Firefox"}},
		{ref: floeRef, want: []string{"No core app set", "publishes no core Flatpak apps"}, notWant: []string{"core app set?"}},
		{ref: brokenRef, want: []string{"Core app set unavailable", "malformed"}, notWant: []string{"core app set?"}},
		{ref: "ghcr.io/frostyard/offline:latest", want: []string{"core app set?", "read", "install time"}},
	} {
		var inspected int
		w := previewWizard(&inspected)
		w.c.entry = CatalogEntry{Name: "x", Ref: tc.ref}
		mustRefresh(t, w)
		m := newPageModel(w, w.flatpaksForm())
		pumpKeys(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
		view := m.View()
		for _, s := range tc.want {
			if !strings.Contains(view, s) {
				t.Errorf("%s: view lacks %q:\n%s", tc.ref, s, view)
			}
		}
		for _, s := range tc.notWant {
			if strings.Contains(view, s) {
				t.Errorf("%s: view shows %q:\n%s", tc.ref, s, view)
			}
		}
	}
}

// With no core set the page still completes: Enter on the explicit-apps
// field submits it.
func TestFlatpaksFormWithoutCoreSetCompletes(t *testing.T) {
	var inspected int
	w := previewWizard(&inspected)
	w.c.entry = CatalogEntry{Name: "floe", Ref: floeRef}
	mustRefresh(t, w)
	m := newPageModel(w, w.flatpaksForm())
	pumpKeys(t, m, keyEnter, keyEnter)
	if m.form.State != huh.StateCompleted {
		t.Fatalf("form state = %v, want completed; view:\n%s", m.form.State, m.View())
	}
}

// An inspection that succeeds without readable metadata is unknown, not
// "no core set": the toggle stays and the next visit retries.
func TestCorePreviewUnreadableInspectionKeepsToggle(t *testing.T) {
	r := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			if strings.HasPrefix(args[1], "docker://") {
				return []byte("not json"), nil
			}
			return nil, errors.New("not cached")
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	w := &wizard{opts: WizardOpts{Runner: r}}
	w.c.entry = CatalogEntry{Name: "snow", Ref: snowRef}
	w.c.coreFlatpaks = true
	mustRefresh(t, w)
	w.flatpaksForm()
	if w.preview.state != corePreviewUnavailable || w.preview.ref != "" || !w.c.coreFlatpaks {
		t.Fatalf("state = %v, ref = %q, coreFlatpaks = %v; want unavailable, retry, answer kept",
			w.preview.state, w.preview.ref, w.c.coreFlatpaks)
	}
}

func TestWrapNames(t *testing.T) {
	apps := []flatpak.CoreApp{{Name: "Alpha"}, {Name: "Beta"}, {Name: "Gamma"}}
	if got, want := wrapNames(apps, 12), "Alpha, Beta,\nGamma"; got != want {
		t.Fatalf("wrapNames = %q, want %q", got, want)
	}
	// Width counts display cells, not bytes: "Éditeur" is 7 cells, 8 bytes.
	accented := []flatpak.CoreApp{{Name: "Éditeur"}, {Name: "Beta"}}
	if got, want := wrapNames(accented, 13), "Éditeur, Beta"; got != want {
		t.Fatalf("wrapNames(non-ASCII) = %q, want %q", got, want)
	}
}

func TestFirstLineStripsControlCharacters(t *testing.T) {
	if got, want := firstLine("bad \x1b[2Jregistry\tsays\nsecond line"), "bad ?[2Jregistry?says"; got != want {
		t.Fatalf("firstLine = %q, want %q", got, want)
	}
}

func mustRefresh(t *testing.T, w *wizard) {
	t.Helper()
	if quit, err := w.refreshCorePreview(context.Background()); quit || err != nil {
		t.Fatalf("refreshCorePreview = quit %v, err %v", quit, err)
	}
}

// The wizard shows progress through showProgress, and an abort there quits
// without touching the previous preview.
func TestRefreshCorePreviewUsesProgressAndPropagatesQuit(t *testing.T) {
	var inspected int
	w := previewWizard(&inspected)
	w.c.entry = CatalogEntry{Name: "snow", Ref: snowRef}
	var titles []string
	w.showProgress = func(ctx context.Context, title string, inspect func(context.Context) corePreview) (corePreview, bool, error) {
		titles = append(titles, title)
		return inspect(ctx), false, nil
	}
	mustRefresh(t, w)
	if len(titles) != 1 || !strings.Contains(titles[0], "snow") || w.preview.state != corePreviewAvailable {
		t.Fatalf("titles = %v, state = %v", titles, w.preview.state)
	}

	w.c.entry = CatalogEntry{Name: "floe", Ref: floeRef}
	w.showProgress = func(context.Context, string, func(context.Context) corePreview) (corePreview, bool, error) {
		return corePreview{}, true, nil
	}
	quit, err := w.refreshCorePreview(context.Background())
	if !quit || err != nil {
		t.Fatalf("aborted refresh = quit %v, err %v; want quit", quit, err)
	}
	if w.preview.ref != snowRef {
		t.Fatalf("an aborted refresh replaced the preview: %+v", w.preview)
	}
}

func TestInspectingModel(t *testing.T) {
	m := &inspectingModel{spin: spinner.New(), title: "Reading snow's core app list…",
		inspect: func() corePreview { return corePreview{ref: snowRef, state: corePreviewNone} }}
	if !strings.Contains(m.View(), "Reading snow's core app list") {
		t.Fatalf("view = %q", m.View())
	}
	// Keys typed while waiting are swallowed, not queued for the next page.
	if _, cmd := m.Update(keyEnter); cmd != nil {
		t.Fatalf("Enter while inspecting returned a command")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("Ctrl-C while inspecting did not interrupt")
	} else if _, ok := cmd().(tea.InterruptMsg); !ok {
		t.Fatalf("Ctrl-C produced %T, want tea.InterruptMsg", cmd())
	}
	msg := previewDoneMsg{m.inspect()}
	_, cmd := m.Update(msg)
	if !m.done || m.result.state != corePreviewNone || m.View() != "" {
		t.Fatalf("after completion: done=%v result=%+v view=%q", m.done, m.result, m.View())
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("completion produced %T, want tea.QuitMsg", cmd())
	}
}
