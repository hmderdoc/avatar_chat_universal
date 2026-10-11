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
