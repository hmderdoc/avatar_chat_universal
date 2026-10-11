package ansi

import "testing"

func TestDecodeSGRMouse(t *testing.T) {
	cases := []struct {
		in      string
		button  int
		x, y    int
		release bool
	}{
		{"\x1b[<0;10;5M", MouseLeft, 9, 4, false},
		{"\x1b[<0;10;5m", MouseLeft, 9, 4, true},
		{"\x1b[<64;1;1M", MouseWheelUp, 0, 0, false},
		{"\x1b[<65;80;25M", MouseWheelDown, 79, 24, false},
		{"\x1b[<16;3;3M", MouseLeft, 2, 2, false}, // ctrl held: still a left press
	}
	for _, c := range cases {
		k, n, ok := tryDecode([]byte(c.in), false)
		if !ok || n != len(c.in) || k.Type != KeyMouse || k.Button != c.button || k.X != c.x || k.Y != c.y || k.Release != c.release {
			t.Errorf("%q -> %+v n=%d ok=%v", c.in, k, n, ok)
		}
	}
	// Motion and junk are swallowed, not delivered.
	for _, in := range []string{"\x1b[<32;5;5M", "\x1b[<0;;5M", "\x1b[<0;5M"} {
		k, n, ok := tryDecode([]byte(in), false)
		if !ok || n != len(in) || k.Type == KeyMouse {
			t.Errorf("%q -> %+v", in, k)
		}
	}
	if _, _, ok := tryDecode([]byte("\x1b[<0;10;"), false); ok {
		t.Error("partial SGR report decoded")
	}
}

func TestDecodeX10MouseOnlyWhenAsked(t *testing.T) {
	press := []byte{0x1b, '[', 'M', 32 + 0, 33 + 9, 33 + 4}
	k, n, ok := tryDecodeMouse(press, false, true)
	if !ok || n != 6 || k.Type != KeyMouse || k.X != 9 || k.Y != 4 || k.Release {
		t.Fatalf("press -> %+v n=%d", k, n)
	}
	rel := []byte{0x1b, '[', 'M', 32 + 3, 40, 40}
	if k, _, _ := tryDecodeMouse(rel, false, true); !k.Release {
		t.Fatalf("release -> %+v", k)
	}
	wheel := []byte{0x1b, '[', 'M', 32 + 65, 40, 40}
	if k, _, _ := tryDecodeMouse(wheel, false, true); k.Button != MouseWheelDown || k.Release {
		t.Fatalf("wheel -> %+v", k)
	}
	// Mouse off: ESC [ M is an ordinary (unknown) CSI and the next bytes
	// are typing, so an F1 on an SCO-style terminal doesn't eat them.
	k, n, _ = tryDecodeMouse([]byte("\x1b[Mhi"), false, false)
	if k.Type == KeyMouse || n != 3 {
		t.Fatalf("mouse off -> %+v n=%d", k, n)
	}
	if _, _, ok := tryDecodeMouse(press[:4], false, true); ok {
		t.Fatal("partial X10 report decoded")
	}
}
