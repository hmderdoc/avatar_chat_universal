package ansi

import (
	"io"
	"testing"
	"time"
)

// nextKey polls in the way the door's main loop does (25 ms timeouts)
// until a key arrives or limit passes.
func nextKey(t *testing.T, in *Input, limit time.Duration) (Key, bool) {
	t.Helper()
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		if k, ok, err := in.Next(25 * time.Millisecond); err != nil {
			t.Fatal(err)
		} else if ok {
			return k, true
		}
	}
	return Key{}, false
}

// An arrow key whose ESC arrives in one packet and "[D" in the next is
// still an arrow, not Esc followed by typed text.
func TestNext_SplitArrowIsNotEsc(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	in := NewInput(r)
	for i := 0; i < 20; i++ {
		go func() {
			w.Write([]byte{0x1B})
			time.Sleep(40 * time.Millisecond)
			w.Write([]byte("[D"))
		}()
		k, ok := nextKey(t, in, time.Second)
		if !ok || k.Type != KeyLeft {
			t.Fatalf("try %d: got %+v ok=%v, want KeyLeft", i, k, ok)
		}
	}
}

// A lone ESC still arrives as Esc, promptly.
func TestNext_BareEscFlushes(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	in := NewInput(r)
	start := time.Now()
	go w.Write([]byte{0x1B})
	k, ok := nextKey(t, in, time.Second)
	if !ok || k.Type != KeyEsc {
		t.Fatalf("got %+v ok=%v, want KeyEsc", k, ok)
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("Esc took %v", d)
	}
}

// A bracketed paste of several lines is one KeyPaste, not typed keys and
// Enters; keys after it decode as usual.
func TestNext_BracketedPaste(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	in := NewInput(r)
	go func() {
		w.Write([]byte("\x1b[200~first line\r\n/quit\r\nthird"))
		time.Sleep(30 * time.Millisecond)
		w.Write([]byte("\x1b[201~x"))
	}()
	k, ok := nextKey(t, in, time.Second)
	if !ok || k.Type != KeyPaste || k.Text != "first line\r\n/quit\r\nthird" {
		t.Fatalf("got %+v", k)
	}
	k, ok = nextKey(t, in, time.Second)
	if !ok || k.Type != KeyChar || k.Rune != 'x' {
		t.Fatalf("after paste got %+v", k)
	}
}

func TestDecodeCtrlArrowsAreWordMoves(t *testing.T) {
	for in, want := range map[string]Key{
		"\x1b[1;5D": {Type: KeyLeft, Ctrl: true},
		"\x1b[1;5C": {Type: KeyRight, Ctrl: true},
		"\x1b[1;3D": {Type: KeyLeft, Ctrl: true},
		"\x1b[D":    {Type: KeyLeft},
	} {
		k, _, ok := tryDecode([]byte(in), false)
		if !ok || k.Type != want.Type || k.Ctrl != want.Ctrl {
			t.Errorf("%q = %+v", in, k)
		}
	}
}
