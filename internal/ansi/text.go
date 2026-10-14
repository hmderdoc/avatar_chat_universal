package ansi

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// unicodeToCP437 is CP437ToUnicode reversed, for the printable bytes only:
// the glyphs CP437 draws for 0x00-0x1F and 0x7F (☺, ♪, ⌂, ...) would go out
// as control codes on a raw CP437 terminal, so they don't map.
var unicodeToCP437 = func() map[rune]byte {
	m := make(map[rune]byte, 160)
	for b := 0x20; b < 0x100; b++ {
		if b != 0x7F {
			m[CP437ToUnicode[b]] = byte(b)
		}
	}
	return m
}()

// lookalikes are common characters CP437 lacks, mostly what phones and
// word processors put in place of plain punctuation.
var lookalikes = map[rune]string{
	'‘': "'", '’': "'", '‚': "'", '‛': "'", '′': "'",
	'“': `"`, '”': `"`, '„': `"`, '‟': `"`, '″': `"`,
	'‐': "-", '‑': "-", '‒': "-", '–': "-", '—': "-", '―': "-", '−': "-",
	'…': "...", '•': "\xF9", '·': "\xFA", '€': "EUR", '™': "TM", '©': "(c)", '®': "(R)",
	'×': "x", '\u2009': " ", '\u202F': " ", '\u3000': " ",
}

// ToCP437 turns chat text, which travels as UTF-8, into the CP437 bytes the
// terminal cells hold. Characters CP437 has map to their byte; common
// punctuation it lacks gets an ASCII stand-in; anything else (emoji, CJK)
// becomes '?'. Zero-width joiners, variation selectors and combining marks
// are dropped. U+0080-U+009F, which no text uses, are taken as raw CP437
// bytes from a sender that never converted them.
//
// Control characters never reach the terminal: tab becomes a space and the
// rest are dropped, so nobody can send escape sequences, bells or
// backspaces to other users' screens. With keepCodes, the transcript's own
// \x01 colour and \x02 effect codes in notices are kept; without it a
// Synchronet \x01 colour code is dropped together with its code letter.
func ToCP437(s string, keepCodes bool) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7F {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			// Not UTF-8: a high byte is most likely raw CP437 already.
			if s[i] >= 0x80 {
				b.WriteByte(s[i])
			}
			i++
			continue
		}
		i += n
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7F:
			if keepCodes && (r == 0x01 || r == 0x02) {
				b.WriteByte(byte(r))
			} else if r == 0x01 && i < len(s) {
				_, n := utf8.DecodeRuneInString(s[i:])
				i += n
			}
		case r < 0x7F:
			b.WriteByte(byte(r))
		case r <= 0x9F:
			b.WriteByte(byte(r))
		case r == '\u200B' || r == '\u200C' || r == '\u200D' || r == '\uFEFF' ||
			unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Mn, r) ||
			unicode.Is(unicode.Me, r):
		default:
			if c, ok := unicodeToCP437[r]; ok {
				b.WriteByte(c)
			} else if alt, ok := lookalikes[r]; ok {
				b.WriteString(alt)
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}
