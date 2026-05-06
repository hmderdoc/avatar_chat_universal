package avatar

import (
	"crypto/md5"
	"encoding/hex"
)

// Identicon generates a procedural 10x6 avatar from a name, ported faithfully
// from /sbbs/repo/exec/load/identicon.js. The pattern is mirrored
// horizontally (left half drives right half), so visually it looks
// symmetrical -- the same algorithm GitHub-style identicons use.
//
// Foreground / background colors and bright bit derive from the first few
// hash bits; the cell pattern derives from subsequent 2-bit groups picking
// from a 4-glyph palette: space, full-block, lower-half-block, full-block.
//
// The returned avatar always passes Validate(): no blink-bit attributes,
// no terminal-confusing chars.
func Identicon(name string) Avatar {
	if name == "" {
		name = "anonymous"
	}
	sum := md5.Sum([]byte(name))
	hash := []byte(hex.EncodeToString(sum[:])) // 32 hex chars

	var bv, bits uint32
	idx := 0
	getBits := func(cnt uint32) uint32 {
		for bits < cnt {
			if idx >= len(hash) {
				idx = 0 // wrap; shouldn't happen for MD5 + this many bits
			}
			d := uint32(hexDigit(hash[idx]))
			idx++
			bv |= d << bits
			bits += 4
		}
		ret := bv & ((1 << cnt) - 1)
		bv >>= cnt
		bits -= cnt
		return ret
	}

	fg := getBits(3)
	bg := uint32(7) - fg
	br := uint32(8) // bright foreground (bit 3)
	if fg == 4 {
		br = 0
	}
	attr := byte(fg | (bg << 4) | br)

	palette := [4]byte{' ', 0xDB, 0xDC, 0xDB}

	out := make(Avatar, Bytes)
	for x := 0; x < 5; x++ {
		for y := 0; y < 6; y++ {
			ch := palette[getBits(2)]
			leftOff := (y*Width + x) * 2
			rightOff := (y*Width + (Width - 1 - x)) * 2
			out[leftOff] = ch
			out[leftOff+1] = attr
			out[rightOff] = ch
			out[rightOff+1] = attr
		}
	}
	return out
}

func hexDigit(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
