// Package termprobe asks the caller's terminal, in band, how many colors it
// can really show. It is for BBS doors and other programs that only have the
// terminal's own byte stream to go on: no dropfile field, no TERM variable, no
// help from the BBS. It has no dependencies and no platform code.
//
// Four standard queries go out in one burst and the replies are read back for
// a bounded time:
//
//	CSI > q            XTVERSION      -> DCS > | name version ST   (xterm family)
//	SGR 38;2;1;2;3 then DCS $ q m ST   DECRQSS SGR readback: the terminal
//	                                  echoes 38;2 (kept 24-bit) or 38;5 (quantized)
//	CSI ? 2 ; 1 S      XTSMGRAPHICS   -> CSI ? 2 ; 0 ; w ; h S    (sixel-capable
//	                                  terminals, including fTelnet)
//	CSI c              DA             -> CSI ? ... c, or CTerm's CSI = 67;84;101;114;109;maj;min;rev c
//	                                  (SyncTERM)
//
// The decision is deliberately conservative: a terminal that answers nothing
// gets Depth16. That is the right default on a BBS, where the common silent
// clients (NetRunner, the Linux console, most Windows telnet clients) are
// 16-color and 24-bit SGR turns into mush on them. Only a positive answer
// raises the depth.
//
// Sixel is decided from the same replies: an xterm-family DA1 that lists
// attribute 4, or, for CTerm/SyncTERM 1.207 and later, a CTerm Device
// Attributes reply (CSI < c) that lists pixel operations (4), the test
// Synchronet's cterm_lib.js supports_sixel() uses. When either kind of
// terminal is detected a short second burst asks for that CTDA and for the
// character cell's pixel size (XTWINOPS CSI 16 t / 14 t / 18 t), again ended
// early by a DA reply. A terminal that answers neither is not sent sixel.
//
// Call ColorDepth before any goroutine that reads terminal input is started,
// otherwise that goroutine will eat the replies as keystrokes. Bytes that
// arrive during the probe and are not part of a reply (a caller typing ahead)
// come back in Result.Leftover so nothing is lost.
package termprobe

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Depth is the number of colors it is safe to emit.
type Depth int

const (
	// Depth16 is the classic CGA/ANSI palette: SGR 30-37/40-47 plus bold.
	// It is also the answer when the terminal stays silent.
	Depth16 Depth = iota
	// Depth256 is the xterm 256-color palette (SGR 38;5;n / 48;5;n).
	Depth256
	// DepthTrue is 24-bit color (SGR 38;2;r;g;b / 48;2;r;g;b).
	DepthTrue
)

func (d Depth) String() string {
	switch d {
	case Depth256:
		return "256"
	case DepthTrue:
		return "truecolor"
	}
	return "16"
}

// TimedReader is the one thing the probe needs from the door's I/O layer: a
// read that gives up after d and reports the timeout as (0, nil). A read that
// returns an error ends the probe early.
//
// Spekder's Term already has this method. For a net.Conn (or anything with
// SetReadDeadline) use DeadlineReader; for a non-blocking file descriptor use
// PollReader.
type TimedReader interface {
	ReadTimeout(p []byte, d time.Duration) (int, error)
}

// Result is what the probe found.
type Result struct {
	// Depth is the decision.
	Depth Depth
	// Reason says which reply decided it, for the door's log.
	Reason string
	// Leftover holds every byte read during the probe that was not part of
	// a recognised reply, in arrival order. Hand it back to your input layer.
	Leftover []byte
	// Terminal is the XTVERSION name, e.g. "iTerm2 3.5.14", when answered.
	Terminal string
	// CTerm is the CTerm/SyncTERM version "major.minor" when the terminal
	// answered DA with the CTerm signature.
	CTerm string
	// Replies is the number of recognised replies, for diagnostics.
	Replies int
	// Raw is every byte read during the probe, replies and leftover alike,
	// in arrival order, for a diagnostic log. Decide leaves it empty.
	Raw []byte

	// Sixel reports that the terminal said it draws DEC sixel graphics;
	// SixelReason says which reply decided it either way.
	Sixel       bool
	SixelReason string
	// CellW / CellH are the character cell's size in pixels, when the
	// terminal reported it (0 otherwise). Only asked of sixel candidates.
	CellW, CellH int
}

// Queries is the exact byte burst ColorDepth writes, exported so a door can
// log it or send it through its own writer.
//
// The SGR set uses an odd colour nothing draws in, and resets immediately, so
// nothing visible changes. Device Attributes goes last because nearly every
// ANSI terminal answers it, and that answer ends the wait early.
const Queries = "\x1b[>q" +
	"\x1b[38;2;1;2;3m\x1bP$qm\x1b\\\x1b[0m" +
	"\x1b[?2;1S" +
	"\x1b[c"

// cellQueries asks for the cell size in pixels (CSI 16 t), the text area in
// pixels (CSI 14 t) and in characters (CSI 18 t). Sent only in the second
// burst, to terminals that look sixel-capable.
const cellQueries = "\x1b[16t\x1b[14t\x1b[18t"

// ctdaQuery is CTerm's own Device Attributes request; SyncTERM answers
// CSI < attrs c, where attribute 4 means pixel operations (sixel).
const ctdaQuery = "\x1b[<c"

// CTDA is asked of every CTerm, whatever version it reports. SyncTERM
// answers it from 1.207 (cterm_lib.js cterm_version_supports_ctda), but
// forks such as fTelnet_enhanced report an older revision and still answer
// it truthfully. A CTerm that ignores it costs nothing: the DA query after
// it ends the wait.

// ColorDepth writes the query burst to w, reads replies from r for at most
// timeout, and returns the decision. 300-500 ms is a sensible timeout for a
// BBS caller; the wait ends as soon as a Device Attributes reply arrives.
func ColorDepth(w io.Writer, r TimedReader, timeout time.Duration) Result {
	if _, err := io.WriteString(w, Queries); err != nil {
		return Result{Depth: Depth16, Reason: "could not send terminal queries: " + err.Error()}
	}
	buf := readUntilDA(r, timeout, nil)
	sig := scan(buf)
	if second := secondBurst(sig); second != "" {
		if _, err := io.WriteString(w, second); err == nil {
			wait := timeout
			if wait > 300*time.Millisecond {
				wait = 300 * time.Millisecond
			}
			buf = readUntilDA(r, wait, buf)
			sig = scan(buf)
		}
	}
	res := decide(sig, timeout)
	res.Raw = buf
	return res
}

// secondBurst is what to ask after the first replies: CTerm's CTDA (for
// any CTerm) and the cell size, for terminals that may draw sixel. "" when
// there is nothing worth asking.
func secondBurst(s signals) string {
	cterm := s.ctermOK
	if !cterm && !da1Sixel(s.da1) {
		return ""
	}
	q := cellQueries + "\x1b[c"
	if cterm {
		q = ctdaQuery + q
	}
	return q
}

// readUntilDA appends replies to buf until a Device Attributes reply that
// was not already in buf arrives, or timeout passes.
func readUntilDA(r TimedReader, timeout time.Duration, buf []byte) []byte {
	deadline := time.Now().Add(timeout)
	start := len(buf)
	scratch := make([]byte, 128)
	for time.Now().Before(deadline) && len(buf)-start < 1024 {
		rem := time.Until(deadline)
		if rem > 50*time.Millisecond {
			rem = 50 * time.Millisecond
		}
		n, err := r.ReadTimeout(scratch, rem)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			if scan(buf[start:]).daSeen {
				break
			}
		} else if err != nil {
			break
		}
	}
	return buf
}

// Decide is the pure decision over a captured reply buffer, exported for
// doors that collect the bytes themselves (and for tests).
func Decide(replies []byte, timeout time.Duration) Result {
	return decide(scan(replies), timeout)
}

type signals struct {
	leftover   []byte
	replies    int
	daSeen     bool
	ctermMajor int
	ctermMinor int
	ctermOK    bool
	terminal   string
	termSeen   bool
	rqss       string // the SGR text echoed by DECRQSS; "" when not answered
	rqssSeen   bool
	graphics   bool
	da1        string // params of a DEC DA1 reply (CSI ? params c)
	ctda       string // params of a CTerm CTDA reply (CSI < params c)
	ctdaSeen   bool
	cellW      int // CSI 6 ; h ; w t
	cellH      int
	areaW      int // CSI 4 ; h ; w t
	areaH      int
	rows       int // CSI 8 ; rows ; cols t
	cols       int
}

func decide(s signals, timeout time.Duration) Result {
	res := Result{Depth: Depth16, Leftover: s.leftover, Terminal: s.terminal, Replies: s.replies}
	if s.ctermOK {
		res.CTerm = strconv.Itoa(s.ctermMajor) + "." + strconv.Itoa(s.ctermMinor)
	}
	decideSixel(s, &res)

	// The DECRQSS echo is direct evidence of what the terminal did with a
	// 24-bit SGR, so it outranks a name. 38;5 means it quantized.
	switch {
	case strings.Contains(s.rqss, "38;2") || strings.Contains(s.rqss, "38:2"):
		res.Depth = DepthTrue
		res.Reason = "DECRQSS echoed the 24-bit SGR"
		return res
	case strings.Contains(s.rqss, "38;5") || strings.Contains(s.rqss, "38:5"):
		res.Depth = Depth256
		res.Reason = "DECRQSS echoed the 24-bit SGR quantized to 256 colors"
		return res
	}

	if s.ctermOK {
		if s.ctermMajor >= 1 {
			res.Depth = DepthTrue
			res.Reason = "CTerm " + res.CTerm + " (SyncTERM) answered Device Attributes"
		} else {
			res.Reason = "CTerm " + res.CTerm + " predates 24-bit color"
		}
		return res
	}

	if s.termSeen {
		if d, why := depthForTerminal(s.terminal); d > Depth16 {
			res.Depth = d
			res.Reason = why
			return res
		}
	}

	if s.graphics {
		res.Depth = DepthTrue
		res.Reason = "terminal reports sixel graphics geometry (XTSMGRAPHICS)"
		return res
	}

	if s.termSeen {
		res.Depth = Depth256
		res.Reason = "XTVERSION answered by an unlisted terminal: " + s.terminal
		return res
	}

	switch {
	case s.daSeen:
		res.Reason = "Device Attributes answered without a 24-bit signal"
	case s.rqssSeen:
		res.Reason = "DECRQSS answered without a color SGR"
	default:
		res.Reason = "no reply to XTVERSION, DECRQSS, XTSMGRAPHICS or DA within " + timeout.String()
	}
	return res
}

// decideSixel fills the sixel fields. CTerm is judged only by its CTDA
// (text-mode SyncTERM builds have no pixels whatever their version);
// everyone else by DA1 attribute 4.
func decideSixel(s signals, res *Result) {
	switch {
	case s.ctermOK && !s.ctdaSeen:
		res.SixelReason = "CTerm " + res.CTerm + " gave no CTDA reply"
	case s.ctermOK:
		if hasAttr(strings.Split(s.ctda, ";"), "4") {
			res.Sixel = true
			res.SixelReason = "CTerm " + res.CTerm + " CTDA lists pixel operations"
		} else {
			res.SixelReason = "CTerm " + res.CTerm + " CTDA lacks pixel operations: " + s.ctda
		}
	case da1Sixel(s.da1):
		res.Sixel = true
		res.SixelReason = "DA1 lists sixel: ?" + s.da1
	case s.da1 != "":
		res.SixelReason = "DA1 lacks sixel: ?" + s.da1
	default:
		res.SixelReason = "no DEC DA1 or CTDA reply"
	}
	if !res.Sixel {
		return
	}
	switch {
	case saneCell(s.cellW, s.cellH):
		res.CellW, res.CellH = s.cellW, s.cellH
	case s.rows > 0 && s.cols > 0 && saneCell(s.areaW/s.cols, s.areaH/s.rows):
		res.CellW, res.CellH = s.areaW/s.cols, s.areaH/s.rows
	}
}

// da1Sixel reports whether DA1 params list attribute 4. The first field is
// the conformance level (62, 64, 65...), not a feature.
func da1Sixel(params string) bool {
	if params == "" {
		return false
	}
	return hasAttr(strings.Split(params, ";")[1:], "4")
}

func hasAttr(attrs []string, want string) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

func saneCell(w, h int) bool { return w >= 4 && w <= 64 && h >= 6 && h <= 128 }

// depthForTerminal maps an XTVERSION name to a depth. Only terminals known
// to render 24-bit SGR are listed; everything else that answers XTVERSION
// is treated as 256-color by the caller.
func depthForTerminal(name string) (Depth, string) {
	n := strings.ToLower(name)
	for _, k := range []string{
		"iterm", "kitty", "wezterm", "foot", "ghostty", "contour", "alacritty",
		"mintty", "rio", "windows terminal", "warp", "konsole", "vte", "mlterm",
		"rxvt-unicode", "zutty", "tabby", "hyper", "terminology",
	} {
		if strings.Contains(n, k) {
			return DepthTrue, "known 24-bit terminal: " + name
		}
	}
	if strings.HasPrefix(n, "xterm(") {
		v, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(n, "xterm("), ")"))
		if v >= 331 {
			return DepthTrue, "XTerm patch " + strconv.Itoa(v) + " renders 24-bit"
		}
		return Depth256, "XTerm patch " + strconv.Itoa(v) + " predates 24-bit"
	}
	return Depth16, ""
}

// scan walks buf once, pulling out every recognised reply and leaving the
// rest in leftover.
func scan(buf []byte) signals {
	var s signals
	i := 0
	for i < len(buf) {
		if buf[i] != 0x1b {
			s.leftover = append(s.leftover, buf[i])
			i++
			continue
		}
		if end, ok := matchDCS(buf, i, &s); ok {
			i = end
			continue
		}
		if end, ok := matchCSI(buf, i, &s); ok {
			i = end
			continue
		}
		// A lone ESC, or a sequence we do not recognise: keep it for the door.
		s.leftover = append(s.leftover, buf[i])
		i++
	}
	return s
}

// matchDCS recognises  ESC P > | text ST   (XTVERSION)  and
// ESC P 1 $ r sgr m ST / ESC P 0 $ r ST     (DECRQSS). ST is ESC \ or BEL.
func matchDCS(buf []byte, i int, s *signals) (int, bool) {
	if i+1 >= len(buf) || buf[i+1] != 'P' {
		return 0, false
	}
	body, end, ok := dcsBody(buf, i+2)
	if !ok {
		return 0, false
	}
	switch {
	case strings.HasPrefix(body, ">|"):
		s.terminal = strings.TrimSpace(body[2:])
		s.termSeen = true
		s.replies++
		return end, true
	case strings.HasPrefix(body, "1$r"), strings.HasPrefix(body, "0$r"):
		s.rqss = body[3:]
		s.rqssSeen = true
		s.replies++
		return end, true
	}
	return 0, false
}

// dcsBody returns the text between ESC P and the string terminator.
func dcsBody(buf []byte, from int) (string, int, bool) {
	for j := from; j < len(buf) && j-from < 200; j++ {
		if buf[j] == 0x07 {
			return string(buf[from:j]), j + 1, true
		}
		if buf[j] == 0x1b && j+1 < len(buf) && buf[j+1] == '\\' {
			return string(buf[from:j]), j + 2, true
		}
	}
	return "", 0, false
}

// matchCSI recognises  ESC [ ? params c   /  ESC [ = params c  (DA) and
// ESC [ ? 2 ; params S  (XTSMGRAPHICS).
func matchCSI(buf []byte, i int, s *signals) (int, bool) {
	if i+1 >= len(buf) || buf[i+1] != '[' {
		return 0, false
	}
	j := i + 2
	prefix := byte(0)
	if j < len(buf) && (buf[j] == '?' || buf[j] == '=' || buf[j] == '>' || buf[j] == '<') {
		prefix = buf[j]
		j++
	}
	start := j
	for j < len(buf) && ((buf[j] >= '0' && buf[j] <= '9') || buf[j] == ';') {
		j++
	}
	if j >= len(buf) {
		return 0, false
	}
	params := string(buf[start:j])
	final := buf[j]
	switch {
	case final == 'c' && (prefix == '?' || prefix == '=' || prefix == 0):
		s.daSeen = true
		s.replies++
		switch prefix {
		case '=':
			parseCTerm(params, s)
		case '?':
			s.da1 = params
		}
		return j + 1, true
	case final == 'c' && prefix == '<':
		// CTerm Device Attributes; not a barrier, the DA after it is.
		s.ctda = params
		s.ctdaSeen = true
		s.replies++
		return j + 1, true
	case final == 't' && prefix == 0:
		f := strings.Split(params, ";")
		if len(f) != 3 {
			return 0, false
		}
		a, _ := strconv.Atoi(f[1])
		b, _ := strconv.Atoi(f[2])
		switch f[0] {
		case "6":
			s.cellH, s.cellW = a, b
		case "4":
			s.areaH, s.areaW = a, b
		case "8":
			s.rows, s.cols = a, b
		default:
			return 0, false
		}
		s.replies++
		return j + 1, true
	case final == 'S' && prefix == '?' && strings.HasPrefix(params, "2;"):
		// CSI ? 2 ; 0 ; w ; h S  — status 0 means "answered".
		s.graphics = true
		s.replies++
		return j + 1, true
	}
	return 0, false
}

// parseCTerm reads CTerm's DA: 67;84;101;114;109 spells "CTerm", then
// major;minor;revision.
func parseCTerm(params string, s *signals) {
	const sig = "67;84;101;114;109"
	if !strings.HasPrefix(params, sig) {
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(params, sig), ";")
	parts := strings.Split(rest, ";")
	if len(parts) < 2 {
		return
	}
	maj, e1 := strconv.Atoi(parts[0])
	min, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return
	}
	s.ctermMajor, s.ctermMinor, s.ctermOK = maj, min, true
}

// String renders the result on one line for a log.
func (r Result) String() string {
	sixel := "no sixel"
	if r.Sixel {
		sixel = "sixel"
		if r.CellW > 0 {
			sixel += fmt.Sprintf(" %dx%d px cells", r.CellW, r.CellH)
		}
	}
	return fmt.Sprintf("%s (%s; %s; %d replies, %d leftover bytes)", r.Depth, r.Reason, sixel, r.Replies, len(r.Leftover))
}
