package ansi

import (
	"io"
	"time"
)

// ScreenSize asks the terminal for its dimensions and returns
// (cols, rows, ok). Tries two queries in order:
//
//  1. The cursor-position trick: save cursor, move to (row=999, col=999) so
//     the terminal clamps to its actual size, query position with `ESC[6n`,
//     restore cursor. Response is `ESC[<row>;<col>R`. This works on every
//     ANSI terminal including SyncTERM, NetRunner, mtelnet, and xterm.
//
//  2. xterm's `ESC[18t` window-size report as a fallback. Some terminals
//     respond to this when they don't implement DSR cursor reports.
//
// Call BEFORE starting the input pump so the response can be read directly
// from r without an intermediate goroutine consuming it.
func ScreenSize(rw io.ReadWriter, timeout time.Duration) (cols, rows int, ok bool) {
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	d, supportsDeadline := rw.(deadliner)
	if !supportsDeadline {
		return 0, 0, false
	}

	// Try cursor-position trick first.
	if c, r, found := queryCursorPos(rw, d, timeout); found {
		return c, r, true
	}
	// Fall back to xterm window-size report.
	if c, r, found := queryWindowSize(rw, d, timeout); found {
		return c, r, true
	}
	return 0, 0, false
}

func queryCursorPos(rw io.ReadWriter, d interface{ SetReadDeadline(time.Time) error }, timeout time.Duration) (cols, rows int, ok bool) {
	// ESC 7         save cursor (DECSC, widely supported)
	// ESC[999;999H  position to extreme, terminal clamps to actual size
	// ESC[6n        device status report -> response ESC[<row>;<col>R
	// ESC 8         restore cursor (DECRC)
	seq := []byte("\x1b7\x1b[999;999H\x1b[6n\x1b8")
	if _, err := rw.Write(seq); err != nil {
		return 0, 0, false
	}
	return readSizeResponse(rw, d, timeout, parseCursorReport)
}

func queryWindowSize(rw io.ReadWriter, d interface{ SetReadDeadline(time.Time) error }, timeout time.Duration) (cols, rows int, ok bool) {
	if _, err := rw.Write([]byte("\x1b[18t")); err != nil {
		return 0, 0, false
	}
	return readSizeResponse(rw, d, timeout, parseSize)
}

// readSizeResponse buffers up to 256 bytes from rw under the given deadline,
// invoking parser after each chunk. parser returns (rows, cols, ok).
func readSizeResponse(rw io.ReadWriter, d interface{ SetReadDeadline(time.Time) error }, timeout time.Duration, parser func([]byte) (int, int, bool)) (cols, rows int, ok bool) {
	deadline := time.Now().Add(timeout)
	if err := d.SetReadDeadline(deadline); err != nil {
		return 0, 0, false
	}
	defer d.SetReadDeadline(time.Time{})

	buf := make([]byte, 0, 64)
	scratch := make([]byte, 32)
	for time.Now().Before(deadline) && len(buf) < 256 {
		n, err := rw.Read(scratch)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			if r, c, found := parser(buf); found {
				return c, r, true
			}
		}
		if err != nil {
			break
		}
	}
	return 0, 0, false
}

// parseCursorReport scans buf for `ESC [ <rows> ; <cols> R`.
func parseCursorReport(buf []byte) (rows, cols int, ok bool) {
	for i := 0; i+4 < len(buf); i++ {
		if buf[i] != 0x1B || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		r := 0
		for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
			r = r*10 + int(buf[j]-'0')
			j++
		}
		if r == 0 || j >= len(buf) || buf[j] != ';' {
			continue
		}
		j++
		c := 0
		for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
			c = c*10 + int(buf[j]-'0')
			j++
		}
		if c == 0 || j >= len(buf) || buf[j] != 'R' {
			continue
		}
		return r, c, true
	}
	return 0, 0, false
}

// parseSize scans buf for the first complete `ESC [ 8 ; <rows> ; <cols> t`
// sequence (xterm window-size report).
func parseSize(buf []byte) (rows, cols int, ok bool) {
	for i := 0; i+5 < len(buf); i++ {
		if buf[i] != 0x1B || buf[i+1] != '[' || buf[i+2] != '8' || buf[i+3] != ';' {
			continue
		}
		j := i + 4
		r := 0
		for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
			r = r*10 + int(buf[j]-'0')
			j++
		}
		if r == 0 || j >= len(buf) || buf[j] != ';' {
			continue
		}
		j++
		c := 0
		for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
			c = c*10 + int(buf[j]-'0')
			j++
		}
		if c == 0 || j >= len(buf) || buf[j] != 't' {
			continue
		}
		return r, c, true
	}
	return 0, 0, false
}
