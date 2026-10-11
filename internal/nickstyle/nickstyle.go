// Package nickstyle is a member's chat handle style: one truecolor hex per
// character of their alias plus an optional short tag. It is the same record
// the Synchronet shell (fshell_ts chat/nick_style.ts) and the website keep in
// data/chat/nickstyle/NNNN.json through mods/load/chat_style_lib.js, and the
// same object every client carries verbatim on the wire as nick.style:
//
//	{"colors": ["#ff0000", "", "#00ff00"], "tag": {"text": "sysop", "fg": "#ffff55", "bg": ""}}
//
// Normalize mirrors chat_style_lib.js normalize() exactly, so a record that
// round-trips through the door is byte-for-byte what the shell would write.
package nickstyle

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	// MaxColors caps the per-character list (chat_style_lib.js MAX_COLORS).
	MaxColors = 60
	// MaxTag caps the tag text (chat_style_lib.js MAX_TAG).
	MaxTag = 12
	// Version is the stored record version (chat_style_lib.js VERSION).
	Version = 1
)

// Tag is the short label drawn after the name in its own colours.
type Tag struct {
	Text string `json:"text"`
	Fg   string `json:"fg"` // '#rrggbb' or ''
	Bg   string `json:"bg"`
}

// Style is one member's handle style. The zero value is the empty style.
type Style struct {
	Colors []string `json:"colors"` // one '#rrggbb' (or '' = default) per alias character
	Tag    Tag      `json:"tag"`
}

// UnmarshalJSON accepts anything a peer might send and normalizes it, so a
// malformed style (numbers in colors, a string tag) never makes the whole
// chat message undecodable.
func (s *Style) UnmarshalJSON(b []byte) error {
	var raw struct {
		Colors []json.RawMessage `json:"colors"`
		Tag    json.RawMessage   `json:"tag"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		*s = Style{}
		return nil
	}
	colors := make([]string, 0, len(raw.Colors))
	for _, c := range raw.Colors {
		var v string
		if json.Unmarshal(c, &v) != nil {
			v = ""
		}
		colors = append(colors, v)
	}
	var tag struct {
		Text json.RawMessage `json:"text"`
		Fg   json.RawMessage `json:"fg"`
		Bg   json.RawMessage `json:"bg"`
	}
	_ = json.Unmarshal(raw.Tag, &tag)
	*s = Normalize(Style{
		Colors: colors,
		Tag:    Tag{Text: rawString(tag.Text), Fg: rawString(tag.Fg), Bg: rawString(tag.Bg)},
	})
	return nil
}

// rawString decodes a JSON string, or stringifies a number/bool the way
// JavaScript's String() would; anything else is ”.
func rawString(b json.RawMessage) string {
	if len(b) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	var f float64
	if json.Unmarshal(b, &f) == nil {
		return fmt.Sprint(f)
	}
	var v bool
	if json.Unmarshal(b, &v) == nil {
		return fmt.Sprint(v)
	}
	return ""
}

// Hex normalizes a colour to lowercase '#rrggbb', expanding '#rgb'; anything
// else is ”.
func Hex(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	if len(s) == 4 && s[0] == '#' && isHex(s[1:]) {
		s = "#" + string([]byte{s[1], s[1], s[2], s[2], s[3], s[3]})
	}
	if len(s) == 7 && s[0] == '#' && isHex(s[1:]) {
		return s
	}
	return ""
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// CleanTag keeps printable ASCII, drops pipes (never a pipe code), trims and
// caps at MaxTag.
func CleanTag(value string) string {
	b := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 0x20 && c <= 0x7e && c != '|' {
			b = append(b, c)
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > MaxTag {
		s = s[:MaxTag]
	}
	return s
}

// Normalize returns a well-formed copy: valid hex or ” per colour, at most
// MaxColors, trailing defaults trimmed, a clean tag whose colours are cleared
// when it has no text.
func Normalize(in Style) Style {
	out := Style{Colors: []string{}}
	for i := 0; i < len(in.Colors) && i < MaxColors; i++ {
		out.Colors = append(out.Colors, Hex(in.Colors[i]))
	}
	for len(out.Colors) > 0 && out.Colors[len(out.Colors)-1] == "" {
		out.Colors = out.Colors[:len(out.Colors)-1]
	}
	out.Tag.Text = CleanTag(in.Tag.Text)
	out.Tag.Fg = Hex(in.Tag.Fg)
	out.Tag.Bg = Hex(in.Tag.Bg)
	if out.Tag.Text == "" {
		out.Tag.Fg, out.Tag.Bg = "", ""
	}
	return out
}

// Wire is the style as the door sends and draws it: the colours only. The
// tag is no longer shown (it made names read differently and broke
// lookups), but a stored one is left in the record. Nil when no colour.
func (s *Style) Wire() *Style {
	if s == nil {
		return nil
	}
	out := &Style{Colors: append([]string(nil), s.Colors...)}
	if out.IsEmpty() {
		return nil
	}
	return out
}

// KeepTag returns next with stored's tag, so a save from a client that no
// longer edits tags doesn't erase one another client wrote.
func KeepTag(next, stored *Style) *Style {
	out := Style{}
	if next != nil {
		out.Colors = append([]string(nil), next.Colors...)
	}
	if stored != nil {
		out.Tag = stored.Tag
	}
	return &out
}

// IsEmpty reports whether the style changes nothing (no colour, no tag).
func (s *Style) IsEmpty() bool {
	if s == nil {
		return true
	}
	for _, c := range s.Colors {
		if c != "" {
			return false
		}
	}
	return s.Tag.Text == ""
}

// Align returns one colour per byte of name (” = default): stored colours
// are per character of the alias, so a longer name pads with default.
func (s *Style) Align(name string) []string {
	out := make([]string, len(name))
	if s == nil {
		return out
	}
	for i := range out {
		if i < len(s.Colors) {
			out[i] = s.Colors[i]
		}
	}
	return out
}

// RGB parses '#rrggbb'; ok is false for ” or anything invalid.
func RGB(hex string) (r, g, b uint8, ok bool) {
	h := Hex(hex)
	if h == "" {
		return 0, 0, 0, false
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		var n uint8
		for _, c := range []byte(h[1+2*i : 3+2*i]) {
			n <<= 4
			if c >= 'a' {
				n |= c - 'a' + 10
			} else {
				n |= c - '0'
			}
		}
		v[i] = n
	}
	return v[0], v[1], v[2], true
}

// ToHex formats a colour as '#rrggbb', clamping and rounding each channel.
func ToHex(r, g, b float64) string {
	return fmt.Sprintf("#%02x%02x%02x", clamp(r), clamp(g), clamp(b))
}

func clamp(v float64) uint8 {
	return uint8(math.Max(0, math.Min(255, math.Round(v))))
}

// Preset fills n colours the way the shell and website editors do
// (chat_style_lib.js preset): "solid" (a), "gradient" (a -> b), "rainbow",
// anything else clears.
func Preset(kind string, n int, a, b string) []string {
	if n < 0 {
		n = 0
	}
	out := make([]string, 0, n)
	switch kind {
	case "solid":
		for i := 0; i < n; i++ {
			out = append(out, Hex(a))
		}
		return out
	case "gradient":
		ar, ag, ab, okA := RGB(a)
		br, bg, bb, okB := RGB(b)
		if !okA || !okB {
			if a == "" {
				a = b
			}
			return Preset("solid", n, a, "")
		}
		for i := 0; i < n; i++ {
			t := 0.0
			if n > 1 {
				t = float64(i) / float64(n-1)
			}
			out = append(out, ToHex(
				float64(ar)+(float64(br)-float64(ar))*t,
				float64(ag)+(float64(bg)-float64(ag))*t,
				float64(ab)+(float64(bb)-float64(ab))*t))
		}
		return out
	case "rainbow":
		for i := 0; i < n; i++ {
			out = append(out, hsvHex(float64(i)/math.Max(1, float64(n))*360, 0.85, 1))
		}
		return out
	}
	for i := 0; i < n; i++ {
		out = append(out, "")
	}
	return out
}

func hsvHex(h, s, v float64) string {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return ToHex((r+m)*255, (g+m)*255, (b+m)*255)
}

// CGA is the sixteen-colour palette in IBM order, as chat_style_lib.js and
// the shell use it, so a colour picked from it in the door is the exact hex
// the web and shell show.
var CGA = [16]string{
	"#000000", "#0000aa", "#00aa00", "#00aaaa", "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
	"#555555", "#5555ff", "#55ff55", "#55ffff", "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
}
