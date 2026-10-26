package installer

import "github.com/egoist/mygo/ui"

// palette is the installer's look in one appearance. The window follows the
// desktop's light or dark appearance unless the user picks one; the sidebar
// is the same deep blue in both.
type palette struct {
	dark bool

	window, card, cardBorder, subtle, inset ui.Color
	text, muted, faint                      ui.Color

	accent, accentSoft, accentText ui.Color
	success, successSoft           ui.Color
	warning, warningSoft           ui.Color
	danger, dangerSoft             ui.Color

	sideTop, sideBottom, sideText, sideMuted, sideActive, sideLine ui.Color
	shadow                                                         ui.Color
}

func newPalette(dark bool) palette {
	p := palette{
		dark:       dark,
		sideTop:    ui.Hex("#0b1d3a"),
		sideBottom: ui.Hex("#143b6b"),
		sideText:   ui.Hex("#f2f7ff"),
		sideMuted:  ui.RGBA(214, 228, 250, 0.62),
		sideActive: ui.RGBA(255, 255, 255, 0.13),
		sideLine:   ui.RGBA(255, 255, 255, 0.16),
	}
	if dark {
		p.window = ui.Hex("#0d1321")
		p.card = ui.Hex("#141c2e")
		p.cardBorder = ui.Hex("#24304a")
		p.subtle = ui.Hex("#1b253b")
		p.inset = ui.Hex("#0f1626")
		p.text = ui.Hex("#e7eef9")
		p.muted = ui.Hex("#9aabc4")
		p.faint = ui.Hex("#6a7c97")
		p.accent = ui.Hex("#5b9bff")
		p.accentSoft = ui.RGBA(91, 155, 255, 0.14)
		p.accentText = ui.Hex("#06142b")
		p.success = ui.Hex("#3fcf8e")
		p.successSoft = ui.RGBA(63, 207, 142, 0.14)
		p.warning = ui.Hex("#f5b14c")
		p.warningSoft = ui.RGBA(245, 177, 76, 0.13)
		p.danger = ui.Hex("#ff6b6b")
		p.dangerSoft = ui.RGBA(255, 107, 107, 0.14)
		p.shadow = ui.RGBA(0, 0, 0, 0.35)
	} else {
		p.window = ui.Hex("#f4f7fb")
		p.card = ui.Hex("#ffffff")
		p.cardBorder = ui.Hex("#dfe6f0")
		p.subtle = ui.Hex("#eef3f9")
		p.inset = ui.Hex("#f6f8fc")
		p.text = ui.Hex("#0f1b2d")
		p.muted = ui.Hex("#56677f")
		p.faint = ui.Hex("#8796ab")
		p.accent = ui.Hex("#2563eb")
		p.accentSoft = ui.RGBA(37, 99, 235, 0.08)
		p.accentText = ui.Hex("#ffffff")
		p.success = ui.Hex("#15924f")
		p.successSoft = ui.RGBA(21, 146, 79, 0.09)
		p.warning = ui.Hex("#b4630a")
		p.warningSoft = ui.RGBA(217, 119, 6, 0.10)
		p.danger = ui.Hex("#d22b2b")
		p.dangerSoft = ui.RGBA(210, 43, 43, 0.08)
		p.shadow = ui.RGBA(15, 27, 45, 0.07)
	}
	return p
}

// theme adapts the frame's theme so the built-in widgets match the palette.
func (p palette) theme(base ui.Theme) *ui.Theme {
	t := base
	t.Dark = p.dark
	t.Background = p.window
	t.Text = p.text
	t.TextMuted = p.muted
	t.Border = p.cardBorder
	t.Accent = p.accent
	t.AccentText = p.accentText
	t.Danger = p.danger
	t.Warning = p.warning
	t.Success = p.success
	t.Radius = 8
	if p.dark {
		t.Surface = ui.Hex("#1c2740")
		t.SurfaceHover = ui.Hex("#233050")
		t.SurfacePressed = ui.Hex("#2a395c")
		t.AccentHover = ui.Hex("#76acff")
		t.AccentPressed = ui.Hex("#4a86ea")
		t.Focus = ui.RGBA(91, 155, 255, 0.55)
	} else {
		t.Surface = ui.Hex("#eef2f8")
		t.SurfaceHover = ui.Hex("#e5ebf4")
		t.SurfacePressed = ui.Hex("#dbe3ef")
		t.AccentHover = ui.Hex("#1d4fd8")
		t.AccentPressed = ui.Hex("#1a46c0")
		t.Focus = ui.RGBA(37, 99, 235, 0.45)
	}
	return &t
}

// card is a raised panel grouping one part of a page.
func card(c *ui.Context, p palette) ui.Element {
	return ui.Column(c).Padding(20).Gap(14).Radius(14).Background(p.card).
		Border(1, p.cardBorder).Shadow(0, 1, 2, 0, p.shadow).Shadow(0, 8, 24, -12, p.shadow)
}

// cardHeader titles a card with an icon tile.
func cardHeader(c *ui.Context, p palette, ic *ui.SVG, title, subtitle string) {
	ui.Row(c).Gap(12).Children(func() {
		iconTile(c, p, ic, p.accent, p.accentSoft, 36)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(13).TextColor(p.muted)
			}
		})
	})
}

// iconTile is an icon centered on a tinted rounded square.
func iconTile(c *ui.Context, p palette, ic *ui.SVG, fg, bg ui.Color, size float32) ui.Element {
	return ui.Row(c).Size(size, size).Radius(size / 3.2).Background(bg).Center().Shrink(0).Children(func() {
		ui.Icon(c, ic).FontSize(size * 0.52).TextColor(fg)
	})
}

// choiceCard is a selectable card on a radio base: the arrows move among the
// cards of its radio group, and Space or a click chooses one.
func choiceCard[T comparable](c *ui.Context, p palette, selected *T, value T, ic *ui.SVG, title, body string, extra func()) ui.Element {
	chosen := *selected == value
	card := ui.RadioBase(c, selected, value).Padding(16).Gap(14).Radius(12).AlignItems(ui.Start).
		Background(p.card).Border(1, p.cardBorder).Label(title).Cursor(ui.CursorPointer).
		Transition(ui.ElementTransition{Colors: true, Duration: 140_000_000})
	if card.Hovered() && !chosen {
		card.Background(p.inset).Border(1, p.cardBorder.Mix(p.accent, 0.35))
	}
	if chosen {
		card.Background(p.accentSoft).Border(2, p.accent).Padding(15)
	}
	fg, bg := p.muted, p.subtle
	if chosen {
		fg, bg = p.accentText, p.accent
	}
	card.Children(func() {
		iconTile(c, p, ic, fg, bg, 40)
		ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600)
			if body != "" {
				ui.Text(c, body).FontSize(13).TextColor(p.muted)
			}
			if extra != nil {
				extra()
			}
		})
		radioMark(c, p, chosen)
	})
	return card
}

// radioMark is the round check mark at a choice card's end.
func radioMark(c *ui.Context, p palette, on bool) {
	mark := ui.Row(c).Size(22, 22).Radius(11).Center().Shrink(0)
	if on {
		mark.Background(p.accent).Children(func() {
			ui.Icon(c, iconCheck).FontSize(14).TextColor(p.accentText)
		})
	} else {
		mark.Border(2, p.cardBorder)
	}
}

type calloutKind int

const (
	calloutInfo calloutKind = iota
	calloutWarning
	calloutDanger
	calloutSuccess
)

// callout is a tinted note with an icon.
func callout(c *ui.Context, p palette, kind calloutKind, title, body string) ui.Element {
	fg, bg, ic := p.accent, p.accentSoft, iconInfo
	switch kind {
	case calloutWarning:
		fg, bg, ic = p.warning, p.warningSoft, iconWarning
	case calloutDanger:
		fg, bg, ic = p.danger, p.dangerSoft, iconError
	case calloutSuccess:
		fg, bg, ic = p.success, p.successSoft, iconSuccess
	}
	return ui.Row(c).Gap(12).Padding(12, 14).Radius(10).Background(bg).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, ic).FontSize(18).TextColor(fg).Shrink(0)
		ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
			if title != "" {
				ui.Text(c, title).FontWeight(600).TextColor(fg)
			}
			if body != "" {
				ui.Text(c, body).FontSize(13).TextColor(p.text)
			}
		})
	})
}

// chip is a small pill of text.
func chip(c *ui.Context, text string, fg, bg ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(12).FontWeight(500).TextColor(fg).
		Padding(3, 9).Radius(999).Background(bg).SingleLine()
}

// statusChip is a pill with a leading icon, for machine capabilities.
func statusChip(c *ui.Context, p palette, ic *ui.SVG, text string, ok bool) ui.Element {
	fg, bg := p.muted, p.subtle
	if ok {
		fg, bg = p.success, p.successSoft
	}
	return ui.Row(c).Gap(6).Padding(6, 12).Radius(999).Background(bg).Children(func() {
		ui.Icon(c, ic).FontSize(14).TextColor(fg)
		ui.Text(c, text).FontSize(13).FontWeight(500).TextColor(fg).SingleLine()
	})
}

// keycap draws one key of a shortcut.
func keycap(c *ui.Context, p palette, key string) ui.Element {
	return ui.Text(c, key).Font("monospace").FontSize(12).TextColor(p.text).SingleLine().
		Padding(2, 7).Radius(5).Background(p.subtle).Border(1, p.cardBorder).
		Shadow(0, 1, 0, 0, p.cardBorder)
}

// keys draws a shortcut such as "Alt+→" as keycaps.
func keys(c *ui.Context, p palette, combo ...string) {
	ui.Row(c).Gap(4).Children(func() {
		for i, k := range combo {
			if i > 0 {
				ui.Text(c, "+").FontSize(11).TextColor(p.faint)
			}
			keycap(c, p, k)
		}
	})
}

// codeBlock shows monospace text that can be selected and copied. Long
// lines scroll sideways rather than wrap, so what is shown is byte-exact.
func codeBlock(c *ui.Context, p palette, text string) ui.Element {
	return ui.Column(c).Radius(10).Background(p.inset).Border(1, p.cardBorder).Clip().Children(func() {
		ui.ScrollHorizontal(c).Padding(14, 16).Selectable().Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(12.5).LineHeight(1.5).NoWrap()
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
func strengthMeter(c *ui.Context, p palette, pw string) {
	s := strength(pw)
	if s == 0 {
		return
	}
	labels := []string{"", "Weak", "Fair", "Good", "Strong"}
	colors := []ui.Color{p.faint, p.danger, p.warning, p.accent, p.success}
	ui.Row(c).Gap(8).Children(func() {
		ui.Row(c).Gap(4).Grow(1).Children(func() {
			for i := 1; i <= 4; i++ {
				seg := ui.Box(c).Height(4).Grow(1).Radius(2).Background(p.subtle)
				if i <= s {
					seg.Background(colors[s])
				}
			}
		})
		ui.Text(c, labels[s]).FontSize(12).FontWeight(600).TextColor(colors[s]).Width(48).TextAlign(ui.End)
	})
}
