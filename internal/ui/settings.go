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
// editor and uploader) and their handle style (per-letter colour and a tag),
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

func colorLabel(hex string) string {
	h := nickstyle.Hex(hex)
	if h == "" {
		return "default"
	}
	for i, c := range nickstyle.CGA {
		if c == h {
			return cgaNames[i]
		}
	}
	return h
}

// settingsPanel holds the working copy; nothing is stored until S.
type settingsPanel struct {
	frame   *ansi.Frame // kept across draws so only changed cells are sent
	name    string
	colors  []string // aligned to name
	tag     nickstyle.Tag
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
		p.tag = style.Tag
	} else {
		p.colors = make([]string, len(name))
	}
	return p
}

func (p *settingsPanel) style() *nickstyle.Style {
	st := nickstyle.Normalize(nickstyle.Style{Colors: p.colors, Tag: p.tag})
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
		switch k.Type {
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
		case 't', 'T':
			if text, ok := a.editTagText(p, av); ok {
				p.tag.Text = nickstyle.CleanTag(text)
				p.changed = true
			}
		case 'g', 'G':
			if p.tag.Text != "" {
				dir := 1
				if k.Rune == 'G' {
					dir = -1
				}
				p.tag.Fg = cycleColor(p.tag.Fg, dir)
				p.changed = true
			}
		case 'b', 'B':
			if p.tag.Text != "" {
				dir := 1
				if k.Rune == 'B' {
					dir = -1
				}
				p.tag.Bg = cycleColor(p.tag.Bg, dir)
				p.changed = true
			}
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
			a.Session.Self.Style = st
		}
	}
	a.Session.Notice("\x01Ghandle style saved\x01n -- shown on your next message")
}

// editTagText reads the tag text inline: printable ASCII, Enter keeps,
// Esc abandons.
func (a *App) editTagText(p *settingsPanel, av avatar.Avatar) (string, bool) {
	buf := []byte(p.tag.Text)
	for {
		p.status = "Tag text: " + string(buf) + "_   (Enter keep, Esc cancel)"
		a.drawSettings(p, av)
		var k ansi.Key
		for {
			key, ok, err := a.Input.Next(200 * time.Millisecond)
			if err != nil {
				p.status = ""
				return "", false
			}
			if ok {
				k = key
				break
			}
		}
		switch k.Type {
		case ansi.KeyEnter:
			p.status = ""
			return string(buf), true
		case ansi.KeyEsc:
			p.status = ""
			return "", false
		case ansi.KeyBackspace, ansi.KeyDelete:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case ansi.KeyChar:
			if k.Rune == 8 || k.Rune == 127 {
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
				}
			} else if k.Rune >= 0x20 && k.Rune <= 0x7e && k.Rune != '|' && len(buf) < nickstyle.MaxTag {
				buf = append(buf, byte(k.Rune))
			}
		}
	}
}

func (a *App) drawSettings(p *settingsPanel, av avatar.Avatar) {
	w := 72
	if w > a.Width-2 {
		w = a.Width - 2
	}
	h := 20
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

	for i := 0; i < w; i++ {
		f.SetCell(i, 0, ' ', th.ModalTitle)
	}
	f.PrintAt(1, 0, []byte(" Settings"), th.ModalTitle)
	closeHint := "[Esc] close "
	f.PrintAt(w-len(closeHint), 0, []byte(closeHint), th.ModalClose)

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
	col := 3
	for i := 0; i < len(p.name) && col < w-4; i++ {
		ch := p.name[i]
		paintHandle(f, col, 9, string(ch), &nickstyle.Style{Colors: []string{p.colors[i]}}, bg, 1, truecolor)
		if i == p.cursor {
			f.SetCell(col, 10, '^', th.ModalHint)
		}
		col += 2
	}
	if len(p.colors) > 0 {
		info := fmt.Sprintf("letter %d: %-13s Left/Right move  f/F colour", p.cursor+1, colorLabel(p.colors[p.cursor]))
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
	tagText := p.tag.Text
	if tagText == "" {
		tagText = "(none)"
	}
	tagLine := fmt.Sprintf("Tag: %-12s  T text  g/G fg %s  b/B bg %s", tagText, colorLabel(p.tag.Fg), colorLabel(p.tag.Bg))
	f.PrintAt(3, 14, []byte(truncate(tagLine, w-4)), bg)

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
	f.PrintAt(3, h-1, []byte(truncate(keys, w-4)), hot)
	_ = f.Render(a.Conn)
}
