package ui

import (
	"fmt"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// modalView is a full-screen overlay that owns input until it closes: the
// "Who's Here" roster and the /channels list.
type modalView interface {
	HandleKey(k ansi.Key) (closed bool)
	Render()
}

// ChannelsModal is the /channels list: the joined channels (with unread
// counts) followed by every public room the server registers, each with its
// live user count. Enter switches to (or joins) the highlighted room, L
// leaves it, Esc closes. The App reads Join / Leave after it closes.
type ChannelsModal struct {
	Frame *ansi.Frame

	rooms    []chat.RoomInfo
	selected int

	// Join / Leave name the room the user picked, set when the modal closes
	// on Enter / L. Both empty means it was just dismissed.
	Join  string
	Leave string

	Theme *theme.Theme
}

func NewChannelsModal(frame *ansi.Frame, rooms []chat.RoomInfo) *ChannelsModal {
	return &ChannelsModal{Frame: frame, rooms: rooms}
}

func (m *ChannelsModal) HandleKey(k ansi.Key) bool {
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

	for x := 0; x < f.W; x++ {
		f.SetCell(x, 0, ' ', titleAttr)
	}
	f.PrintAt(2, 0, []byte(" Channels "), titleAttr)

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
	if visible < 1 {
		visible = 1
	}
	top := 0
	if m.selected >= visible {
		top = m.selected - visible + 1
	}
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

	help := " Up/Down move   Enter join/switch   L leave   Esc close "
	hx := (f.W - len(help)) / 2
	if hx < 1 {
		hx = 1
	}
	f.PrintAt(hx, f.H-1, []byte(truncate(help, f.W-2)), th.ModalHint)
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
