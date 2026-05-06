package avatar

import (
	"bytes"
	"testing"
)

// validBytes returns 120 bytes of (' ', LIGHTGRAY) cells — a blank-but-valid avatar.
func validBytes() []byte {
	out := make([]byte, Bytes)
	for i := 0; i < Bytes; i += 2 {
		out[i] = ' '
		out[i+1] = 0x07
	}
	return out
}

func TestValidateAcceptsCleanAvatar(t *testing.T) {
	if err := Avatar(validBytes()).Validate(); err != nil {
		t.Errorf("clean avatar rejected: %v", err)
	}
}

func TestValidateRejectsWrongLength(t *testing.T) {
	if err := Avatar(make([]byte, 100)).Validate(); err == nil {
		t.Error("100 bytes should not validate")
	}
	if err := Avatar(make([]byte, 121)).Validate(); err == nil {
		t.Error("121 bytes should not validate")
	}
}

func TestValidateRejectsForbiddenChars(t *testing.T) {
	// Mirrors avatar_lib.js:88-101 -- 0x0B (VT) and 0x1A (SUB) are NOT in
	// Synchronet's reject list and must round-trip cleanly.
	forbidden := []byte{0x00, 0x07, 0x08, 0x09, 0x0A, 0x0C, 0x0D, 0x1B, 0xFF}
	for _, b := range forbidden {
		buf := validBytes()
		buf[10] = b // arbitrary char position
		if err := Avatar(buf).Validate(); err == nil {
			t.Errorf("char 0x%02x in body should be rejected", b)
		}
	}
	allowed := []byte{0x0B, 0x1A}
	for _, b := range allowed {
		buf := validBytes()
		buf[10] = b
		if err := Avatar(buf).Validate(); err != nil {
			t.Errorf("char 0x%02x should be allowed (matches Synchronet): %v", b, err)
		}
	}
}

func TestValidateRejectsBlinkAttribute(t *testing.T) {
	buf := validBytes()
	buf[5] = 0x80 | 0x07 // attr at odd index, blink bit set
	if err := Avatar(buf).Validate(); err == nil {
		t.Error("blink-bit attribute should be rejected")
	}
}

func TestBase64RoundTrip(t *testing.T) {
	a := Avatar(validBytes())
	encoded := a.Base64()
	decoded, err := FromBase64(encoded)
	if err != nil {
		t.Fatalf("FromBase64: %v", err)
	}
	if !bytes.Equal(a, decoded) {
		t.Errorf("round-trip mismatch")
	}
}

func TestFromBase64RejectsGarbage(t *testing.T) {
	if _, err := FromBase64("not-base64!@#"); err == nil {
		t.Error("garbage base64 should fail")
	}
	if _, err := FromBase64("c2hvcnQ="); err == nil {
		t.Error("short payload should fail")
	}
}

func TestCell(t *testing.T) {
	buf := validBytes()
	// Place a known glyph at (3, 2)
	o := (2*Width + 3) * 2
	buf[o] = '#'
	buf[o+1] = 0x4F // bright white on red
	a := Avatar(buf)
	ch, attr := a.Cell(3, 2)
	if ch != '#' {
		t.Errorf("char = 0x%02x, want '#'", ch)
	}
	if byte(attr) != 0x4F {
		t.Errorf("attr = 0x%02x, want 0x4F", attr)
	}
}
