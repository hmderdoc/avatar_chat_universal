package ui

import (
	"io"
	"os"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// ShowSplash loads an ANSI or BIN graphic from path, parses it into a
// 2D char/attr grid (via ansi.LoadFile -- handles SAUCE, SGR, cursor
// positioning), centers the grid in (width x height), and renders it
// through the standard Frame pipeline so charset conversion (CP437 ->
// UTF-8 when needed) and proper cell positioning happen for free.
//
// Dismissed by any keypress or after timeout. Missing file = no-op.
// EOF (user disconnected) is surfaced so the caller can exit cleanly.
func ShowSplash(conn io.Writer, in *ansi.Input, path string, width, height int, charset ansi.Charset, timeout time.Duration) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Missing splash is fine; just skip.
		return nil
	}
	frame, err := ansi.LoadFile(path, data)
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

	// Strobe loop: snapshot the original cells, then on every tick rewrite
	// each colored cell's FG/BG to one of red/green/blue (CGA 4/2/1) and
	// re-render. The Frame's dirty-diff renderer means most ticks emit
	// only attribute changes, not full cell repaints. Stops on any key
	// or when the splash timeout elapses.
	const strobeTick = 80 * time.Millisecond
	orig := frame.Snapshot()
	deadline := time.Now().Add(timeout)
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
			dismissed = true
			break
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
