package ui

import (
	"sort"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// rosterEntry holds the data we need to render one user in the roster
// modal: their nick, the BBS they connect from, and their avatar (real
// from message cache, or identicon fallback).
type rosterEntry struct {
	Name   string
	Host   string
	Avatar avatar.Avatar
}

// RosterModal is the "Who's Here" overlay. List of users on the left, the
// currently-selected user's avatar + name + host on the right. Up/Down
// moves the selection; Esc closes.
type RosterModal struct {
	Frame *ansi.Frame

	entries  []rosterEntry
	selected int
	closed   bool
}

// NewRosterModal builds a modal from the WHO results plus the session's
// avatar/host cache. Names are deduplicated case-insensitively (the same
// user connected from two sessions appears once).
func NewRosterModal(frame *ansi.Frame, names []string, sess *chat.Session) *RosterModal {
	seen := map[string]bool{}
	var unique []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		key := strings.ToLower(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, n)
	}
	sort.Slice(unique, func(i, j int) bool {
		return strings.ToLower(unique[i]) < strings.ToLower(unique[j])
	})

	entries := make([]rosterEntry, 0, len(unique))
	for _, n := range unique {
		var a avatar.Avatar
		if b64 := sess.AvatarFor(n); b64 != "" {
			if av, err := avatar.FromBase64(b64); err == nil {
				a = av
			}
		}
		if a == nil {
			a = avatar.Identicon(n)
		}
		entries = append(entries, rosterEntry{
			Name:   n,
			Host:   sess.HostFor(n),
			Avatar: a,
		})
	}
	return &RosterModal{Frame: frame, entries: entries}
}

// HandleKey processes a key press; returns true if the modal wants to close.
func (m *RosterModal) HandleKey(k ansi.Key) bool {
	switch k.Type {
	case ansi.KeyEsc:
		m.closed = true
		return true
	case ansi.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
	case ansi.KeyDown:
		if m.selected < len(m.entries)-1 {
			m.selected++
		}
	case ansi.KeyHome:
		m.selected = 0
	case ansi.KeyEnd:
		m.selected = len(m.entries) - 1
	case ansi.KeyPgUp:
		m.selected -= 10
		if m.selected < 0 {
			m.selected = 0
		}
	case ansi.KeyPgDn:
		m.selected += 10
		if m.selected >= len(m.entries) {
			m.selected = len(m.entries) - 1
		}
	case ansi.KeyChar:
		switch k.Rune {
		case 'q', 'Q':
			m.closed = true
			return true
		}
	}
	return false
}

// Closed reports whether the modal has been dismissed.
func (m *RosterModal) Closed() bool { return m.closed }

// Render paints the entire modal into m.Frame. The frame is the full screen
// minus the bottom row (which the App reserves for cursor scratch).
func (m *RosterModal) Render() {
	f := m.Frame
	f.Clear()

	// Outer border.
	titleAttr := ansi.Yellow | ansi.BgBlue
	bgAttr := ansi.LightGray | ansi.BgBlack
	listAttr := ansi.LightGray | ansi.BgBlack
	selAttr := ansi.Black | ansi.BgCyan
	labelAttr := ansi.LightCyan | ansi.BgBlack

	// Title row.
	for x := 0; x < f.W; x++ {
		f.SetCell(x, 0, ' ', titleAttr)
	}
	f.PrintAt(2, 0, []byte(" Who's Here "), titleAttr)

	// List pane: left half. Preview pane: right half.
	listW := f.W / 2
	if listW < 24 {
		listW = 24
	}
	previewX := listW + 2

	// List items.
	listStart := 2
	listEnd := f.H - 2
	visible := listEnd - listStart
	if visible < 1 {
		visible = 1
	}
	// Scroll list so selected is visible.
	top := 0
	if m.selected >= visible {
		top = m.selected - visible + 1
	}
	if top < 0 {
		top = 0
	}
	for i := 0; i < visible && top+i < len(m.entries); i++ {
		row := listStart + i
		idx := top + i
		entry := m.entries[idx]
		attr := listAttr
		marker := " "
		if idx == m.selected {
			attr = selAttr
			marker = ">"
		}
		// Pad full row width so highlight extends.
		line := marker + " " + truncate(entry.Name, listW-4)
		line = padRight(line, listW)
		f.PrintAt(1, row, []byte(line), attr)
	}

	// Vertical separator.
	for y := listStart - 1; y < listEnd+1 && y < f.H-1; y++ {
		f.SetCell(listW+1, y, 0xB3, bgAttr) // CP437 │
	}

	// Preview pane. Strict clipping: we never write past previewMaxX so
	// long names/hosts can't wrap into the list pane.
	previewMaxX := f.W - 2
	if m.selected >= 0 && m.selected < len(m.entries) {
		entry := m.entries[m.selected]
		if entry.Avatar != nil {
			entry.Avatar.RenderTo(f, previewX, 2)
		}
		nameX := previewX + avatar.Width + 2
		labelW := previewMaxX - nameX // chars available for label + value
		if labelW < 4 {
			labelW = 4
		}
		writeClipped(f, nameX, 2, "Nick: ", entry.Name, labelAttr, ansi.White|ansi.BgBlack, labelW)
		if entry.Host != "" {
			writeClipped(f, nameX, 4, "BBS:  ", entry.Host, labelAttr, ansi.White|ansi.BgBlack, labelW)
		}
	}

	// Help line at bottom.
	help := " Up/Down move    Esc close "
	hx := (f.W - len(help)) / 2
	if hx < 1 {
		hx = 1
	}
	f.PrintAt(hx, f.H-1, []byte(help), ansi.Yellow|ansi.BgBlack)
}

func truncate(s string, w int) string {
	if w < 1 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	return s[:w]
}

// writeClipped paints "label" + "value" at (x,y) in two attrs, clipping the
// total to maxW cells. Each cell is written via SetCell directly (NOT
// PrintAt) so an over-long string can't wrap to the next row.
func writeClipped(f *ansi.Frame, x, y int, label, value string, labelAttr, valueAttr ansi.Attr, maxW int) {
	col := x
	end := x + maxW
	for i := 0; i < len(label) && col < end; i++ {
		f.SetCell(col, y, label[i], labelAttr)
		col++
	}
	for i := 0; i < len(value) && col < end; i++ {
		f.SetCell(col, y, value[i], valueAttr)
		col++
	}
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}
