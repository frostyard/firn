package installer

import (
	"bytes"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
)

// The installer draws frostui's Frostyard look (ADR-0022): the kit's theme
// for the appearance the user or the desktop picks, in Inter, with the
// brand's accent rather than the desktop's.
func TestFrostyardLook(t *testing.T) {
	desktopAccent := ui.Hex("#e01b24")
	cases := []struct {
		name        string
		appearance  string
		desktopDark bool
		wantDark    bool
	}{
		{"follows a light desktop", "", false, false},
		{"follows a dark desktop", "", true, true},
		{"--appearance light over a dark desktop", "light", true, false},
		{"--appearance dark over a light desktop", "dark", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestApp(t, Options{Appearance: tc.appearance})
			tt := ui.NewTester(a.view, 1120, 760)
			tt.SetDark(tc.desktopDark)
			tt.SetPreferences(ui.Preferences{Accent: desktopAccent})
			tt.Frame()
			want := frostui.FrostyardTheme(tc.wantDark)
			switch {
			case a.t == nil:
				t.Fatal("the view set no theme")
			case a.t.Dark != tc.wantDark:
				t.Errorf("dark = %v, want %v", a.t.Dark, tc.wantDark)
			case a.t.Background != want.Background:
				t.Errorf("background = %v, want Frostyard's %v", a.t.Background, want.Background)
			case a.t.Accent != want.Accent:
				t.Errorf("accent = %v, want the brand's %v, not the desktop's", a.t.Accent, want.Accent)
			case a.t.Font != frostui.FontFamily:
				t.Errorf("font = %q, want %q", a.t.Font, frostui.FontFamily)
			}
		})
	}
}

// Every icon the installer ships parses, and frostui draws the installer's
// own in place of the desktop theme's, the names its components use
// included.
func TestIconsAreTheInstallers(t *testing.T) {
	useIcons()
	for name, data := range firnIcons {
		want, err := ui.ParseSVG(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := frostui.IconSVG(name)
		if got == nil {
			t.Errorf("%s: frostui finds no icon", name)
			continue
		}
		if !sameSVG(got, want) {
			t.Errorf("%s: frostui draws another icon than the installer's", name)
		}
	}
	for _, name := range []string{"window-close", "dialog-warning"} {
		if _, ok := firnIcons[name]; !ok {
			t.Errorf("the kit draws %q, which the installer does not ship", name)
		}
	}
}

// sameSVG compares two icons by how they draw at one size.
func sameSVG(a, b *ui.SVG) bool {
	draw := func(s *ui.SVG) []byte {
		tt := ui.NewTester(func(c *ui.Context) { ui.Icon(c, s).FontSize(24) }, 32, 32)
		tt.Frame()
		return tt.Image().Pix
	}
	return bytes.Equal(draw(a), draw(b))
}
