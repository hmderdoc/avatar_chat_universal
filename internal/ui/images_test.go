package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

func testImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 3), uint8(y * 6), 200, 255})
		}
	}
	return img
}

// readyManager is an imageManager whose links are already downloaded, so
// layout and blitting can be tested without the network.
func readyManager(cfg ImageConfig, urls ...string) *imageManager {
	cfg.Fetcher = inlineimg.NewFetcher("")
	m := newImageManager(cfg)
	for _, u := range urls {
		m.entries[u] = &imageEntry{url: u, state: imgReady, img: testImage(), at: time.Now(), from: "bob",
			pics: map[picKey]*inlineimg.Picture{}}
	}
	return m
}

func findText(f *ansi.Frame, text string) (int, int, bool) {
	for y := 0; y < f.H; y++ {
		var row strings.Builder
		for x := 0; x < f.W; x++ {
			row.WriteByte(f.CellAt(x, y).Char)
		}
		if i := strings.Index(row.String(), text); i >= 0 {
			return i, y, true
		}
	}
	return 0, 0, false
}

func TestTranscript_InlinePictureStates(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 40)
	tr := NewTranscript(f, "alice")
	tr.Images = readyManager(ImageConfig{NSFWKeywords: inlineimg.DefaultNSFWKeywords}, "https://x.org/cat.png")
	// The loading link must not start a real download in a unit test.
	tr.Images.cfg.Fetcher.Timeout = time.Millisecond
	tr.Images.queue = make([]string, imageMaxQueue) // full: nothing is fetched
	now := time.Now().UnixNano() / 1000000
	bob := &chat.Nick{Name: "bob"}
	tr.Render([]*chat.Message{
		{Nick: bob, Str: "cat https://x.org/cat.png", Time: now},
		{Nick: &chat.Nick{Name: "carol"}, Str: "later https://x.org/slow.png", Time: now},
		{Nick: &chat.Nick{Name: "dave"}, Str: "https://x.org/nude-beach.jpg", Time: now},
	})

	// Ready: picture cells (not the bubble colour)
	// right under the text line, starting at the bubble's text column.
	_, y, ok := findText(f, "cat https://x.org/cat.png")
	if !ok {
		t.Fatal("message text not rendered")
	}
	x0 := avatarW + avatarMargin + 1
	c := f.CellAt(x0, y+1)
	if c.Attr == tr.Theme.BubbleLeft {
		t.Fatalf("no picture under the text: %+v", c)
	}
	// Pending: a one-line placeholder.
	if _, _, ok := findText(f, "[loading image...]"); !ok {
		t.Fatal("no loading placeholder for the pending link")
	}
	// Veiled: the NSFW label, and no download queued for it.
	if _, _, ok := findText(f, "NSFW - /nsfw to show"); !ok {
		t.Fatal("NSFW veil not drawn")
	}
	if e := tr.Images.entries["https://x.org/nude-beach.jpg"]; e == nil || !e.nsfw || e.queued {
		t.Fatalf("veiled entry = %+v, want nsfw and not queued", e)
	}
}

func TestApp_SixelBlitOnlyWhenNeeded(t *testing.T) {
	addr := startUITestServer(t)
	ctx := context.Background()
	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "main")
	if err := alice.Connect(ctx, 0); err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "main")
	if err := bob.Connect(ctx, 0); err != nil {
		t.Fatal(err)
	}
	defer bob.Close()

	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), alice, 80, 25, ansi.CharsetCP437)
	app.images = readyManager(ImageConfig{Sixel: true, CellW: 8, CellH: 16}, "https://x.org/a.png")
	app.transcript.Images = app.images

	say := func(text string) {
		n := len(alice.Messages())
		if err := bob.Send(text); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100 && len(alice.Messages()) == n; i++ {
			alice.Cycle()
			time.Sleep(10 * time.Millisecond)
		}
	}
	frame := func() string {
		app.draw()
		var b bytes.Buffer
		if err := app.composeInto(&b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	const dcs = "\x1bP0;1;0q"

	say("look https://x.org/a.png")
	if first := frame(); strings.Count(first, dcs) != 1 {
		t.Fatalf("first frame: %d sixel images, want 1", strings.Count(first, dcs))
	}
	if again := frame(); strings.Contains(again, "\x1bP") {
		t.Fatal("unchanged frame re-sent the sixel")
	}
	// A new message moves the picture up: it is sent again at its new place.
	say("and another line")
	if moved := frame(); strings.Count(moved, dcs) != 1 {
		t.Fatal("moved picture was not re-sent")
	}
	// A modal covers the transcript: no sixel while it's up.
	app.openRoster(nil)
	if covered := frame(); strings.Contains(covered, "\x1bP") {
		t.Fatal("sixel drawn under a modal")
	}
	app.closeModal()
	if back := frame(); strings.Count(back, dcs) != 1 {
		t.Fatal("picture not restored after the modal closed")
	}
}
