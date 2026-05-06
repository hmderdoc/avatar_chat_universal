package ansi

import (
	"errors"
	"io"
	"sync"
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
)

type Key struct {
	Type KeyType
	Rune rune // valid when Type == KeyChar (or for control chars)
	Ctrl bool
	// Cols / Rows are populated only when Type == KeyResize.
	Cols int
	Rows int
}

// Input decodes a byte stream from a terminal connection into Keys. It
// handles xterm/VT100 escape sequences for arrow keys, function keys,
// Home/End/PgUp/PgDn, Delete/Insert, and the SS3-prefixed variants.
//
// A bare ESC press is reported only after the poll timeout elapses with no
// follow-up byte, matching the convention used by avatar_chat.js:2554.
type Input struct {
	reader  io.Reader // kept so callers can grab raw access via Pause/Resume
	bytes   chan byte
	pending []byte

	mu        sync.Mutex
	closed    bool
	closedErr error

	// Pause/Resume coordination. The pump holds pumpLock as an RLock during
	// every Read; Pause grabs the WLock to guarantee exclusive access to
	// the underlying reader. The pump uses short read deadlines so the
	// RLock is released frequently enough for Pause to acquire the WLock
	// without long delay.
	pumpLock sync.RWMutex
}

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
			if errors.Is(err, io.EOF) {
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
				continue
			default:
			}
			break
		}
	decode:

		// 2) Now check the closed state.
		closed, closedErr := in.isClosed()

		// 3) Try to decode. When closed, force-flush so trailing bytes
		//    (like a bare ESC) deliver instead of waiting forever.
		if k, n, ok := tryDecode(in.pending, closed); ok {
			in.pending = in.pending[n:]
			return k, true, nil
		}

		// 4) Closed and nothing left to decode → surface the close.
		if closed && len(in.pending) == 0 {
			return Key{}, false, closedErr
		}

		// 5) Block for one byte or the poll timeout. After timeout, give the
		//    decoder one last shot with escFlush=true.
		select {
		case b, ok := <-in.bytes:
			if !ok {
				continue // back to drain → decode → maybe surface close
			}
			in.pending = append(in.pending, b)
		case <-deadline.C:
			if k, n, ok := tryDecode(in.pending, true); ok {
				in.pending = in.pending[n:]
				return k, true, nil
			}
			return Key{}, false, nil
		}
	}
}

// tryDecode looks at the head of buf and returns (Key, bytes-consumed, ok).
// If escFlush is true, a lone ESC at buf[0] is delivered as a bare KeyEsc;
// otherwise the decoder waits for more bytes (returns ok=false).
func tryDecode(buf []byte, escFlush bool) (Key, int, bool) {
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
		return Key{Type: KeyRight}, consumed, true
	case 'D':
		return Key{Type: KeyLeft}, consumed, true
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
	return Key{Type: KeyEsc}, consumed, true
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
