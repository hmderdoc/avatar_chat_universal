package ui

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/idle"
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

	headerFrame       *ansi.Frame
	actionFrame       *ansi.Frame
	bgAnimFrame       *ansi.Frame
	transcriptFrame   *ansi.Frame
	fgAnimFrame       *ansi.Frame
	transcriptComp    *ansi.Compositor
	bottomActionFrame *ansi.Frame
	inputFrame        *ansi.Frame

	header          *Header
	actionBar       *ActionBar
	bottomActionBar *ActionBar
	transcript      *Transcript
	inputLine       *InputLine

	// ServerAddr is shown in the header stats line ("...| host:port").
	ServerAddr string

	lastMotdRefresh time.Time

	modalFrame  *ansi.Frame
	modal       *RosterModal
	imageViewer *ImageViewer

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
	TVPopupSecs int    // how long a message popup lingers (5-15, default 10)
	TVAvatars   bool   // show avatars in popups (default off; video competes)

	// TV lounge runtime state.
	tvTuner          *chat.Tuner
	tvConsumer       *telnetvision.Consumer
	tvOptedOut       bool
	tvShowTranscript bool
	tvPopups         []tvPopup
	tvLastTick       time.Time

	dirty bool
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
//	row 0       : header (green/blue, "avatar_chat_universal" + #channel)
//	row 1       : action bar (blue, slash-command hints)
//	rows 2..H-3 : transcript area (3 layered frames composited)
//	  - bgAnimFrame    : opaque, holds BG animations or solid black
//	  - transcriptFrame: transparent overlay, opaque only where bubbles paint
//	  - fgAnimFrame    : transparent overlay, FG animations paint sparse cells
//	row H-2     : status (gray, ad-hoc messages, last-msg timestamp)
//	row H-1     : input prompt
//	row H       : reserved (cursor scratch)
func (a *App) layout() {
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	// row 0          : header (green; stats/MOTD rotation)
	// row 1          : top action bar (blue, white-on-magenta pills)
	// rows 2..H-4    : transcript (composited bg/transcript/fg layers)
	// row H-3        : bottom action bar (magenta, black-on-cyan pills)
	// row H-2        : input line (yellow > prompt)
	// row H-1        : reserved (cursor scratch)
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
	a.actionFrame = ansi.NewFrame(0, 1, a.Width, 1, th.TopActionBar)
	a.bgAnimFrame = ansi.NewFrame(0, 2, a.Width, transcriptH, ansi.Black|ansi.BgBlack)
	a.transcriptFrame = ansi.NewTransparentFrame(0, 2, a.Width, transcriptH)
	a.fgAnimFrame = ansi.NewTransparentFrame(0, 2, a.Width, transcriptH)
	a.transcriptComp = ansi.NewCompositor(a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame)
	a.transcriptComp.Charset = a.Charset
	a.bottomActionFrame = ansi.NewFrame(0, usable-2, a.Width, 1, th.BottomActionBar)
	a.inputFrame = ansi.NewFrame(0, usable-1, a.Width, 1, ansi.LightGray|ansi.BgBlack)
	for _, f := range []*ansi.Frame{a.headerFrame, a.actionFrame, a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame, a.bottomActionFrame, a.inputFrame} {
		f.Charset = a.Charset
	}

	a.header = NewHeader(a.headerFrame)
	a.header.StatsAttr = th.HeaderStats
	a.header.MotdAttr = th.HeaderMotd
	a.actionBar = NewActionBar(a.actionFrame)
	a.bottomActionBar = NewActionBar(a.bottomActionFrame)
	selfName := ""
	if a.Session != nil && a.Session.Self != nil {
		selfName = a.Session.Self.Name
	}
	a.transcript = NewTranscript(a.transcriptFrame, selfName)
	a.transcript.Theme = th
	a.inputLine = NewInputLine(a.inputFrame, "> ")
	a.inputLine.PromptAttr = th.InputPrompt
	a.inputLine.TextAttr = th.InputText
	a.inputLine.CursorAttr = th.InputCursor
	a.inputLine.CompletionProvider = func() []string {
		return a.Session.KnownNicks()
	}

	// Action bar attrs come from the theme (defaults to "futurewave"). Top
	// bar = pills on the bar's BG; bottom bar same with its own palette.
	a.actionBar.BarAttr = th.TopActionBar
	a.actionBar.ButtonAttr = th.TopActionPill
	a.actionBar.HighlightAttr = th.TopActionHighlight
	a.bottomActionBar.BarAttr = th.BottomActionBar
	a.bottomActionBar.ButtonAttr = th.BottomActionPill
	a.bottomActionBar.HighlightAttr = th.BottomActionHighlight

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
	}
	// Notices route into the transcript itself now that there's no status
	// bar; nothing extra to do here, but keep a callback so per-event
	// dirty-flagging still works from external triggers.
	a.Session.OnNotice = func(string) {
		a.dirty = true
	}
	a.dirty = true
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
	if len(text) > maxW {
		text = text[:maxW-1] + "…"
		// fall back to "..." in CP437 mode
		if a.Charset == ansi.CharsetCP437 {
			text = string([]byte(text)[:maxW-3]) + "..."
		}
	}
	bg := ansi.LightGray | ansi.BgBlue
	if a.Theme != nil {
		// Reuse the bottom action bar palette so the ticker visually
		// "belongs" with the chat chrome.
		bg = a.Theme.BottomActionBar
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
	if err := ansi.ShowCursor(a.Conn); err != nil {
		return err
	}
	if err := ansi.ClearScreen(a.Conn); err != nil {
		return err
	}

	a.draw()
	if err := a.render(); err != nil {
		return err
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

		// Maybe-reconnect: if the chat connection died, try to bring it
		// back up with exponential backoff (1s, 2s, 4s, ... capped at 30s).
		if !a.Session.IsAlive() {
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


		key, ok, err := a.Input.Next(a.PollInterval)
		if err != nil {
			return err
		}
		if ok {
			a.markActive()
			if exit, err := a.handleKey(key); err != nil {
				return err
			} else if exit {
				return nil
			}
		}

		if a.dirty {
			a.draw()
			if err := a.render(); err != nil {
				return err
			}
			a.dirty = false
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
	// Stray Cursor Position Report from the terminal -- ignore. We probe
	// once at startup; any other CPR response is unsolicited and not
	// something the chat UI cares about.
	if k.Type == ansi.KeyResize {
		return false, nil
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
		closed := a.modal.HandleKey(k)
		a.dirty = true
		if closed {
			a.closeRoster()
		}
		return false, nil
	}

	// Global hotkeys first.
	switch k.Type {
	case ansi.KeyPgUp:
		// In lounge mode, scrolling up pulls the transcript over the video.
		if a.loungeActive() {
			a.tvShowTranscript = true
			a.forceFullRedraw()
		}
		a.transcript.Scroll += 5
		a.dirty = true
		return false, nil
	case ansi.KeyPgDn:
		a.transcript.Scroll -= 5
		if a.transcript.Scroll < 0 {
			a.transcript.Scroll = 0
		}
		// Scrolled back to the bottom in lounge mode: drop the transcript and
		// return to the full video.
		if a.loungeActive() && a.transcript.Scroll == 0 && a.tvShowTranscript {
			a.tvShowTranscript = false
			a.forceFullRedraw()
		}
		a.dirty = true
		return false, nil
	case ansi.KeyEsc:
		return true, nil
	}

	if k.Type == ansi.KeyChar && k.Ctrl && k.Rune == 0x18 { // Ctrl-X
		return true, nil
	}

	submit, cancel := a.inputLine.HandleKey(k)
	if cancel {
		return true, nil
	}
	if submit {
		text := a.inputLine.Value()
		a.inputLine.Reset()
		a.inputLine.PushHistory(text)
		if exit, handled := a.handleSlashCommand(text); handled {
			a.dirty = true
			return exit, nil
		}
		if err := a.Session.Send(text); err != nil {
			a.Session.Notice(fmt.Sprintf("send failed: %v", err))
		}
		a.transcript.Scroll = 0
	}
	a.dirty = true
	return false, nil
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
	cmd := parts[0]
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
	case "/msg", "/m":
		if len(parts) < 3 {
			a.Session.Notice("usage: /msg <user> <text>")
			return false, true
		}
		recipient := parts[1]
		// Find where the message body starts in the original input so we
		// preserve the user's exact spacing/punctuation.
		bodyStart := len(cmd) + 1 + len(parts[1]) + 1
		if bodyStart > len(text) {
			bodyStart = len(text)
		}
		body := text[bodyStart:]
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
		if err := a.Session.SendPrivate(target, body); err != nil {
			a.Session.Notice(fmt.Sprintf("/r failed: %v", err))
		}
		return false, true
	case "/channels":
		a.Session.Notice("channels: #" + a.Session.Channel + " (multi-channel UI: use /join <name> to switch)")
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
	case "/img":
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
		// In this build there's only one active channel; treat /part as quit.
		return true, true
	case "/avatar":
		a.handleAvatarCmd(parts)
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
		a.tvShowTranscript = !a.tvShowTranscript
		if !a.tvShowTranscript {
			a.transcript.Scroll = 0
		}
		a.forceFullRedraw()
		return false, true
	case "/tvcolor":
		if strings.EqualFold(a.TVColor, "16") {
			a.TVColor = "truecolor"
		} else {
			a.TVColor = "16"
		}
		a.forceFullRedraw()
		a.Session.Notice("TV color: " + a.TVColor)
		return false, true
	case "/help", "/?":
		a.Session.Notice("/me /msg /r /join /part /who /img /channels /clear /avatar /quit")
		a.Session.Notice("TV: /tvtuner <host> <port> <channel> | /tvtuner off | /tvon | /tvoff | /transcript | /tvcolor")
		return false, true
	}
	a.Session.Notice(fmt.Sprintf("unknown command: %s", cmd))
	return false, true
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
		a.Session.Notice("  SyncTERM: \x01WAlt-S\x01n then pick the .bin")
		a.Session.Notice("  NetRunner / fTelnet: Send menu → Zmodem")
		a.Session.Notice("  Press \x01WEsc\x01n to cancel.")
		a.draw()
		_ = a.render()
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

// tryReconnect attempts to bring the chat session back up. The first time
// the connection drops it logs a notice into the transcript; subsequent
// retries are silent until success or final failure. Backoff doubles up
// to 30s between attempts.
func (a *App) tryReconnect(ctx context.Context, st *reconnectState) {
	now := time.Now()
	if !st.nextAttempt.IsZero() && now.Before(st.nextAttempt) {
		return
	}
	first := st.delay == 0
	if first {
		st.delay = time.Second
		a.Session.OnNotice = func(string) {} // suppress notice spam during reconnect
		a.Session.Notice("connection lost, reconnecting...")
		a.dirty = true
	}
	if err := a.Session.Reconnect(ctx); err != nil {
		st.delay *= 2
		if st.delay > 30*time.Second {
			st.delay = 30 * time.Second
		}
		st.nextAttempt = now.Add(st.delay)
		a.Session.Notice(fmt.Sprintf("reconnect failed (%v), retrying in %v", err, st.delay))
		a.dirty = true
		return
	}
	// Success.
	st.delay = 0
	st.nextAttempt = time.Time{}
	a.Session.OnNotice = func(text string) {
		a.Session.Notice(text)
		a.dirty = true
	}
	a.Session.Notice("reconnected")
	a.dirty = true
}

// openAvatarManager runs the unified /avatar UI: pick from library,
// upload, disable, or future-editor. The OnAvatarPick callback owns the
// conn for the duration; we route the user's choice to the appropriate
// store mutation and then force a full redraw.
func (a *App) openAvatarManager() {
	if a.OnAvatarPick == nil {
		a.Session.Notice("/avatar: not wired")
		return
	}
	b64, action, err := a.OnAvatarPick()
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

	frame := ansi.NewFrame(x, y, w, h, ansi.White|ansi.BgBlue)
	frame.Charset = a.Charset
	frame.PrintAt(2, 0, []byte(" Avatar Upload "), ansi.LightCyan|ansi.BgMagenta)

	lines := []struct {
		text string
		attr ansi.Attr
	}{
		{"Create your 10x6 avatar:", ansi.LightCyan | ansi.BgBlue},
		{"", 0},
		{" 1. Open an ANSI editor (Moebius, Pablo Draw,", ansi.White | ansi.BgBlue},
		{"    or any tool that exports CP437 .bin).", ansi.White | ansi.BgBlue},
		{" 2. Draw inside a 10-wide x 6-tall canvas.", ansi.White | ansi.BgBlue},
		{" 3. Save as a .bin file.", ansi.White | ansi.BgBlue},
		{" 4. Press Enter below, then send via Zmodem", ansi.White | ansi.BgBlue},
		{"    (Alt-S in SyncTERM).", ansi.White | ansi.BgBlue},
		{"", 0},
		{" [Enter] start upload     [Esc] cancel", ansi.Yellow | ansi.BgBlue},
	}
	for i, ln := range lines {
		if ln.text == "" {
			continue
		}
		frame.PrintAt(1, 1+i, []byte(ln.text), ln.attr)
	}
	if err := frame.Render(a.Conn); err != nil {
		return false
	}

	for {
		k, ok, err := a.Input.Next(200 * time.Millisecond)
		if err != nil {
			return false
		}
		if !ok {
			continue
		}
		switch k.Type {
		case ansi.KeyEnter:
			return true
		case ansi.KeyEsc:
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
	a.modalFrame = ansi.NewFrame(0, 0, a.Width, usable, ansi.LightGray|ansi.BgBlack)
	a.modalFrame.Charset = a.Charset
	a.modal = NewRosterModal(a.modalFrame, users, a.Session)
	_ = ansi.ClearScreen(a.Conn)
	a.dirty = true
}

// closeRoster tears down the roster and forces a full redraw of the chat UI.
func (a *App) closeRoster() {
	a.modal = nil
	a.modalFrame = nil
	a.forceFullRedraw()
}

// openImageViewer opens the /img modal showing the queued bitmaps.
func (a *App) openImageViewer() {
	images := a.Session.Bitmaps()
	usable := a.Height - 1
	if usable < 12 {
		usable = a.Height
	}
	a.imageViewer = NewImageViewer(images, a.Width, usable)
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
	a.bottomActionFrame.SetFill(t.BottomActionBar)

	// Action bars.
	a.actionBar.BarAttr = t.TopActionBar
	a.actionBar.ButtonAttr = t.TopActionPill
	a.actionBar.HighlightAttr = t.TopActionHighlight
	a.bottomActionBar.BarAttr = t.BottomActionBar
	a.bottomActionBar.ButtonAttr = t.BottomActionPill
	a.bottomActionBar.HighlightAttr = t.BottomActionHighlight

	// Header (MOTD strip).
	a.header.StatsAttr = t.HeaderStats
	a.header.MotdAttr = t.HeaderMotd

	// Input line.
	a.inputLine.PromptAttr = t.InputPrompt
	a.inputLine.TextAttr = t.InputText
	a.inputLine.CursorAttr = t.InputCursor

	// Transcript reads from .Theme on every render.
	a.transcript.Theme = t

	// Screensaver profile overrides. nil/empty = inherit whatever the
	// main config set.
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

// forceFullRedraw clears the screen and marks every frame for repaint.
// Used when transitioning out of any modal.
func (a *App) forceFullRedraw() {
	_ = ansi.ClearScreen(a.Conn)
	for _, f := range []*ansi.Frame{a.headerFrame, a.actionFrame, a.bgAnimFrame, a.transcriptFrame, a.fgAnimFrame, a.bottomActionFrame, a.inputFrame} {
		f.ForceRedraw()
	}
	a.transcriptComp.ForceRedraw()
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
	a.actionBar.Render()
	if a.loungeActive() {
		// TV lounge mode paints the video + caption + popups into the three
		// transcript layers instead of the normal transcript.
		a.drawLounge()
	} else {
		// Always render the transcript: the bg-anim layer shows through any
		// transparent cells in the transcript (gutters between bubbles), so
		// chat stays visible while idle animations play behind it.
		a.transcript.Render(a.Session.Messages())
	}
	a.bottomActionBar.Render()
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
	a.header.StatsText = fmt.Sprintf("Avatar Chat | %s | users %d | joined 1 | %s",
		channel, users, addr)
	if a.Session != nil {
		a.header.MotdText = a.Session.MotdText()
	}
}

// refreshActionBar rebuilds both the top blue and bottom magenta button
// bars. The top bar carries the high-frequency hotkeys; the bottom bar
// carries the verbose chat commands so they don't duplicate each other.
//
// /img gets a [N] count when there are queued images, plus a Highlight
// flag (yellow pill) when there are unread ones, plus a 500ms flash so
// it's hard to miss.
func (a *App) refreshActionBar() {
	imgN := 0
	unread := 0
	if a.Session != nil {
		imgN = len(a.Session.Bitmaps())
		unread = a.Session.UnviewedBitmaps()
	}
	imgLabel := "/img"
	if imgN > 0 {
		imgLabel = fmt.Sprintf("/img [%d]", imgN)
	}
	imgBtn := ActionButton{Label: imgLabel}
	if unread > 0 {
		imgBtn.Highlight = true
		// 500ms flash: hide on the off-phase.
		if (time.Now().UnixNano() / 1000000/500)%2 == 1 {
			imgBtn.Hidden = true
		}
	}

	a.actionBar.Buttons = []ActionButton{
		{Label: "/who"},
		imgBtn,
		{Label: "/channels"},
		{Label: "/avatar"},
		{Label: "/help"},
		{Label: "Tab user"},
		{Label: "Esc exit"},
	}

	// Bottom bar focuses on the commands that ARE NOT in the top bar so
	// the two rows don't duplicate each other.
	a.bottomActionBar.Buttons = []ActionButton{
		{Label: "Up/PgUp history"},
		{Label: "/join"},
		{Label: "/part"},
		{Label: "/me"},
		{Label: "/msg"},
		{Label: "/r"},
		{Label: "/clear"},
		{Label: "/quit"},
	}
}

func (a *App) render() error {
	if a.imageViewer != nil {
		return a.imageViewer.Render(a.Conn)
	}
	if a.modal != nil {
		return a.modalFrame.Render(a.Conn)
	}
	if err := a.headerFrame.Render(a.Conn); err != nil {
		return err
	}
	if err := a.actionFrame.Render(a.Conn); err != nil {
		return err
	}
	if err := a.transcriptComp.Render(a.Conn); err != nil {
		return err
	}
	if err := a.bottomActionFrame.Render(a.Conn); err != nil {
		return err
	}
	if err := a.inputFrame.Render(a.Conn); err != nil {
		return err
	}
	// Park the terminal cursor at the end of the input buffer. If the user's
	// BBS client does local echo (which Mac Terminal/iTerm/etc. often do for
	// SSH sessions without telnet IAC negotiation), the next typed character
	// lands on top of our input line instead of below the drawn area.
	col := a.inputFrame.X + a.inputLine.CursorCol() + 1 // 1-based
	row := a.inputFrame.Y + 1
	return ansi.MoveCursor(a.Conn, col, row)
}
