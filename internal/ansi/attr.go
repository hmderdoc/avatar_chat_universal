package ansi

import (
	"fmt"
	"io"
	"strings"
)

// Attr is a CGA-style attribute byte:
//
//	bits 0-2: foreground base color (0-7)
//	bit  3:   foreground intensity (bold/bright)
//	bits 4-6: background color      (0-7)
//	bit  7:   blink (or background intensity, depending on terminal mode)
//
// This matches the byte format used by Synchronet's avatar collections and
// the Frame.js attribute model (avatar_lib.js:5-8 byte order).
type Attr uint8

const (
	Black     Attr = 0
	Blue      Attr = 1
	Green     Attr = 2
	Cyan      Attr = 3
	Red       Attr = 4
	Magenta   Attr = 5
	Brown     Attr = 6
	LightGray Attr = 7

	// High intensity (foreground only with bit 3)
	DarkGray     Attr = 8
	LightBlue    Attr = 9
	LightGreen   Attr = 10
	LightCyan    Attr = 11
	LightRed     Attr = 12
	LightMagenta Attr = 13
	Yellow       Attr = 14
	White        Attr = 15

	// Background helpers — shift the base color into bits 4-6.
	BgBlack     Attr = 0 << 4
	BgBlue      Attr = 1 << 4
	BgGreen     Attr = 2 << 4
	BgCyan      Attr = 3 << 4
	BgRed       Attr = 4 << 4
	BgMagenta   Attr = 5 << 4
	BgBrown     Attr = 6 << 4
	BgLightGray Attr = 7 << 4

	Blink Attr = 1 << 7
)

// CGA fg base (0-7) → ANSI SGR code. Synchronet/IBM ordering puts blue at 1,
// green at 2, red at 4 (CGA palette), which differs from the ANSI standard
// ordering (red=1, green=2, blue=4). This table maps between them.
var cgaFgToANSI = [8]int{30, 34, 32, 36, 31, 35, 33, 37}
var cgaBgToANSI = [8]int{40, 44, 42, 46, 41, 45, 43, 47}

// SgrToCGAFg maps an SGR foreground code (30..37) to its CGA palette
// index. SGR uses ROYGBIV-ish ordering (31=red, 34=blue) but the CGA
// hardware uses RGB-bit ordering (1=blue, 4=red), so an ANSI parser
// MUST translate via this table or every red-blue and yellow-cyan
// will swap. SgrToCGABg is the same translation for codes 40..47.
var SgrToCGAFg = [8]byte{0, 4, 2, 6, 1, 5, 3, 7}
var SgrToCGABg = [8]byte{0, 4, 2, 6, 1, 5, 3, 7}

// SGR returns the full reset-then-set SGR escape sequence for this attribute.
// The reset (`0`) makes each emit self-contained — terminals don't need to
// remember prior attribute state.
func (a Attr) SGR() string {
	fgBase := int(a & 7)
	fgBright := a&8 != 0
	bg := int((a >> 4) & 7)
	blink := a&Blink != 0

	var b strings.Builder
	b.WriteString("\x1b[0")
	if fgBright {
		b.WriteString(";1")
	}
	if blink {
		b.WriteString(";5")
	}
	fmt.Fprintf(&b, ";%d;%d", cgaFgToANSI[fgBase], cgaBgToANSI[bg])
	b.WriteString("m")
	return b.String()
}

// FG and BG helpers compose attributes.
func FG(a Attr, fg Attr) Attr { return (a &^ 0x0F) | (fg & 0x0F) }
func BG(a Attr, bg Attr) Attr { return (a &^ 0x70) | ((bg & 7) << 4) }

// Reset writes ANSI reset to w.
func Reset(w io.Writer) error {
	_, err := io.WriteString(w, "\x1b[0m")
	return err
}

// ClearScreen writes the clear-screen + cursor-home sequence.
func ClearScreen(w io.Writer) error {
	_, err := io.WriteString(w, "\x1b[2J\x1b[H")
	return err
}

// MoveCursor writes the absolute cursor-position sequence. col/row are 1-based.
func MoveCursor(w io.Writer, col, row int) error {
	_, err := fmt.Fprintf(w, "\x1b[%d;%dH", row, col)
	return err
}

// HideCursor / ShowCursor toggle the terminal cursor.
func HideCursor(w io.Writer) error { _, err := io.WriteString(w, "\x1b[?25l"); return err }
func ShowCursor(w io.Writer) error { _, err := io.WriteString(w, "\x1b[?25h"); return err }
