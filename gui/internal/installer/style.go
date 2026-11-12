package installer

import (
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
)

// The installer draws frostyard's look with frostui (ADR-0022): the kit's
// Frostyard and Frostyard light themes, set in Inter, and its cards, pills,
// keys and buttons. What a wizard needs and the kit has no component for
// (choice cards, titled notes, the strength meter) is built here from the
// kit's colors and metrics, so it changes when the kit's look does.

// applyTheme sets the frame's theme, Frostyard or Frostyard light, as the
// user or the desktop chose, with the desktop's high contrast and text size
// but not its accent, and returns it. frostui.Adapt keeps the theme and
// registers Inter on its first call.
func (a *app) applyTheme(c *ui.Context) *ui.Theme {
	prefs := c.Preferences()
	prefs.Accent = ui.Color{}
	c.SetTheme(frostui.FrostyardTheme(a.dark(c)).WithPreferences(prefs))
	return frostui.Adapt(c)
}

// cardHeader titles a card with an icon tile.
func cardHeader(c *ui.Context, icon, title, subtitle string) {
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
		frostui.IconTile(c, icon, frostui.Accent, 36).Shrink(0)
		ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
			frostui.Heading(c, title)
			if subtitle != "" {
				frostui.Muted(c, subtitle)
			}
		})
	})
}

// choiceCard is a selectable card on a radio base: the arrows move among the
// cards of its radio group, and Space or a click chooses one. The chosen
// card takes the selected fill inside an accent hairline.
func choiceCard[T comparable](c *ui.Context, selected *T, value T, icon, title, body string, extra func()) ui.Element {
	t := c.Theme()
	k := frostui.ColorsOf(t)
	chosen := *selected == value
	card := ui.RadioBase(c, selected, value).Padding(14, 16).Gap(14).Radius(frostui.RadiusCard).AlignItems(ui.Start).
		Background(k.Panel).Border(1, t.Border).Label(title).Cursor(ui.CursorPointer).
		Transition(ui.ElementTransition{Colors: true, Duration: 140_000_000})
	switch {
	case chosen:
		card.Background(k.SelectedFill.Over(k.Panel)).Border(1, t.Accent)
	case card.Hovered():
		card.Border(1, frostui.ToneLine(t, frostui.Accent))
	}
	tone := frostui.Neutral
	if chosen {
		tone = frostui.Accent
	}
	card.Children(func() {
		frostui.IconTile(c, icon, tone, 36).Shrink(0)
		ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
			title := frostui.Heading(c, title)
			if chosen {
				title.TextColor(k.SelectedText)
			}
			if body != "" {
				frostui.Muted(c, body)
			}
			if extra != nil {
				extra()
			}
		})
		radioMark(c, chosen)
	})
	return card
}

// radioMark is the round mark at a choice card's end.
func radioMark(c *ui.Context, on bool) {
	t := c.Theme()
	mark := ui.Row(c).Size(20, 20).Radius(10).Center().Shrink(0)
	if on {
		mark.Background(t.Accent).Children(func() {
			frostui.Icon(c, iconCheck).FontSize(13).TextColor(t.AccentText)
		})
	} else {
		mark.Border(1.5, frostui.ToneLine(t, frostui.Neutral))
	}
}

// toneIcon is the icon a note in tone leads with.
func toneIcon(tone frostui.Tone) string {
	switch tone {
	case frostui.Warning:
		return iconWarning
	case frostui.Danger:
		return iconError
	case frostui.Success:
		return iconSuccess
	}
	return iconInfo
}

// callout is a note in a tone: the kit's banner, with a title over its text.
func callout(c *ui.Context, tone frostui.Tone, title, body string) ui.Element {
	t := c.Theme()
	color := frostui.ToneColor(t, tone)
	return ui.Row(c).Gap(frostui.SpaceM).Padding(frostui.SpaceM, frostui.SpaceL).Radius(frostui.RadiusChip).
		Background(frostui.ToneFill(t, tone)).Border(1, frostui.ToneLine(t, tone)).AlignItems(ui.Start).Children(func() {
		frostui.Icon(c, toneIcon(tone)).FontSize(frostui.TextSize(c, 17)).TextColor(color).Shrink(0)
		ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
			if title != "" {
				frostui.Strong(c, title).TextColor(color)
			}
			if body != "" {
				frostui.Body(c, body).FontSize(frostui.TextSize(c, 13))
			}
		})
	})
}

// tag is a small outlined name that keeps its case, as an image reference,
// a partition label or a Flatpak ID: frostui.Pill sets its text in capitals,
// which suits a status but would misquote a name.
func tag(c *ui.Context, text string, tone frostui.Tone, mono bool) ui.Element {
	t := c.Theme()
	color := frostui.ColorsOf(t).TagText
	if tone != frostui.Neutral {
		color = frostui.ToneColor(t, tone)
	}
	el := ui.Text(c, text).FontSize(frostui.TextSize(c, 12)).TextColor(color).SingleLine().
		Padding(2, 7).Radius(frostui.RadiusChip).Border(1, frostui.ToneLine(t, tone))
	if mono {
		el.Font("monospace")
	}
	return el
}

// gradientButton is the kit's primary button, on the brand gradient, with
// its icon after the label: a step forward. frostui.LabeledButton puts the
// icon first.
func gradientButton(c *ui.Context, label, icon string) ui.Element {
	k := frostui.ColorsOf(c.Theme())
	return ui.PrimaryButton(c, "").Label(label).
		LinearGradient(ui.LinearGradient{From: k.GradientFrom, To: k.GradientTo, Angle: 135}).TextColor(k.OnGradient).
		Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).SingleLine().FontWeight(600)
				frostui.Icon(c, icon)
			})
		})
}

// dangerButton is a primary button for an action that erases a disk, drawn
// as the kit draws a destructive confirmation: the danger tone's face,
// outline and text.
func dangerButton(c *ui.Context, label, icon string) ui.Element {
	t := c.Theme()
	return ui.PrimaryButton(c, "").Label(label).
		Background(frostui.ToneFill(t, frostui.Danger)).TextColor(t.Danger).Border(1, frostui.ToneLine(t, frostui.Danger)).
		Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if icon != "" {
					frostui.Icon(c, icon)
				}
				ui.Text(c, label).SingleLine().FontWeight(600)
			})
		})
}

// dialogPanel draws a dialog's backdrop and panel as the kit draws what
// floats over the window: the panel face inside a strong hairline, square
// and without a shadow.
func dialogPanel(c *ui.Context, backdrop, panel ui.Element, width float32, label string) {
	t := c.Theme()
	k := frostui.ColorsOf(t)
	backdrop.Background(ui.RGBA(2, 8, 15, 0.6))
	panel.Width(width).Padding(24).Gap(frostui.SpaceL).Radius(frostui.RadiusCard).Background(k.Panel).
		Border(1, k.LineStrong).Role(ui.RoleAlertDialog).Label(label)
}

// codeBlock shows monospace text that can be selected and copied. Long
// lines scroll sideways rather than wrap, so what is shown is byte-exact.
func codeBlock(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	return ui.Column(c).Radius(frostui.RadiusChip).Background(frostui.ColorsOf(t).Code).Border(1, t.Border).Clip().Children(func() {
		ui.ScrollHorizontal(c).Padding(12, 14).Selectable().Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(frostui.TextSize(c, 12.5)).LineHeight(1.5).NoWrap()
		})
	})
}

// strength rates a password from 0 (empty) to 4 (strong), as guidance only:
// the schema sets no rule.
func strength(pw string) int {
	if pw == "" {
		return 0
	}
	var lower, upper, digit, other bool
	for _, r := range pw {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			classes++
		}
	}
	n := len([]rune(pw))
	switch {
	case n >= 16 || (n >= 12 && classes >= 3):
		return 4
	case n >= 10 && classes >= 2:
		return 3
	case n >= 8:
		return 2
	default:
		return 1
	}
}

// strengthMeter shows strength as four segments and a word.
func strengthMeter(c *ui.Context, pw string) {
	s := strength(pw)
	if s == 0 {
		return
	}
	t := c.Theme()
	labels := []string{"", "Weak", "Fair", "Good", "Strong"}
	tones := []frostui.Tone{frostui.Neutral, frostui.Danger, frostui.Warning, frostui.Accent, frostui.Success}
	color := frostui.ToneColor(t, tones[s])
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(frostui.SpaceXS).Grow(1).Children(func() {
			for i := 1; i <= 4; i++ {
				seg := ui.Box(c).Height(4).Grow(1).Radius(frostui.RadiusChip).Background(frostui.ToneFill(t, frostui.Neutral))
				if i <= s {
					seg.Background(color)
				}
			}
		})
		ui.Text(c, labels[s]).FontSize(frostui.TextSize(c, 12)).FontWeight(600).TextColor(color).Width(52).TextAlign(ui.End)
	})
}

// ghostButton is a secondary button as the kit draws one: a hairline and no
// face, its text in the hover color while the pointer is over it.
func ghostButton(c *ui.Context, label string) ui.Element {
	b := ui.Button(c, label).Background(ui.Transparent)
	if b.Hovered() {
		b.TextColor(frostui.ColorsOf(c.Theme()).HoverText)
	}
	return b
}
