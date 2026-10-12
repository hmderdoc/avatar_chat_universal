package main

import (
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/prefs"
)

// The first-run offer of name colours is made once, and never to a name
// that already has colours.
func TestOfferHandleColorsOnce(t *testing.T) {
	p := &prefs.Store{Root: t.TempDir(), BBSID: "TEST"}
	if !offerHandleColors(p, "Alice", nil) {
		t.Fatal("no style: want the offer")
	}
	if offerHandleColors(p, "Alice", nil) {
		t.Fatal("offered twice")
	}
	if offerHandleColors(p, "alice", &nickstyle.Style{}) {
		t.Fatal("offered again under another case of the name")
	}
	if !offerHandleColors(p, "Bob", &nickstyle.Style{Colors: []string{"", ""}}) {
		t.Fatal("only default colours: want the offer")
	}
	if offerHandleColors(p, "Carol", &nickstyle.Style{Colors: []string{"#ff0000"}}) {
		t.Fatal("has colours: no offer")
	}
	if p.Get("Carol", handleOfferKey) != "" {
		t.Fatal("a name with colours was recorded as offered")
	}
	if !offerHandleColors(p, "Carol", nil) {
		t.Fatal("colours cleared, never offered: want the offer")
	}
}

func TestCellFromGeometry(t *testing.T) {
	// fTelnet's reply on a 150x67 screen: 9x16 cells.
	raw := []byte("\x1b[?2;0;1356;1081S\x1b[=67;84;101;114;109;1;208c")
	if w, h := cellFromGeometry(raw, 150, 67); w != 9 || h != 16 {
		t.Errorf("150x67 = %dx%d, want 9x16", w, h)
	}
	if w, h := cellFromGeometry(raw, 0, 0); w != 0 || h != 0 {
		t.Errorf("no screen size = %dx%d, want 0x0", w, h)
	}
	if w, h := cellFromGeometry([]byte("\x1b[?1;2c"), 80, 25); w != 0 || h != 0 {
		t.Errorf("no geometry = %dx%d", w, h)
	}
	// Nonsense (a cell wider than tall) falls back to the default.
	if w, h := cellFromGeometry([]byte("\x1b[?2;0;1600;100S"), 80, 25); w != 0 || h != 0 {
		t.Errorf("insane = %dx%d", w, h)
	}
}
