package idle

// figletFont is a 5-row bitmap font, 4 columns per glyph. Each '#' becomes
// a CP437 full block (0xDB) and space becomes... space (transparent in
// the FG layer once cleared).
//
// This is hand-coded rather than parsed from TDF/FIGlet font files, which
// keeps the binary small and predictable. Only ASCII letters, digits, and
// a few punctuation marks are defined; missing glyphs render as a blank
// 4x5 block.
const (
	figletGlyphWidth  = 4
	figletGlyphHeight = 5
	figletGlyphPad    = 1 // blank columns inserted between glyphs
)

var figletFont = map[byte][figletGlyphHeight]string{
	' ': {
		"    ",
		"    ",
		"    ",
		"    ",
		"    ",
	},
	'A': {
		" ## ",
		"#  #",
		"####",
		"#  #",
		"#  #",
	},
	'B': {
		"### ",
		"#  #",
		"### ",
		"#  #",
		"### ",
	},
	'C': {
		" ###",
		"#   ",
		"#   ",
		"#   ",
		" ###",
	},
	'D': {
		"### ",
		"#  #",
		"#  #",
		"#  #",
		"### ",
	},
	'E': {
		"####",
		"#   ",
		"### ",
		"#   ",
		"####",
	},
	'F': {
		"####",
		"#   ",
		"### ",
		"#   ",
		"#   ",
	},
	'G': {
		" ###",
		"#   ",
		"# ##",
		"#  #",
		" ###",
	},
	'H': {
		"#  #",
		"#  #",
		"####",
		"#  #",
		"#  #",
	},
	'I': {
		"### ",
		" #  ",
		" #  ",
		" #  ",
		"### ",
	},
	'J': {
		"  ##",
		"   #",
		"   #",
		"#  #",
		" ## ",
	},
	'K': {
		"#  #",
		"# # ",
		"##  ",
		"# # ",
		"#  #",
	},
	'L': {
		"#   ",
		"#   ",
		"#   ",
		"#   ",
		"####",
	},
	'M': {
		"#  #",
		"####",
		"####",
		"#  #",
		"#  #",
	},
	'N': {
		"#  #",
		"## #",
		"# ##",
		"#  #",
		"#  #",
	},
	'O': {
		" ## ",
		"#  #",
		"#  #",
		"#  #",
		" ## ",
	},
	'P': {
		"### ",
		"#  #",
		"### ",
		"#   ",
		"#   ",
	},
	'Q': {
		" ## ",
		"#  #",
		"#  #",
		"# # ",
		" ## ",
	},
	'R': {
		"### ",
		"#  #",
		"### ",
		"# # ",
		"#  #",
	},
	'S': {
		" ###",
		"#   ",
		" ## ",
		"   #",
		"### ",
	},
	'T': {
		"####",
		" #  ",
		" #  ",
		" #  ",
		" #  ",
	},
	'U': {
		"#  #",
		"#  #",
		"#  #",
		"#  #",
		" ## ",
	},
	'V': {
		"#  #",
		"#  #",
		"#  #",
		" ## ",
		" ## ",
	},
	'W': {
		"#  #",
		"#  #",
		"####",
		"####",
		"# # ",
	},
	'X': {
		"#  #",
		" ## ",
		" ## ",
		" ## ",
		"#  #",
	},
	'Y': {
		"#  #",
		"#  #",
		" ## ",
		" #  ",
		" #  ",
	},
	'Z': {
		"####",
		"   #",
		" ## ",
		"#   ",
		"####",
	},
	'0': {
		" ## ",
		"#  #",
		"#  #",
		"#  #",
		" ## ",
	},
	'1': {
		" #  ",
		"##  ",
		" #  ",
		" #  ",
		"### ",
	},
	'2': {
		"### ",
		"   #",
		" ## ",
		"#   ",
		"####",
	},
	'3': {
		"### ",
		"   #",
		" ## ",
		"   #",
		"### ",
	},
	'4': {
		"#  #",
		"#  #",
		"####",
		"   #",
		"   #",
	},
	'5': {
		"####",
		"#   ",
		"### ",
		"   #",
		"### ",
	},
	'6': {
		" ## ",
		"#   ",
		"### ",
		"#  #",
		" ## ",
	},
	'7': {
		"####",
		"   #",
		"  # ",
		" #  ",
		"#   ",
	},
	'8': {
		" ## ",
		"#  #",
		" ## ",
		"#  #",
		" ## ",
	},
	'9': {
		" ## ",
		"#  #",
		" ###",
		"   #",
		" ## ",
	},
	'!': {
		" #  ",
		" #  ",
		" #  ",
		"    ",
		" #  ",
	},
	'?': {
		"### ",
		"   #",
		" ## ",
		"    ",
		" #  ",
	},
	'.': {
		"    ",
		"    ",
		"    ",
		"    ",
		" #  ",
	},
	',': {
		"    ",
		"    ",
		"    ",
		" #  ",
		"#   ",
	},
	'-': {
		"    ",
		"    ",
		"####",
		"    ",
		"    ",
	},
	':': {
		"    ",
		" #  ",
		"    ",
		" #  ",
		"    ",
	},
	'\'': {
		" #  ",
		" #  ",
		"    ",
		"    ",
		"    ",
	},
}

// figletGlyph returns the 5-row bitmap for one byte, falling back to a
// blank glyph for anything unsupported. Callers should pre-uppercase
// alphabetic chars since the font only defines uppercase.
func figletGlyph(b byte) [figletGlyphHeight]string {
	if b >= 'a' && b <= 'z' {
		b -= 32
	}
	if g, ok := figletFont[b]; ok {
		return g
	}
	return figletFont[' ']
}

// figletMeasure returns the (width, height) cells the rendered string will
// occupy. Width = sum of glyph widths + (n-1) pad columns.
func figletMeasure(s string) (int, int) {
	if s == "" {
		return 0, 0
	}
	w := len(s)*figletGlyphWidth + (len(s)-1)*figletGlyphPad
	if w < 0 {
		w = 0
	}
	return w, figletGlyphHeight
}
