package ui

import (
	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// helpSection is one group of lines in the /help panel.
type helpSection struct {
	title string
	lines []string
}

// HelpModal is the /help panel: the commands and keys in short sections,
// word-wrapped to the box. Up/Down, PgUp/PgDn, Home/End and the wheel
// scroll it when it doesn't fit; Esc (or q, or the [X]) closes.
type HelpModal struct {
	Frame *ansi.Frame
	Theme *theme.Theme

	sections []helpSection
	top      int // first visible row of the wrapped text

	rows  int // visible text rows at the last Render
	total int // wrapped rows at the last Render
	hints hintLine
}

// helpSections is what /help shows; lines for features this system has
// switched off are left out.
func (a *App) helpSections() []helpSection {
	cmds := []string{"/me /msg /r /join /part /who /img /channels /clear /avatar /settings /theme /draw /ignore /quit"}
	if a.UserThemes {
		cmds = append(cmds, "/theme lists the colour themes; /theme <name> switches and remembers it (or c/C in /settings)")
	}
	cmds = append(cmds, "/mouse on|off: wheel scrolls; click buttons, names, pictures and /play lines")

	look := "/settings (/prefs): avatar, handle colours and theme in one panel"
	if a.BoardWide {
		look = "/avatar and /settings change your BBS avatar and handle style everywhere on this system"
	}

	var pics []string
	if a.images != nil {
		pics = append(pics, "image links show inline; /img zooms them; /nsfw shows or hides veiled ones")
	}
	if a.cards != nil {
		pics = append(pics, "other links get a preview card (site, title, summary) under the message")
	}
	if a.OnDraw != nil {
		pics = append(pics, "/draw [WxH] opens an ANSI art editor (default 80x25, up to 80x40); Ctrl-Y posts it")
	}
	if a.OnMediaUpload != nil {
		pics = append(pics, "/upload sends a picture or ANSI art by Zmodem; its link lands in your input line")
	}

	tv := []string{
		"TV: /tvtuner <host> <port> <channel> | /tvtuner off | /tvon | /tvoff",
		"TV: PgUp=history (PgDn/Down or 30s closes) | /tvcolor or Ctrl-T = truecolor/16",
	}
	if a.AudioOK {
		tv = append(tv, "audio: TV sound plays in the lounge (/tvmute)")
	}
	if a.alinks != nil {
		tv = append(tv, "audio links: /play plays the newest, /play N or /play list for others, /stop stops")
	}
	if a.videos != nil {
		tv = append(tv, "video links: /play takes over the screen like the TV; Esc or /stop ends it")
	}

	out := []helpSection{
		{"Commands", cmds},
		{"Rooms and people", []string{
			"/join keeps the old channel joined (unread counts in /channels); /part leaves it",
			"/ignore <name> hides someone's lines; /ignore lists; /unignore <name> undoes it",
		}},
		{"Your look", []string{look}},
	}
	if len(pics) > 0 {
		out = append(out, helpSection{"Pictures and links", pics})
	}
	return append(out, helpSection{"TV and media", tv})
}

// openHelp shows the /help panel over the chat.
func (a *App) openHelp() {
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	a.modalFrame = ansi.NewFrame(0, 0, a.Width, usable, a.theme().ModalBg)
	a.modalFrame.Charset = a.Charset
	a.modal = &HelpModal{Frame: a.modalFrame, Theme: a.theme(), sections: a.helpSections()}
	_ = ansi.ClearScreen(a.Conn)
	a.dirty = true
}

// helpRow is one wrapped row of the panel's text.
type helpRow struct {
	text    string
	heading bool
}

// wrapped lays the sections out at width w: a heading, its lines indented
// (continuations further), a blank row between sections.
func (m *HelpModal) wrapped(w int) []helpRow {
	var out []helpRow
	for i, s := range m.sections {
		if i > 0 {
			out = append(out, helpRow{})
		}
		out = append(out, helpRow{text: s.title, heading: true})
		for _, ln := range s.lines {
			for j, part := range wrapPlain(ln, w-4) {
				if j > 0 {
					part = "  " + part
				}
				out = append(out, helpRow{text: "  " + part})
			}
		}
	}
	return out
}

// scroll moves the view by d rows, kept within the text.
func (m *HelpModal) scroll(d int) {
	m.top += d
	if last := m.total - m.rows; m.top > last {
		m.top = last
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *HelpModal) HandleKey(k ansi.Key) bool {
	page := m.rows - 1
	if page < 1 {
		page = 1
	}
	switch k.Type {
	case ansi.KeyEsc:
		return true
	case ansi.KeyUp:
		m.scroll(-1)
	case ansi.KeyDown:
		m.scroll(1)
	case ansi.KeyPgUp:
		m.scroll(-page)
	case ansi.KeyPgDn:
		m.scroll(page)
	case ansi.KeyHome:
		m.top = 0
	case ansi.KeyEnd:
		m.scroll(m.total)
	case ansi.KeyChar:
		if k.Rune == 'q' || k.Rune == 'Q' {
			return true
		}
	}
	return false
}

// HandleMouse: the wheel scrolls, a click on "Esc close" or the [X]
// closes.
func (m *HelpModal) HandleMouse(k ansi.Key) bool {
	if closeHit(k, m.Frame.X, m.Frame.Y) {
		return true
	}
	if hk, ok := m.hints.hit(k, m.Frame.X, m.Frame.Y); ok {
		return m.HandleKey(hk)
	}
	return m.HandleKey(wheelKey(k))
}

func (m *HelpModal) Render() {
	f := m.Frame
	f.Clear()
	th := orDefault(m.Theme)
	drawBox(f, th, th.ModalBg)
	boxTitle(f, "Help", th.ModalTitle)

	textW := f.W - 4 // two columns in from each side
	rows := m.wrapped(textW)
	const y0 = 2
	m.rows = f.H - 2 - y0 - 1 // a blank row above the key line
	if m.rows < 1 {
		m.rows = 1
	}
	m.total = len(rows)
	m.scroll(0)
	for i := 0; i < m.rows && m.top+i < len(rows); i++ {
		r := rows[m.top+i]
		attr := th.ModalBg
		if r.heading {
			attr = th.ModalLabel
		}
		putStr(f, 2, y0+i, truncate(r.text, textW), attr)
	}

	help := " Esc close "
	if m.total > m.rows {
		help = " Up/Down PgUp/PgDn scroll   Esc close "
	}
	hx := (f.W - len(help)) / 2
	if hx < 1 {
		hx = 1
	}
	m.hints = drawHints(f, hx, f.H-2, truncate(help, f.W-2), th.ModalHint, hint{"Esc close", escKey})
}
