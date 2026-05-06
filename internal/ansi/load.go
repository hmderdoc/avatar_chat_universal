package ansi

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LoadFile reads a SAUCE-tagged ANSI or BIN graphic from disk and returns
// it as a Frame at (0, 0) sized to the artwork's natural dimensions. The
// caller can reposition it before rendering with frame.X / frame.Y. ANSI
// SGR/cursor sequences are parsed into per-cell (char, attr) pairs so the
// returned Frame can be composited and rendered via Frame.Render -- the
// same path the chat UI uses, with proper CP437->UTF-8 conversion.
//
// File-type detection: SAUCE record tags wins; otherwise extension. Plain
// .txt and .asc files load via the ANSI path with a black-on-lightgray
// default attribute.
func LoadFile(path string, data []byte) (*Frame, error) {
	body, sauce := parseSauce(data)
	dataType, fileType := byte(0), byte(0)
	width, height := 0, 0
	if sauce != nil {
		dataType = sauce.DataType
		fileType = sauce.FileType
		width = sauce.TInfo1
		height = sauce.TInfo2
	}

	ext := strings.ToLower(filepath.Ext(path))

	// Resolve the kind. SAUCE wins; otherwise fall back to extension.
	kind := ""
	switch {
	case dataType == 1 && fileType == 1:
		kind = "ans"
	case dataType == 5:
		kind = "bin"
		// For BIN files, SAUCE stores width as file_type * 2.
		width = int(fileType) * 2
		if width > 0 && len(body) > 0 {
			height = (len(body) / 2) / width
		}
	case ext == ".bin":
		kind = "bin"
	default:
		kind = "ans"
	}

	switch kind {
	case "bin":
		if width <= 0 {
			return nil, fmt.Errorf("ansi: BIN file has no width (no SAUCE, no -cols hint)")
		}
		if height <= 0 {
			height = (len(body) / 2) / width
		}
		return LoadBIN(body, width, height)
	default:
		if width <= 0 {
			width = 80
		}
		if height <= 0 {
			height = 25
		}
		return LoadANSI(body, width, height), nil
	}
}

// LoadBIN parses a CP437 char/attr grid (2 bytes per cell, row-major) into
// a Frame of the given dimensions.
func LoadBIN(data []byte, w, h int) (*Frame, error) {
	f := NewFrame(0, 0, w, h, LightGray|BgBlack)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 2
			if off+1 >= len(data) {
				return f, nil
			}
			f.SetCell(x, y, data[off], Attr(data[off+1]))
		}
	}
	return f, nil
}

// LoadANSI walks an ANSI byte stream, applies SGR + cursor-positioning
// sequences, and lays each printable byte at (x, y) in a Frame. defaultW
// and defaultH are used when the stream doesn't push past those bounds;
// rows beyond defaultH grow the Frame so a SAUCE-less file with longer
// content still loads. Width is fixed -- bytes that would land beyond
// the right edge wrap to the next row.
func LoadANSI(data []byte, defaultW, defaultH int) *Frame {
	type cell struct {
		ch   byte
		attr Attr
	}
	rows := make([][]cell, 0, defaultH)
	ensure := func(y int) {
		for len(rows) <= y {
			rows = append(rows, make([]cell, defaultW))
		}
	}

	x, y := 0, 0
	curAttr := LightGray | BgBlack
	intensity := byte(0)
	blink := byte(0)
	fg := byte(7)  // LightGray
	bg := byte(0)  // BgBlack
	savedX, savedY := 0, 0

	i := 0
	for i < len(data) {
		b := data[i]
		// Stop on the BBS-era EOF marker so any trailing junk after the
		// art doesn't confuse our cursor state.
		if b == 0x1A {
			break
		}
		if b == 0x1B && i+1 < len(data) {
			// Escape sequence. We handle CSI (`\x1b[ ... final`).
			if data[i+1] == '[' {
				// Skim params + intermediates until a final byte 0x40..0x7E.
				end := i + 2
				for end < len(data) {
					c := data[end]
					if c >= 0x40 && c <= 0x7E {
						break
					}
					end++
				}
				if end >= len(data) {
					break
				}
				params := string(data[i+2 : end])
				final := data[end]
				switch final {
				case 'm':
					// SGR. Apply in left-to-right order.
					for _, p := range parseParams(params) {
						switch {
						case p == 0:
							fg = 7
							bg = 0
							intensity = 0
							blink = 0
						case p == 1:
							intensity = 0x08
						case p == 5:
							blink = 0x80
						case p == 22:
							intensity = 0
						case p == 25:
							blink = 0
						case p >= 30 && p <= 37:
							// SGR uses BGR-ordered color codes (31=red,
							// 34=blue); CGA palette uses RGB-bit indexing
							// (1=blue, 4=red). Translate or every red-blue
							// pair will swap on screen.
							fg = SgrToCGAFg[p-30]
						case p >= 40 && p <= 47:
							bg = SgrToCGABg[p-40]
						}
					}
					curAttr = Attr(fg|intensity) | Attr(bg<<4) | Attr(blink)
				case 'H', 'f':
					// Cursor position: row;col, 1-based.
					ps := parseParams(params)
					ny, nx := 1, 1
					if len(ps) >= 1 && ps[0] > 0 {
						ny = ps[0]
					}
					if len(ps) >= 2 && ps[1] > 0 {
						nx = ps[1]
					}
					y = ny - 1
					x = nx - 1
				case 'A':
					// Cursor up.
					n := 1
					ps := parseParams(params)
					if len(ps) >= 1 && ps[0] > 0 {
						n = ps[0]
					}
					y -= n
					if y < 0 {
						y = 0
					}
				case 'B':
					n := 1
					ps := parseParams(params)
					if len(ps) >= 1 && ps[0] > 0 {
						n = ps[0]
					}
					y += n
				case 'C':
					n := 1
					ps := parseParams(params)
					if len(ps) >= 1 && ps[0] > 0 {
						n = ps[0]
					}
					x += n
					if x >= defaultW {
						x = defaultW - 1
					}
				case 'D':
					n := 1
					ps := parseParams(params)
					if len(ps) >= 1 && ps[0] > 0 {
						n = ps[0]
					}
					x -= n
					if x < 0 {
						x = 0
					}
				case 's':
					savedX, savedY = x, y
				case 'u':
					x, y = savedX, savedY
				}
				i = end + 1
				continue
			}
			// Other ESC-prefixed sequences: skip 2 bytes and move on.
			i += 2
			continue
		}
		switch b {
		case '\r':
			x = 0
		case '\n':
			y++
		case '\b':
			if x > 0 {
				x--
			}
		case '\t':
			x = (x + 8) &^ 7
			if x >= defaultW {
				x = 0
				y++
			}
		default:
			ensure(y)
			if x >= defaultW {
				x = 0
				y++
				ensure(y)
			}
			if x >= 0 && y >= 0 {
				rows[y][x] = cell{ch: b, attr: curAttr}
			}
			x++
		}
		i++
	}

	h := len(rows)
	if h < defaultH {
		h = defaultH
	}
	f := NewFrame(0, 0, defaultW, h, LightGray|BgBlack)
	for ry := 0; ry < len(rows); ry++ {
		for rx := 0; rx < defaultW && rx < len(rows[ry]); rx++ {
			c := rows[ry][rx]
			if c.ch == 0 {
				continue
			}
			f.SetCell(rx, ry, c.ch, c.attr)
		}
	}
	return f
}

// parseParams splits a CSI parameter string ("1;33;44") into integers.
// Empty fields return 0 so the caller can detect "default" via len/value.
func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	out := make([]int, 0, 4)
	cur := 0
	have := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ';' {
			out = append(out, cur)
			cur = 0
			have = false
			continue
		}
		if c < '0' || c > '9' {
			continue
		}
		cur = cur*10 + int(c-'0')
		have = true
	}
	if have || len(out) > 0 {
		out = append(out, cur)
	}
	return out
}

// SauceRecord is the parsed fields of an 128-byte SAUCE trailer, just
// the slots the loader needs. Add more if you need them later.
type SauceRecord struct {
	DataType byte
	FileType byte
	TInfo1   int // for ANSI: width in chars; for BIN: file_type doubles as width hint
	TInfo2   int // for ANSI: height in chars
	Size     int // file size with SAUCE stripped (i.e. body length)
}

// parseSauce returns the body (data minus SAUCE + any trailing 0x1A) and
// a parsed record, or (data, nil) if no SAUCE is present. Comment blocks
// (when present) are also stripped.
func parseSauce(data []byte) ([]byte, *SauceRecord) {
	if len(data) < 128 || string(data[len(data)-128:len(data)-128+7]) != "SAUCE00" {
		return data, nil
	}
	tail := data[len(data)-128:]
	rec := &SauceRecord{
		DataType: tail[94],
		FileType: tail[95],
		TInfo1:   int(tail[96]) | int(tail[97])<<8,
		TInfo2:   int(tail[98]) | int(tail[99])<<8,
		Size:     int(tail[90]) | int(tail[91])<<8 | int(tail[92])<<16 | int(tail[93])<<24,
	}
	commentCount := int(tail[104])
	trailer := 128
	if commentCount > 0 {
		trailer += 5 + 64*commentCount
	}
	body := data[:len(data)-trailer]
	if rec.Size > 0 && rec.Size <= len(body) {
		body = body[:rec.Size]
	}
	// Strip a single trailing 0x1A EOF marker if present.
	if len(body) > 0 && body[len(body)-1] == 0x1A {
		body = body[:len(body)-1]
	}
	return body, rec
}
