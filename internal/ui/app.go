package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/canvas"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/idle"
	"github.com/hmderdoc/avatar_chat_universal/internal/ignore"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/telnetvision"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// App is the top-level chat UI. It owns the screen layout (header,
// transcript, status, input), the key-input loop, and the periodic poll of
// the chat session.
type App struct {
	Conn    io.Writer
	Input   *ansi.Input
	Session *chat.Session
	Width   int
	Height  int
	Charset ansi.Charset
	Theme   *theme.Theme

	// OnAvatarSet is called when the user enters `/avatar set <base64>`.
	// Implementer should validate, persist, and return error on failure.
	// Returning nil signals success and the App will update Session.Self.Avatar.
	OnAvatarSet func(b64 string) error
	// OnAvatarDisable is called for `/avatar off`.
	OnAvatarDisable func() error
	// OnAvatarUpload runs an XMODEM-CRC receive against the conn. It must
	// pause/resume the input pump itself (via Input.WithRawAccess) and
	// return the new avatar's base64 on success.
	OnAvatarUpload func() (b64 string, err error)
	// OnMediaUpload runs a Zmodem receive for a chat attachment, posts it to
	// the system's media server and returns the URL to send plus how many
	// days the server keeps it. nil = uploads are not configured.
	OnMediaUpload func() (url string, days int, err error)
	// OnAvatarPick relaunches the bundled+sysop avatar selector. The
	// callback owns the conn for the duration. Returns:
	//   - b64 set + action "picked" when the user chose an avatar.
	//   - action "upload" / "disable" / "editor" when the user clicked an
	//     in-selector action button.
	//   - action "" (or "cancelled") when dismissed.
	OnAvatarPick func() (b64 string, action string, err error)
	// OnAvatarEditor opens the in-door drawing UI. Returns the new
	// avatar's base64 on save; empty string + nil error on cancel.
	OnAvatarEditor func() (b64 string, err error)
	// OnDraw opens the full-screen ANSI art editor on a w x h canvas and
	// returns the [BITMAP|...] message to post (with the size sent), or
	// "" when the user left without sending.
	OnDraw func(w, h int) (msg string, sentW, sentH int, err error)
	// OnStyleSave persists the user's handle style from /settings (an
	// empty style clears it). On success the App puts it on Session.Self
	// so it rides on the next message.
	OnStyleSave func(style *nickstyle.Style) error

	// Ignores is the user's ignore list (/ignore, ignore.go); the Session's
	// Ignore hook should use it too. Nil disables the commands.
	Ignores *ignore.List

	// Mouse asks the terminal for mouse reports when Run starts (see
	// mouse.go); OnMouseSave stores a user's /mouse choice. ResizeEvery is
	// how often to probe the screen size (0 = never: the size is pinned or
	// the terminal didn't answer the startup probe).
	Mouse         bool
	MouseAllowed  bool // the sysop's mouse = on; /mouse can't override off
	OnMouseSave   func(on bool) error
	ResizeEvery   time.Duration
	mouseOn       bool
	mousePaused   bool // pauseMouse turned it off for a sub-program
	lastSizeProbe time.Time

	// Per-user themes. ThemeDir holds the themes/<name>.ini files /theme
	// offers; SysopTheme is the board's own (what "default" goes back to);
	// UserThemes lets users pick; OnThemeSave stores a pick ("" = back to
	// the sysop's) for the next session.
	ThemeDir    string
	SysopTheme  *theme.Theme
	UserThemes  bool
	OnThemeSave func(name string) error
	// idleBase is the main config's screensaver profile, captured on the
	// first ApplyTheme so switching themes doesn't stack their overrides.
	idleBase *themeIdle

	// BoardWide is set when avatars and handle styles are the user's real
	// BBS records (Synchronet mode); /settings and /avatar help say so.
	BoardWide bool
	// StartInSettings opens /settings once chat starts: the first-run
	// offer of name colours was taken (cmd/avatar_chat_universal).
	StartInSettings bool
	// pendingStyle carries unsaved /settings handle edits across an avatar
	// action (picker, editor, upload) and back into the panel.
	pendingStyle *nickstyle.Style
	pendingTheme *theme.Theme // likewise for an unsaved /settings theme pick

	// PollInterval is how often we drain the session and re-render. 25ms
	// matches the JS door's poll_delay_ms default.
	PollInterval time.Duration

	// IdleEnabled controls whether the transcript area swaps to a
	// background animation after IdleTimeout of inactivity.
	IdleEnabled bool
	IdleTimeout time.Duration
	IdleSwitch  time.Duration
	IdleFPS     int
	// IdleRandom picks the next animation by random choice when true,
	// otherwise iterates IdleSequence in order.
	IdleRandom   bool
	IdleSequence []string // canonical names (lowercase) to play in order
	IdleDisable  []string // canonical names to skip

	// FigletMessages / FigletColors / FigletMove are forwarded into the
	// figlet_message animation when the rotation picks it.
	FigletMessages []string
	FigletColors   bool
	FigletMove     bool

	// AnsiGalleryFiles is the resolved list of .ans/.bin paths handed to
	// the ansi_gallery idle animation. Empty disables the animation.
	AnsiGalleryFiles []string

	// IdleInterleaveAnsi inserts a single piece of ansi_gallery art
	// between every procedural idle animation, so the rotation looks like
	// proc -> art -> proc -> art -> ... Mirrors the behavior of the JS
	// future_shell screensaver. Quietly disabled when AnsiGalleryFiles
	// is empty.
	IdleInterleaveAnsi bool

	headerFrame     *ansi.Frame
	actionFrame     *ansi.Frame // the two button rows
	bgAnimFrame     *ansi.Frame
	transcriptFrame *ansi.Frame
	fgAnimFrame     *ansi.Frame
	transcriptComp  *ansi.Compositor
	inputFrame      *ansi.Frame

	header     *Header
	actionBar  *ActionBar
	transcript *Transcript
	inputLine  *InputLine

	// A clicked button: pressName is drawn pushed in, and once that frame
	// has gone out (pressDrawn) and pressHold has passed (pressUntil) its
	// action runs. See pressButton / pressTick.
	pressName  string
	pressLabel string
	pressDrawn bool
	pressUntil time.Time

	// ServerAddr is shown in the header stats line ("...| host:port").
	ServerAddr string

	// tooSmall is set while the terminal is below minCols x minRows; the
	// screen then only says so (tooSmallShown once it has).
	tooSmall, tooSmallShown bool

	// InputMaxLength caps a typed line (the door ini's input_max_length;
	// 0 keeps the input line's default).
	InputMaxLength int

	lastMotdRefresh time.Time

	modalFrame  *ansi.Frame
	modal       modalView // roster or /channels list; nil when closed
	imageViewer *ImageViewer

	// Bell rings the terminal (BEL) when a PM or a mention of our name
	// arrives, at most once per bellGap. Set from config `bell`.
	Bell        bool
	bellPending bool // a BEL rides at the front of the next composed frame
	lastBell    time.Time
	// unreadPM counts PMs received since the user last sent a line; shown
	// as a flashing count on the Who button.
	unreadPM int
	// images is the inline picture pipeline (nil = image links stay text).
	images *imageManager
	// cards is the link-preview pipeline (nil = plain links stay text).
	cards *cardManager
	// alinks plays audio links (/play); nil when off or the terminal has no audio.
	alinks *audioLinks
	// videos plays video links (/play) on the lounge screen; nil without ffmpeg.
	videos *videoState
	// sixelSig identifies the sixel pictures on screen after the last
	// frame (URL@x,y each); "" when none are.
	sixelSig string

	// flashPhase is the last 500 ms on/off phase drawn for flashing buttons;
	// a phase change re-dirties the screen so the flash actually animates.
	flashPhase int64

	idleAnim        idle.Animation
	idleAnimStarted time.Time
	idleLastActive  time.Time
	idleLastTick    time.Time
	idleLastMsgAt   time.Time // last incoming message during idle; drives ticker overlay
	idleRegistry    *idle.Registry
	idlePool        []string // resolved playback list after disable filter
	idlePoolIdx     int

	// TV lounge config (see internal/ui/tvlounge.go).
	TVColor     string // "truecolor" (default) or "16"
	// AudioOK says the caller's terminal plays SyncTERM Audio APCs (the
	// startup probe answered); TVAudio lets the lounge use it.
	AudioOK bool
	TVAudio bool
	TVPopupSecs int    // how long a message popup lingers (5-15, default 10)
	TVAvatars   bool   // show avatars in popups (default off; video competes)

	// TV lounge runtime state.
	tvTuner          *chat.Tuner
	tvConsumer       *telnetvision.Consumer
	tvAudio          *apcaudio.Session // nil unless the feed carries audio to this caller
	tvOptedOut       bool
	tvShowTranscript bool
	// tvTranscriptShownAt is when the history overlay was last shown or
	// interacted with; the overlay auto-dismisses after tvTranscriptIdle.
	tvTranscriptShownAt time.Time
	tvPopups            []tvPopup
	tvLastTick          time.Time

	dirty bool

	// Render pacer: renderBuf is scratch for composing a frame; pending holds
	// the composed frame currently being drained to the conn (one frame in
	// flight at a time), with pendingOff the write cursor into it. See
	// paceFrame / flushPending.
	renderBuf  bytes.Buffer
	pending    []byte
	pendingOff int
	// audioOut collects Audio APCs between frames. paceFrame sends it as a
	// unit of its own, never inside a half-written frame, so an APC can
	// never land in the middle of an escape sequence.
	audioOut bytes.Buffer
}

func NewApp(conn io.Writer, in *ansi.Input, sess *chat.Session, width, height int, charset ansi.Charset) *App {
	a := &App{
		Conn:           conn,
		Input:          in,
		Session:        sess,
		Width:          width,
		Height:         height,
		Charset:        charset,
		Theme:          theme.Default(),
		PollInterval:   25 * time.Millisecond,
		IdleEnabled:    true,
		IdleTimeout:    180 * time.Second,
		IdleSwitch:     60 * time.Second,
		IdleFPS:        8,
		IdleRandom:     true,
		idleLastActive: time.Now(),
		idleRegistry:   idle.NewRegistry(),
		TVColor:        "truecolor",
		TVPopupSecs:    10,
	}
	a.layout()
	return a
}

// layout builds the screen frames. We reserve the very bottom row of the
// terminal so any local-pty side effects don't corrupt our drawn UI.
//
//	row 0       : header (stats/MOTD rotation, the [X] at the left)
//	rows 1..2   : the raised buttons (Who, Rooms, Images, Settings, Help,
//	              Quit), faces on row 1, half-block shadows into row 2
//	rows 3..H-3 : transcript area (3 layered frames composited)
//	  - bgAnimFrame    : opaque, holds BG animations or solid black
//	  - transcriptFrame: transparent overlay, opaque only where bubbles paint
//	  - fgAnimFrame    : transparent overlay, FG animations paint sparse cells
//	              (the TV lounge takes its last row for the caption bar)
//	row H-2     : input line (yellow > prompt)
//	row H-1     : reserved (cursor scratch)
func (a *App) layout() {
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	transcriptH := usable - 4
	if transcriptH < 1 {
		transcriptH = 1
	}
	th := a.Theme
	if th == nil {
		th = theme.Default()
		a.Theme = th
	}
	a.headerFrame = ansi.NewFrame(0, 0, a.Width, 1, th.HeaderStats)
	a.actionFrame = ansi.NewFrame(0, 1, a.Width, 2, th.TopActionBar)
	a.bgAnimFrame = ansi.NewFrame(0, 3, a.Width, transcriptH, ansi.Black|ansi.BgBlack)
	a.transcriptFrame = ansi.NewTransparentFrame(0, 3, a.Width, transcriptH)
	a.fgAnimFrame = ansi.NewTransparentFrame(0, 3, a.Width, transcriptH)
	a.transcriptComp = ansi.NewCompositor(a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame)
	a.transcriptComp.Charset = a.Charset
	a.inputFrame = ansi.NewFrame(0, usable-1, a.Width, 1, th.InputBg)
	for _, f := range []*ansi.Frame{a.headerFrame, a.actionFrame, a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame, a.inputFrame} {
		f.Charset = a.Charset
	}

	a.header = NewHeader(a.headerFrame)
	a.header.StatsAttr = th.HeaderStats
	a.header.MotdAttr = th.HeaderMotd
	// The [X] at the left end does what Esc (and the Quit button) does.
	a.header.Close = true
	a.header.CloseAttr = th.CloseButton
	a.actionBar = NewActionBar(a.actionFrame)
	selfName := ""
	if a.Session != nil && a.Session.Self != nil {
		selfName = a.Session.Self.Name
	}
	a.transcript = NewTranscript(a.transcriptFrame, selfName)
	if a.Session != nil && a.Session.Self != nil {
		a.transcript.SelfHost = a.Session.Self.Host
	}
	a.transcript.Theme = th
	a.transcript.Images = a.images
	a.transcript.Cards = a.cards
	if a.alinks != nil || a.videos != nil {
		a.transcript.AudioLabel = a.mediaLabel // survive a re-layout (theme, resize)
	}
	a.inputLine = NewInputLine(a.inputFrame, "> ")
	if a.InputMaxLength > 0 {
		a.inputLine.MaxLength = a.InputMaxLength
	}
	a.inputLine.PromptAttr = th.InputPrompt
	a.inputLine.TextAttr = th.InputText
	a.inputLine.CursorAttr = th.InputCursor
	a.inputLine.CompletionProvider = func() []string {
		nicks := a.Session.KnownNicks()
		out := nicks[:0]
		for _, n := range nicks {
			if !strings.EqualFold(n, a.selfName()) {
				out = append(out, n)
			}
		}
		return out
	}

	// Button attrs come from the theme (defaults to "futurewave"): faces
	// and shadows on the bar's BG.
	a.actionBar.BarAttr = th.TopActionBar
	a.actionBar.ButtonAttr = th.TopActionPill
	a.actionBar.HighlightAttr = th.TopActionHighlight
	a.actionBar.ShadowAttr = th.TopActionShadow

	a.Session.OnMessage = func(m *chat.Message) {
		a.dirty = true
		// Incoming messages do NOT kill the screensaver -- the user
		// still gets a small ticker overlay showing the most recent
		// message so they know chat's flowing, but the procedural /
		// gallery animation keeps running. Only a real keypress
		// dismisses idle (handled in handleKey via markActive).
		a.idleLastMsgAt = time.Now()
		// In TV lounge mode, comments become timeboxed popups over the video.
		if a.loungeActive() {
			a.addPopup(m)
		}
		if a.fromOther(m) && !a.muted(m) {
			if m.Private {
				a.unreadPM++
				a.ring()
			} else if chat.MentionsName(m.Str, a.selfName()) {
				a.ring()
			}
		}
	}
	// A joined channel we aren't viewing: the Session counts it for the
	// /channels badge; a mention there still rings.
	a.Session.OnBackground = func(_ string, m *chat.Message) {
		a.dirty = true
		if chat.MentionsName(m.Str, a.selfName()) && !a.muted(m) {
			a.ring()
		}
	}
	// Notices route into the transcript itself now that there's no status
	// bar; nothing extra to do here, but keep a callback so per-event
	// dirty-flagging still works from external triggers.
	a.Session.OnNotice = func(string) {
		a.dirty = true
	}
	a.dirty = true
}

// bellGap is the minimum time between two BELs, so a burst of PMs or a
// channel that keeps saying your name beeps once, not once per line.
const bellGap = 3 * time.Second

// ring queues a BEL for the next frame, rate-limited by bellGap.
func (a *App) ring() {
	if !a.Bell || time.Since(a.lastBell) < bellGap {
		return
	}
	a.lastBell = time.Now()
	a.bellPending = true
	a.dirty = true
}

// hasFlashing reports whether any button is flashing for attention.
func (a *App) hasFlashing() bool {
	if a.unreadPM > 0 || a.unreadChannels() > 0 {
		return true
	}
	return a.Session != nil && a.Session.UnviewedBitmaps() > 0
}

func (a *App) unreadChannels() int {
	if a.Session == nil {
		return 0
	}
	return a.Session.UnreadTotal()
}

// SetInputMaxLength caps a typed line at n characters (n <= 0 keeps the
// default), now and after any re-layout.
func (a *App) SetInputMaxLength(n int) {
	a.InputMaxLength = n
	if n > 0 && a.inputLine != nil {
		a.inputLine.MaxLength = n
	}
}

func (a *App) selfName() string {
	if a.Session != nil && a.Session.Self != nil {
		return a.Session.Self.Name
	}
	return ""
}

// fromOther reports whether m is a real message from someone else (not a
// notice, not our own line echoed locally).
func (a *App) fromOther(m *chat.Message) bool {
	return m != nil && m.Nick != nil && m.Nick.Name != "" && (a.Session == nil || !chat.SameUser(a.Session.Self, m.Nick))
}

// markActive records that something happened (input or new message), exits
// idle mode if active, resets the inactivity timer, and wipes any leftover
// animation residue from the bg/fg layers.
func (a *App) markActive() {
	a.idleLastActive = time.Now()
	if a.idleAnim != nil {
		a.idleAnim = nil
		a.bgAnimFrame.Clear()
		a.fgAnimFrame.Clear()
		a.transcriptComp.ForceRedraw()
	}
}

// idleTick advances the current animation if it's time. Starts an animation
// if we just crossed the idle threshold; rotates animations at IdleSwitch
// intervals. Updates the status bar with the current animation name and a
// countdown to the next switch.
func (a *App) idleTick() {
	if !a.IdleEnabled || a.modal != nil || a.imageViewer != nil {
		return
	}
	now := time.Now()
	if a.idleAnim == nil {
		if now.Sub(a.idleLastActive) < a.IdleTimeout {
			return
		}
		a.startAnimation(nil)
		a.idleAnimStarted = now
		a.bgAnimFrame.ForceRedraw()
		a.fgAnimFrame.ForceRedraw()
		a.transcriptComp.ForceRedraw()
	}
	// Rotate to the next animation when (a) the IdleSwitch timer expires,
	// or (b) the current animation reports Done() -- the latter is how a
	// one-shot ansi_gallery hands control back after its single piece.
	switchNow := now.Sub(a.idleAnimStarted) > a.IdleSwitch
	if !switchNow {
		if d, ok := a.idleAnim.(interface{ Done() bool }); ok && d.Done() {
			switchNow = true
		}
	}
	if switchNow {
		a.startAnimation(a.idleAnim)
		a.idleAnimStarted = now
		a.bgAnimFrame.Clear()
		a.fgAnimFrame.Clear()
		a.transcriptComp.ForceRedraw()
	}
	fps := a.IdleFPS
	if pref, ok := a.idleAnim.(idle.FPSPreferring); ok {
		if v := pref.PreferredFPS(); v > 0 {
			fps = v
		}
	}
	tickInterval := time.Second / time.Duration(maxInt(fps, 1))
	if now.Sub(a.idleLastTick) < tickInterval {
		return
	}
	a.idleLastTick = now
	// BG animations paint to the bg layer; FG animations paint to the fg
	// layer (which is transparent except where the animation writes,
	// letting the chat below show through).
	if a.idleAnim.Category() == idle.Foreground {
		a.idleAnim.Tick(a.fgAnimFrame)
	} else {
		a.idleAnim.Tick(a.bgAnimFrame)
	}
	// After the animation has painted, layer a "you've got mail" ticker
	// over the bottom row of the FG layer if a message arrived recently.
	// The compositor stacks bg < transcript < fg, so painting on fg sits
	// on top of whatever animation is running.
	a.renderIdleTicker()
	a.dirty = true
}

// renderIdleTicker paints the most recent chat message at the bottom of
// the fg animation layer for a few seconds after it arrives, so the user
// idling at a screensaver still sees activity. Cleared back to
// transparent once the message is older than tickerWindow.
func (a *App) renderIdleTicker() {
	const tickerWindow = 6 * time.Second
	if a.idleAnim == nil || a.fgAnimFrame == nil || a.Session == nil {
		return
	}
	row := a.fgAnimFrame.H - 1
	if row < 0 {
		return
	}
	// Always wipe the ticker row first so a stale message doesn't linger
	// once the window has closed. ClearCell makes the cell transparent
	// again on a transparent frame so the bg animation shows through.
	for x := 0; x < a.fgAnimFrame.W; x++ {
		a.fgAnimFrame.ClearCell(x, row)
	}
	if a.idleLastMsgAt.IsZero() || time.Since(a.idleLastMsgAt) > tickerWindow {
		return
	}
	msgs := a.Session.Messages()
	if len(msgs) == 0 {
		return
	}
	last := msgs[len(msgs)-1]
	if last == nil {
		return
	}
	nick := ""
	if last.Nick != nil {
		nick = last.Nick.Name
	}
	text := " " + nick + ": " + last.Str + " "
	maxW := a.fgAnimFrame.W
	if len(text) > maxW && maxW >= 3 {
		// ASCII "..." in both charsets: cells are CP437 bytes written
		// byte-by-byte below, so a multi-byte Unicode ellipsis would render
		// as garbage on the wire.
		text = text[:maxW-3] + "..."
	}
	bg := ansi.LightGray | ansi.BgBlue
	if a.Theme != nil {
		// The strip palette (the lounge's key strip uses it too) so the
		// ticker visually "belongs" with the chat chrome.
		bg = a.Theme.StripBar
	}
	// Pad the row to full width with the bg attr.
	for x := 0; x < maxW; x++ {
		a.fgAnimFrame.SetCell(x, row, ' ', bg)
	}
	for i := 0; i < len(text) && i < maxW; i++ {
		c := text[i]
		if c < 0x20 {
			c = ' '
		}
		a.fgAnimFrame.SetCell(i, row, c, bg)
	}
}

// startAnimation chooses the next animation and starts it. Honors the
// configured disable list and sequence/random preference, and avoids
// repeating the same animation back-to-back when alternatives exist.
//
// Foreground animations (currently just avatars_float) are special-cased
// here since they need late-binding context (the live avatar cache) that
// the registry's plain (w, h) factory can't provide.
func (a *App) startAnimation(prev idle.Animation) {
	w := a.transcriptFrame.W
	h := a.transcriptFrame.H

	// Interleave: between every procedural animation, slot in a single
	// piece of ansi_gallery art. Skip when the previous animation was
	// itself the gallery (so we alternate, not stack), or when no gallery
	// files are configured.
	if a.IdleInterleaveAnsi && len(a.AnsiGalleryFiles) > 0 {
		prevWasGallery := prev != nil && prev.Name() == "ansi_gallery"
		if !prevWasGallery {
			a.idleAnim = idle.NewAnsiGalleryOneShot(w, h, a.AnsiGalleryFiles)
			return
		}
	}

	pool := a.idlePool
	if len(pool) == 0 {
		pool = a.resolvePool()
	}
	if len(pool) == 0 {
		pool = a.idleRegistry.Names()
		if len(pool) == 0 {
			a.idleAnim = idle.NewStarfield(w, h)
			return
		}
	}
	var pick string
	if a.IdleRandom {
		for tries := 0; tries < 6; tries++ {
			cand := pool[rand.Intn(len(pool))]
			if prev == nil || cand != prev.Name() {
				pick = cand
				break
			}
		}
		if pick == "" {
			pick = pool[0]
		}
	} else {
		a.idlePoolIdx = (a.idlePoolIdx + 1) % len(pool)
		pick = pool[a.idlePoolIdx]
		if prev != nil && pick == prev.Name() && len(pool) > 1 {
			a.idlePoolIdx = (a.idlePoolIdx + 1) % len(pool)
			pick = pool[a.idlePoolIdx]
		}
	}
	a.idleAnim = a.buildAnimation(pick, w, h)
}

// buildAnimation instantiates an animation by canonical name, handling the
// special FG cases that need extra context.
func (a *App) buildAnimation(name string, w, h int) idle.Animation {
	switch name {
	case "avatars_float":
		return idle.NewAvatarsFloat(w, h, a.collectAvatars())
	case "figlet_message":
		msgs := a.FigletMessages
		if len(msgs) == 0 {
			msgs = []string{"Avatar Chat"}
		}
		return idle.NewFigletMessage(w, h, msgs, a.FigletColors, a.FigletMove)
	case "ansi_gallery":
		return idle.NewAnsiGallery(w, h, a.AnsiGalleryFiles)
	}
	if anim := a.idleRegistry.Build(name, w, h); anim != nil {
		return anim
	}
	return idle.NewStarfield(w, h)
}

// collectAvatars decodes every cached avatar from the chat session into
// real Avatar values paired with the owning user's name. Returns nothing
// when the session is empty (callers fall back to identicons).
func (a *App) collectAvatars() []idle.AvatarPick {
	if a.Session == nil {
		return nil
	}
	var out []idle.AvatarPick
	seen := map[string]bool{}
	for _, e := range a.Session.CachedAvatarsWithNames() {
		key := strings.ToLower(e.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		av, err := avatar.FromBase64(e.Base64)
		if err != nil {
			continue
		}
		name := e.Name
		if name == "" {
			name = "anonymous"
		}
		out = append(out, idle.AvatarPick{Name: name, Avatar: av})
	}
	return out
}

// extraAnimations are FG animations that aren't in the registry because
// they need late-binding context (avatar pool, chat state, config-driven
// messages). Listed here so resolvePool() includes them in the rotation.
var extraAnimations = []string{"avatars_float", "figlet_message", "ansi_gallery"}

// resolvePool builds the playback list the App will draw from: either the
// explicit IdleSequence (filtered through IdleDisable) or every registered
// animation plus extras (also filtered).
func (a *App) resolvePool() []string {
	disabled := map[string]bool{}
	for _, d := range a.IdleDisable {
		disabled[d] = true
	}
	var pool []string
	source := a.IdleSequence
	if len(source) == 0 {
		source = append([]string{}, a.idleRegistry.Names()...)
		source = append(source, extraAnimations...)
	}
	known := map[string]bool{}
	for _, n := range a.idleRegistry.Names() {
		known[n] = true
	}
	for _, n := range extraAnimations {
		known[n] = true
	}
	for _, name := range source {
		if disabled[name] {
			continue
		}
		// Skip ansi_gallery when no files were configured -- it'd just
		// render black during its rotation turn, which looks broken.
		if name == "ansi_gallery" && len(a.AnsiGalleryFiles) == 0 {
			continue
		}
		if known[name] {
			pool = append(pool, name)
		}
	}
	a.idlePool = pool
	return pool
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// reconnectState tracks an in-flight reconnect attempt with exponential
// backoff so we don't hammer the server.
type reconnectState struct {
	nextAttempt time.Time
	delay       time.Duration
	lostAt      time.Time  // when the drop was noticed
	dialing     chan error // the background dial's result; nil when idle
	failed      bool       // a retry has failed and said so
}

// Run is the main loop: drain chat updates, read keys, render. Returns when
// the user exits (Esc) or the connection breaks.
func (a *App) Run(ctx context.Context) error {
	// Disable terminal auto-wrap so a long input line doesn't push the
	// cursor onto the next row when the user types past the right edge.
	// Re-enable on exit. Show cursor (we want users to see where they're
	// typing — the OS cursor is the input caret, not a frame cell).
	if _, err := a.Conn.Write([]byte("\x1b[?7l")); err != nil {
		return err
	}
	defer a.Conn.Write([]byte("\x1b[?7h"))
	defer ansi.ShowCursor(a.Conn)
	defer a.silenceOnExit()
	if a.Mouse {
		a.setMouse(true)
		defer a.setMouse(false)
	}
	// Pastes arrive bracketed, as one KeyPaste: several lines go into the
	// input line instead of each being sent.
	_, _ = a.Conn.Write([]byte(ansi.PasteOn))
	defer a.Conn.Write([]byte(ansi.PasteOff))
	a.lastSizeProbe = time.Now()
	if err := ansi.ShowCursor(a.Conn); err != nil {
		return err
	}
	if err := ansi.ClearScreen(a.Conn); err != nil {
		return err
	}

	a.draw()
	if err := a.renderBlocking(); err != nil {
		return err
	}
	if a.StartInSettings {
		a.StartInSettings = false
		a.openSettings()
	}

	var reconnect reconnectState

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Drain any chat updates first.
		a.Session.Cycle()
		if a.images != nil && a.images.drain() {
			a.dirty = true
		}
		if a.cards != nil && a.cards.drain() {
			a.dirty = true
		}
		a.audioTick(time.Now())
		a.videoTick()
		a.probeSize(time.Now())

		// Maybe-reconnect: if the chat connection died, try to bring it
		// back up with exponential backoff (1s, 2s, 4s, ... capped at 30s).
		if !a.Session.IsAlive() || reconnect.dialing != nil {
			a.tryReconnect(ctx, &reconnect)
		}

		// Reconcile TV-lounge tuner state (open/close the video stream when
		// the room is tuned/untuned). In lounge mode the video drives the
		// redraw; otherwise the idle screensaver may kick in.
		a.tvSync()
		if a.loungeActive() {
			a.tvTick()
		} else {
			a.idleTick()
		}

		// Keep the render loop dirty while a join/leave notice is still
		// playing its color sweep, so the wave actually animates.
		if a.Session != nil && a.Session.HasActiveEffect() {
			a.dirty = true
		}

		// Periodically refresh the MOTD so the header rotation has
		// fresh content. 5s cadence matches the JS door's
		// refreshMotd at avatar_chat.js:1997-2037.
		if time.Since(a.lastMotdRefresh) > 5*time.Second {
			a.lastMotdRefresh = time.Now()
			if a.Session != nil {
				go a.Session.RefreshMotd()
			}
			a.dirty = true
		}


		// A clicked button's action, once its pushed-in frame has shown.
		if a.pressTick(time.Now()) {
			return nil
		}

		key, ok, err := a.Input.Next(a.PollInterval)
		if err != nil {
			return err
		}
		if ok {
			// The key that wakes the screensaver only wakes it: it isn't
			// typed, and an Esc doesn't quit.
			woke := a.idleAnim != nil && key.Type != ansi.KeyResize && key.Type != ansi.KeyPaste
			a.markActive()
			if woke {
				a.dirty = true
			} else if exit, err := a.handleKey(key); err != nil {
				return err
			} else if exit {
				return nil
			}
		}

		if a.hasFlashing() {
			if phase := time.Now().UnixNano() / 1000000 / 500; phase != a.flashPhase {
				a.flashPhase = phase
				a.dirty = true
			}
		}

		// Pace output: drain any frame still in flight, and compose a new one
		// only when the previous has fully flushed. This is the backpressure
		// point — on a slow/stalled link the door stops producing instead of
		// firehosing past the SSH channel window.
		if err := a.paceFrame(); err != nil {
			return err
		}
	}
}

// Relayout rebuilds every frame at the current Width/Height, re-applies
// the active theme, and forces a full redraw on the next render. Use this
// after assigning new Width/Height on the App (e.g. from a one-shot
// terminal-size probe at startup).
func (a *App) Relayout() {
	a.layout()
	if th := a.Theme; th != nil {
		a.ApplyTheme(th)
	}
	a.forceFullRedraw()
}

func (a *App) handleKey(k ansi.Key) (exit bool, err error) {
	// Cursor Position Report: the answer to probeSize (or a stray one,
	// which Resize ignores when the size hasn't changed).
	if k.Type == ansi.KeyResize {
		if a.ResizeEvery > 0 {
			a.Resize(k.Cols, k.Rows)
		}
		return false, nil
	}
	if k.Type == ansi.KeyMouse {
		return a.handleMouse(k), nil
	}

	// If a modal is open, route input to it. The modal closes on Esc; on
	// close we restore the chat UI by force-redrawing every frame.
	if a.imageViewer != nil {
		closed := a.imageViewer.HandleKey(k)
		// Mark current image as viewed for the unread count.
		a.Session.MarkBitmapViewed(a.imageViewer.CurrentIndex())
		a.dirty = true
		if closed {
			a.closeImageViewer()
		}
		return false, nil
	}
	if a.modal != nil {
		m := a.modal
		closed := m.HandleKey(k)
		a.dirty = true
		if closed {
			return a.afterModal(m), nil
		}
		return false, nil
	}

	// Global hotkeys first.
	switch k.Type {
	case ansi.KeyPgUp:
		if a.loungeActive() {
			// First press: just SHOW the most recent page (no scroll-back) —
			// chat vanishes here, so the user wants to see what they missed.
			// Once it's up, PgUp pages further back into history.
			if !a.tvShowTranscript {
				a.showLoungeHistory()
			} else {
				a.pageLoungeHistory(a.transcript.PageRows())
			}
			return false, nil
		}
		a.transcript.Scroll += a.transcript.PageRows()
		a.dirty = true
		return false, nil
	case ansi.KeyPgDn:
		if a.loungeActive() && a.tvShowTranscript {
			// At the bottom already: PgDn dismisses the overlay. Otherwise it
			// pages forward (toward the newest messages).
			if a.transcript.Scroll == 0 {
				a.hideLoungeHistory()
			} else {
				a.pageLoungeHistory(-a.transcript.PageRows())
			}
			return false, nil
		}
		a.transcript.Scroll -= a.transcript.PageRows()
		if a.transcript.Scroll < 0 {
			a.transcript.Scroll = 0
		}
		a.dirty = true
		return false, nil
	case ansi.KeyEsc:
		return a.escape(), nil // Esc ends a video before it ends the door
	}

	// Down arrow on an empty input mirrors PgDn while the history overlay is
	// up: page toward the newest messages, and dismiss when already at the
	// bottom. With a non-empty buffer, Down keeps doing input-history recall.
	if k.Type == ansi.KeyDown && a.loungeActive() && a.tvShowTranscript && a.inputLine.Value() == "" {
		if a.transcript.Scroll == 0 {
			a.hideLoungeHistory()
		} else {
			a.pageLoungeHistory(-a.transcript.PageRows())
		}
		return false, nil
	}

	// End at the end of the line (or an empty one) goes back to the latest
	// messages; Home on an empty line goes to the oldest. Otherwise they
	// move the input cursor.
	if k.Type == ansi.KeyEnd && a.inputLine.AtEnd() && (a.transcript.Scroll > 0 || a.loungeActive() && a.tvShowTranscript) {
		if a.loungeActive() {
			a.hideLoungeHistory()
		} else {
			a.transcript.Scroll = 0
		}
		a.dirty = true
		return false, nil
	}
	if k.Type == ansi.KeyHome && a.inputLine.Value() == "" && !a.loungeActive() {
		a.transcript.Scroll = 1 << 30 // Render clamps it to the oldest row
		a.dirty = true
		return false, nil
	}
	if k.Type == ansi.KeyPaste {
		if a.inputLine.Paste(k.Text) {
			a.Session.Notice(fmt.Sprintf("paste cut to %d characters", a.inputLine.MaxLength))
		}
		a.dirty = true
		return false, nil
	}
	if k.Type == ansi.KeyChar && k.Ctrl && k.Rune == 0x0C { // Ctrl-L: repaint the whole screen
		a.forceFullRedraw()
		a.dirty = true
		return false, nil
	}
	if k.Type == ansi.KeyChar && k.Ctrl && k.Rune == 0x18 { // Ctrl-X
		return true, nil
	}
	if k.Type == ansi.KeyChar && k.Ctrl && k.Rune == 0x14 { // Ctrl-T: TV truecolor/16 toggle
		if a.loungeActive() {
			a.toggleTVColor()
		}
		return false, nil
	}

	submit, cancel := a.inputLine.HandleKey(k)
	if cancel {
		return true, nil
	}
	if submit {
		text := a.inputLine.Value()
		a.inputLine.Reset()
		a.inputLine.PushHistory(text)
		// Typing a line is the user engaging with chat: PMs are now seen.
		a.unreadPM = 0
		if exit, handled := a.handleSlashCommand(text); handled {
			a.dirty = true
			return exit, nil
		}
		if err := a.Session.Send(text); err != nil {
			a.Session.Notice(fmt.Sprintf("send failed: %v", err))
		}
		a.transcript.Scroll = 0
		// Sending a line drops the history overlay so the user sees their
		// message land back over the video.
		if a.loungeActive() {
			a.hideLoungeHistory()
		}
	}
	a.dirty = true
	return false, nil
}

// splitRecipient splits the text after /msg into the recipient and the
// body. A name with spaces is either quoted ("Beta Tester" hi) or, unquoted,
// the longest known name the text starts with; otherwise it's the first
// word. The body keeps the user's own spacing.
func splitRecipient(rest string, known []string) (name, body string) {
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return strings.TrimSpace(rest[1 : end+1]), strings.TrimLeft(rest[end+2:], " \t")
		}
	}
	for _, k := range known {
		if strings.Contains(k, " ") && len(k) > len(name) && len(rest) > len(k) &&
			strings.EqualFold(rest[:len(k)], k) && rest[len(k)] == ' ' {
			name = rest[:len(k)]
		}
	}
	if name == "" {
		name, _, _ = strings.Cut(rest, " ")
	}
	return name, strings.TrimLeft(rest[len(name):], " \t")
}

// msgPrefix is the input line that starts a PM to name, quoted when the
// name has spaces.
func msgPrefix(name string) string {
	if strings.ContainsAny(name, " \t") {
		name = `"` + name + `"`
	}
	return "/msg " + name + " "
}

// handleSlashCommand intercepts /-prefixed input. Returns (exit, handled).
// If handled is true, the message should NOT be sent to chat.
func (a *App) handleSlashCommand(text string) (exit bool, handled bool) {
	if len(text) == 0 || text[0] != '/' {
		return false, false
	}
	parts := splitFields(text)
	if len(parts) == 0 {
		return false, false
	}
	cmd := strings.ToLower(parts[0]) // caps lock is common on BBS terminals
	switch cmd {
	case "/quit", "/q", "/exit", "/bye":
		return true, true
	case "/me":
		if len(parts) < 2 {
			a.Session.Notice("usage: /me <action>")
			return false, true
		}
		action := text[len(cmd)+1:]
		if err := a.Session.Action(action); err != nil {
			a.Session.Notice(fmt.Sprintf("/me failed: %v", err))
		}
		return false, true
	case "/msg", "/m", "/pm", "/tell", "/whisper", "/private":
		recipient, body := splitRecipient(text[len(cmd):], a.Session.KnownNicks())
		if recipient == "" || strings.TrimSpace(body) == "" {
			a.Session.Notice(`usage: /msg <user> <text>  (quote a name with spaces: /msg "Beta Tester" hi)`)
			return false, true
		}
		if err := a.Session.SendPrivate(recipient, body); err != nil {
			a.Session.Notice(fmt.Sprintf("/msg failed: %v", err))
		}
		return false, true
	case "/r", "/reply":
		target := a.Session.LastReplyTarget()
		if target == "" {
			a.Session.Notice("/r: no PM to reply to yet")
			return false, true
		}
		if len(parts) < 2 {
			a.Session.Notice("usage: /r <message>  (replies to " + target + ")")
			return false, true
		}
		bodyStart := len(cmd) + 1
		body := text[bodyStart:]
		if err := a.Session.ReplyPrivate(body); err != nil {
			a.Session.Notice(fmt.Sprintf("/r failed: %v", err))
		}
		return false, true
	case "/channels", "/list":
		a.openChannels()
		return false, true
	case "/clear":
		a.Session.Clear()
		a.Session.Notice("transcript cleared (local only)")
		return false, true
	case "/who":
		users, err := a.Session.Who()
		if err != nil {
			a.Session.Notice(fmt.Sprintf("/who: %v", err))
			return false, true
		}
		// Always include ourselves so the modal isn't empty when we're
		// alone, and so we can preview our own avatar.
		if a.Session.Self != nil {
			users = append([]string{a.Session.Self.Name}, users...)
		}
		a.openRoster(users)
		return false, true
	case "/img", "/image", "/pic":
		a.openImageViewer()
		return false, true
	case "/join", "/j":
		if len(parts) < 2 {
			a.Session.Notice("usage: /join <channel>")
			return false, true
		}
		// Strip leading '#' if user typed it -- channels are stored
		// without the prefix; the prefix is purely visual.
		ch := strings.TrimPrefix(parts[1], "#")
		if ch == "" {
			a.Session.Notice("usage: /join <channel>")
			return false, true
		}
		if err := a.Session.JoinChannel(ch, 200); err != nil {
			a.Session.Notice(fmt.Sprintf("/join: %v", err))
		}
		// Header text refreshes from Session.Channel each draw, so no
		// further plumbing needed.
		return false, true
	case "/part", "/p":
		// Leave this channel for the previous one; the last one exits.
		return a.partCurrent(), true
	case "/avatar":
		a.handleAvatarCmd(parts)
		return false, true
	case "/mouse":
		a.handleMouseCmd(parts[1:])
		return false, true
	case "/ignore":
		a.handleIgnore(parts[1:])
		return false, true
	case "/unignore":
		a.handleUnignore(parts[1:])
		return false, true
	case "/theme", "/themes":
		a.handleTheme(parts[1:])
		return false, true
	case "/settings", "/prefs", "/options":
		a.openSettings()
		return false, true
	case "/tvtuner":
		a.handleTunerCmd(parts)
		return false, true
	case "/tvoff":
		if a.Session.Tuner() == nil {
			a.Session.Notice("/tvoff: the room isn't tuned to a TV")
			return false, true
		}
		a.tvOptedOut = true
		a.forceFullRedraw()
		a.Session.Notice("left the TV lounge (/tvon to rejoin)")
		return false, true
	case "/tvon":
		if a.Session.Tuner() == nil {
			a.Session.Notice("/tvon: the room isn't tuned to a TV")
			return false, true
		}
		a.tvOptedOut = false
		a.forceFullRedraw()
		a.Session.Notice("rejoined the TV lounge")
		return false, true
	case "/transcript", "/scrollback":
		if !a.loungeActive() {
			a.Session.Notice("/transcript: only in TV lounge mode (PgUp also works)")
			return false, true
		}
		if a.tvShowTranscript {
			a.hideLoungeHistory()
		} else {
			a.showLoungeHistory()
		}
		return false, true
	case "/nsfw":
		if a.images == nil && a.cards == nil {
			a.Session.Notice("/nsfw: inline images and link previews are off on this system")
			return false, true
		}
		shown, n := false, 0
		if a.images != nil {
			shown, n = a.images.toggleReveal()
		} else {
			shown = !a.cards.revealed
		}
		if a.cards != nil {
			a.cards.revealed = shown
		}
		if shown {
			a.Session.Notice(fmt.Sprintf("showing pictures and links the NSFW rule veiled (%d pictures so far); /nsfw hides them again", n))
		} else {
			a.Session.Notice("NSFW pictures and links veiled again (/nsfw to show)")
		}
		a.forceFullRedraw()
		return false, true
	case "/play":
		a.handlePlay(parts[1:])
		return false, true
	case "/stop":
		stoppedAudio := a.stopAudio()
		if !a.stopVideo() && !stoppedAudio {
			a.Session.Notice("/stop: nothing playing")
		}
		return false, true
	case "/tvmute", "/mute":
		a.toggleTVMute()
		return false, true
	case "/tvcolor":
		a.toggleTVColor()
		return false, true
	case "/upload":
		a.handleMediaUpload()
		return false, true
	case "/draw", "/art":
		a.handleDraw(parts[1:])
		return false, true
	case "/help", "/?":
		a.openHelp()
		return false, true
	}
	a.Session.Notice(fmt.Sprintf("unknown command: %s (/help lists them)", cmd))
	return false, true
}

// handleDraw parses "/draw [WxH]" (or "/draw W H"), runs the art editor and
// posts what it returns.
func (a *App) handleDraw(args []string) {
	defer a.pauseMouse()()
	if a.OnDraw == nil {
		a.Session.Notice("/draw: the art editor is not available here")
		return
	}
	w, h, ok := parseDrawSize(args)
	if !ok {
		a.Session.Notice("/draw: size is WxH, e.g. /draw 80x25 (up to 80x40)")
		return
	}
	msg, sw, sh, err := a.OnDraw(w, h)
	a.forceFullRedraw()
	if err != nil {
		a.Session.Notice(fmt.Sprintf("/draw: %v", err))
		return
	}
	if msg == "" {
		a.Session.Notice("/draw: nothing sent")
		return
	}
	if err := a.Session.Send(msg); err != nil {
		a.Session.Notice(fmt.Sprintf("/draw: send failed: %v", err))
		return
	}
	a.transcript.Scroll = 0
	a.Session.Notice(fmt.Sprintf("\x01Gart sent\x01n (%dx%d)", sw, sh))
}

// parseDrawSize reads an optional canvas size; the default matches the web
// chat's art editor. Sizes are clamped to what the editor allows.
func parseDrawSize(args []string) (w, h int, ok bool) {
	w, h = 80, 25
	switch len(args) {
	case 0:
		return w, h, true
	case 1:
		if _, err := fmt.Sscanf(strings.ToLower(args[0]), "%dx%d", &w, &h); err != nil {
			return 0, 0, false
		}
	case 2:
		if _, err := fmt.Sscanf(args[0]+" "+args[1], "%d %d", &w, &h); err != nil {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	if w < 1 || h < 1 {
		return 0, 0, false
	}
	if w > canvas.MaxWidth {
		w = canvas.MaxWidth
	}
	if h > canvas.MaxHeight {
		h = canvas.MaxHeight
	}
	return w, h, true
}

// handleMediaUpload receives one file by Zmodem, hands it to the media
// server and puts the returned link in the input line for the user to send,
// the web chat's convention: nothing is posted until they press Enter.
func (a *App) handleMediaUpload() {
	defer a.pauseMouse()()
	if a.OnMediaUpload == nil {
		a.Session.Notice("/upload: file uploads are not set up on this system")
		return
	}
	a.Session.Notice("\x01WZmodem ready.\x01n Send a picture (png jpg gif webp bmp) or art (.ans .bin):")
	a.Session.Notice("  SyncTERM: \x01WAlt-U\x01n, pick the file  |  NetRunner / fTelnet: Send -> Zmodem")
	a.Session.Notice("  Press \x01WEsc\x01n or cancel in your terminal to stop.")
	a.draw()
	_ = a.renderBlocking()
	url, days, err := a.OnMediaUpload()
	a.forceFullRedraw()
	if err != nil {
		a.Session.Notice(fmt.Sprintf("/upload: %v", err))
		return
	}
	a.inputLine.SetValue(url)
	note := "uploaded -- the link is in your input line; press Enter to send it"
	if days > 0 {
		note += fmt.Sprintf(" (kept %d days)", days)
	}
	a.Session.Notice("\x01G" + note + "\x01n")
	a.dirty = true
}

func (a *App) handleAvatarCmd(parts []string) {
	// Bare `/avatar` opens the unified avatar manager UI: pick from
	// library, upload, disable, or (future) edit. Subcommands still work
	// for power users.
	if len(parts) < 2 {
		a.openAvatarManager()
		return
	}
	sub := strings.ToLower(parts[1])
	switch sub {
	case "set":
		if len(parts) < 3 {
			a.Session.Notice("usage: /avatar set <base64>")
			return
		}
		b64 := parts[2]
		if a.OnAvatarSet == nil {
			a.Session.Notice("/avatar set: not wired")
			return
		}
		if err := a.OnAvatarSet(b64); err != nil {
			a.Session.Notice(fmt.Sprintf("/avatar set: %v", err))
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = b64
		}
		a.Session.Notice("avatar updated (will appear on your next message)")
	case "pick", "choose", "select":
		a.openAvatarManager()
	case "off", "disable":
		if a.OnAvatarDisable == nil {
			a.Session.Notice("/avatar off: not wired")
			return
		}
		if err := a.OnAvatarDisable(); err != nil {
			a.Session.Notice(fmt.Sprintf("/avatar off: %v", err))
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = ""
		}
		a.Session.Notice("avatar disabled")
	case "upload":
		if a.OnAvatarUpload == nil {
			a.Session.Notice("/avatar upload: not wired")
			return
		}
		// Make the screen quiet while the protocol does its thing — we
		// can't simultaneously render frames AND let the file-transfer
		// own the wire. Most BBS terminals don't auto-start on our
		// ZRINIT, so we tell the user how to trigger the upload.
		a.Session.Notice("\x01WZmodem ready.\x01n Trigger the upload in your terminal:")
		a.Session.Notice("  SyncTERM: \x01WAlt-U\x01n then pick the .bin")
		a.Session.Notice("  NetRunner / fTelnet: Send menu -> Zmodem")
		a.Session.Notice("  Press \x01WEsc\x01n to cancel.")
		a.draw()
		_ = a.renderBlocking()
		b64, err := a.OnAvatarUpload()
		// Force a full repaint after upload because the protocol writes
		// to the conn and may have scrolled the screen.
		a.forceFullRedraw()
		if err != nil {
			a.Session.Notice(fmt.Sprintf("upload: %v", err))
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = b64
		}
		a.Session.Notice("avatar uploaded -- attached to your next message")
	default:
		a.Session.Notice("unknown /avatar subcommand: " + sub)
	}
}

// handleTunerCmd implements /tvtuner: tune the room to a telnetvision feed,
// turn it off, or (bare) rejoin the lounge if you'd opted out. The tune is
// shared — it broadcasts a marker so everyone in the channel enters lounge
// mode (and is applied locally immediately, since our own echo is suppressed).
func (a *App) handleTunerCmd(parts []string) {
	if len(parts) == 1 {
		if a.Session.Tuner() == nil {
			a.Session.Notice("usage: /tvtuner <host> <port> <channel>   (or /tvtuner off)")
			return
		}
		a.tvOptedOut = false
		a.forceFullRedraw()
		a.Session.Notice("rejoined the TV lounge")
		return
	}
	if strings.EqualFold(parts[1], "off") {
		if err := a.Session.ClearTuner(); err != nil {
			a.Session.Notice(fmt.Sprintf("/tvtuner off: %v", err))
		}
		return
	}
	if len(parts) < 4 {
		a.Session.Notice("usage: /tvtuner <host> <port> <channel>   (e.g. /tvtuner futureland.today 7601 cam)")
		return
	}
	port, err := strconv.Atoi(parts[2])
	if err != nil || port <= 0 || port > 65535 {
		a.Session.Notice("/tvtuner: port must be 1-65535")
		return
	}
	a.tvOptedOut = false
	if err := a.Session.SetTuner(parts[1], port, parts[3]); err != nil {
		a.Session.Notice(fmt.Sprintf("/tvtuner: %v", err))
	}
}

// tryReconnect brings the chat session back up. The dial runs in the
// background so typing, scrolling and quitting keep working while a dead
// host times out; resubscribing and the catch-up run here once it lands.
// The drop and the first failed retry are noted in the transcript, then
// nothing until it's back. Backoff doubles up to 30s between attempts.
func (a *App) tryReconnect(ctx context.Context, st *reconnectState) {
	now := time.Now()
	if st.dialing != nil {
		select {
		case err := <-st.dialing:
			st.dialing = nil
			if err == nil {
				err = a.Session.Resume(st.lostAt)
			}
			if err != nil {
				st.delay *= 2
				if st.delay > 30*time.Second {
					st.delay = 30 * time.Second
				}
				st.nextAttempt = now.Add(st.delay)
				if !st.failed {
					st.failed = true
					a.Session.Notice(fmt.Sprintf("reconnect failed (%v), still trying", err))
				}
				a.dirty = true
				return
			}
			*st = reconnectState{}
			a.Session.Notice("reconnected")
			a.dirty = true
		default:
		}
		return
	}
	if !st.nextAttempt.IsZero() && now.Before(st.nextAttempt) {
		return
	}
	if st.delay == 0 {
		st.delay = time.Second
		st.lostAt = now
		a.Session.Notice("connection lost, reconnecting...")
		a.dirty = true
	}
	done := make(chan error, 1)
	st.dialing = done
	go func() { done <- a.Session.Redial(ctx) }()
}

// reconnecting reports whether the chat connection is down.
func (a *App) reconnecting() bool { return a.Session != nil && !a.Session.IsAlive() }

// openAvatarManager runs the unified /avatar UI: pick from library,
// upload, disable, or future-editor. The OnAvatarPick callback owns the
// conn for the duration; we route the user's choice to the appropriate
// store mutation and then force a full redraw.
func (a *App) openAvatarManager() {
	a.applyAvatarAction(a.OnAvatarPick)
}

// applyAvatarAction runs pick (the selector, or a fixed action from
// /settings) and carries out what it returns.
func (a *App) applyAvatarAction(pick func() (string, string, error)) {
	defer a.pauseMouse()()
	if pick == nil {
		a.Session.Notice("/avatar: not wired")
		return
	}
	b64, action, err := pick()
	a.forceFullRedraw()
	if err != nil {
		a.Session.Notice(fmt.Sprintf("/avatar: %v", err))
		return
	}
	switch action {
	case "picked":
		if a.Session.Self != nil {
			a.Session.Self.Avatar = b64
		}
		a.Session.Notice("\x01Gavatar updated\x01n -- attached to your next message")
	case "upload":
		if a.OnAvatarUpload == nil {
			a.Session.Notice("/avatar upload: not wired")
			return
		}
		if !a.confirmUpload() {
			a.forceFullRedraw()
			a.Session.Notice("/avatar upload: cancelled")
			return
		}
		uploaded, uerr := a.OnAvatarUpload()
		a.forceFullRedraw()
		if uerr != nil {
			a.Session.Notice(fmt.Sprintf("/avatar upload: %v", uerr))
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = uploaded
		}
		a.Session.Notice("\x01Gavatar uploaded\x01n -- attached to your next message")
	case "disable":
		if a.OnAvatarDisable == nil {
			a.Session.Notice("/avatar off: not wired")
			return
		}
		if err := a.OnAvatarDisable(); err != nil {
			a.Session.Notice(fmt.Sprintf("/avatar off: %v", err))
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = ""
		}
		a.Session.Notice("avatar disabled")
	case "editor":
		if a.OnAvatarEditor == nil {
			a.Session.Notice("/avatar editor: not wired")
			return
		}
		edited, eerr := a.OnAvatarEditor()
		a.forceFullRedraw()
		if eerr != nil {
			a.Session.Notice(fmt.Sprintf("/avatar editor: %v", eerr))
			return
		}
		if edited == "" {
			a.Session.Notice("/avatar editor: cancelled")
			return
		}
		if a.Session.Self != nil {
			a.Session.Self.Avatar = edited
		}
		a.Session.Notice("\x01Gavatar updated\x01n -- attached to your next message")
	default:
		a.Session.Notice("/avatar: cancelled")
	}
}

// confirmUpload draws a brief "how to make an avatar" prompt over the chat
// UI and waits for the user to press Enter (proceed) or Esc (cancel).
// Synchronous: blocks the main loop until the user dismisses it. The chat
// UI is force-redrawn by the caller after this returns.
func (a *App) confirmUpload() bool {
	w := 60
	if w > a.Width-4 {
		w = a.Width - 4
	}
	if w < 40 {
		w = 40
	}
	h := 13
	x := (a.Width - w) / 2
	y := (a.Height - h) / 2
	if y < 0 {
		y = 0
	}

	th := a.theme()
	frame := ansi.NewFrame(x, y, w, h, th.DialogBg)
	frame.Charset = a.Charset
	drawBox(frame, th, th.DialogBg)
	boxTitle(frame, "Avatar Upload", th.DialogTitle)

	lines := []struct {
		text string
		attr ansi.Attr
	}{
		{"Create your 10x6 avatar:", th.DialogHead},
		{"", 0},
		{" 1. Open an ANSI editor (Moebius, Pablo Draw,", th.DialogBg},
		{"    or any tool that exports CP437 .bin).", th.DialogBg},
		{" 2. Draw inside a 10-wide x 6-tall canvas.", th.DialogBg},
		{" 3. Save as a .bin file.", th.DialogBg},
		{" 4. Press Enter below, then send via Zmodem", th.DialogBg},
		{"    (Alt-U in SyncTERM).", th.DialogBg},
		{"", 0},
		{" [Enter] start upload     [Esc] cancel", th.DialogHint},
	}
	if a.BoardWide {
		// The spare row above the keys: say what the upload changes.
		lines[8] = struct {
			text string
			attr ansi.Attr
		}{" It becomes your BBS avatar, everywhere here.", th.DialogOK}
	}
	var hints hintLine
	for i, ln := range lines {
		if ln.text == "" {
			continue
		}
		if i == len(lines)-1 { // the key line; its actions are clickable
			hints = drawHints(frame, 2, 1+i, truncate(ln.text, w-4), ln.attr,
				hint{"[Enter] start upload", enterKey}, hint{"[Esc] cancel", escKey})
			continue
		}
		frame.PrintAt(2, 1+i, []byte(truncate(ln.text, w-4)), ln.attr)
	}
	if err := frame.Render(a.Conn); err != nil {
		return false
	}
	_ = a.parkCursor(a.Conn)
	// The avatar action paused the mouse for the transfer; the [X] needs it.
	if a.mousePaused {
		a.setMouse(true)
		defer a.setMouse(false)
	}

	for {
		k, ok, err := a.Input.Next(200 * time.Millisecond)
		if err != nil {
			return false
		}
		if !ok {
			continue
		}
		if hk, ok := hints.hit(k, frame.X, frame.Y); ok {
			k = hk
		}
		switch k.Type {
		case ansi.KeyEnter:
			return true
		case ansi.KeyEsc:
			return false
		}
		if closeHit(k, frame.X, frame.Y) {
			return false
		}
		if k.Type == ansi.KeyChar && (k.Rune == 'q' || k.Rune == 'Q') {
			return false
		}
	}
}

// openRoster builds and displays a "Who's Here" modal over the chat UI.
// While open, normal frames don't render; HandleKey routes to the modal.
func (a *App) openRoster(users []string) {
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	a.modalFrame = ansi.NewFrame(0, 0, a.Width, usable, a.theme().ModalBg)
	a.modalFrame.Charset = a.Charset
	roster := NewRosterModal(a.modalFrame, users, a.Session)
	roster.Truecolor = a.tvColorTrue()
	roster.Theme = a.theme()
	roster.Self = a.selfName()
	if a.Ignores != nil {
		// I toggles through what /ignore and /unignore do (their notices
		// are waiting in the chat when the panel closes).
		roster.Ignored = func(name string) bool { return a.Ignores.Hidden(name, "") }
		roster.ToggleIgnore = func(name string) {
			if a.Ignores.Hidden(name, "") {
				a.handleUnignore([]string{name})
			} else {
				a.handleIgnore([]string{name})
			}
		}
	}
	a.modal = roster
	_ = ansi.ClearScreen(a.Conn)
	a.dirty = true
}

// openChannels shows the /channels list (joined rooms + the server's public
// room registry, with user and unread counts).
func (a *App) openChannels() {
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	a.modalFrame = ansi.NewFrame(0, 0, a.Width, usable, a.theme().ModalBg)
	a.modalFrame.Charset = a.Charset
	cm := NewChannelsModal(a.modalFrame, a.Session.Rooms())
	cm.Theme = a.theme()
	a.modal = cm
	_ = ansi.ClearScreen(a.Conn)
	a.dirty = true
}

// closeModal tears down the roster or channel list and forces a full
// redraw of the chat UI.
func (a *App) closeModal() {
	a.modal = nil
	a.modalFrame = nil
	a.forceFullRedraw()
}

// afterModal closes m and acts on what was picked in it: a room to join
// or leave, or someone to message (the /msg waits in the input line).
func (a *App) afterModal(m modalView) (exit bool) {
	a.closeModal()
	switch m := m.(type) {
	case *ChannelsModal:
		return a.applyChannelChoice(m)
	case *RosterModal:
		if m.Msg != "" {
			a.inputLine.SetValue(msgPrefix(m.Msg))
		}
	}
	return false
}

// openImageViewer opens the /img modal showing the queued bitmaps.
func (a *App) openImageViewer() {
	images := a.Session.Bitmaps()
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	a.imageViewer = NewImageViewer(images, a.Width, usable)
	a.imageViewer.Theme = a.theme()
	a.imageViewer.Charset = a.Charset // the box, and raster cells
	if rasters := a.rasterItems(); len(rasters) > 0 {
		a.imageViewer.WithRasters(rasters, a.zoomPicture)
	}
	if len(images) > 0 {
		a.Session.MarkBitmapViewed(a.imageViewer.CurrentIndex())
	}
	_ = ansi.ClearScreen(a.Conn)
	a.dirty = true
}

// closeImageViewer dismisses the image viewer and restores chat.
func (a *App) closeImageViewer() {
	a.imageViewer = nil
	a.forceFullRedraw()
}

// ApplyTheme rebinds every theme-driven color slot on the live UI and
// applies any idle/screensaver overrides the theme carries. Call this
// after NewApp + main config has been wired in: idle overrides win over
// main config (so a theme can ship a curated screensaver profile), and
// color overrides win over the constructor's defaults.
func (a *App) ApplyTheme(t *theme.Theme) {
	if t == nil {
		return
	}
	a.Theme = t

	// Frame fills (used on Clear and as default Print attr).
	a.headerFrame.SetFill(t.HeaderStats)
	a.actionFrame.SetFill(t.TopActionBar)

	// Buttons.
	a.actionBar.BarAttr = t.TopActionBar
	a.actionBar.ButtonAttr = t.TopActionPill
	a.actionBar.HighlightAttr = t.TopActionHighlight
	a.actionBar.ShadowAttr = t.TopActionShadow
	a.header.CloseAttr = t.CloseButton

	// Header (MOTD strip).
	a.header.StatsAttr = t.HeaderStats
	a.header.MotdAttr = t.HeaderMotd

	// Input line.
	a.inputLine.PromptAttr = t.InputPrompt
	a.inputLine.TextAttr = t.InputText
	a.inputLine.CursorAttr = t.InputCursor
	if a.inputFrame != nil {
		a.inputFrame.SetFill(t.InputBg)
	}

	// Transcript reads from .Theme on every render.
	a.transcript.Theme = t

	// Screensaver profile overrides. nil/empty = inherit whatever the
	// main config set.
	if a.idleBase == nil {
		a.idleBase = &themeIdle{random: a.IdleRandom, sequence: a.IdleSequence, disable: a.IdleDisable}
	} else {
		a.IdleRandom, a.IdleSequence, a.IdleDisable = a.idleBase.random, a.idleBase.sequence, a.idleBase.disable
	}
	if t.IdleRandom != nil {
		a.IdleRandom = *t.IdleRandom
	}
	if len(t.IdleSequence) > 0 {
		a.IdleSequence = t.IdleSequence
	}
	if len(t.IdleDisable) > 0 {
		a.IdleDisable = t.IdleDisable
	}

	a.forceFullRedraw()
}

// theme is the active theme, never nil.
func (a *App) theme() *theme.Theme {
	if a.Theme == nil {
		a.Theme = theme.Default()
	}
	return a.Theme
}

// orDefault lets a component built outside the App draw before a theme is
// assigned.
func orDefault(t *theme.Theme) *theme.Theme {
	if t == nil {
		return theme.Default()
	}
	return t
}

// forceFullRedraw clears the screen and marks every frame for repaint.
// Used when transitioning out of any modal.
func (a *App) forceFullRedraw() {
	_ = ansi.ClearScreen(a.Conn)
	for _, f := range []*ansi.Frame{a.headerFrame, a.actionFrame, a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame, a.inputFrame} {
		f.ForceRedraw()
	}
	a.transcriptComp.ForceRedraw()
	if a.imageViewer != nil {
		a.imageViewer.ForceRedraw()
	}
	a.dirty = true
}

// splitFields splits on whitespace runs, similar to strings.Fields, without
// importing the whole strings.Fields path (we already have indexSpace).
func splitFields(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		if j > i {
			out = append(out, s[i:j])
		}
		i = j
	}
	return out
}

func indexSpace(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return i
		}
	}
	return -1
}

func (a *App) draw() {
	if a.imageViewer != nil {
		// Image viewer renders directly to conn during render(); nothing
		// to do here.
		return
	}
	if a.modal != nil {
		a.modal.Render()
		return
	}
	// Refresh header text every draw so user count, MOTD, and image count
	// updates surface without explicit invalidation.
	a.refreshHeader()
	a.refreshActionBar()
	a.header.Render()
	a.actionBar.Pressed = a.pressName
	a.actionBar.Render()
	if a.pressName != "" && !a.pressDrawn {
		// The pushed-in face is in this frame; hold it pressHold from now.
		a.pressDrawn = true
		a.pressUntil = time.Now().Add(pressHold)
	}
	a.transcript.Reserve = 0
	if a.loungeActive() {
		// TV lounge mode paints the video, the top hint strip, popups and
		// the caption bar (on the last row) into the three transcript layers.
		a.drawLounge()
	} else {
		// Always render the transcript: the bg-anim layer shows through any
		// transparent cells in the transcript (gutters between bubbles), so
		// chat stays visible while idle animations play behind it.
		a.transcript.Truecolor = a.tvColorTrue()
		a.transcript.SixelLive = a.sixelLive()
		a.transcript.Render(a.Session.Messages())
	}
	a.inputLine.Render()
}

// refreshHeader rebuilds the stats text and MOTD slot. Cheap; the Frame
// dirty-diff means unchanged text emits zero bytes.
func (a *App) refreshHeader() {
	users := 0
	channel := ""
	if a.Session != nil {
		users = a.Session.UserCount()
		channel = a.Session.Channel
	}
	addr := a.ServerAddr
	if addr == "" {
		addr = "(local)"
	}
	joined := 1
	if a.Session != nil {
		joined = len(a.Session.Joined())
	}
	a.header.StatsText = fmt.Sprintf("Avatar Chat | %s | users %d | joined %d | %s",
		channel, users, joined, addr)
	if a.reconnecting() {
		a.header.StatsText = fmt.Sprintf("Avatar Chat | %s | reconnecting... | %s", channel, addr)
	}
	if a.Session != nil {
		a.header.MotdText = a.Session.MotdText()
	}
}

// refreshActionBar rebuilds the button row: Who, Rooms, Images,
// Settings, Help, Quit. The typed commands (/me, /msg, /join, /theme, ...)
// have no buttons; Help lists them.
//
// Attention: Images gets a [N] count when there are queued images, and is
// highlighted and flashes (500 ms) while some are unread; Who counts and
// flashes unread PMs; Rooms counts and flashes unread lines on joined
// rooms not being viewed.
func (a *App) refreshActionBar() {
	imgN := 0
	unread := 0
	if a.Session != nil {
		imgN = len(a.Session.Bitmaps())
		unread = a.Session.UnviewedBitmaps()
	}
	if a.images != nil {
		// Downloaded pictures page in /img too; they're already on screen
		// inline, so they count but never flash.
		imgN += a.images.readyCount()
	}
	flashOff := (time.Now().UnixNano()/1000000/500)%2 == 1
	badge := func(label string, n int, flash bool, optional bool) ActionButton {
		b := ActionButton{Label: label, optional: optional}
		if n > 0 {
			b.Label = fmt.Sprintf("%s [%d]", label, n)
		}
		if flash {
			b.Highlight, b.Hidden, b.optional = true, flashOff, false
		}
		return b
	}
	unreadRooms := a.unreadChannels()
	a.actionBar.Buttons = fitButtons([]ActionButton{
		badge("Who", a.unreadPM, a.unreadPM > 0, true),
		badge("Rooms", unreadRooms, unreadRooms > 0, true),
		badge("Images", imgN, unread > 0, false),
		{Label: "Settings", optional: true},
		{Label: "Help", optional: true},
		{Label: "Quit Esc"},
	}, a.Width)
}

// composeInto writes the current UI frame to w. The render loop composes
// into an in-memory buffer (so a frame can be held in flight and paced out
// without blocking); the one-shot paint paths write straight to the conn via
// renderBlocking.
func (a *App) composeInto(w io.Writer) error {
	if a.bellPending {
		// First in the frame, so it can't split an escape sequence.
		if _, err := w.Write([]byte{0x07}); err != nil {
			return err
		}
		a.bellPending = false
	}
	if a.tooSmall {
		if !a.tooSmallShown {
			a.tooSmallShown = true
			_, err := fmt.Fprintf(w, "\x1b[0m\x1b[2J\x1b[HWindow too small: chat needs %dx%d.\r\nMake it bigger to continue.", minCols, minRows)
			return err
		}
		return nil
	}
	if a.imageViewer != nil {
		if err := a.imageViewer.Render(w); err != nil {
			return err
		}
		return a.restCursor(w)
	}
	if a.modal != nil {
		if err := a.modalFrame.Render(w); err != nil {
			return err
		}
		return a.restCursor(w)
	}
	if err := a.headerFrame.Render(w); err != nil {
		return err
	}
	if err := a.actionFrame.Render(w); err != nil {
		return err
	}
	// Sixel pictures live outside the cell model. When the set on screen
	// changes, repaint every transcript cell first (wiping stale pixels)
	// and land the current ones on top; see blitSixels.
	sixelForced := false
	if sig := sixelSignature(a.sixelRegions()); sig != a.sixelSig && a.sixelSig != "" {
		a.transcriptComp.ForceRedraw()
		sixelForced = true
	}
	if err := a.transcriptComp.Render(w); err != nil {
		return err
	}
	if err := a.inputFrame.Render(w); err != nil {
		return err
	}
	if err := a.blitSixels(w, sixelForced); err != nil {
		return err
	}
	// Park the terminal cursor at the end of the input buffer. If the user's
	// BBS client does local echo (which Mac Terminal/iTerm/etc. often do for
	// SSH sessions without telnet IAC negotiation), the next typed character
	// lands on top of our input line instead of below the drawn area.
	return a.restCursor(w)
}

// restCursor moves the terminal cursor to cursorSpot.
func (a *App) restCursor(w io.Writer) error {
	col, row := a.cursorSpot()
	return ansi.MoveCursor(w, col, row)
}

// cursorSpot is where the terminal cursor rests between frames (1-based):
// the input caret, or the left of the input row while a panel is up.
func (a *App) cursorSpot() (col, row int) {
	row = a.inputFrame.Y + 1
	if a.imageViewer != nil || a.modal != nil {
		return 1, row
	}
	return a.inputFrame.X + a.inputLine.CursorCol() + 1, row
}

// parkCursor puts the terminal cursor where the chat screen keeps it, at
// the left of the input row, while a panel is up, so the reserved row below
// takes any stray newline. Left where the panel's last write ended (often
// the box's bottom-right corner), a terminal that ignores the no-wrap mode,
// or a client echoing keys, runs past the edge and scrolls the screen.
func (a *App) parkCursor(w io.Writer) error {
	return ansi.MoveCursor(w, 1, a.inputFrame.Y+1)
}

// renderBlocking composes the current frame and writes all of it to the conn
// with the ordinary blocking Write. Used for one-shot paints (startup, and
// before handing the wire to a file transfer) where there is no next frame to
// race and we just want the screen fully on the wire before continuing.
func (a *App) renderBlocking() error {
	a.renderBuf.Reset()
	if err := a.composeInto(&a.renderBuf); err != nil {
		return err
	}
	_, err := a.Conn.Write(a.renderBuf.Bytes())
	return err
}

// paceFrame is the render-loop output path. It keeps at most one composed
// frame in flight: it first tries to drain the frame already pending, and
// only composes a new one once that has fully flushed. On a slow link the
// non-blocking write short-returns, we hold off composing, and the TV
// consumer's drop-to-latest means the next frame we compose is the freshest
// one — graceful frame-drop instead of stuffing the SSH channel past its
// window. Frames are written whole and in order, so a partial flush never
// corrupts an escape sequence; we just resume at pendingOff next tick.
func (a *App) paceFrame() error {
	if err := a.flushPending(); err != nil {
		return err
	}
	if !a.frameInFlight() && a.audioOut.Len() > 0 {
		a.pending = append(a.pending[:0], a.audioOut.Bytes()...)
		a.pendingOff = 0
		a.audioOut.Reset()
		return a.flushPending()
	}
	if a.frameInFlight() || !a.dirty {
		return nil
	}
	a.draw()
	a.renderBuf.Reset()
	if err := a.composeInto(&a.renderBuf); err != nil {
		return err
	}
	a.dirty = false
	if a.renderBuf.Len() == 0 {
		return nil
	}
	// Copy the composed frame into the pending buffer (reusing its backing
	// array; renderBuf is reset on the next compose) and start draining it.
	a.pending = append(a.pending[:0], a.renderBuf.Bytes()...)
	a.pendingOff = 0
	return a.flushPending()
}

// frameInFlight reports whether a composed frame is still partially unwritten.
func (a *App) frameInFlight() bool {
	return a.pendingOff < len(a.pending)
}

// flushPending writes as much of the in-flight frame as the conn will accept
// without blocking. When the conn exposes a non-blocking write (real socket
// mode), a full send buffer short-returns and we retry the remainder next
// tick — this is where Synchronet's backpressure finally reaches the door.
// stdio / local consoles fall back to a single blocking Write.
func (a *App) flushPending() error {
	if !a.frameInFlight() {
		return nil
	}
	n, err := a.writeConn(a.pending[a.pendingOff:])
	if err != nil {
		return err
	}
	a.pendingOff += n
	return nil
}

// writeConn writes p to the conn, preferring a non-blocking write when the
// conn supports it (see termio.NonBlockingWriter). The interface is declared
// locally so app.go stays decoupled from the termio package; Go interfaces
// are structural, so the concrete socket conn satisfies it.
func (a *App) writeConn(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	type nonBlockingWriter interface {
		WriteNB(p []byte) (int, error)
	}
	if nb, ok := a.Conn.(nonBlockingWriter); ok {
		return nb.WriteNB(p)
	}
	return a.Conn.Write(p)
}

// silenceOnExit stops any audio still queued on the caller's terminal when
// the door exits, so nothing plays on over the BBS menu. The in-flight frame
// is finished first (blocking) so the Flush cannot splice into it.
func (a *App) silenceOnExit() {
	a.shutdownVideo()
	if a.alinks != nil && a.alinks.cancel != nil {
		a.alinks.cancel()
	}
	playing := a.alinks != nil && a.alinks.player != nil
	if a.tvAudio == nil && !playing && a.audioOut.Len() == 0 {
		return
	}
	if a.frameInFlight() {
		_, _ = a.Conn.Write(a.pending[a.pendingOff:])
		a.pendingOff = len(a.pending)
	}
	a.audioOut.Reset() // unplayed clips: drop them, only the Flush matters
	if a.tvAudio != nil {
		_ = a.tvAudio.Stop(&a.audioOut)
		a.tvAudio = nil
	}
	if playing {
		_ = a.alinks.player.Stop(&a.audioOut)
		a.alinks.player = nil
	}
	_, _ = a.Conn.Write(a.audioOut.Bytes())
	a.audioOut.Reset()
}
