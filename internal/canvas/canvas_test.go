package canvas

import (
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
)

func TestNewClampsSize(t *testing.T) {
	c := New(200, 99)
	if c.W != MaxWidth || c.H != MaxHeight {
		t.Fatalf("got %dx%d", c.W, c.H)
	}
	if !c.Blank() {
		t.Fatal("new canvas should be blank")
	}
}

func TestHalfBlockPixels(t *testing.T) {
	c := New(2, 2)
	c.SetPixel(0, 0, 12) // top half bright red
	if got := c.At(0, 0); got.Ch != HalfUpper || got.Fg() != 12 || got.Bg() != 0 {
		t.Fatalf("top pixel cell = %+v", got)
	}
	c.SetPixel(0, 1, 12) // both halves the same: a full block
	if got := c.At(0, 0); got.Ch != FullBlock || got.Fg() != 12 {
		t.Fatalf("both halves cell = %+v", got)
	}
	c.SetPixel(0, 0, 1) // dark top over bright bottom: bright goes in fg, lower half
	if got := c.At(0, 0); got.Ch != HalfLower || got.Fg() != 12 || got.Bg() != 1 {
		t.Fatalf("dark over bright = %+v", got)
	}
	if c.Pixel(0, 0) != 1 || c.Pixel(0, 1) != 12 {
		t.Fatalf("pixels read back %d/%d", c.Pixel(0, 0), c.Pixel(0, 1))
	}
	c.SetPixel(1, 2, 14) // two bright halves: the bottom shows dim
	c.SetPixel(1, 3, 10)
	if got := c.At(1, 1); got.Ch != HalfUpper || got.Fg() != 14 || got.Bg() != 2 {
		t.Fatalf("bright over bright = %+v", got)
	}
}

func TestFill(t *testing.T) {
	c := New(5, 3)
	wall := Cell{Ch: FullBlock, Attr: Attr(7, 0)}
	for y := 0; y < 3; y++ {
		c.Set(2, y, wall)
	}
	n := c.Fill(0, 0, Cell{Ch: '#', Attr: Attr(4, 1)})
	if n != 6 {
		t.Fatalf("filled %d cells, want 6 (left of the wall)", n)
	}
	if c.At(4, 0).Ch != Space || c.At(2, 1) != wall {
		t.Fatal("fill leaked past the wall")
	}
	if c.Fill(0, 0, Cell{Ch: '#', Attr: Attr(4, 1)}) != 0 {
		t.Fatal("refilling with the same cell should change nothing")
	}
}

func TestBitmapTrimsAndRoundTrips(t *testing.T) {
	c := New(80, 25)
	if _, ok := c.Bitmap("me"); ok {
		t.Fatal("a blank canvas should not encode")
	}
	c.Set(3, 1, Cell{Ch: 'A', Attr: Attr(1, 4)}) // CGA blue on red
	msg, ok := c.Bitmap("Beta Tester")
	if !ok {
		t.Fatal("no message")
	}
	img, err := bitmap.Parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != 4 || img.Height != 2 || img.From != "BetaTester" {
		t.Fatalf("got %dx%d from %q, want 4x2 trimmed", img.Width, img.Height, img.From)
	}
	got := img.Cells[1][3]
	// The wire uses xterm order: CGA blue (1) is 4, CGA red (4) is 1.
	if got.Char != 'A' || got.FG != 4 || got.BG != 1 {
		t.Fatalf("cell = %+v", got)
	}
	if img.Cells[0][0].Char != ' ' {
		t.Fatal("leading blank space should be kept")
	}
}

func TestFullCanvasFitsTheWebLimit(t *testing.T) {
	// Worst case for compression: every cell different.
	c := New(MaxWidth, MaxHeight)
	for i := range c.Cells {
		c.Cells[i] = Cell{Ch: byte(i*7 + 1), Attr: byte(i*13) & 0x7F}
	}
	msg, _ := c.Bitmap("x")
	if len(msg) > MaxMessage {
		t.Fatalf("an %dx%d canvas encodes to %d chars, over %d", MaxWidth, MaxHeight, len(msg), MaxMessage)
	}
}

func TestHistory(t *testing.T) {
	h := NewHistory(2)
	c := New(1, 1)
	for i := 0; i < 3; i++ {
		h.Push(c)
	}
	if h.Len() != 2 {
		t.Fatalf("len %d, want capped at 2", h.Len())
	}
	if h.Pop() == nil || h.Pop() == nil || h.Pop() != nil {
		t.Fatal("pop order wrong")
	}
}

func key(t ansi.KeyType) ansi.Key { return ansi.Key{Type: t} }
func ch(r rune) ansi.Key         { return ansi.Key{Type: ansi.KeyChar, Rune: r} }
func ctrl(r rune) ansi.Key       { return ansi.Key{Type: ansi.KeyChar, Rune: r, Ctrl: true} }

func TestEditorTypingUndoAndSend(t *testing.T) {
	e := NewEditor(nil, nil, 80, 25, 80, 25)
	e.From = "alice"
	for _, r := range "Hi" {
		e.HandleKey(ch(r))
	}
	if e.c.At(0, 0).Ch != 'H' || e.c.At(1, 0).Ch != 'i' || e.cx != 2 {
		t.Fatalf("typing: %q%q cursor %d", e.c.At(0, 0).Ch, e.c.At(1, 0).Ch, e.cx)
	}
	e.HandleKey(key(ansi.KeyF1)) // F-set 6, F1 = light shade
	if e.c.At(2, 0).Ch != 176 {
		t.Fatalf("F1 stamped %d, want 176", e.c.At(2, 0).Ch)
	}
	e.HandleKey(ctrl(ctrlZ))
	if e.c.At(2, 0).Ch != Space {
		t.Fatal("undo did not remove the stamp")
	}
	done, res := e.HandleKey(ctrl(ctrlY))
	if !done || !res.Sent || res.W != 2 || res.H != 1 {
		t.Fatalf("send: done=%v %+v", done, res)
	}
	img, err := bitmap.Parse(res.Message)
	if err != nil || img.Cells[0][0].Char != 'H' {
		t.Fatalf("sent message does not decode: %v", err)
	}
}

func TestEditorEscConfirmsUnsentWork(t *testing.T) {
	e := NewEditor(nil, nil, 80, 25, 10, 5)
	if done, _ := e.HandleKey(key(ansi.KeyEsc)); !done {
		t.Fatal("Esc on an untouched canvas should just close")
	}
	e = NewEditor(nil, nil, 80, 25, 10, 5)
	e.HandleKey(ch('x'))
	if done, _ := e.HandleKey(key(ansi.KeyEsc)); done {
		t.Fatal("Esc with unsent work closed without asking")
	}
	if done, _ := e.HandleKey(ch('n')); done || e.c.At(0, 0).Ch != 'x' {
		t.Fatal("answering no should keep the drawing")
	}
	e.HandleKey(key(ansi.KeyEsc))
	if done, res := e.HandleKey(ch('y')); !done || res.Sent {
		t.Fatal("answering yes should discard")
	}
}

func TestEditorHalfModeAndBrush(t *testing.T) {
	e := NewEditor(nil, nil, 80, 25, 10, 5)
	e.HandleKey(ctrl(ctrlO))
	e.fg = 10
	e.HandleKey(ch(' '))
	e.HandleKey(ctrl(ctrlB))
	e.HandleKey(key(ansi.KeyDown)) // brush paints the bottom half of row 0
	if got := e.c.At(0, 0); got.Ch != FullBlock || got.Fg() != 10 {
		t.Fatalf("half mode + brush gave %+v", got)
	}
	e.HandleKey(key(ansi.KeyDown)) // into row 1's top half
	if e.cy != 1 || e.c.Pixel(0, 2) != 10 {
		t.Fatalf("cursor row %d, pixel %d", e.cy, e.c.Pixel(0, 2))
	}
}

func TestEditorDrawFitsScreen(t *testing.T) {
	e := NewEditor(nil, nil, 80, 25, 80, 40)
	e.frame = ansi.NewFrame(0, 0, 80, 25, ansi.LightGray|ansi.BgBlack)
	e.cy = 39
	e.draw()
	if e.vy != 39-23+1 {
		t.Fatalf("viewport did not follow the cursor: vy=%d", e.vy)
	}
	var row strings.Builder
	for x := 0; x < 80; x++ {
		row.WriteByte(e.frame.CellAt(x, 24).Char)
	}
	if !strings.HasPrefix(row.String(), "^Y send") {
		t.Fatalf("key line = %q", row.String())
	}
}
