package ui

import (
	"fmt"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// The /settings panel: the user's avatar (routed to the existing selector,
// editor and uploader) and their handle style (a colour per letter),
// in one place, like the shell's and the website's settings cards. In
// Synchronet mode both are the user's board-wide records.
//
// Like confirmUpload it is synchronous: it owns the conn and the input until
// the user leaves, then the caller force-redraws the chat UI.

// settingsResult is what the panel asks the App to do on exit.
type settingsResult int

const (
	settingsClosed settingsResult = iota
	settingsPick
	settingsEditor
	settingsUpload
	settingsDisable
)

// settingsColors is what f/F, g/G and b/B cycle through: default (”) and
// the sixteen CGA colours, as the web and shell spell them.
var settingsColors = append([]string{""}, nickstyle.CGA[:]...)

// cgaNames labels the cycle for terminals that can't show truecolor.
var cgaNames = [16]string{
	"black", "blue", "green", "cyan", "red", "magenta", "brown", "light gray",
	"dark gray", "light blue", "light green", "light cyan", "light red",
	"light magenta", "yellow", "white",
}

// cycleColor steps hex through settingsColors. A colour outside the cycle
// (a truecolor gradient set on the web) starts from its nearest CGA entry.
func cycleColor(hex string, dir int) string {
	idx := 0
	h := nickstyle.Hex(hex)
	if h != "" {
		idx = -1
		for i, c := range settingsColors {
			if c == h {
				idx = i
			}
		}
		if idx < 0 {
			r, g, b, _ := nickstyle.RGB(h)
			idx = 1 + int(handleQ.Nearest16(shadecellRGB(r, g, b)))
		}
	}
	n := len(settingsColors)
	return settingsColors[((idx+dir)%n+n)%n]
}

// colorLabel names a letter's colour without showing its hex: the CGA
// name, or "~" and the nearest one for a colour off the palette.
func colorLabel(hex string) string {
	h := nickstyle.Hex(hex)
	if h == "" {
		return "default"
	}
	if i := cgaIndex(h); i >= 0 {
		return cgaNames[i]
	}
	r, g, b, _ := nickstyle.RGB(h)
	return "~" + cgaNames[handleQ.Nearest16(shadecellRGB(r, g, b))]
}

// settingsPanel holds the working copy; nothing is stored until S.
type settingsPanel struct {
	frame   *ansi.Frame // kept across draws so only changed cells are sent
	name    string
	colors  []string // aligned to name
	cursor  int
	status  string
	changed bool

	// theme is the pick shown (and previewed in the panel's own colours);
	// it is applied and stored only on S.
	theme        *theme.Theme
	themeChanged bool
}

func newSettingsPanel(name string, style *nickstyle.Style) *settingsPanel {
	p := &settingsPanel{name: name}
	if style != nil {
		p.colors = style.Align(name)
	} else {
		p.colors = make([]string, len(name))
	}
	return p
}

func (p *settingsPanel) style() *nickstyle.Style {
	st := nickstyle.Normalize(nickstyle.Style{Colors: p.colors})
	return &st
}

// openSettings runs the panel until the user saves, cancels or picks an
// avatar action, and routes that action through the avatar manager.
func (a *App) openSettings() {
	for {
		result := a.runSettings()
		a.forceFullRedraw()
		switch result {
		case settingsPick:
			a.applyAvatarAction(a.OnAvatarPick)
		case settingsEditor:
			a.applyAvatarAction(func() (string, string, error) { return "", "editor", nil })
		case settingsUpload:
			a.applyAvatarAction(func() (string, string, error) { return "", "upload", nil })
		case settingsDisable:
			a.applyAvatarAction(func() (string, string, error) { return "", "disable", nil })
		default:
			return
		}
		// Back to the panel so the user sees the new avatar.
	}
}

func (a *App) runSettings() settingsResult {
	name := ""
	var current *nickstyle.Style
	var av avatar.Avatar
	if a.Session != nil && a.Session.Self != nil {
		name = a.Session.Self.Name
		current = a.Session.Self.Style
		if b, err := avatar.FromBase64(a.Session.Self.Avatar); err == nil {
			av = b
		}
	}
	carried := a.pendingStyle != nil
	if carried {
		current = a.pendingStyle
	}
	p := newSettingsPanel(name, current)
	p.changed = carried
	p.theme = a.theme()
	if a.pendingTheme != nil {
		p.theme = a.pendingTheme
		p.themeChanged = p.theme.Name != a.themeName()
	}
	// Consumed here; keepAndGo sets them again on the way out.
	a.pendingStyle, a.pendingTheme = nil, nil

	for {
		a.drawSettings(p, av)
		var k ansi.Key
		for {
			key, ok, err := a.Input.Next(200 * time.Millisecond)
			if err != nil {
				return settingsClosed
			}
			if ok {
				k = key
				break
			}
		}
		if k.Type == ansi.KeyEsc {
			return settingsClosed
		}
		if k.Type == ansi.KeyMouse {
			// The [X] is Esc; a letter picks it and opens the grid.
			if p.frame != nil && closeHit(k, p.frame.X, p.frame.Y) {
				return settingsClosed
			}
			if i, ok := p.letterAt(k); ok {
				p.cursor = i
				a.pickColour(p)
			}
			continue
		}
		switch k.Type {
		case ansi.KeyEnter:
			a.pickColour(p)
			continue
		case ansi.KeyLeft:
			if p.cursor > 0 {
				p.cursor--
			}
			continue
		case ansi.KeyRight:
			if p.cursor < len(p.colors)-1 {
				p.cursor++
			}
			continue
		}
		if k.Type != ansi.KeyChar {
			continue
		}
		// Avatar actions keep any unsaved handle edits for the return trip.
		keepAndGo := func(r settingsResult) settingsResult {
			if p.changed {
				a.pendingStyle = p.style()
			}
			if p.themeChanged {
				a.pendingTheme = p.theme
			}
			return r
		}
		switch k.Rune {
		case 'p', 'P':
			return keepAndGo(settingsPick)
		case 'e', 'E':
			return keepAndGo(settingsEditor)
		case 'u', 'U':
			return keepAndGo(settingsUpload)
		case 'd', 'D':
			return keepAndGo(settingsDisable)
		case 'f', 'F':
			if len(p.colors) > 0 {
				dir := 1
				if k.Rune == 'F' {
					dir = -1
				}
				p.colors[p.cursor] = cycleColor(p.colors[p.cursor], dir)
				p.changed = true
			}
		case '1':
			if len(p.colors) > 0 {
				p.colors = padColors(nickstyle.Preset("solid", len(p.name), p.colors[p.cursor], ""), len(p.name))
				p.changed = true
			}
		case '2':
			if n := len(p.colors); n > 0 {
				p.colors = padColors(nickstyle.Preset("gradient", n, p.colors[0], p.colors[n-1]), n)
				p.changed = true
			}
		case '3':
			p.colors = padColors(nickstyle.Preset("rainbow", len(p.name), "", ""), len(p.name))
			p.changed = true
		case '4':
			p.colors = make([]string, len(p.name))
			p.changed = true
		case 'c', 'C':
			if a.UserThemes {
				dir := 1
				if k.Rune == 'C' {
					dir = -1
				}
				name := a.cycleTheme(p.theme.Name, dir)
				if t, err := theme.LoadNamed(a.ThemeDir, name); err == nil {
					p.theme = t
					p.themeChanged = p.theme.Name != a.themeName()
					p.status = ""
				} else {
					p.status = err.Error()
				}
			}
		case 's', 'S':
			if p.changed {
				a.saveStyle(p)
			}
			if p.themeChanged {
				if err := a.SetUserTheme(p.theme.Name, true); err != nil {
					a.Session.Notice(fmt.Sprintf("/settings: %v", err))
				} else {
					a.Session.Notice(fmt.Sprintf("\x01Gtheme: %s\x01n", a.themeName()))
				}
			}
			return settingsClosed
		}
	}
}

// Where drawSettings puts the letters: frame row, first column, step.
const (
	lettersY    = 9
	lettersX0   = 3
	lettersStep = 2
)

// letterAt maps a left press on the letters row to a letter index.
func (p *settingsPanel) letterAt(k ansi.Key) (int, bool) {
	f := p.frame
	if f == nil || k.Release || k.Button != ansi.MouseLeft || k.Y != f.Y+lettersY {
		return 0, false
	}
	rel := k.X - f.X - lettersX0
	if rel < 0 || rel%lettersStep != 0 {
		return 0, false
	}
	i := rel / lettersStep
	if i >= len(p.colors) || lettersX0+i*lettersStep >= f.W-4 {
		return 0, false
	}
	return i, true
}

func padColors(c []string, n int) []string {
	for len(c) < n {
		c = append(c, "")
	}
	return c[:n]
}

func (a *App) saveStyle(p *settingsPanel) {
	st := p.style()
	if a.OnStyleSave == nil {
		a.Session.Notice("/settings: handle style storage not wired")
		return
	}
	if err := a.OnStyleSave(st); err != nil {
		a.Session.Notice(fmt.Sprintf("/settings: %v", err))
		return
	}
	if a.Session.Self != nil {
		if st.IsEmpty() {
			a.Session.Self.Style = nil
		} else {
			a.Session.Self.Style = st.Wire()
		}
	}
	a.Session.Notice("\x01Ghandle style saved\x01n -- shown on your next message")
}

func (a *App) drawSettings(p *settingsPanel, av avatar.Avatar) {
	w := 72
	if w > a.Width-2 {
		w = a.Width - 2
	}
	h := 21
	if h > a.Height-1 {
		h = a.Height - 1
	}
	x := (a.Width - w) / 2
	y := (a.Height - h) / 2
	if y < 0 {
		y = 0
	}
	th := p.theme
	if th == nil {
		th = a.theme()
	}
	bg := th.ModalBg
	hot := th.ModalText
	head := th.ModalLabel
	f := p.frame
	if f == nil || f.X != x || f.Y != y || f.W != w || f.H != h {
		f = ansi.NewFrame(x, y, w, h, bg)
		f.Charset = a.Charset
		p.frame = f
	}
	f.SetFill(bg)
	f.Clear()
	truecolor := a.tvColorTrue()

	drawBox(f, th, bg)
	boxTitle(f, "Settings", th.ModalTitle)
	closeHint := " [Esc] close "
	f.PrintAt(w-1-len(closeHint), 0, []byte(closeHint), th.ModalClose)

	rule := func(row int, label string) {
		for i := 1; i < w-1; i++ {
			f.SetCell(i, row, 0xC4, th.ModalRule) // CP437 ─
		}
		if label != "" {
			f.PrintAt(2, row, []byte(" "+label+" "), head)
		}
	}

	rule(1, "Avatar")
	if av != nil {
		av.RenderTo(f, 3, 2)
	} else {
		f.PrintAt(3, 4, []byte("(none)"), th.ModalRule)
	}
	actions := [][2]string{
		{"P", "Pick from library"},
		{"E", "Draw in editor"},
		{"U", "Upload .bin (Zmodem)"},
		{"D", "Disable"},
	}
	for i, act := range actions {
		f.PrintAt(17, 2+i, []byte(fmt.Sprintf("%-22s", act[1])), bg)
		f.PrintAt(40, 2+i, []byte("("+act[0]+")"), hot)
	}

	rule(8, "Handle")
	// Letters row: each letter in its colour, the selected one bracketed.
	col := lettersX0
	for i := 0; i < len(p.name) && col < w-4; i++ {
		ch := p.name[i]
		paintHandle(f, col, lettersY, string(ch), &nickstyle.Style{Colors: []string{p.colors[i]}}, bg, 1, truecolor)
		if i == p.cursor {
			f.SetCell(col, lettersY+1, '^', th.ModalHint)
		}
		col += lettersStep
	}
	if len(p.colors) > 0 {
		info := fmt.Sprintf("letter %d: %-14s Left/Right move  Enter pick  f/F colour", p.cursor+1, colorLabel(p.colors[p.cursor]))
		f.PrintAt(3, 11, []byte(truncate(info, w-4)), bg)
	}
	f.PrintAt(3, 12, []byte("Preview: "), bg)
	paintHandle(f, 12, 12, p.name, p.style(), th.ModalHint, w-14, truecolor)
	f.PrintAt(3, 13, []byte("Preset: "), bg)
	presets := []string{"1 Solid", "2 Gradient (first->last)", "3 Rainbow", "4 Clear"}
	px := 11
	for _, ps := range presets {
		if px+len(ps) >= w-1 {
			break
		}
		f.PrintAt(px, 13, []byte(ps[:1]), hot)
		f.PrintAt(px+1, 13, []byte(ps[1:]), bg)
		px += len(ps) + 2
	}
	rule(15, "Theme")
	themeLine := fmt.Sprintf("Theme: %-16s (set by the sysop)", th.Name)
	if a.UserThemes {
		themeLine = fmt.Sprintf("Theme: %-16s c/C change (this panel previews it)", th.Name)
	}
	f.PrintAt(3, 16, []byte(truncate(themeLine, w-4)), bg)
	note := "Changes apply in this chat."
	if a.BoardWide {
		note = "Avatar and handle style are BBS-wide; the theme is this door's."
	}
	f.PrintAt(3, 17, []byte(truncate(note, w-4)), th.ModalOK)
	if p.status != "" {
		f.PrintAt(3, 18, []byte(truncate(p.status, w-4)), th.ModalHint)
	}
	keys := "S save   Esc cancel"
	if p.changed || p.themeChanged {
		keys = "S save   Esc cancel (unsaved changes)"
	}
	f.PrintAt(3, h-2, []byte(truncate(keys, w-4)), hot)
	_ = f.Render(a.Conn)
	_ = a.parkCursor(a.Conn)
}
