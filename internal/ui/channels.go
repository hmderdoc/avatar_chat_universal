package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// modalView is a full-screen overlay that owns input until it closes: the
// "Who's Here" roster, the /channels list and /help.
type modalView interface {
	HandleKey(k ansi.Key) (closed bool)
	HandleMouse(k ansi.Key) (closed bool)
	Render()
}

// ChannelsModal is the /channels list: the joined channels (with unread
// counts, the one being viewed marked "here") followed by every public room
// the server registers, each with its live user count. Enter switches to
// (or joins) the highlighted room, L leaves it, J asks for a room name on
// a prompt line and joins (or creates) it, Esc closes. The App reads Join
// / Leave after it closes.
type ChannelsModal struct {
	Frame *ansi.Frame

	rooms    []chat.RoomInfo
	selected int

	// prompting is the J prompt being up, name what's typed on it.
	prompting bool
	name      []byte

	// Join / Leave name the room the user picked, set when the modal closes
	// on Enter / L. Both empty means it was just dismissed.
	Join  string
	Leave string

	Theme *theme.Theme

	list  listGeom
	hints hintLine
}

// roomNameMax caps what the J prompt takes.
const roomNameMax = 40

func NewChannelsModal(frame *ansi.Frame, rooms []chat.RoomInfo) *ChannelsModal {
	// Joined rooms first, in the order given; then the others.
	sort.SliceStable(rooms, func(i, j int) bool { return rooms[i].Joined && !rooms[j].Joined })
	return &ChannelsModal{Frame: frame, rooms: rooms}
}

func (m *ChannelsModal) HandleKey(k ansi.Key) bool {
	if m.prompting {
		return m.promptKey(k)
	}
	switch k.Type {
	case ansi.KeyEsc:
		return true
	case ansi.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
	case ansi.KeyDown:
		if m.selected < len(m.rooms)-1 {
			m.selected++
		}
	case ansi.KeyHome:
		m.selected = 0
	case ansi.KeyEnd:
		m.selected = len(m.rooms) - 1
	case ansi.KeyPgUp:
		m.selected -= 10
		if m.selected < 0 {
			m.selected = 0
		}
	case ansi.KeyPgDn:
		m.selected += 10
		if m.selected >= len(m.rooms) {
			m.selected = len(m.rooms) - 1
		}
	case ansi.KeyEnter:
		if r, ok := m.current(); ok {
			m.Join = r.Name
		}
		return true
	case ansi.KeyChar:
		switch k.Rune {
		case 'q', 'Q':
			return true
		case 'l', 'L':
			if r, ok := m.current(); ok && r.Joined {
				m.Leave = r.Name
				return true
			}
		case 'j', 'J':
			m.prompting, m.name = true, m.name[:0]
		}
	}
	return false
}

// promptKey edits the J prompt: Enter joins the typed room (a leading '#'
// is dropped, as /join does), Esc puts the prompt away.
func (m *ChannelsModal) promptKey(k ansi.Key) bool {
	switch k.Type {
	case ansi.KeyEsc:
		m.prompting = false
	case ansi.KeyEnter:
		name := strings.TrimPrefix(strings.TrimSpace(string(m.name)), "#")
		m.prompting = false
		if name != "" {
			m.Join = name
			return true
		}
	case ansi.KeyBackspace, ansi.KeyDelete:
		if len(m.name) > 0 {
			m.name = m.name[:len(m.name)-1]
		}
	case ansi.KeyChar:
		// Room names are one word, as /join takes them.
		// The prompt shows the '#' already.
		if !k.Ctrl && k.Rune > ' ' && k.Rune < 0x7f && len(m.name) < roomNameMax && !(k.Rune == '#' && len(m.name) == 0) {
			m.name = append(m.name, byte(k.Rune))
		}
	}
	return false
}

func (m *ChannelsModal) current() (chat.RoomInfo, bool) {
	if m.selected < 0 || m.selected >= len(m.rooms) {
		return chat.RoomInfo{}, false
	}
	return m.rooms[m.selected], true
}

func (m *ChannelsModal) Render() {
	f := m.Frame
	f.Clear()
	th := orDefault(m.Theme)
	titleAttr := th.ModalTitle
	headAttr := th.ModalLabel
	listAttr := th.ModalBg
	selAttr := th.ModalSelect
	unreadAttr := th.ModalUnread

	drawBox(f, th, listAttr)
	boxTitle(f, "Rooms", titleAttr)

	nameW := f.W - 30
	if nameW > 30 {
		nameW = 30
	}
	if nameW < 10 {
		nameW = 10
	}
	row := func(marker, name, users, unread, state string) string {
		return fmt.Sprintf("%s %-*s %6s %7s  %s", marker, nameW, truncate(name, nameW), users, unread, state)
	}
	f.PrintAt(1, 2, []byte(row(" ", "Room", "Users", "Unread", "")), headAttr)

	if len(m.rooms) == 0 {
		f.PrintAt(3, 4, []byte("No rooms to show."), listAttr)
	}
	listStart := 3
	visible := f.H - 2 - listStart
	if m.prompting {
		visible -= 2 // a blank row, then the prompt above the key line
	}
	if visible < 1 {
		visible = 1
	}
	top := 0
	if m.selected >= visible {
		top = m.selected - visible + 1
	}
	m.list = listGeom{top: top, y0: listStart, rows: visible, x1: f.W - 1, n: len(m.rooms)}
	for i := 0; i < visible && top+i < len(m.rooms); i++ {
		idx := top + i
		r := m.rooms[idx]
		users := "?"
		if r.Users >= 0 {
			users = fmt.Sprint(r.Users)
		}
		unread := ""
		if r.Unread > 0 {
			unread = fmt.Sprint(r.Unread)
		}
		state := ""
		switch {
		case r.Current:
			state = "here"
		case r.Joined:
			state = "joined"
		}
		attr := listAttr
		marker := " "
		if idx == m.selected {
			attr = selAttr
			marker = ">"
		} else if r.Unread > 0 {
			attr = unreadAttr
		}
		line := padRight(row(marker, "#"+r.Name, users, unread, state), f.W-2)
		f.PrintAt(1, listStart+i, []byte(truncate(line, f.W-2)), attr)
	}

	help := " Up/Down move   Enter join/switch   J join by name   L leave   Esc close "
	hints := []hint{{"Enter join/switch", enterKey}, {"J join by name", charKey('j')}, {"L leave", charKey('l')}, {"Esc close", escKey}}
	if m.prompting {
		prompt := "Join room: #" + string(m.name)
		f.PrintAt(3, f.H-3, []byte(truncate(prompt, f.W-6)), th.ModalText)
		if x := 3 + len(prompt); x < f.W-2 {
			f.SetCell(x, f.H-3, '_', th.ModalHint) // the caret
		}
		help = " Type a room name   Enter join   Esc cancel "
		hints = []hint{{"Enter join", enterKey}, {"Esc cancel", escKey}}
	}
	hx := (f.W - len(help)) / 2
	if hx < 1 {
		hx = 1
	}
	m.hints = drawHints(f, hx, f.H-2, truncate(help, f.W-2), th.ModalHint, hints...)
}

// applyChannelChoice acts on what the user picked in the /channels list.
func (a *App) applyChannelChoice(m *ChannelsModal) (exit bool) {
	switch {
	case m.Join != "":
		if strings.EqualFold(m.Join, a.Session.Channel) {
			return false
		}
		if err := a.Session.JoinChannel(m.Join, 200); err != nil {
			a.Session.Notice(fmt.Sprintf("/join: %v", err))
		}
		a.transcript.Scroll = 0
	case m.Leave != "":
		if strings.EqualFold(m.Leave, a.Session.Channel) {
			return a.partCurrent()
		}
		if err := a.Session.Leave(m.Leave); err != nil {
			a.Session.Notice(fmt.Sprintf("leave: %v", err))
		} else {
			a.Session.Notice("left " + m.Leave)
		}
	}
	return false
}

// partCurrent leaves the channel being viewed and falls back to the most
// recently viewed other one. Parting the last joined channel exits the
// door, as /part always has.
func (a *App) partCurrent() (exit bool) {
	old := a.Session.Channel
	next, err := a.Session.PartCurrent(200)
	if err != nil {
		a.Session.Notice(fmt.Sprintf("/part: %v", err))
		return false
	}
	if next == "" {
		return true
	}
	a.transcript.Scroll = 0
	a.Session.Notice("left " + old)
	return false
}

// HandleMouse: the wheel moves the selection, a click picks a row and a
// click on the picked row joins it, like Enter; a click on a key-line
// action does its key; the [X] is Esc (it closes the panel even with the
// J prompt up).
func (m *ChannelsModal) HandleMouse(k ansi.Key) bool {
	if closeHit(k, m.Frame.X, m.Frame.Y) {
		return true
	}
	if hk, ok := m.hints.hit(k, m.Frame.X, m.Frame.Y); ok {
		return m.HandleKey(hk)
	}
	if m.prompting {
		return false
	}
	if idx, ok := m.list.pick(k); ok {
		if idx == m.selected {
			return m.HandleKey(ansi.Key{Type: ansi.KeyEnter})
		}
		m.selected = idx
		return false
	}
	return m.HandleKey(wheelKey(k))
}
