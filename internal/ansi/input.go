package ansi

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// KeyType enumerates non-rune keys the input decoder can produce. Printable
// keys come back as KeyChar with Rune set; control characters come back as
// KeyChar with the appropriate ASCII control byte in Rune.
type KeyType int

const (
	KeyChar KeyType = iota
	KeyEnter
	KeyTab
	KeyBackspace
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyInsert
	KeyDelete
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	// KeyResize is emitted when the input decoder sees a Cursor Position
	// Report from the terminal in response to our `\x1b[6n` probe (the
	// app's resize-detection ping). Cols/Rows carry the new dims.
	KeyResize
	// KeyMouse is a mouse report (after EnableMouse): X/Y are the 0-based
	// cell, Button the xterm button code (see the Mouse* constants).
	KeyMouse
	// KeyPaste is text the terminal pasted with bracketed paste on
	// (PasteOn): all of it in Text, line breaks included, so a paste of
	// several lines isn't taken as several Enters.
	KeyPaste
)

// Mouse button codes in Key.Button. Wheel events come as presses.
const (
	MouseLeft      = 0
	MouseMiddle    = 1
	MouseRight     = 2
	MouseWheelUp   = 64
	MouseWheelDown = 65
)

// MouseOn asks for button press/release reports (DECSET 1000) in SGR form
// (1006); a terminal without 1006 falls back to the X10 byte form, which
// the decoder also reads while SetMouse(true) is in effect. MouseOff
// undoes both.
const (
	MouseOn  = "\x1b[?1000h\x1b[?1006h"
	MouseOff = "\x1b[?1006l\x1b[?1000l"
)

// PasteOn asks the terminal to bracket pasted text (DECSET 2004), which
// the decoder delivers as one KeyPaste; PasteOff undoes it. A terminal
// without it ignores both and pastes as typing, as before.
const (
	PasteOn  = "\x1b[?2004h"
	PasteOff = "\x1b[?2004l"
)

// Bracketed paste markers, and how long a paste may take to finish
// before what has arrived is delivered anyway.
const (
	pasteStart   = "\x1b[200~"
	pasteEnd     = "\x1b[201~"
	pasteTimeout = 2 * time.Second
	pasteMax     = 64 * 1024
)

// SizeProbe parks the cursor at the far corner, asks where it is and puts
// it back; the reply decodes as KeyResize. The cursor is hidden for the
// trip so the jump doesn't flash (the chat UI always shows it). The save
// and restore are ANSI.SYS's CSI s / CSI u, not DECSC/DECRC (ESC 7 / ESC 8):
// BBS terminals without DECSC print those as a literal "7" and "8", while
// an unknown CSI is just ignored.
const SizeProbe = "\x1b[?25l\x1b[s\x1b[999;999H\x1b[6n\x1b[u\x1b[?25h"

type Key struct {
	Type KeyType
	Rune rune // valid when Type == KeyChar (or for control chars)
	Ctrl bool
	// Cols / Rows are populated only when Type == KeyResize.
	Cols int
	Rows int
	// Text is the pasted text, for KeyPaste.
	Text string
	// Mouse fields, for KeyMouse. Release is set for a button release
	// (SGR 'm', or X10 button 3, which doesn't say which button).
	X, Y    int
	Button  int
	Release bool
}

// Input decodes a byte stream from a terminal connection into Keys. It
// handles xterm/VT100 escape sequences for arrow keys, function keys,
// Home/End/PgUp/PgDn, Delete/Insert, and the SS3-prefixed variants.
//
// A bare ESC press is reported only once escWait passes with no follow-up
// byte, matching the convention used by avatar_chat.js:2554. The wait runs
// from the last byte received, not from the start of the poll, so an arrow
// key whose bytes arrive split isn't taken for Esc.
type Input struct {
	reader  io.Reader // kept so callers can grab raw access via Pause/Resume
	bytes   chan byte
	pending []byte
	// lastByte is when the newest pending byte arrived.
	lastByte time.Time
	// pasteSince is when a bracketed paste began; zero outside one.
	pasteSince time.Time

	mu        sync.Mutex
	closed    bool
	closedErr error

	// Pause/Resume coordination. The pump holds pumpLock as an RLock during
	// every Read; Pause grabs the WLock to guarantee exclusive access to
	// the underlying reader. The pump uses short read deadlines so the
	// RLock is released frequently enough for Pause to acquire the WLock
	// without long delay.
	pumpLock sync.RWMutex

	// x10 enables the legacy X10 mouse form (ESC [ M b x y). Off by
	// default: some terminals send ESC [ M for F1, and the three bytes
	// after it are only a mouse report once we've asked for reports.
	x10 atomic.Bool
}

// escWait is how long a lone ESC waits for the rest of a sequence before it
// counts as the Esc key: long enough for a sequence split across telnet
// packets, short enough that Esc still feels immediate.
const escWait = 75 * time.Millisecond

// SetMouse tells the decoder whether mouse reporting is on, so it reads
// the X10 form; the SGR form is always decoded.
func (in *Input) SetMouse(on bool) { in.x10.Store(on) }

// NewInput starts a goroutine that reads from r and feeds bytes into the
// decoder. The goroutine exits on read error/EOF; subsequent Next calls
// continue to drain pending bytes, then return (zero, false, err).
func NewInput(r io.Reader) *Input {
	in := &Input{
		reader: r,
		bytes:  make(chan byte, 256),
	}
	go in.pump()
	return in
}

func (in *Input) pump() {
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	dl, _ := in.reader.(deadliner)
	buf := make([]byte, 64)
	for {
		// Hold the read lock for one read attempt. Use a short deadline
		// when supported so Pause() can grab the write lock promptly.
		in.pumpLock.RLock()
		if dl != nil {
			_ = dl.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		}
		n, err := in.reader.Read(buf)
		if dl != nil {
			_ = dl.SetReadDeadline(time.Time{})
		}
		in.pumpLock.RUnlock()

		for i := 0; i < n; i++ {
			in.bytes <- buf[i]
		}
		if err != nil {
			if isTimeout(err) {
				continue
			}
			in.mu.Lock()
			in.closed = true
			// Always store an error so callers of Next() see a non-nil
			// err when the connection drops. Without this, EOF would
			// produce (Key{}, false, nil) and the main loop -- which
			// only exits on err != nil -- would spin forever, holding
			// the BBS node open even after the user disconnected.
			if err == io.EOF {
				in.closedErr = io.EOF
			} else {
				in.closedErr = err
			}
			in.mu.Unlock()
			close(in.bytes)
			return
		}
	}
}

func isTimeout(err error) bool {
	type t interface{ Timeout() bool }
	if v, ok := err.(t); ok {
		return v.Timeout()
	}
	return false
}

// WithRawAccess pauses the input pump, runs fn with exclusive access to the
// underlying reader/writer, and then resumes. Use this for sub-protocols
// (file transfer, terminal queries) that need to read bytes directly
// without the pump munging them into Key events.
//
// While paused, any pending bytes that hadn't yet been decoded remain in
// in.pending and will be re-decoded on the next Next() call. Callers should
// drain or accept that those bytes belong to the prior session.
func (in *Input) WithRawAccess(rw io.ReadWriter, fn func() error) error {
	in.pumpLock.Lock()
	defer in.pumpLock.Unlock()
	// Drop pending bytes — they were typed before we paused but the upload
	// (or whatever) wants a fresh start.
	in.pending = nil
	// Drain any queued bytes in the channel.
	for {
		select {
		case <-in.bytes:
		default:
			return fn()
		}
	}
}

func (in *Input) isClosed() (bool, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.closed, in.closedErr
}

// Next returns the next decoded key, or (zero, false, nil) on timeout, or
// (zero, false, err) when the connection has closed and we've drained all
// pending bytes.
//
// IMPORTANT: drain before reading the closed flag. The pump goroutine pushes
// buffered bytes into in.bytes BEFORE marking closed=true and closing the
// channel. Reading the flag first would let us declare EOF while bytes are
// still sitting in the channel waiting to be decoded.
func (in *Input) Next(timeout time.Duration) (Key, bool, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	escTried := false // the flush after escWait found no key; wait for bytes

	for {
		// 1) Drain whatever's already in the channel (non-blocking).
		for {
			select {
			case b, ok := <-in.bytes:
				if !ok {
					// Channel is closed AND empty. Stop draining.
					goto decode
				}
				in.pending = append(in.pending, b)
				in.lastByte = time.Now()
				continue
			default:
			}
			break
		}
	decode:

		// 2) Now check the closed state.
		closed, closedErr := in.isClosed()

		// Bracketed paste: everything up to the end marker is one key.
		if in.pasteSince.IsZero() && bytes.HasPrefix(in.pending, []byte(pasteStart)) {
			in.pending = in.pending[len(pasteStart):]
			in.pasteSince = time.Now()
		}
		if !in.pasteSince.IsZero() {
			if k, ok := in.takePaste(closed); ok {
				return k, true, nil
			}
			select {
			case b, ok := <-in.bytes:
				if ok {
					in.pending = append(in.pending, b)
					in.lastByte = time.Now()
				}
				continue
			case <-deadline.C:
				return Key{}, false, nil
			}
		}

		// 3) Try to decode. When closed, force-flush so trailing bytes
		//    (like a bare ESC) deliver instead of waiting forever.
		if k, n, ok := tryDecodeMouse(in.pending, closed, in.x10.Load()); ok {
			in.pending = in.pending[n:]
			return k, true, nil
		}

		// 4) Closed and nothing left to decode → surface the close.
		if closed && len(in.pending) == 0 {
			return Key{}, false, closedErr
		}

		// 5) Block for one byte or the poll timeout. Once a lone ESC has
		//    waited escWait for a follow-up byte, give the decoder one last
		//    shot with escFlush=true.
		var escDone <-chan time.Time
		if len(in.pending) > 0 && in.pending[0] == 0x1B && !escTried {
			escDone = time.After(escWait - time.Since(in.lastByte))
		}
		select {
		case b, ok := <-in.bytes:
			if !ok {
				continue // back to drain → decode → maybe surface close
			}
			in.pending = append(in.pending, b)
			in.lastByte = time.Now()
			escTried = false
		case <-escDone:
			if k, n, ok := tryDecodeMouse(in.pending, true, in.x10.Load()); ok {
				in.pending = in.pending[n:]
				return k, true, nil
			}
			escTried = true
		case <-deadline.C:
			if len(in.pending) > 0 && in.pending[0] == 0x1B && time.Since(in.lastByte) < escWait {
				return Key{}, false, nil // the rest of a sequence may be on its way
			}
			if k, n, ok := tryDecodeMouse(in.pending, true, in.x10.Load()); ok {
				in.pending = in.pending[n:]
				return k, true, nil
			}
			return Key{}, false, nil
		}
	}
}

// takePaste delivers a bracketed paste once its end marker is in, or what
// has arrived when it runs too long, too large or the input closed.
func (in *Input) takePaste(closed bool) (Key, bool) {
	if i := bytes.Index(in.pending, []byte(pasteEnd)); i >= 0 {
		k := Key{Type: KeyPaste, Text: string(in.pending[:i])}
		in.pending = in.pending[i+len(pasteEnd):]
		in.pasteSince = time.Time{}
		return k, true
	}
	if closed || len(in.pending) >= pasteMax || time.Since(in.pasteSince) > pasteTimeout {
		k := Key{Type: KeyPaste, Text: string(in.pending)}
		in.pending = in.pending[:0]
		in.pasteSince = time.Time{}
		return k, true
	}
	return Key{}, false
}

// tryDecode looks at the head of buf and returns (Key, bytes-consumed, ok).
// If escFlush is true, a lone ESC at buf[0] is delivered as a bare KeyEsc;
// otherwise the decoder waits for more bytes (returns ok=false).
func tryDecode(buf []byte, escFlush bool) (Key, int, bool) {
	return tryDecodeMouse(buf, escFlush, false)
}

// tryDecodeMouse is tryDecode with the X10 mouse form switched on or off.
func tryDecodeMouse(buf []byte, escFlush, x10 bool) (Key, int, bool) {
	if x10 && len(buf) >= 3 && buf[0] == 0x1b && buf[1] == '[' && buf[2] == 'M' {
		if len(buf) < 6 {
			if escFlush {
				return Key{Type: KeyChar, Rune: 0}, len(buf), true
			}
			return Key{}, 0, false
		}
		cb := int(buf[3]) - 32
		k := Key{Type: KeyMouse, X: int(buf[4]) - 33, Y: int(buf[5]) - 33, Button: cb &^ (4 | 8 | 16 | 32)}
		if cb&3 == 3 && cb&64 == 0 {
			k.Release, k.Button = true, MouseLeft
		}
		if cb&32 != 0 { // motion; we don't ask for it
			return Key{Type: KeyChar, Rune: 0}, 6, true
		}
		return k, 6, true
	}
	if len(buf) == 0 {
		return Key{}, 0, false
	}
	b := buf[0]

	switch b {
	case 0x09:
		return Key{Type: KeyTab}, 1, true
	case 0x0A, 0x0D:
		return Key{Type: KeyEnter}, 1, true
	case 0x08, 0x7F:
		return Key{Type: KeyBackspace}, 1, true
	case 0x1B:
		return decodeEsc(buf, escFlush)
	}

	if b < 0x20 {
		return Key{Type: KeyChar, Rune: rune(b), Ctrl: true}, 1, true
	}
	return Key{Type: KeyChar, Rune: rune(b)}, 1, true
}

func decodeEsc(buf []byte, escFlush bool) (Key, int, bool) {
	if len(buf) == 1 {
		if escFlush {
			return Key{Type: KeyEsc}, 1, true
		}
		return Key{}, 0, false
	}
	switch buf[1] {
	case '[':
		return decodeCSI(buf)
	case 'O':
		return decodeSS3(buf)
	}
	// Alt-<char>: ESC followed by a single non-introducer byte. For now we
	// just consume the ESC and return the rune as-is (chat door doesn't bind
	// alt yet).
	return Key{Type: KeyChar, Rune: rune(buf[1])}, 2, true
}

// decodeCSI handles `ESC [ ... final` sequences.
func decodeCSI(buf []byte) (Key, int, bool) {
	if len(buf) < 3 {
		return Key{}, 0, false
	}
	end := 2
	for end < len(buf) {
		c := buf[end]
		if c >= 0x40 && c <= 0x7E {
			break
		}
		end++
	}
	if end >= len(buf) {
		return Key{}, 0, false
	}
	final := buf[end]
	params := string(buf[2:end])
	consumed := end + 1

	// SGR mouse report: ESC [ < b ; x ; y M (press) or m (release).
	if len(params) > 0 && params[0] == '<' && (final == 'M' || final == 'm') {
		if k, ok := parseSGRMouse(params[1:], final == 'm'); ok {
			return k, consumed, true
		}
		return Key{Type: KeyChar, Rune: 0}, consumed, true
	}

	switch final {
	case 'R':
		// Cursor Position Report: \x1b[<row>;<col>R. We send `\x1b[6n`
		// after parking the cursor at (999;999) so the terminal clamps
		// to its actual bottom-right; the response tells us the size.
		rows, cols := parsePos(params)
		if rows > 0 && cols > 0 {
			return Key{Type: KeyResize, Rows: rows, Cols: cols}, consumed, true
		}
		// Malformed -- swallow it.
		return Key{Type: KeyChar, Rune: 0}, consumed, true
	case 'A':
		return Key{Type: KeyUp}, consumed, true
	case 'B':
		return Key{Type: KeyDown}, consumed, true
	case 'C':
		return Key{Type: KeyRight, Ctrl: wordMod(params)}, consumed, true
	case 'D':
		return Key{Type: KeyLeft, Ctrl: wordMod(params)}, consumed, true
	case 'H':
		return Key{Type: KeyHome}, consumed, true
	case 'F':
		return Key{Type: KeyEnd}, consumed, true
	// SyncTERM in ANSI-BBS mode sends `\x1b[V` for PageUp and `\x1b[U`
	// for PageDown (different from the xterm `\x1b[5~` / `[6~` form).
	// Some other BBS terminals use `[I` / `[G` for the same. Accept all.
	case 'V', 'I':
		return Key{Type: KeyPgUp}, consumed, true
	case 'U', 'G':
		return Key{Type: KeyPgDn}, consumed, true
	case 'Z':
		return Key{Type: KeyTab, Ctrl: true}, consumed, true
	case '~':
		switch params {
		case "1", "7":
			return Key{Type: KeyHome}, consumed, true
		case "4", "8":
			return Key{Type: KeyEnd}, consumed, true
		case "2":
			return Key{Type: KeyInsert}, consumed, true
		case "3":
			return Key{Type: KeyDelete}, consumed, true
		case "5":
			return Key{Type: KeyPgUp}, consumed, true
		case "6":
			return Key{Type: KeyPgDn}, consumed, true
		case "11":
			return Key{Type: KeyF1}, consumed, true
		case "12":
			return Key{Type: KeyF2}, consumed, true
		case "13":
			return Key{Type: KeyF3}, consumed, true
		case "14":
			return Key{Type: KeyF4}, consumed, true
		case "15":
			return Key{Type: KeyF5}, consumed, true
		case "17":
			return Key{Type: KeyF6}, consumed, true
		case "18":
			return Key{Type: KeyF7}, consumed, true
		case "19":
			return Key{Type: KeyF8}, consumed, true
		case "20":
			return Key{Type: KeyF9}, consumed, true
		case "21":
			return Key{Type: KeyF10}, consumed, true
		case "23":
			return Key{Type: KeyF11}, consumed, true
		case "24":
			return Key{Type: KeyF12}, consumed, true
		}
	}
	// Unknown CSI sequence -- swallow it as a no-op KeyChar with rune 0
	// instead of synthesizing KeyEsc. Many terminals (macOS Terminal.app,
	// iTerm2, modern xterm) emit vendor-specific CSI sequences on startup
	// or in response to mode queries (bracketed-paste markers, terminal
	// status reports, etc.). Returning KeyEsc here makes any modal that
	// treats Esc as "cancel" -- the avatar selector, the editor, the
	// upload dialog -- close immediately when the terminal sends one of
	// those sequences. The user sees the modal flash and then a
	// "cancelled" message and has no idea why.
	return Key{Type: KeyChar, Rune: 0}, consumed, true
}

// decodeSS3 handles `ESC O X` sequences.
func decodeSS3(buf []byte) (Key, int, bool) {
	if len(buf) < 3 {
		return Key{}, 0, false
	}
	switch buf[2] {
	case 'A':
		return Key{Type: KeyUp}, 3, true
	case 'B':
		return Key{Type: KeyDown}, 3, true
	case 'C':
		return Key{Type: KeyRight}, 3, true
	case 'D':
		return Key{Type: KeyLeft}, 3, true
	case 'H':
		return Key{Type: KeyHome}, 3, true
	case 'F':
		return Key{Type: KeyEnd}, 3, true
	case 'P':
		return Key{Type: KeyF1}, 3, true
	case 'Q':
		return Key{Type: KeyF2}, 3, true
	case 'R':
		return Key{Type: KeyF3}, 3, true
	case 'S':
		return Key{Type: KeyF4}, 3, true
	}
	// Unknown SS3 — same no-op treatment as unknown CSI.
	return Key{Type: KeyChar, Rune: 0}, 3, true
}

// parsePos parses a CSI parameter string of the form "<rows>;<cols>" into
// integer rows and cols. Returns (0, 0) on any parse failure so the caller
// can ignore malformed reports.
func parsePos(s string) (rows, cols int) {
	semi := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			semi = i
			break
		}
	}
	if semi < 0 {
		return 0, 0
	}
	rows = atoiSafe(s[:semi])
	cols = atoiSafe(s[semi+1:])
	return rows, cols
}

// wordMod reports whether an arrow's xterm modifier parameter ("1;5" for
// Ctrl, "1;3" for Alt) asks for a word move; Key.Ctrl carries it.
func wordMod(params string) bool {
	return strings.HasSuffix(params, ";5") || strings.HasSuffix(params, ";3")
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0
		}
	}
	return n
}

// parseSGRMouse reads "b;x;y" (1-based cells). Motion reports (bit 32) are
// dropped, as are modifier bits.
func parseSGRMouse(p string, release bool) (Key, bool) {
	var v [3]int
	i := 0
	for _, part := range splitSemis(p) {
		if i == 3 {
			return Key{}, false
		}
		n := atoiSafe(part)
		if part == "" || (n == 0 && part != "0") {
			return Key{}, false
		}
		v[i] = n
		i++
	}
	if i != 3 || v[1] < 1 || v[2] < 1 || v[0]&32 != 0 {
		return Key{}, false
	}
	return Key{Type: KeyMouse, Button: v[0] &^ (4 | 8 | 16), X: v[1] - 1, Y: v[2] - 1, Release: release}, true
}

func splitSemis(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
