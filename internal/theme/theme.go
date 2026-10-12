// Package theme owns the color palette and screensaver profile for the
// avatar_chat_universal door. The default theme ("futurewave") matches the
// out-of-the-box look; sysops can ship custom themes by placing a
// themes/<name>.ini file alongside the door binary and pointing the main
// config's `theme = <name>` key at it.
//
// Theme files are flat key=value INIs (same syntax as avatar_chat.ini).
// Every key is optional -- unset keys inherit from Default(). Color values
// are pipe-separated combinations of CGA names: "lightcyan|bgmagenta",
// "white", "yellow|bgblue", etc. See ParseColor for the full list.
package theme

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Theme groups every color slot the chat UI knows about plus optional
// screensaver overrides. A theme can leave the idle fields nil/empty to
// inherit the main config's values; setting them lets a theme ship a
// curated screensaver profile.
type Theme struct {
	Name string

	// Header bar (MOTD strip across the top).
	HeaderStats ansi.Attr // "MOTD :" label
	HeaderMotd  ansi.Attr // MOTD body text

	// The button rows under the header (Who, Rooms, Images, ...).
	TopActionBar       ansi.Attr // background fill of the two rows
	TopActionPill      ansi.Attr // a button's face and label
	TopActionHighlight ansi.Attr // a button calling for attention (new pictures, PMs)
	// TopActionShadow is the half-block shadow under and right of each
	// button: foreground only, drawn on the bar's background.
	TopActionShadow ansi.Attr

	// Strips over the transcript area: the TV lounge's key strip on the
	// picture and the screensaver's message ticker.
	StripBar  ansi.Attr
	StripPill ansi.Attr

	// Input line (the row where the user types).
	InputPrompt ansi.Attr
	InputText   ansi.Attr
	InputCursor ansi.Attr
	InputBg     ansi.Attr // fill of the rest of the input row

	// Message bubbles.
	BubbleLeft    ansi.Attr // others' messages
	BubbleSelf    ansi.Attr // own messages
	BubblePrivate ansi.Attr // private (PM) messages, regardless of side
	BubbleMention ansi.Attr // others' public lines that say your name
	MentionEdge   ansi.Attr // fg of the bar down the left edge of a mention

	// Link-preview cards inside a bubble: foregrounds only, drawn on the
	// bubble's own background.
	CardSite  ansi.Attr // site name line
	CardTitle ansi.Attr // page title
	CardEdge  ansi.Attr // bar down the card's left edge

	// Bubble header line (speaker, timestamp, BBS host).
	SpeakerLeft ansi.Attr
	SpeakerSelf ansi.Attr
	Timestamp   ansi.Attr
	Host        ansi.Attr

	// Notice rendering (non-bubble status lines like "* Last msg ..." and
	// "* Users in main: ...").
	NoticeDefault ansi.Attr

	// Modal frames (/who roster, /channels, /settings, image viewer).
	ModalBg     ansi.Attr // fill and plain list text
	ModalTitle  ansi.Attr // title bar
	ModalClose  ansi.Attr // "[Esc] close" on the title bar
	ModalText   ansi.Attr // bright text: values, key letters
	ModalLabel  ansi.Attr // section headings and field labels
	ModalSelect ansi.Attr // the highlighted list row
	ModalHint   ansi.Attr // key help along the bottom, markers
	ModalUnread ansi.Attr // unread counts in /channels
	ModalRule   ansi.Attr // divider lines and "(none)"
	ModalOK     ansi.Attr // confirmation and info lines
	ViewerBar   ansi.Attr // image viewer header and footer rows
	// ModalFrame is the box around every modal and dialog: foreground
	// only, drawn on the panel's own background, so it stands apart from
	// the bars and bubbles behind it.
	ModalFrame ansi.Attr
	// CloseButton is the "[X]" at the top left of every modal and of the
	// chat screen; a click on it is Esc.
	CloseButton ansi.Attr

	// Dialogs drawn over the chat (the avatar upload instructions).
	DialogBg    ansi.Attr
	DialogTitle ansi.Attr
	DialogHead  ansi.Attr
	DialogHint  ansi.Attr
	DialogOK    ansi.Attr

	// NSFW veil: shade noise in two attrs plus the reveal label.
	VeilDark  ansi.Attr
	VeilLight ansi.Attr
	VeilLabel ansi.Attr

	// TV lounge caption bar -- the live-subtitle strip painted across the
	// last row of the chat area while a channel is tuned to a telnetvision feed.
	TVCaption ansi.Attr
	TVChat    ansi.Attr // chat lines over the picture
	TVChatDim ansi.Attr // the band behind them and the ": " separator

	// Screensaver / idle profile. nil/empty means "inherit from main config".
	IdleRandom   *bool
	IdleSequence []string
	IdleDisable  []string
}

// Default returns the built-in "futurewave" theme: the look the door has
// shipped with from day one. All other themes load on top of this, so any
// key the sysop omits stays consistent.
func Default() *Theme {
	return &Theme{
		Name: "futurewave",

		HeaderStats: ansi.Black | ansi.BgGreen,
		HeaderMotd:  ansi.White | ansi.BgGreen,

		TopActionBar:       ansi.White | ansi.BgBlue,
		TopActionPill:      ansi.LightCyan | ansi.BgMagenta,
		TopActionHighlight: ansi.White | ansi.BgRed,
		TopActionShadow:    ansi.Black,
		StripBar:           ansi.White | ansi.BgMagenta,
		StripPill:          ansi.Black | ansi.BgCyan,

		InputPrompt: ansi.Yellow,
		InputText:   ansi.LightGreen,
		InputCursor: ansi.LightGray | ansi.BgLightGray,
		InputBg:     ansi.LightGray | ansi.BgBlack,

		BubbleLeft:    ansi.Black | ansi.BgCyan,
		BubbleSelf:    ansi.White | ansi.BgBlue,
		BubblePrivate: ansi.White | ansi.BgRed,
		BubbleMention: ansi.Black | ansi.BgBrown,
		MentionEdge:   ansi.Yellow,
		CardSite:      ansi.DarkGray,
		CardTitle:     ansi.White,
		CardEdge:      ansi.LightBlue,

		SpeakerLeft: ansi.LightMagenta,
		SpeakerSelf: ansi.LightCyan,
		Timestamp:   ansi.LightBlue,
		Host:        ansi.Magenta,

		NoticeDefault: ansi.DarkGray,

		ModalBg:     ansi.LightGray | ansi.BgBlack,
		ModalTitle:  ansi.Yellow | ansi.BgBlue,
		ModalClose:  ansi.White | ansi.BgBlue,
		ModalText:   ansi.White | ansi.BgBlack,
		ModalLabel:  ansi.LightCyan | ansi.BgBlack,
		ModalSelect: ansi.Black | ansi.BgCyan,
		ModalHint:   ansi.Yellow | ansi.BgBlack,
		ModalUnread: ansi.Yellow | ansi.BgBlack,
		ModalRule:   ansi.DarkGray | ansi.BgBlack,
		ModalOK:     ansi.LightGreen | ansi.BgBlack,
		ViewerBar:   ansi.White | ansi.BgBlue,
		ModalFrame:  ansi.Yellow,
		CloseButton: ansi.LightRed | ansi.BgBlack,

		DialogBg:    ansi.White | ansi.BgBlue,
		DialogTitle: ansi.LightCyan | ansi.BgMagenta,
		DialogHead:  ansi.LightCyan | ansi.BgBlue,
		DialogHint:  ansi.Yellow | ansi.BgBlue,
		DialogOK:    ansi.LightGreen | ansi.BgBlue,

		VeilDark:  ansi.DarkGray | ansi.BgBlack,
		VeilLight: ansi.LightGray | ansi.BgBlack,
		VeilLabel: ansi.White | ansi.BgMagenta,

		TVCaption: ansi.White | ansi.BgBlue,
		TVChat:    ansi.White | ansi.BgBlack,
		TVChatDim: ansi.LightGray | ansi.BgBlack,
	}
}

// Load reads the theme INI at path, applying overrides on top of Default().
// A missing file returns Default() with no error so sysops can drop a theme
// in later without restarting; only parse errors are returned.
func Load(path string) (*Theme, error) {
	t := Default()
	if path == "" {
		return t, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return t, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		applyKey(t, key, val)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("theme: %s: %v", path, err)
	}
	return t, nil
}

func applyKey(t *Theme, key, val string) {
	// Color slots; retired names still load (retiredKeys).
	if k, ok := retiredKeys[key]; ok {
		key = k
	}
	if attrPtr := colorSlot(t, key); attrPtr != nil {
		if a, ok := ParseColor(val); ok {
			*attrPtr = a
		}
		return
	}
	// Idle / screensaver overrides.
	switch key {
	case "name":
		t.Name = val
	case "idle_random":
		b := parseBool(val)
		t.IdleRandom = &b
	case "idle_sequence":
		t.IdleSequence = splitCSV(val)
	case "idle_disable":
		t.IdleDisable = splitCSV(val)
	}
}

// colorSlot returns a pointer to the named ansi.Attr field on t, or nil if
// the key isn't a known color slot. Centralizing this here keeps Load and
// any future "show defaults" command in sync.
func colorSlot(t *Theme, key string) *ansi.Attr {
	switch key {
	case "header_stats":
		return &t.HeaderStats
	case "header_motd":
		return &t.HeaderMotd
	case "top_action_bar":
		return &t.TopActionBar
	case "top_action_pill":
		return &t.TopActionPill
	case "top_action_highlight":
		return &t.TopActionHighlight
	case "top_action_shadow":
		return &t.TopActionShadow
	case "strip_bar":
		return &t.StripBar
	case "strip_pill":
		return &t.StripPill
	case "input_prompt":
		return &t.InputPrompt
	case "input_text":
		return &t.InputText
	case "input_cursor":
		return &t.InputCursor
	case "input_bg":
		return &t.InputBg
	case "bubble_left":
		return &t.BubbleLeft
	case "bubble_self":
		return &t.BubbleSelf
	case "bubble_private":
		return &t.BubblePrivate
	case "bubble_mention":
		return &t.BubbleMention
	case "mention_edge":
		return &t.MentionEdge
	case "card_site":
		return &t.CardSite
	case "card_title":
		return &t.CardTitle
	case "card_edge":
		return &t.CardEdge
	case "speaker_left":
		return &t.SpeakerLeft
	case "speaker_self":
		return &t.SpeakerSelf
	case "timestamp":
		return &t.Timestamp
	case "host":
		return &t.Host
	case "notice_default":
		return &t.NoticeDefault
	case "modal_bg":
		return &t.ModalBg
	case "modal_title":
		return &t.ModalTitle
	case "modal_close":
		return &t.ModalClose
	case "modal_text":
		return &t.ModalText
	case "modal_label":
		return &t.ModalLabel
	case "modal_select":
		return &t.ModalSelect
	case "modal_hint":
		return &t.ModalHint
	case "modal_unread":
		return &t.ModalUnread
	case "modal_rule":
		return &t.ModalRule
	case "modal_ok":
		return &t.ModalOK
	case "viewer_bar":
		return &t.ViewerBar
	case "modal_frame":
		return &t.ModalFrame
	case "close_button":
		return &t.CloseButton
	case "dialog_bg":
		return &t.DialogBg
	case "dialog_title":
		return &t.DialogTitle
	case "dialog_head":
		return &t.DialogHead
	case "dialog_hint":
		return &t.DialogHint
	case "dialog_ok":
		return &t.DialogOK
	case "veil_dark":
		return &t.VeilDark
	case "veil_light":
		return &t.VeilLight
	case "veil_label":
		return &t.VeilLabel
	case "tv_caption":
		return &t.TVCaption
	case "tv_chat":
		return &t.TVChat
	case "tv_chat_dim":
		return &t.TVChatDim
	}
	return nil
}

// ParseColor turns "lightcyan|bgmagenta" into the matching ansi.Attr.
// Pipe-separated tokens are ORed together; whitespace is ignored. Case
// insensitive. Returns false on any unknown token so callers can fall back
// to a default rather than render with a junk color.
func ParseColor(s string) (ansi.Attr, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var out ansi.Attr
	for _, raw := range strings.Split(s, "|") {
		tok := strings.ToLower(strings.TrimSpace(raw))
		if tok == "" {
			continue
		}
		v, ok := colorTokens[tok]
		if !ok {
			return 0, false
		}
		out |= v
	}
	return out, true
}

var colorTokens = map[string]ansi.Attr{
	"black":        ansi.Black,
	"blue":         ansi.Blue,
	"green":        ansi.Green,
	"cyan":         ansi.Cyan,
	"red":          ansi.Red,
	"magenta":      ansi.Magenta,
	"brown":        ansi.Brown,
	"lightgray":    ansi.LightGray,
	"darkgray":     ansi.DarkGray,
	"lightblue":    ansi.LightBlue,
	"lightgreen":   ansi.LightGreen,
	"lightcyan":    ansi.LightCyan,
	"lightred":     ansi.LightRed,
	"lightmagenta": ansi.LightMagenta,
	"yellow":       ansi.Yellow,
	"white":        ansi.White,
	"bgblack":      ansi.BgBlack,
	"bgblue":       ansi.BgBlue,
	"bggreen":      ansi.BgGreen,
	"bgcyan":       ansi.BgCyan,
	"bgred":        ansi.BgRed,
	"bgmagenta":    ansi.BgMagenta,
	"bgbrown":      ansi.BgBrown,
	"bglightgray":  ansi.BgLightGray,
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.ToLower(strings.TrimSpace(p))
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validName is what a theme name may look like: it becomes a file name
// under the themes directory, so nothing that can climb out of it.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidName reports whether name is usable as a theme name.
func ValidName(name string) bool { return validName.MatchString(name) }

// List returns the theme names available in dir (themes/<name>.ini),
// sorted. A name that isn't ValidName is skipped. The built-in default is
// always included, file or not.
func List(dir string) []string {
	seen := map[string]bool{Default().Name: true}
	out := []string{Default().Name}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(n), ".ini") {
			continue
		}
		n = strings.ToLower(strings.TrimSuffix(n, n[len(n)-4:]))
		if ValidName(n) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// LoadNamed loads themes/<name>.ini from dir. Unlike Load, a name that
// isn't ValidName or has no file is an error (except the built-in
// default), since a user picked it from a list.
func LoadNamed(dir, name string) (*Theme, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidName(name) {
		return nil, fmt.Errorf("theme: bad name %q", name)
	}
	path := findFile(dir, name)
	if path == "" {
		if name == Default().Name {
			return Default(), nil
		}
		return nil, fmt.Errorf("theme: no theme called %q", name)
	}
	t, err := Load(path)
	if err != nil {
		return nil, err
	}
	// The file name is the theme's identity for per-user choice; a name
	// key inside the file is only a label.
	t.Name = name
	return t, nil
}

// findFile returns dir's <name>.ini matched without regard to case, as
// List names them, or "".
func findFile(dir, name string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), name+".ini") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// Keys returns every color key a theme file may set, in file order.
func Keys() []string { return append([]string(nil), colorKeys...) }

var colorKeys = []string{
	"header_stats", "header_motd",
	"top_action_bar", "top_action_pill", "top_action_highlight", "top_action_shadow",
	"strip_bar", "strip_pill",
	"input_prompt", "input_text", "input_cursor", "input_bg",
	"bubble_left", "bubble_self", "bubble_private", "bubble_mention", "mention_edge",
	"card_site", "card_title", "card_edge",
	"speaker_left", "speaker_self", "timestamp", "host",
	"notice_default",
	"modal_bg", "modal_title", "modal_close", "modal_text", "modal_label",
	"modal_select", "modal_hint", "modal_unread", "modal_rule", "modal_ok", "viewer_bar",
	"modal_frame", "close_button",
	"dialog_bg", "dialog_title", "dialog_head", "dialog_hint", "dialog_ok",
	"veil_dark", "veil_light", "veil_label",
	"tv_caption", "tv_chat", "tv_chat_dim",
}

// retiredKeys are names older theme files use, mapped to the slot that
// took over their job ("" = the slot is gone and the line is skipped), so
// a custom theme written for the old bottom bar still loads.
var retiredKeys = map[string]string{
	"bottom_action_bar":       "strip_bar",
	"bottom_action_pill":      "strip_pill",
	"bottom_action_highlight": "",
}

// Lint reports every line of a theme file that Load would ignore: an
// unknown key, a color it can't parse, or a foreground the same as its
// background (text that can't be seen).
func Lint(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			out = append(out, fmt.Sprintf("line %d: no '='", n))
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "name", "idle_random", "idle_sequence", "idle_disable":
			continue
		}
		if k, ok := retiredKeys[key]; ok {
			if k == "" {
				continue
			}
			key = k
		}
		var probe Theme
		if colorSlot(&probe, key) == nil {
			out = append(out, fmt.Sprintf("line %d: unknown key %q", n, key))
			continue
		}
		a, ok := ParseColor(val)
		if !ok {
			out = append(out, fmt.Sprintf("line %d: %s: bad color %q", n, key, val))
			continue
		}
		if strings.Contains(val, "|") && a&0x0F == (a>>4)&0x07 && key != "input_cursor" {
			out = append(out, fmt.Sprintf("line %d: %s: text same colour as its background", n, key))
		}
	}
	return out, sc.Err()
}
