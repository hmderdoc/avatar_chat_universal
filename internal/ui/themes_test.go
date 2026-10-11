package ui

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

func themeApp(t *testing.T, keys string) (*App, *[]string) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "night.ini"), []byte("input_text = white\nmodal_bg = lightblue|bgblack\nidle_sequence = starfield\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "plain.ini"), []byte("input_text = yellow\n"), 0o644)
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader(keys)), sess, 80, 25, ansi.CharsetCP437)
	app.IdleSequence = []string{"plasma"}
	app.ThemeDir = dir
	app.SysopTheme = theme.Default()
	app.UserThemes = true
	app.ApplyTheme(app.SysopTheme)
	var saved []string
	app.OnThemeSave = func(name string) error { saved = append(saved, name); return nil }
	return app, &saved
}

func TestThemeCommandSwitchesSavesAndResets(t *testing.T) {
	app, saved := themeApp(t, "")
	app.handleTheme([]string{"night"})
	if app.Theme.Name != "night" || app.inputLine.TextAttr != ansi.White {
		t.Fatalf("theme %q input %v", app.Theme.Name, app.inputLine.TextAttr)
	}
	if len(app.IdleSequence) != 1 || app.IdleSequence[0] != "starfield" {
		t.Fatalf("idle = %v", app.IdleSequence)
	}
	// A theme without an idle profile goes back to the main config's.
	app.handleTheme([]string{"plain"})
	if app.IdleSequence[0] != "plasma" || app.inputLine.TextAttr != ansi.Yellow {
		t.Fatalf("after plain: idle %v input %v", app.IdleSequence, app.inputLine.TextAttr)
	}
	app.handleTheme([]string{"default"})
	if app.Theme.Name != "futurewave" {
		t.Fatalf("default -> %q", app.Theme.Name)
	}
	if want := []string{"night", "plain", ""}; strings.Join(*saved, ",") != strings.Join(want, ",") {
		t.Fatalf("saved %q, want %q", *saved, want)
	}
	app.handleTheme([]string{"../night"})
	app.handleTheme([]string{"missing"})
	if len(*saved) != 3 || app.Theme.Name != "futurewave" {
		t.Fatalf("bad names changed something: %q %q", *saved, app.Theme.Name)
	}
}

func TestThemeCommandOffWhenSysopPins(t *testing.T) {
	app, saved := themeApp(t, "")
	app.UserThemes = false
	app.handleTheme([]string{"night"})
	if app.Theme.Name != "futurewave" || len(*saved) != 0 {
		t.Fatal("user_themes = off still switched")
	}
}

// The modals draw in the theme's colours.
func TestModalsUseTheme(t *testing.T) {
	app, _ := themeApp(t, "")
	app.handleTheme([]string{"night"})
	app.openChannels()
	app.modal.(*ChannelsModal).Render()
	if got := app.modalFrame.CellAt(5, 10).Attr; got != ansi.LightBlue|ansi.BgBlack {
		t.Fatalf("channels fill = %v", got)
	}
}

func TestSettingsPicksTheme(t *testing.T) {
	// c: next theme (futurewave -> night), s: save.
	app, saved := themeApp(t, "cs")
	styleSaved := false
	app.OnStyleSave = func(*nickstyle.Style) error { styleSaved = true; return nil }
	app.openSettings()
	if app.Theme.Name != "night" || strings.Join(*saved, ",") != "night" {
		t.Fatalf("theme %q saved %q", app.Theme.Name, *saved)
	}
	if styleSaved {
		t.Fatal("an unchanged handle style was rewritten")
	}
}

// Unsaved edits survive a trip to the avatar picker and back.
func TestSettingsKeepsEditsAcrossAvatarPick(t *testing.T) {
	// f: letter 1 colour, c: theme, p: picker (cancelled), then s on return.
	in := ansi.NewInput(bytes.NewReader([]byte("fcps")))
	app, saved := themeApp(t, "")
	app.Input = in
	var style *nickstyle.Style
	app.OnStyleSave = func(st *nickstyle.Style) error { style = st; return nil }
	app.OnAvatarPick = func() (string, string, error) { return "", "cancelled", nil }
	app.openSettings()
	if style == nil || len(style.Colors) == 0 || style.Colors[0] != "#000000" {
		t.Fatalf("style edit lost: %+v", style)
	}
	if strings.Join(*saved, ",") != "night" {
		t.Fatalf("theme pick lost: %q", *saved)
	}
}
