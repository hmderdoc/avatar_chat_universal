package ui

import (
	"fmt"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// /ignore and /unignore. Ignored people's lines, art and join/leave
// notices are dropped (chat.Session.Ignore), and they never ring. An entry
// from the shell's shared list that only mutes notifications still shows.

// muted reports whether m is from someone on the ignore list at all, so it
// doesn't ring even when the line is shown.
func (a *App) muted(m *chat.Message) bool {
	if a.Ignores == nil || m == nil || m.Nick == nil {
		return false
	}
	_, ok := a.Ignores.Match(m.Nick.Name, m.Str)
	return ok
}

func (a *App) handleIgnore(args []string) {
	if a.Ignores == nil {
		a.Session.Notice("/ignore: not available here")
		return
	}
	if len(args) == 0 {
		entries := a.Ignores.Entries()
		if len(entries) == 0 {
			a.Session.Notice("ignoring nobody. /ignore <name> hides someone; /unignore <name> undoes it")
			return
		}
		var own, shared []string
		for _, e := range entries {
			if e.Shared {
				label := e.Label
				if !e.HideChat {
					label += " (notifications only)"
				}
				shared = append(shared, label)
			} else {
				own = append(own, e.Label)
			}
		}
		if len(own) > 0 {
			a.Session.Notice("ignoring: " + strings.Join(own, ", "))
		}
		if len(shared) > 0 {
			a.Session.Notice("ignored in your shell settings: " + strings.Join(shared, ", "))
		}
		return
	}
	name := strings.Join(args, " ")
	if strings.EqualFold(strings.TrimSpace(name), a.selfName()) {
		a.Session.Notice("/ignore: that's you")
		return
	}
	added, err := a.Ignores.Add(name)
	switch {
	case err != nil:
		a.Session.Notice(fmt.Sprintf("/ignore: %v", err))
	case !added:
		a.Session.Notice(fmt.Sprintf("already ignoring %s", name))
	default:
		a.Session.Refilter()
		a.Session.Notice(fmt.Sprintf("\x01Gignoring %s\x01n -- /unignore %s undoes it", name, name))
	}
	a.dirty = true
}

func (a *App) handleUnignore(args []string) {
	if a.Ignores == nil {
		a.Session.Notice("/unignore: not available here")
		return
	}
	if len(args) == 0 {
		a.Session.Notice("/unignore <name>")
		return
	}
	name := strings.Join(args, " ")
	removed, shared, err := a.Ignores.Remove(name)
	switch {
	case err != nil:
		a.Session.Notice(fmt.Sprintf("/unignore: %v", err))
	case shared:
		a.Session.Notice(fmt.Sprintf("%s is ignored in your shell settings; change it there", name))
	case removed:
		a.Session.Notice(fmt.Sprintf("\x01Gno longer ignoring %s\x01n (new messages show)", name))
	default:
		a.Session.Notice(fmt.Sprintf("not ignoring %s", name))
	}
	a.dirty = true
}
