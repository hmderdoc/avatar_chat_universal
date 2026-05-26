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
// composite — video on the bg layer, the (optional) history overlay in the
// middle, and a command-hint strip + timeboxed message popups on the fg layer.
// The live caption bar takes over the bottom action row (where the command
// pills normally sit), with popups stacking up just above it.
//
// Layered defaults (overridable via config / commands):
//   - 24-bit truecolor (TVColor), with a 16-color toggle
//   - message popups linger 5-15s (TVPopupSecs) then vanish
//   - avatars in popups off by default (they compete with video)

const tvFPS = 12 // video refresh cap; the consumer is drop-to-latest

// tvHistoryIdle is how long the on-demand history overlay stays up after the
// last PgUp/PgDn before it auto-dismisses back to the full video.
const tvHistoryIdle = 30 * time.Second

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

// toggleTVColor flips the video between 24-bit truecolor and the 16-color CGA
// approximation. Truecolor support can't be sniffed reliably over a BBS wire
// (it isn't in the dropfile and there's no portable in-band query), so this is
// a manual switch — bound to both /tvcolor and Ctrl-T while in the lounge.
func (a *App) toggleTVColor() {
	if a.tvColorTrue() {
		a.TVColor = "16"
	} else {
		a.TVColor = "truecolor"
	}
	a.forceFullRedraw()
	a.dirty = true
	a.Session.Notice("TV color: " + a.TVColor)
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
		a.tvOptedOut = false // a fresh tune opts everyone back in
		// Drop the old feed unconditionally; the want-check below reopens it
		// for the new tuner. Without this, retuning (joining a different tuned
		// channel) would keep streaming the previous one.
		if a.tvConsumer != nil {
			a.tvConsumer.Close()
			a.tvConsumer = nil
		}
		a.clearLoungeResidue()
	}
	// Stream only while tuned AND not opted out — so /tvoff stops the bytes.
	want := a.tvTuner != nil && !a.tvOptedOut
	switch {
	case want && a.tvConsumer == nil:
		a.tvConsumer = telnetvision.NewConsumer(a.tvTuner.Host, a.tvTuner.Port, a.tvTuner.Channel)
		a.tvConsumer.Start()
	case !want && a.tvConsumer != nil:
		a.tvConsumer.Close()
		a.tvConsumer = nil
		a.clearLoungeResidue()
	}
}

// clearLoungeResidue blanks the video/popup/transcript layers and forces a
// full repaint, so leaving (or switching) a TV channel doesn't leave residual
// half-block characters bleeding through the normal chat view.
func (a *App) clearLoungeResidue() {
	a.tvShowTranscript = false
	a.transcript.Scroll = 0
	a.bgAnimFrame.Clear()
	a.fgAnimFrame.Clear()
	a.transcriptFrame.Clear()
	a.forceFullRedraw()
}

// tvTick paces the video redraw while the lounge is active and expires the
// history overlay once it's gone untouched for tvHistoryIdle.
func (a *App) tvTick() {
	if a.tvShowTranscript && time.Since(a.tvTranscriptShownAt) > tvHistoryIdle {
		a.hideLoungeHistory()
	}
	now := time.Now()
	if now.Sub(a.tvLastTick) < time.Second/tvFPS {
		return
	}
	a.tvLastTick = now
	a.dirty = true
}

// showLoungeHistory pulls the transcript over the video. The FIRST press is a
// plain "show the latest page" — it does NOT scroll back — because chat here
// vanishes after a few seconds and the user just wants to see what they missed.
// Paging further back is a separate, subsequent action (see pageLoungeHistory).
func (a *App) showLoungeHistory() {
	a.tvShowTranscript = true
	a.transcript.Scroll = 0
	a.tvTranscriptShownAt = time.Now()
	a.forceFullRedraw()
	a.dirty = true
}

// pageLoungeHistory scrolls the already-open overlay back by delta lines
// (positive = older), refreshing the idle timer so browsing keeps it up.
func (a *App) pageLoungeHistory(delta int) {
	a.transcript.Scroll += delta
	if a.transcript.Scroll < 0 {
		a.transcript.Scroll = 0
	}
	a.tvTranscriptShownAt = time.Now()
	a.dirty = true
}

// hideLoungeHistory dismisses the overlay and returns to the full video.
func (a *App) hideLoungeHistory() {
	if !a.tvShowTranscript {
		return
	}
	a.tvShowTranscript = false
	a.transcript.Scroll = 0
	a.forceFullRedraw()
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

// drawLounge paints the TV screen into the three transcript layers plus the
// bottom action row. Layout while tuned:
//
//	top of video (fg row 0) : command-hint strip (relocated from the bottom)
//	video body              : telnetvision frame on the bg layer
//	just above the caption  : timeboxed message popups (or the history overlay)
//	bottom action row       : live caption bar
//
// The caller still renders the header and the top action bar; we own the
// bottom action frame here so the caption can live where the command bar was.
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

	// Top layer: command-hint strip on the first row, then message popups
	// stacked up from the bottom (which sits just above the caption bar).
	// Popups hide while the history overlay is up so the two don't collide.
	a.fgAnimFrame.Clear()
	a.drawHintsTop()
	if !a.tvShowTranscript {
		a.drawPopups()
	}

	// Bottom action row: the live caption bar, replacing the command pills
	// (which moved to the top strip above).
	caption := ""
	if fr != nil {
		caption = fr.Caption
	}
	a.drawCaptionBar(caption)
}

// loungeHints are the command labels shown on the relocated top strip while a
// channel is tuned. The history controls lead since the transcript only lives
// on demand here; the rest mirror the normal bottom action bar.
var loungeHints = []string{
	"PgUp history", "PgDn/Dn close", "/join", "/me", "/msg", "/r", "/clear", "/quit",
}

// drawHintsTop paints the command-hint pills across the first row of the video
// area (fg layer), reusing the theme's bottom-bar palette. This is the bar that
// used to live on the bottom action row before the caption took that spot.
func (a *App) drawHintsTop() {
	f := a.fgAnimFrame
	barAttr := a.Theme.BottomActionBar
	pillAttr := a.Theme.BottomActionPill
	for x := 0; x < f.W; x++ {
		f.SetCell(x, 0, ' ', barAttr)
	}
	x := 1
	for i, lbl := range loungeHints {
		if i > 0 {
			x += 2 // gap between pills, in bar bg
		}
		pill := " " + lbl + " "
		for j := 0; j < len(pill); j++ {
			if x+j >= f.W {
				break
			}
			f.SetCell(x+j, 0, pill[j], pillAttr)
		}
		x += len(pill)
	}
}

// drawCaptionBar renders the live caption as a centered bar on the bottom
// action row (the frame that normally holds the command pills). When the feed
// has no caption yet we show a quiet "tuned to <channel>" placeholder so the
// row never reads as a mystery blank strip. Color comes from Theme.TVCaption.
func (a *App) drawCaptionBar(caption string) {
	f := a.bottomActionFrame
	f.Clear()
	w := f.W
	bar := a.Theme.TVCaption
	for x := 0; x < w; x++ {
		f.SetCell(x, 0, ' ', bar)
	}
	text := strings.TrimSpace(caption)
	if text == "" {
		label := "the channel"
		if a.tvTuner != nil && a.tvTuner.Channel != "" {
			label = a.tvTuner.Channel
		}
		text = "tuned to " + label
	}
	runes := []rune(text)
	if len(runes) > w {
		runes = runes[len(runes)-w:] // keep the most recent words
	}
	pad := (w - len(runes)) / 2
	if pad < 0 {
		pad = 0
	}
	for i, r := range runes {
		if pad+i >= w {
			break
		}
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

// popupMaxRows caps how tall a single wrapped popup can grow before it's
// truncated with a trailing "..." -- enough to read a normal sentence without
// letting one long line bury the video.
const popupMaxRows = 3

// drawPopups renders recent message popups bottom-up on the fg layer, dropping
// any older than the popup window. Long lines wrap ("nick: text" across rows);
// a compact avatar bubble is used instead when TVAvatars is on.
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
			y = a.drawPopupLines(f, y, p)
		}
	}
}

// drawPopupLines draws a wrapped popup ("nick: text", wrapping across rows on a
// black band) ending at row bottomY, and returns the row above it for the next
// (older) popup. The message is capped at popupMaxRows; overflow is trimmed
// with a trailing "...". The nick keeps its color on the first row only.
func (a *App) drawPopupLines(f *ansi.Frame, bottomY int, p tvPopup) int {
	if bottomY < 1 {
		return 0
	}
	nick := ""
	if p.msg.Nick != nil {
		nick = p.msg.Nick.Name
	}
	maxW := f.W - 2 // 1-col margin each side
	if maxW < 1 {
		return 0
	}
	prefix := nick + ": "
	lines := wrapPlain(prefix+p.msg.Str, maxW)
	if len(lines) > popupMaxRows {
		lines = lines[:popupMaxRows]
		last := lines[popupMaxRows-1]
		if len(last) > maxW-3 {
			last = last[:maxW-3]
		}
		lines[popupMaxRows-1] = last + "..."
	}
	// Clip to the rows actually available above the caption: row 0 holds the
	// hint strip, so only rows 1..bottomY are usable. Keep the tail (the most
	// recent lines) when a popup can't fully fit.
	if len(lines) > bottomY {
		lines = lines[len(lines)-bottomY:]
	}
	top := bottomY - len(lines) + 1
	band := ansi.LightGray | ansi.BgBlack
	for ry := top; ry <= bottomY; ry++ {
		for x := 0; x < f.W; x++ {
			f.SetCell(x, ry, ' ', band)
		}
	}
	for i, ln := range lines {
		ry := top + i
		// Colorize "nick: " only on the first row, and only if the wrap left
		// it intact at the start (a pathologically long nick may not).
		if i == 0 && strings.HasPrefix(ln, prefix) {
			x := putStr(f, 1, ry, nick, a.nickAttr(nick))
			x = putStr(f, x, ry, ": ", ansi.LightGray|ansi.BgBlack)
			putStr(f, x, ry, ln[len(prefix):], ansi.White|ansi.BgBlack)
		} else {
			putStr(f, 1, ry, ln, ansi.White|ansi.BgBlack)
		}
	}
	return top - 1
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
	putStr(f, textX, top, nick, a.nickAttr(nick))
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

// Nick badge coloring. In TV mode chat scrolls past quickly, so each speaker
// gets a stable colored name badge (fg + contrasting bg) seeded by their nick,
// to act as a quick-glance identifier over the video.

// nickFgPalette is the foreground set: the 12 saturated CGA colors, omitting
// the neutrals (black, dark gray, light gray, white) so a nick never washes
// out against the chrome or the video.
var nickFgPalette = []ansi.Attr{
	ansi.Blue, ansi.Green, ansi.Cyan, ansi.Red, ansi.Magenta, ansi.Brown,
	ansi.LightBlue, ansi.LightGreen, ansi.LightCyan, ansi.LightRed,
	ansi.LightMagenta, ansi.Yellow,
}

// nickBgPalette is the 8 CGA background colors (backgrounds can only use the
// low 8 of the palette).
var nickBgPalette = []ansi.Attr{
	ansi.BgBlack, ansi.BgBlue, ansi.BgGreen, ansi.BgCyan,
	ansi.BgRed, ansi.BgMagenta, ansi.BgBrown, ansi.BgLightGray,
}

// cgaLuma is the approximate perceptual luminance (0-255) of each CGA color
// index 0-15, from the standard IBM RGB levels (0x00/0x55/0xAA/0xFF) under
// Rec.601 weights. Used to keep a nick's fg readable against its bg.
var cgaLuma = [16]int{
	0, 19, 100, 119, 51, 70, 101, 170,
	85, 104, 185, 204, 136, 155, 236, 255,
}

// nickContrastMin is the minimum fg/bg luminance gap we accept; below this the
// pairing reads as mud (e.g. light-green on green). Every fg in the palette
// can hit this against at least one bg, so the fallback is rarely used.
const nickContrastMin = 96

// nickHash is a small FNV-1a over the lower-cased nick, so the color is stable
// per person and case-insensitive (Bob and bob land on the same badge).
func nickHash(nick string) uint32 {
	var h uint32 = 2166136261
	for _, r := range strings.ToLower(nick) {
		h ^= uint32(r)
		h *= 16777619
	}
	return h
}

// nickAttr maps a nick to a stable fg+bg badge: an fg from nickFgPalette, and
// the first bg (scanning from a hash-derived offset so backgrounds vary too)
// whose luminance contrasts enough with that fg. Falls back to the highest-
// contrast bg if none clears nickContrastMin.
func (a *App) nickAttr(nick string) ansi.Attr {
	h := nickHash(nick)
	fg := nickFgPalette[h%uint32(len(nickFgPalette))]
	fgL := cgaLuma[fg&0x0F]
	start := h / uint32(len(nickFgPalette)) // independent stream for the bg
	best, bestDelta := ansi.BgBlack, -1
	for i := 0; i < len(nickBgPalette); i++ {
		bg := nickBgPalette[(start+uint32(i))%uint32(len(nickBgPalette))]
		delta := fgL - cgaLuma[(bg>>4)&7]
		if delta < 0 {
			delta = -delta
		}
		if delta >= nickContrastMin {
			return fg | bg
		}
		if delta > bestDelta {
			best, bestDelta = bg, delta
		}
	}
	return fg | best
}
