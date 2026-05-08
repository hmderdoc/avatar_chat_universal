// Package avatar implements Synchronet-compatible 10x6 CP437 avatars: the
// 120-byte (char,attr) cell format, validation rules, base64 round-trip, and
// per-user INI persistence.
//
// Format reference: /sbbs/repo/exec/load/avatar_lib.js (lines 5-8 for size,
// 82-107 for validation, 109-119 for INI persistence shape).
package avatar

import (
	"encoding/base64"
	"fmt"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

const (
	Width  = 10
	Height = 6
	Bytes  = Width * Height * 2 // 120
)

// Avatar is exactly Bytes (120) bytes of (char, attr) pairs in row-major order.
// Even indices are CP437 chars, odd indices are CGA attribute bytes.
type Avatar []byte

// Validate enforces the byte-set rules from /sbbs/repo/exec/load/avatar_lib.js
// lines 82-107: no terminal-confusing control chars in the char positions, no
// blink-bit set in the attribute positions, exact 120-byte length.
func (a Avatar) Validate() error {
	if len(a) != Bytes {
		return fmt.Errorf("avatar: length %d, want %d", len(a), Bytes)
	}
	for i := 0; i < len(a); i += 2 {
		if reason := charForbidden(a[i]); reason != "" {
			return fmt.Errorf("avatar: byte %d: %s (0x%02x)", i, reason, a[i])
		}
		if a[i+1]&0x80 != 0 {
			return fmt.Errorf("avatar: byte %d: blink-bit attribute 0x%02x not allowed", i+1, a[i+1])
		}
	}
	return nil
}

// charForbidden mirrors Synchronet's avatar_lib.js:88-101 byte-set rule.
// Note: 0x0B (VT) and 0x1A (SUB/EOF) are NOT in Synchronet's reject list,
// even though they're control chars -- they render as legitimate CP437
// glyphs that someone might want in their avatar.
func charForbidden(b byte) string {
	switch b {
	case 0x00:
		return "null"
	case 0x07:
		return "BEL"
	case 0x08:
		return "BS"
	case 0x09:
		return "TAB"
	case 0x0A:
		return "LF"
	case 0x0C:
		return "FF"
	case 0x0D:
		return "CR"
	case 0x1B:
		return "ESC"
	case 0xFF:
		return "IAC"
	}
	return ""
}

// Cell returns the (char, attr) pair at avatar coordinate (x,y). The blink bit
// is preserved in the returned attribute; renderers should mask it off.
func (a Avatar) Cell(x, y int) (byte, ansi.Attr) {
	o := (y*Width + x) * 2
	return a[o], ansi.Attr(a[o+1])
}

// RenderTo blits the avatar into f starting at (fx, fy). The blink attribute
// bit is masked off (avatars use it for high-intensity bg, not actual blink,
// per avatar_lib.js:322,349).
func (a Avatar) RenderTo(f *ansi.Frame, fx, fy int) {
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			ch, attr := a.Cell(x, y)
			f.SetCell(fx+x, fy+y, ch, attr&0x7F)
		}
	}
}

// Base64 encodes the 120-byte payload as standard base64, matching the
// `[avatar] data = ...` field in Synchronet's user INI files.
func (a Avatar) Base64() string {
	return base64.StdEncoding.EncodeToString(a)
}

// FromBase64 decodes and validates a base64-encoded avatar.
func FromBase64(s string) (Avatar, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("avatar: base64: %v", err)
	}
	if len(data) != Bytes {
		return nil, fmt.Errorf("avatar: decoded length %d, want %d", len(data), Bytes)
	}
	a := Avatar(data)
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

// Clone returns a deep copy so mutations to the original don't affect us.
func (a Avatar) Clone() Avatar {
	out := make(Avatar, len(a))
	copy(out, a)
	return out
}
