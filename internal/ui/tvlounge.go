package ui

import (
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/telnetvision"
)

// TV lounge mode: when the channel is "tuned" to a telnetvision feed, the
// transcript region becomes a video screen. Reuses the existing 3-layer
// composite — video on the bg layer, the (optional) transcript in the middle,
// and timeboxed message popups + a top caption bar on the fg layer.
//
// Layered defaults (overridable via config / commands):
//   - 24-bit truecolor (TVColor), with a 16-color toggle
//   - message popups linger 5-15s (TVPopupSecs) then vanish
//   - avatars in popups off by default (they compete with video)

const tvFPS = 12 // video refresh cap; the consumer is drop-to-latest

type tvPopup struct {
	msg *chat.Message
	at  time.Time
}

// loungeActive reports whether we should be rendering the TV screen right now:
// the channel is tuned and the user hasn't opted out.
func (a *App) loungeActive() bool {
	return a.tvTuner != nil && !a.tvOptedOut
}

func (a *App) tvColorTrue() bool {
	return !strings.EqualFold(strings.TrimSpace(a.TVColor), "16")
}

func (a *App) tvPopupWindow() time.Duration {
	secs := a.TVPopupSecs
	if secs < 5 {
		secs = 5
	}
	if secs > 15 {
		secs = 15
	}
	return time.Duration(secs) * time.Second
}

// tvSync reconciles the session's tuner state with our consumer connection,
// opening or closing the telnetvision stream when the room is tuned/untuned or
// retuned. Called once per main-loop iteration.
func (a *App) tvSync() {
	cur := a.Session.Tuner()
	changed := (cur == nil) != (a.tvTuner == nil)
	if cur != nil && a.tvTuner != nil {
		changed = cur.Host != a.tvTuner.Host || cur.Port != a.tvTuner.Port || cur.Channel != a.tvTuner.Channel
	}
	if changed {
		a.tvTuner = cur
		a.tvPopups = nil
		a.tvShowTranscript = false
		a.tvOptedOut = false // a fresh tune opts everyone back in
		a.forceFullRedraw()
	}
	// Stream only while tuned AND not opted out — so /tvoff stops the bytes.
	want := a.tvTuner != nil && !a.tvOptedOut
	if want && a.tvConsumer == nil {
		a.tvConsumer = telnetvision.NewConsumer(a.tvTuner.Host, a.tvTuner.Port, a.tvTuner.Channel)
		a.tvConsumer.Start()
	} else if !want && a.tvConsumer != nil {
		a.tvConsumer.Close()
		a.tvConsumer = nil
	}
}

// tvTick paces the video redraw while the lounge is active.
func (a *App) tvTick() {
	now := time.Now()
	if now.Sub(a.tvLastTick) < time.Second/tvFPS {
		return
	}
	a.tvLastTick = now
	a.dirty = true
}

// addPopup records an incoming chat line as a timeboxed popup. Notices
// (nil nick) are skipped — only real comments pop over the video.
func (a *App) addPopup(m *chat.Message) {
	if m == nil || m.Nick == nil {
		return
	}
	a.tvPopups = append(a.tvPopups, tvPopup{msg: m, at: time.Now()})
	if len(a.tvPopups) > 12 {
		a.tvPopups = a.tvPopups[len(a.tvPopups)-12:]
	}
}

// drawLounge paints the TV screen into the three transcript layers. The
// caller still renders the header, action bars, and input line.
func (a *App) drawLounge() {
	bg := a.bgAnimFrame
	bg.Clear()
	var fr *telnetvision.Frame
	if a.tvConsumer != nil {
		fr = a.tvConsumer.Latest()
	}
	if fr != nil {
		fr.RenderTo(bg, 0, 0, bg.W, bg.H, telnetvision.RenderOpts{
			Truecolor:  a.tvColorTrue(),
			Saturation: 1.8,
			Dither:     true,
		})
	} else {
		label := "the channel"
		if a.tvTuner != nil {
			label = a.tvTuner.Channel
		}
		msg := "Tuning in to " + label + " ..."
		bg.PrintAt((bg.W-len(msg))/2, bg.H/2, []byte(msg), ansi.White|ansi.BgBlack)
	}

	// Middle layer: the transcript, only when the user has pulled it up.
	if a.tvShowTranscript {
		a.transcript.Render(a.Session.Messages())
	} else {
		a.transcriptFrame.Clear()
	}

	// Top layer: caption bar (top row) + message popups (bottom), but popups
	// hide while the transcript is up so the two don't fight for space.
	a.fgAnimFrame.Clear()
	if fr != nil && strings.TrimSpace(fr.Caption) != "" {
		a.drawCaptionTop(fr.Caption)
	}
	if !a.tvShowTranscript {
		a.drawPopups()
	}
}

// drawCaptionTop renders the live caption as a centered bar on the FIRST row
// of the video area (the door keeps the bottom for chat input, the very top
// for info — so subtitles sit just below the chrome, not over the input).
func (a *App) drawCaptionTop(caption string) {
	f := a.fgAnimFrame
	w := f.W
	bar := ansi.White | ansi.BgBlue
	for x := 0; x < w; x++ {
		f.SetCell(x, 0, ' ', bar)
	}
	runes := []rune(strings.TrimSpace(caption))
	if len(runes) > w {
		runes = runes[len(runes)-w:] // keep the most recent words
	}
	pad := (w - len(runes)) / 2
	for i, r := range runes {
		c := byte('?')
		if r < 128 {
			c = byte(r)
			if c < 0x20 {
				c = ' '
			}
		}
		f.SetCell(pad+i, 0, c, bar)
	}
}

// drawPopups renders recent message popups bottom-up on the fg layer, dropping
// any older than the popup window. Single-line ("nick: text") by default; a
// compact avatar bubble when TVAvatars is on and the sender has an avatar.
func (a *App) drawPopups() {
	f := a.fgAnimFrame
	now := time.Now()
	window := a.tvPopupWindow()
	kept := a.tvPopups[:0]
	for _, p := range a.tvPopups {
		if now.Sub(p.at) <= window {
			kept = append(kept, p)
		}
	}
	a.tvPopups = kept
	if len(kept) == 0 {
		return
	}
	y := f.H - 1 // bottom of the video area (input line is a separate frame)
	for i := len(kept) - 1; i >= 0 && y >= 1; i-- {
		p := kept[i]
		if a.TVAvatars {
			y = a.drawPopupBubble(f, y, p)
		} else {
			a.drawPopupLine(f, y, p)
			y--
		}
	}
}

// drawPopupLine draws a single-row popup: "nick: text", nick colorized, on a
// black band so it reads over the video.
func (a *App) drawPopupLine(f *ansi.Frame, row int, p tvPopup) {
	nick := ""
	if p.msg.Nick != nil {
		nick = p.msg.Nick.Name
	}
	band := ansi.LightGray | ansi.BgBlack
	for x := 0; x < f.W; x++ {
		f.SetCell(x, row, ' ', band)
	}
	x := 1
	x = putStr(f, x, row, nick, a.nickColor(nick)|ansi.BgBlack)
	x = putStr(f, x, row, ": ", ansi.LightGray|ansi.BgBlack)
	putStr(f, x, row, p.msg.Str, ansi.White|ansi.BgBlack)
}

// drawPopupBubble draws a compact avatar bubble (avatar + nick + wrapped text)
// ending at row bottomY, and returns the row above it for the next popup.
func (a *App) drawPopupBubble(f *ansi.Frame, bottomY int, p tvPopup) int {
	top := bottomY - avatar.Height + 1
	if top < 1 {
		return 0 // no room left
	}
	nick, av := "", ""
	if p.msg.Nick != nil {
		nick = p.msg.Nick.Name
		av = p.msg.Nick.Avatar
	}
	if avv, err := avatar.FromBase64(av); err == nil {
		avv := avv // avoid aliasing surprises
		avv.RenderTo(f, 0, top)
	}
	textX := avatar.Width + 1
	maxW := f.W - textX
	if maxW < 4 {
		return top - 1
	}
	putStr(f, textX, top, nick, a.nickColor(nick)|ansi.BgBlack)
	lines := wrapPlain(p.msg.Str, maxW)
	for i, ln := range lines {
		ry := top + 1 + i
		if ry > bottomY {
			break
		}
		putStr(f, textX, ry, ln, ansi.White|ansi.BgBlack)
	}
	return top - 1
}

// putStr writes s at (x,row) in CP437 bytes (non-ASCII -> '?'), clipped to the
// frame width, and returns the next x.
func putStr(f *ansi.Frame, x, row int, s string, attr ansi.Attr) int {
	for _, r := range s {
		if x >= f.W {
			break
		}
		c := byte('?')
		if r < 128 {
			c = byte(r)
			if c < 0x20 {
				c = ' '
			}
		}
		f.SetCell(x, row, c, attr)
		x++
	}
	return x
}

// wrapPlain word-wraps s to width w (ASCII-ish; coarse but fine for popups).
func wrapPlain(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for len(s) > w {
		cut := w
		if sp := strings.LastIndexByte(s[:w], ' '); sp > 0 {
			cut = sp
		}
		out = append(out, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// nickColor maps a nick to a stable bright CGA color so speakers are
// distinguishable in popups.
func (a *App) nickColor(nick string) ansi.Attr {
	palette := []ansi.Attr{
		ansi.LightCyan, ansi.LightGreen, ansi.Yellow, ansi.LightMagenta,
		ansi.LightRed, ansi.White, ansi.LightBlue,
	}
	var h uint32
	for i := 0; i < len(nick); i++ {
		h = h*31 + uint32(nick[i])
	}
	return palette[h%uint32(len(palette))]
}
