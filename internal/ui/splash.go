package ui

import (
	_ "embed"
	"io"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Splash artwork is sourced from the repo root (./splash.ans). The
// Makefile copies it here before each build so go:embed (which can only
// read paths at-or-below this file) can pick it up. To customize, drop
// your replacement at the repo's top-level splash.ans and rebuild --
// don't edit this copy directly, it gets overwritten.
//
//go:embed splash.ans
var embeddedSplash []byte

// ShowSplash renders the splash artwork compiled into the binary,
// centered in (width x height), via the standard Frame pipeline (SGR
// colors, SAUCE-aware sizing, CP437->UTF-8 charset translation as
// needed). Set timeout to zero (or negative) to skip the splash
// entirely.
//
// Dismissed by any keypress (after a brief minimum-visible window so
// in-flight keystrokes from a previous screen can't insta-dismiss it)
// or after timeout. EOF (user disconnected) is surfaced so the caller
// can exit cleanly.
func ShowSplash(conn io.Writer, in *ansi.Input, width, height int, charset ansi.Charset, timeout time.Duration) error {
	if timeout <= 0 || len(embeddedSplash) == 0 {
		return nil
	}
	frame, err := ansi.LoadFile("splash.ans", embeddedSplash)
	if err != nil || frame == nil {
		return nil
	}
	frame.Charset = charset

	// Center the loaded artwork in the terminal. If the art is wider or
	// taller than the screen, anchor at the top-left (better than
	// negative offsets that would clip silently and look wrong).
	if frame.W < width {
		frame.X = (width - frame.W) / 2
	}
	if frame.H < height {
		frame.Y = (height - frame.H) / 2
	}

	if _, err := conn.Write([]byte("\x1b[2J\x1b[H")); err != nil {
		return err
	}
	if err := frame.Render(conn); err != nil {
		return err
	}

	// Drain any queued keypresses left over from the prior screen
	// (e.g. the Enter the user just pressed to confirm an avatar
	// selection). Without this, the strobe loop's first in.Next would
	// instantly return that stale key and dismiss the splash before the
	// user ever sees it.
	for {
		_, ok, err := in.Next(0)
		if err != nil || !ok {
			break
		}
	}

	// Strobe loop: snapshot the original cells, then on every tick rewrite
	// each colored cell's FG/BG to one of red/green/blue (CGA 4/2/1) and
	// re-render. The Frame's dirty-diff renderer means most ticks emit
	// only attribute changes, not full cell repaints. Stops on any key
	// after a short "must show" window, or when the splash timeout
	// elapses.
	const strobeTick = 80 * time.Millisecond
	const minVisible = 600 * time.Millisecond
	orig := frame.Snapshot()
	start := time.Now()
	deadline := start.Add(timeout)
	tick := 0
	dismissed := false
	for !dismissed && time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		pollFor := strobeTick
		if remaining < pollFor {
			pollFor = remaining
		}
		if pollFor <= 0 {
			break
		}
		k, ok, err := in.Next(pollFor)
		if err != nil {
			return err
		}
		if ok {
			_ = k
			// Ignore dismissal during the first ~600ms so the user
			// actually gets to see the splash; an in-flight keystroke
			// from the previous screen shouldn't kill it instantly.
			if time.Since(start) >= minVisible {
				dismissed = true
				break
			}
		}
		tick++
		frame.CycleColors(orig, tick)
		if err := frame.Render(conn); err != nil {
			return err
		}
	}

	// Reset attributes so the chat UI render that follows doesn't inherit
	// any leftover SGR state from the artwork.
	if _, err := conn.Write([]byte("\x1b[0m")); err != nil {
		return err
	}
	return nil
}

// ProbeTerminalSize does a one-shot Cursor Position Report probe to discover
// the connected terminal's actual dimensions. Useful when the drop-file's
// reported screen size doesn't match reality (e.g. the user reshaped their
// client between login and door launch). Sends `save | jump-to-far-corner |
// query | restore`, then waits up to timeout for the matching CPR response.
//
// Returns (cols, rows) on success or (0, 0) on timeout / parse error. The
// caller should fall back to whatever it had before in either case.
//
// Should only be called once during startup; doing it on a hot loop causes
// visible cursor flicker every probe interval.
func ProbeTerminalSize(conn io.Writer, in *ansi.Input, timeout time.Duration) (cols, rows int) {
	if _, err := conn.Write([]byte("\x1b[s\x1b[999;999H\x1b[6n\x1b[u")); err != nil {
		return 0, 0
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		k, ok, err := in.Next(remaining)
		if err != nil {
			return 0, 0
		}
		if !ok {
			continue
		}
		if k.Type == ansi.KeyResize && k.Cols > 0 && k.Rows > 0 {
			return k.Cols, k.Rows
		}
		// Discard any other keypress that arrived while we were waiting.
	}
	return 0, 0
}
