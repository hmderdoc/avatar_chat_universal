package ui

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

func startUITestServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := chatserver.New(addr)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ListenAndServe(ctx)
	t.Cleanup(cancel)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

// Reproduces the App-layer flow: in a tuned room the lounge is active; switching
// to an untuned room turns it off; switching back must turn it on again.
func TestApp_LoungeReengagesOnSwitchBack(t *testing.T) {
	addr := startUITestServer(t)
	ctx := context.Background()

	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "rooma")
	if err := alice.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	if err := alice.SetTuner("127.0.0.1", 7601, "cam"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "rooma")
	if err := bob.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer bob.Close()

	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), bob, 80, 25, ansi.CharsetCP437)
	defer func() {
		if app.tvConsumer != nil {
			app.tvConsumer.Close()
		}
	}()

	app.tvSync()
	if !app.loungeActive() {
		t.Fatal("lounge not active in tuned room at start")
	}

	if err := bob.JoinChannel("roomb", 50); err != nil {
		t.Fatal(err)
	}
	app.tvSync()
	if app.loungeActive() {
		t.Fatal("lounge should be off in untuned room")
	}

	if err := bob.JoinChannel("rooma", 50); err != nil {
		t.Fatal(err)
	}
	app.tvSync()
	if !app.loungeActive() {
		t.Fatal("BUG: lounge did not re-engage on switching back to the tuned room")
	}
}

// loungeApp spins up a session tuned to a (dead) feed and returns an App with
// the lounge active, ready to exercise the history-overlay state machine.
func loungeApp(t *testing.T) *App {
	t.Helper()
	addr := startUITestServer(t)
	ctx := context.Background()

	sess := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "rooma")
	if err := sess.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	if err := sess.SetTuner("127.0.0.1", 7601, "cam"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	t.Cleanup(func() {
		if app.tvConsumer != nil {
			app.tvConsumer.Close()
		}
	})
	app.tvSync()
	if !app.loungeActive() {
		t.Fatal("lounge not active in tuned room")
	}
	return app
}

// The first PgUp in the lounge SHOWS the latest page without scrolling back;
// further PgUp pages back; PgDn pages forward and, once at the bottom,
// dismisses the overlay.
func TestApp_LoungeHistoryShowThenPageThenDismiss(t *testing.T) {
	app := loungeApp(t)

	if _, err := app.handleKey(ansi.Key{Type: ansi.KeyPgUp}); err != nil {
		t.Fatal(err)
	}
	if !app.tvShowTranscript {
		t.Fatal("first PgUp should show the history overlay")
	}
	if app.transcript.Scroll != 0 {
		t.Fatalf("first PgUp should NOT scroll back, want Scroll=0 got %d", app.transcript.Scroll)
	}

	if _, err := app.handleKey(ansi.Key{Type: ansi.KeyPgUp}); err != nil {
		t.Fatal(err)
	}
	if app.transcript.Scroll != 5 {
		t.Fatalf("second PgUp should page back, want Scroll=5 got %d", app.transcript.Scroll)
	}

	if _, err := app.handleKey(ansi.Key{Type: ansi.KeyPgDn}); err != nil {
		t.Fatal(err)
	}
	if app.transcript.Scroll != 0 {
		t.Fatalf("PgDn should page forward, want Scroll=0 got %d", app.transcript.Scroll)
	}
	if !app.tvShowTranscript {
		t.Fatal("overlay should still be up after paging back to the bottom")
	}

	if _, err := app.handleKey(ansi.Key{Type: ansi.KeyPgDn}); err != nil {
		t.Fatal(err)
	}
	if app.tvShowTranscript {
		t.Fatal("PgDn at the bottom should dismiss the overlay")
	}
}

// The history overlay auto-dismisses once it's gone untouched past the idle
// window.
func TestApp_LoungeHistoryAutoDismiss(t *testing.T) {
	app := loungeApp(t)
	app.showLoungeHistory()
	if !app.tvShowTranscript {
		t.Fatal("showLoungeHistory should raise the overlay")
	}
	app.tvTranscriptShownAt = time.Now().Add(-tvHistoryIdle - time.Second)
	app.tvTick()
	if app.tvShowTranscript {
		t.Fatal("overlay should auto-dismiss after the idle window")
	}
}

// A long popup message wraps across rows (capped at popupMaxRows) instead of
// clipping at the screen edge, with the nick colored on the first row.
func TestDrawPopupLines_WrapsLongMessage(t *testing.T) {
	app := &App{}
	f := ansi.NewFrame(0, 0, 30, 12, ansi.White|ansi.BgBlack)
	p := tvPopup{msg: &chat.Message{
		Nick: &chat.Nick{Name: "bob"},
		Str:  "this is a fairly long chat message that has to wrap across several rows",
	}}
	bottomY := f.H - 1

	top := app.drawPopupLines(f, bottomY, p) + 1
	rows := bottomY - top + 1
	if rows < 2 {
		t.Fatalf("long message should wrap to multiple rows, got %d", rows)
	}
	if rows > popupMaxRows {
		t.Fatalf("popup exceeded the row cap: %d > %d", rows, popupMaxRows)
	}
	if got := f.CellAt(1, top).Char; got != 'b' {
		t.Fatalf("first popup row should start with the nick 'b', got %q", rune(got))
	}
}

// nickAttr must be deterministic, draw its foreground from the saturated
// palette (never a neutral), and always pair it with a background that clears
// the luminance-contrast floor.
func TestNickAttr_PaletteContrastAndStability(t *testing.T) {
	app := &App{}
	neutral := map[ansi.Attr]bool{
		ansi.Black: true, ansi.DarkGray: true, ansi.LightGray: true, ansi.White: true,
	}
	nicks := []string{"bob", "alice", "carol", "dave", "eve", "mallory", "trent", "z", "", "xX_long_nick_42", "SysOp"}
	for _, n := range nicks {
		got := app.nickAttr(n)
		fg := got & 0x0F
		bgIdx := int((got >> 4) & 7)
		if neutral[fg] {
			t.Errorf("nick %q: fg %d is a neutral color", n, fg)
		}
		delta := cgaLuma[int(fg)] - cgaLuma[bgIdx]
		if delta < 0 {
			delta = -delta
		}
		if delta < nickContrastMin {
			t.Errorf("nick %q: fg=%d bg=%d contrast %d < %d", n, fg, bgIdx, delta, nickContrastMin)
		}
		if again := app.nickAttr(n); again != got {
			t.Errorf("nick %q: not deterministic (%d vs %d)", n, got, again)
		}
	}
	if app.nickAttr("bob") != app.nickAttr("Bob") {
		t.Error("nick color should be case-insensitive (bob vs Bob)")
	}
}

// A short popup stays on a single row.
func TestDrawPopupLines_ShortMessageSingleRow(t *testing.T) {
	app := &App{}
	f := ansi.NewFrame(0, 0, 40, 12, ansi.White|ansi.BgBlack)
	p := tvPopup{msg: &chat.Message{Nick: &chat.Nick{Name: "ann"}, Str: "hi"}}
	bottomY := f.H - 1

	if top := app.drawPopupLines(f, bottomY, p) + 1; top != bottomY {
		t.Fatalf("short message should occupy exactly one row, got top=%d bottomY=%d", top, bottomY)
	}
}
