package bitmap

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"strconv"
	"strings"
)

// MaxHeight is the tallest image the envelope can carry: the payload's
// first byte holds the height.
const MaxHeight = 255

// xtermFromCGA maps a CGA/IBM colour index (1=blue, 4=red, the order ansi.Attr
// uses) to the xterm/ANSI index the wire carries (1=red, 4=blue). It swaps
// 1<->4 and 3<->6 and their bright pairs, so it is its own inverse; 16..255
// pass through. Same table as the web chat's cgaToXterm and Moebius's
// PAL_SWAP.
var xtermFromCGA = [16]uint8{0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15}

// WireColor converts a CGA index to the wire's xterm index (and back).
func WireColor(cga uint8) uint8 {
	if cga < 16 {
		return xtermFromCGA[cga]
	}
	return cga
}

// SanitizeFrom keeps a sender name to the characters every client accepts
// in the pipe-delimited envelope, as Moebius's sanitize_from does.
func SanitizeFrom(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			b.WriteRune(r)
			if b.Len() == 32 {
				break
			}
		}
	}
	return b.String()
}

// Encode builds a [BITMAP|w|h|from|hex] envelope from cells, row-major,
// whose FG and BG are already wire (xterm) indices, the form Parse returns.
// It panics if len(cells) != width*height, which is a caller bug; callers
// keep height <= MaxHeight.
func Encode(width, height int, from string, cells []Cell) string {
	n := width * height
	if len(cells) != n || width < 1 || height < 1 || height > MaxHeight {
		panic("bitmap.Encode: bad dimensions")
	}
	raw := make([]byte, 1+3*n)
	raw[0] = byte(height)
	for i, c := range cells {
		raw[1+i] = c.FG
		raw[1+n+i] = c.BG
		ch := c.Char
		if ch == 0 {
			ch = ' '
		}
		raw[1+2*n+i] = ch
	}
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return prefix + strconv.Itoa(width) + "|" + strconv.Itoa(height) + "|" + SanitizeFrom(from) + "|" + hex.EncodeToString(buf.Bytes()) + "]"
}
