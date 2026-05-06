package avatar

import (
	"bytes"
	"testing"
)

func TestIdenticonValid(t *testing.T) {
	for _, name := range []string{"alice", "bob", "anonymous", "", "Hm Derdoc"} {
		a := Identicon(name)
		if err := a.Validate(); err != nil {
			t.Errorf("Identicon(%q) failed validation: %v", name, err)
		}
	}
}

func TestIdenticonStable(t *testing.T) {
	a := Identicon("alice")
	b := Identicon("alice")
	if !bytes.Equal(a, b) {
		t.Error("Identicon(\"alice\") not deterministic")
	}
	c := Identicon("bob")
	if bytes.Equal(a, c) {
		t.Error("Identicon(\"alice\") and Identicon(\"bob\") collide")
	}
}

func TestIdenticonHorizontalMirror(t *testing.T) {
	a := Identicon("test")
	for y := 0; y < Height; y++ {
		for x := 0; x < Width/2; x++ {
			leftCh, _ := a.Cell(x, y)
			rightCh, _ := a.Cell(Width-1-x, y)
			if leftCh != rightCh {
				t.Errorf("identicon not mirrored at (%d,%d): left=0x%02x right=0x%02x",
					x, y, leftCh, rightCh)
			}
		}
	}
}
