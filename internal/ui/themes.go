package ui

import (
	"fmt"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// Per-user themes: /theme lists the sysop's themes/<name>.ini files and
// switches between them; the choice is saved through OnThemeSave and
// loaded again at the next launch. The sysop's theme stays the default,
// and user_themes = off pins everyone to it.

// themeIdle is the screensaver profile from the main config, kept so a
// switch away from a theme that set one goes back to it.
type themeIdle struct {
	random   bool
	sequence []string
	disable  []string
}

// ThemeNames lists what /theme offers: the built-in default plus every
// valid themes/<name>.ini.
func (a *App) ThemeNames() []string {
	return theme.List(a.ThemeDir)
}

// themeName is the active theme's name.
func (a *App) themeName() string { return a.theme().Name }

// SetUserTheme loads the named theme ("" or "default" means the sysop's)
// and applies it. With save, the choice is stored for the next session.
func (a *App) SetUserTheme(name string, save bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	reset := name == "" || name == "default" || name == "reset"
	var t *theme.Theme
	if reset {
		t = a.sysopTheme()
	} else {
		var err error
		if t, err = theme.LoadNamed(a.ThemeDir, name); err != nil {
			return err
		}
	}
	if save && a.OnThemeSave != nil {
		stored := t.Name
		if reset {
			stored = ""
		}
		if err := a.OnThemeSave(stored); err != nil {
			return err
		}
	}
	a.ApplyTheme(t)
	return nil
}

func (a *App) sysopTheme() *theme.Theme {
	if a.SysopTheme != nil {
		return a.SysopTheme
	}
	return theme.Default()
}

// handleTheme is /theme [name|default].
func (a *App) handleTheme(args []string) {
	if !a.UserThemes {
		a.Session.Notice(fmt.Sprintf("theme: %s (the sysop sets the theme here)", a.themeName()))
		return
	}
	if len(args) == 0 || args[0] == "list" {
		names := a.ThemeNames()
		marked := make([]string, len(names))
		for i, n := range names {
			marked[i] = n
			if n == a.themeName() {
				marked[i] = "\x01Y" + n + "\x01n"
			}
		}
		a.Session.Notice("themes: " + strings.Join(marked, ", "))
		a.Session.Notice(fmt.Sprintf("/theme <name> switches and remembers it; /theme default = %s", a.sysopTheme().Name))
		return
	}
	if err := a.SetUserTheme(args[0], true); err != nil {
		a.Session.Notice(fmt.Sprintf("/theme: %v -- /theme lists them", err))
		return
	}
	a.Session.Notice(fmt.Sprintf("\x01Gtheme: %s\x01n", a.themeName()))
}

// cycleTheme steps through ThemeNames from current by dir.
func (a *App) cycleTheme(current string, dir int) string {
	names := a.ThemeNames()
	idx := 0
	for i, n := range names {
		if n == current {
			idx = i
		}
	}
	n := len(names)
	return names[((idx+dir)%n+n)%n]
}
