package upload

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"time"
)

// traceWriter is set when AVATAR_CHAT_ZMODEM_TRACE=<path> is in the env at
// the time ReceiveZMODEM is called. Every byte read from or written to the
// connection is appended in a human-readable form. This is a debug aid for
// terminal-specific issues (SyncTERM, NetRunner etc.); zero overhead when
// the env var is unset.
var (
	traceMu sync.Mutex
	traceFP *os.File
)

func openTrace() {
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceFP != nil {
		return
	}
	path := os.Getenv("AVATAR_CHAT_ZMODEM_TRACE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	traceFP = f
	fmt.Fprintf(traceFP, "\n--- zmodem session start %s ---\n", time.Now().Format(time.RFC3339))
}

func tracef(direction string, data []byte) {
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceFP == nil {
		return
	}
	fmt.Fprintf(traceFP, "%s [%d]:", direction, len(data))
	for _, b := range data {
		if b >= 0x20 && b < 0x7F {
			fmt.Fprintf(traceFP, " %02x(%c)", b, b)
		} else {
			fmt.Fprintf(traceFP, " %02x", b)
		}
	}
	fmt.Fprintln(traceFP)
	_ = traceFP.Sync()
}

// ZMODEM-CRC32 receiver, sufficient to accept a single file from SyncTERM,
// NetRunner, mtelnet, fTelnet, and lrzsz `sz`. Sends ZRINIT to wake the
// remote sender, processes ZFILE/ZDATA/ZEOF/ZFIN, returns the file payload.
//
// References:
//   - Chuck Forsberg, "ZMODEM Protocol Reference" (zmodem.txt)
//   - lrzsz/zmodem.h for byte values
//
// What we DO support: CRC32, single file, ZDLE-escaped binary frames,
// hex headers (ZHEX), binary-32 headers (ZBIN32), ZDLE-end markers (h/i/j/k).
// What we DON'T: resume offsets, file mode/timestamp, multi-file batches,
// security challenges, compression — none of which an avatar upload needs.

const (
	zpad = '*'
	zdle = 0x18

	// Format markers placed after `ZPAD ZPAD ZDLE`.
	zbin   = 'A'
	zbin32 = 'C'
	zhex   = 'B'

	// Frame types.
	zrqinit  = 0
	zrinit   = 1
	zsinit   = 2
	zack     = 3
	zfile    = 4
	zskip    = 5
	znak     = 6
	zabort   = 7
	zfin     = 8
	zrpos    = 9
	zdata    = 10
	zeof     = 11
	zferr    = 12
	zcrc     = 13
	zcompl   = 15
	zcanType = 16

	// ZDLE-end-of-subpacket codes (immediately after ZDLE).
	zcrce = 'h' // end of frame, header packet follows
	zcrcg = 'i' // frame continues nonstop
	zcrcq = 'j' // frame continues, ACK expected
	zcrcw = 'k' // ACK expected, end of frame
	zrub0 = 'l' // rubout filler 0x7F
	zrub1 = 'm' // rubout filler 0xFF

	// ZRINIT capability bits we advertise (bits 0..7 in flag byte 0).
	canfdx = 0x01
	canov  = 0x02
	canbrk = 0x04
	can32  = 0x20
	canfc32 = 0x20
)

// crc32Table is the IEEE polynomial table; matches Zmodem's CRC32.
var crc32Table = crc32.MakeTable(crc32.IEEE)

// zCRC32 computes Zmodem's CRC32 over data: init=0xFFFFFFFF, polynomial
// IEEE, complement at end. crc32.Update with the IEEE table does
// init=0xFFFFFFFF and ~result, matching the standard.
func zCRC32(data []byte) uint32 {
	return crc32.Checksum(data, crc32Table)
}

// ReceiveZMODEM negotiates and receives a single file via Zmodem. Returns
// the file bytes (possibly truncated to maxBytes) and the offered filename.
// The caller's conn must support deadlines.
//
// Set AVATAR_CHAT_ZMODEM_TRACE=/path/to/log.txt before launching the door
// to capture every byte to/from the connection during the upload. Useful
// when a specific terminal (SyncTERM, NetRunner, ...) misbehaves.
func ReceiveZMODEM(conn ConnIO, maxBytes int) ([]byte, string, error) {
	openTrace()
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	conn = &tracingConn{ConnIO: conn}
	r := newZmodemReader(conn)

	// 1) Send the canonical "rz\r" wake-up + an initial ZRINIT. Some
	// senders look for the literal "rz\r" string; others look for the
	// ZPAD-ZPAD-ZDLE sequence in our ZRINIT. We send both so SyncTERM,
	// fTelnet, NetRunner, and lrzsz all auto-start.
	if _, err := conn.Write([]byte("rz\r")); err != nil {
		return nil, "", err
	}
	if err := r.sendHex(zrinit, [4]byte{0, 0, 0, canfdx | canov | can32}); err != nil {
		return nil, "", err
	}

	// 2) Wait for ZFILE. Some senders begin with ZRQINIT, then ZSINIT,
	// then ZFILE. We send ZRINIT once at start (above) and then quietly
	// wait. We do NOT retransmit ZRINIT periodically: terminals like
	// SyncTERM buffer pending bytes while their file picker is up, so
	// extra ZRINITs sit in their input buffer; if the user then hits
	// Esc to abort, SyncTERM returns to terminal mode, processes the
	// buffered ZRINIT, auto-detects Zmodem again, and re-opens its
	// picker -- making it impossible to actually cancel. TCP-tunneled
	// connections (every modern BBS) don't lose bytes, so the retry
	// hurts and never helps.
	//
	// While waiting we also accept a bare Esc keypress as user-cancel.
	// SyncTERM's auto-launched file picker doesn't always send 5x CAN
	// when the user hits Esc to dismiss it without selecting a file --
	// it just closes the picker and hands keystrokes back to terminal
	// mode. If the user then hits Esc to back out of our hint screen,
	// that 0x1B byte arrives here. We treat a lone 0x1B (no follow-up
	// within 100ms, so not a CSI/SS3 sequence) as cancel.
	r.escCancelOK = true
	defer func() { r.escCancelOK = false }()
	var filename string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		typ, _, err := r.recvHeader(2 * time.Second)
		if err != nil {
			if errors.Is(err, errTimeout) {
				// Just check the deadline and keep waiting.
				continue
			}
			if errors.Is(err, errCancelled) {
				r.sendCancel(250 * time.Millisecond)
				return nil, "", fmt.Errorf("upload cancelled")
			}
			return nil, "", fmt.Errorf("zmodem: waiting for ZFILE: %w", err)
		}
		switch typ {
		case zrqinit:
			// Sender wake-up. Reply with ZRINIT.
			if err := r.sendHex(zrinit, [4]byte{0, 0, 0, canfdx | canov | can32}); err != nil {
				return nil, "", err
			}
		case zsinit:
			// Sender info; subpacket follows. Drain it.
			if _, _, err := r.recvSubpacket(0, 4*time.Second); err != nil {
				return nil, "", fmt.Errorf("zmodem: drain ZSINIT: %w", err)
			}
			// ACK with ZACK.
			if err := r.sendHex(zack, [4]byte{0, 0, 0, 0}); err != nil {
				return nil, "", err
			}
		case zfile:
			// File info subpacket follows.
			info, _, err := r.recvSubpacket(0, 4*time.Second)
			if err != nil {
				return nil, "", fmt.Errorf("zmodem: file info: %w", err)
			}
			if i := bytes.IndexByte(info, 0); i >= 0 {
				filename = string(info[:i])
			} else {
				filename = string(info)
			}
			goto have_file
		case zfin:
			return nil, "", fmt.Errorf("zmodem: sender sent ZFIN before any file")
		case zcanType:
			return nil, "", fmt.Errorf("zmodem: sender cancelled")
		}
	}
	return nil, "", fmt.Errorf("upload timed out (no file received within 30s)")

have_file:
	// 3) Send ZRPOS 0 to start fresh.
	if err := r.sendHex(zrpos, [4]byte{0, 0, 0, 0}); err != nil {
		return nil, "", err
	}

	// 4) Read ZDATA frames + subpackets until ZEOF.
	var payload []byte
	for {
		typ, flags, err := r.recvHeader(8 * time.Second)
		if err != nil {
			if errors.Is(err, errCancelled) {
				r.sendCancel(250 * time.Millisecond)
				return nil, filename, fmt.Errorf("upload cancelled")
			}
			return nil, filename, fmt.Errorf("zmodem: waiting for ZDATA/ZEOF: %w", err)
		}
		switch typ {
		case zdata:
			// flags carry the offset (little-endian); we don't care for
			// our 120-byte case, but acknowledge it cleanly anyway.
			_ = flags
			done, err := r.readSubpackets(&payload, maxBytes, 8*time.Second)
			if err != nil {
				return nil, filename, fmt.Errorf("zmodem: read data: %w", err)
			}
			if done {
				// Frame ended via ZCRCE/ZCRCW; sender will send another
				// header next (typically ZEOF).
			}
		case zeof:
			// Acknowledge by sending ZRINIT (asks for next file).
			if err := r.sendHex(zrinit, [4]byte{0, 0, 0, canfdx | canov | can32}); err != nil {
				return nil, filename, err
			}
		case zfin:
			// Send ZFIN back, then "OO" to terminate.
			_ = r.sendHex(zfin, [4]byte{0, 0, 0, 0})
			_, _ = conn.Write([]byte{'O', 'O'})
			if maxBytes > 0 && len(payload) > maxBytes {
				payload = payload[:maxBytes]
			}
			return payload, filename, nil
		case zcanType:
			return nil, filename, fmt.Errorf("zmodem: sender cancelled")
		case zabort, zferr:
			return nil, filename, fmt.Errorf("zmodem: sender aborted (type %d)", typ)
		case zskip:
			return nil, filename, fmt.Errorf("zmodem: sender skipped file")
		default:
			// Unknown frame: re-NAK with ZRPOS at current size.
			if err := r.sendHex(zrpos, [4]byte{0, 0, 0, 0}); err != nil {
				return nil, filename, err
			}
		}
	}
}

// tracingConn wraps a ConnIO so every byte in/out is forwarded to the
// trace log when one is configured. Pass-through when no trace file.
type tracingConn struct {
	ConnIO
}

func (t *tracingConn) Read(p []byte) (int, error) {
	n, err := t.ConnIO.Read(p)
	if n > 0 && traceFP != nil {
		tracef("R", p[:n])
	}
	return n, err
}

func (t *tracingConn) Write(p []byte) (int, error) {
	if traceFP != nil {
		tracef("W", p)
	}
	return t.ConnIO.Write(p)
}

// errCancelled is returned when 5 consecutive 0x18 bytes (ZCAN) appear in
// the byte stream -- the canonical Zmodem cancel signal that SyncTERM, lrzsz,
// and friends emit when the user presses Esc on the sender side.
var errCancelled = errors.New("zmodem: cancelled")

// cancelSeq is the canonical Zmodem cancel: 5 CAN bytes guarantee the
// remote sees the cancel even if the line has 1-byte CAN noise; 8 BS
// (backspace) bytes erase the "cancelled" prompt that the remote
// typically prints. Per Forsberg, this is what every Zmodem
// implementation should send on user-abort.
var cancelSeq = []byte{
	zdle, zdle, zdle, zdle, zdle, zdle, zdle, zdle,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
}

// sendCancel writes the canonical Zmodem cancel sequence and then drains
// up to drainFor of trailing bytes the sender may still be flushing
// (status text, ZRQINIT retries, etc.). Drained bytes are discarded so
// they don't leak into the next protocol layer.
func (r *zmodemReader) sendCancel(drainFor time.Duration) {
	_, _ = r.conn.Write(cancelSeq)
	deadline := time.Now().Add(drainFor)
	for time.Now().Before(deadline) {
		// Read with a short timeout; ignore both the byte and any error.
		// The CAN counter is irrelevant here -- we're just clearing the
		// pipe before returning to chat.
		_, _ = readByte(r.conn, 100*time.Millisecond)
	}
	r.cans = 0
}

// zmodemReader holds incremental decode state for the ZDLE-escaped wire.
type zmodemReader struct {
	conn ConnIO
	buf  []byte // tiny look-ahead buffer
	cans int    // consecutive 0x18 byte count for cancel detection

	// escCancelOK is set true during the initial ZFILE-wait phase: while
	// it's set, a lone 0x1B (Esc) byte in the resync loop is treated as
	// a user cancel. After ZFILE arrives we clear it because 0x1B can
	// appear inside binary data frames.
	escCancelOK bool
}

func newZmodemReader(c ConnIO) *zmodemReader {
	return &zmodemReader{conn: c}
}

// readByte reads one raw byte (no ZDLE handling). Honors timeout. Tracks
// consecutive 0x18 bytes; if 5 arrive in a row it returns errCancelled so
// the caller can clean up instead of waiting for the protocol to time out.
func (r *zmodemReader) readByte(timeout time.Duration) (byte, error) {
	if len(r.buf) > 0 {
		b := r.buf[0]
		r.buf = r.buf[1:]
		if b == zdle {
			r.cans++
		} else {
			r.cans = 0
		}
		if r.cans >= 5 {
			return 0, errCancelled
		}
		return b, nil
	}
	b, err := readByte(r.conn, timeout)
	if errors.Is(err, errTimeout) {
		return 0, errTimeout
	}
	if err == nil {
		if b == zdle {
			r.cans++
		} else {
			r.cans = 0
		}
		if r.cans >= 5 {
			return 0, errCancelled
		}
	}
	return b, err
}

// recvHeader scans for `ZPAD [ZPAD] ZDLE <fmt>` and decodes the header.
// Returns frame type, the 4-byte flags/data, and any error.
func (r *zmodemReader) recvHeader(timeout time.Duration) (byte, [4]byte, error) {
	deadline := time.Now().Add(timeout)
	// Sync to ZPAD ZDLE prefix. Senders may send single or double ZPAD;
	// we accept either.
	for time.Now().Before(deadline) {
		b, err := r.readByte(timeout)
		if err != nil {
			return 0, [4]byte{}, err
		}
		if r.escCancelOK && b == 0x1B {
			// Possible user cancel. Peek for a follow-up byte: a real
			// CSI (`\x1b[`) or SS3 (`\x1bO`) will arrive within a few
			// ms. A lone Esc with no follow-up is the user pressing
			// the cancel key.
			next, perr := r.readByte(100 * time.Millisecond)
			if errors.Is(perr, errTimeout) {
				return 0, [4]byte{}, errCancelled
			}
			if perr != nil {
				return 0, [4]byte{}, perr
			}
			// Not a lone Esc -- push the lookahead back and resume.
			r.buf = append([]byte{next}, r.buf...)
			continue
		}
		if b != zpad {
			continue
		}
		// Possibly a second ZPAD.
		nxt, err := r.readByte(2 * time.Second)
		if err != nil {
			return 0, [4]byte{}, err
		}
		if nxt == zpad {
			nxt, err = r.readByte(2 * time.Second)
			if err != nil {
				return 0, [4]byte{}, err
			}
		}
		if nxt != zdle {
			continue
		}
		fmtByte, err := r.readByte(2 * time.Second)
		if err != nil {
			return 0, [4]byte{}, err
		}
		switch fmtByte {
		case zhex:
			return r.recvHexHeader(timeout)
		case zbin:
			return r.recvBin16Header(timeout)
		case zbin32:
			return r.recvBin32Header(timeout)
		default:
			// Junk; resync.
			continue
		}
	}
	return 0, [4]byte{}, fmt.Errorf("zmodem: header timeout")
}

func (r *zmodemReader) recvHexHeader(timeout time.Duration) (byte, [4]byte, error) {
	hexBuf := make([]byte, 14) // type(1) + flags(4) + crc(2) = 7 bytes => 14 hex chars
	for i := 0; i < 14; i++ {
		b, err := r.readByte(timeout)
		if err != nil {
			return 0, [4]byte{}, err
		}
		hexBuf[i] = b
	}
	dec := make([]byte, 7)
	for i := 0; i < 7; i++ {
		hi := hexNibble(hexBuf[i*2])
		lo := hexNibble(hexBuf[i*2+1])
		if hi == 0xff || lo == 0xff {
			return 0, [4]byte{}, fmt.Errorf("zmodem: bad hex header")
		}
		dec[i] = hi<<4 | lo
	}
	// Trailing CR/LF/XON; consume up to 3 bytes politely.
	for i := 0; i < 3; i++ {
		b, err := r.readByte(500 * time.Millisecond)
		if err != nil {
			break
		}
		if b != '\r' && b != '\n' && b != 0x11 && b != 0x8a {
			r.buf = append([]byte{b}, r.buf...)
			break
		}
	}
	typ := dec[0]
	var flags [4]byte
	copy(flags[:], dec[1:5])
	// CRC16 verify.
	want := uint16(dec[5])<<8 | uint16(dec[6])
	got := crc16(dec[:5])
	if want != got {
		return 0, [4]byte{}, fmt.Errorf("zmodem: hex CRC mismatch: got %04x want %04x", got, want)
	}
	return typ, flags, nil
}

func (r *zmodemReader) recvBin16Header(timeout time.Duration) (byte, [4]byte, error) {
	dec := make([]byte, 5+2) // type + 4 flags + CRC16
	for i := 0; i < len(dec); i++ {
		b, err := r.readEscaped(timeout)
		if err != nil {
			return 0, [4]byte{}, err
		}
		dec[i] = b
	}
	typ := dec[0]
	var flags [4]byte
	copy(flags[:], dec[1:5])
	want := uint16(dec[5])<<8 | uint16(dec[6])
	got := crc16(dec[:5])
	if want != got {
		return 0, [4]byte{}, fmt.Errorf("zmodem: bin16 CRC mismatch")
	}
	return typ, flags, nil
}

func (r *zmodemReader) recvBin32Header(timeout time.Duration) (byte, [4]byte, error) {
	dec := make([]byte, 5+4)
	for i := 0; i < len(dec); i++ {
		b, err := r.readEscaped(timeout)
		if err != nil {
			return 0, [4]byte{}, err
		}
		dec[i] = b
	}
	typ := dec[0]
	var flags [4]byte
	copy(flags[:], dec[1:5])
	wantBytes := dec[5:9]
	want := uint32(wantBytes[0]) | uint32(wantBytes[1])<<8 | uint32(wantBytes[2])<<16 | uint32(wantBytes[3])<<24
	got := zCRC32(dec[:5])
	if want != got {
		return 0, [4]byte{}, fmt.Errorf("zmodem: bin32 CRC mismatch")
	}
	return typ, flags, nil
}

// readEscaped reads one logical byte from the wire, applying ZDLE
// dequoting. Returns errSubpacketEnd with the end-marker code as the byte
// when a ZDLE+e/g/q/w sequence is encountered (caller distinguishes).
type errSubpacketEnd struct {
	code byte
}

func (e *errSubpacketEnd) Error() string { return fmt.Sprintf("subpacket-end %c", e.code) }

func (r *zmodemReader) readEscaped(timeout time.Duration) (byte, error) {
	for {
		b, err := r.readByte(timeout)
		if err != nil {
			return 0, err
		}
		if b != zdle {
			return b, nil
		}
		nxt, err := r.readByte(timeout)
		if err != nil {
			return 0, err
		}
		switch nxt {
		case zcrce, zcrcg, zcrcq, zcrcw:
			return 0, &errSubpacketEnd{code: nxt}
		case zrub0:
			return 0x7F, nil
		case zrub1:
			return 0xFF, nil
		case zdle: // very rare: literal ZDLE
			return zdle, nil
		default:
			return nxt ^ 0x40, nil
		}
	}
}

// recvSubpacket reads bytes until a ZDLE end-marker, verifies the CRC32
// (or CRC16 if explicitly requested), and returns the decoded payload
// plus the end-marker code (e/g/q/w).
func (r *zmodemReader) recvSubpacket(_ int, timeout time.Duration) ([]byte, byte, error) {
	var data []byte
	for {
		b, err := r.readEscaped(timeout)
		if err != nil {
			var spe *errSubpacketEnd
			if errors.As(err, &spe) {
				// Read CRC32 (4 bytes), include the code byte itself in
				// the CRC input as Zmodem requires.
				crcInput := append(append([]byte(nil), data...), spe.code)
				var crc4 [4]byte
				for i := 0; i < 4; i++ {
					cb, cerr := r.readEscaped(timeout)
					if cerr != nil {
						return nil, spe.code, cerr
					}
					crc4[i] = cb
				}
				want := uint32(crc4[0]) | uint32(crc4[1])<<8 | uint32(crc4[2])<<16 | uint32(crc4[3])<<24
				got := zCRC32(crcInput)
				if want != got {
					return nil, spe.code, fmt.Errorf("zmodem: subpacket CRC mismatch")
				}
				return data, spe.code, nil
			}
			return nil, 0, err
		}
		data = append(data, b)
		if len(data) > 1<<20 {
			return nil, 0, fmt.Errorf("zmodem: subpacket too large")
		}
	}
}

// readSubpackets consumes ZDATA subpackets, accumulating into payload.
// Returns true when the frame ended cleanly (ZCRCE/ZCRCW), false if it
// continued mid-frame. The ZACK position is the cumulative byte count
// received so far -- senders verify this matches their own count before
// proceeding, so a hardcoded 0 here can cause an abort.
func (r *zmodemReader) readSubpackets(payload *[]byte, maxBytes int, timeout time.Duration) (bool, error) {
	for {
		data, end, err := r.recvSubpacket(0, timeout)
		if err != nil {
			return false, err
		}
		*payload = append(*payload, data...)
		if maxBytes > 0 && len(*payload) > maxBytes*4 {
			return false, fmt.Errorf("zmodem: payload exceeds %d bytes", maxBytes*4)
		}
		switch end {
		case zcrce:
			return true, nil
		case zcrcg:
			// Continue; sender keeps sending without ACK.
			continue
		case zcrcq:
			// ACK and continue.
			if err := r.sendHex(zack, posLE(len(*payload))); err != nil {
				return false, err
			}
		case zcrcw:
			// ACK and end.
			if err := r.sendHex(zack, posLE(len(*payload))); err != nil {
				return false, err
			}
			return true, nil
		}
	}
}

// posLE encodes a byte position as a 4-byte little-endian flags slot.
func posLE(n int) [4]byte {
	return [4]byte{
		byte(n),
		byte(n >> 8),
		byte(n >> 16),
		byte(n >> 24),
	}
}

// sendHex writes a ZHEX header frame: ZPAD ZPAD ZDLE 'B' <14 hex chars>
// CR LF XON. Senders treat this as a perfectly valid header.
func (r *zmodemReader) sendHex(typ byte, flags [4]byte) error {
	var raw [7]byte
	raw[0] = typ
	copy(raw[1:5], flags[:])
	c := crc16(raw[:5])
	raw[5] = byte(c >> 8)
	raw[6] = byte(c & 0xFF)

	out := make([]byte, 0, 4+14+3)
	out = append(out, zpad, zpad, zdle, zhex)
	for _, b := range raw {
		out = append(out, hexChar(b>>4), hexChar(b&0x0F))
	}
	// lrzsz convention: \r \x8A then XON (unless ZACK/ZFIN, but we don't
	// hit that often enough to special-case). The 0x8A is "high-bit
	// linefeed" which several BBS terminals expect verbatim.
	out = append(out, '\r', 0x8A)
	if typ != zack && typ != zfin {
		out = append(out, 0x11)
	}
	_, err := r.conn.Write(out)
	return err
}

func hexChar(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' + (n - 10)
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0xff
}

// crc16 implements CRC-16/XMODEM (poly 0x1021, init 0). Same as Zmodem
// uses for ZHEX/ZBIN frames.
func crc16(data []byte) uint16 {
	var c uint16
	for _, b := range data {
		c ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if c&0x8000 != 0 {
				c = (c << 1) ^ 0x1021
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// helper used by tests; not exported.
var _ = io.EOF
