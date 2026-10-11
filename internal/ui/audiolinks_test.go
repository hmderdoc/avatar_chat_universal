package ui

import (
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

func TestAudioCardsAndNoAutoplay(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 20)
	tr := NewTranscript(f, "alice")
	a := &App{transcript: tr}
	a.EnableAudioLinks(AudioLinkConfig{Fetcher: inlineimg.NewFetcher("")})
	now := time.Now().UnixNano() / 1000000
	tr.Render([]*chat.Message{
		{Nick: &chat.Nick{Name: "bob"}, Str: "listen https://x.org/tunes/Song%20One.mp3", Time: now},
		{Nick: &chat.Nick{Name: "cy"}, Str: "https://x.org/a.opus", Time: now},
	})
	if _, _, ok := findText(f, "\x0e Song One.mp3  /play"); !ok {
		t.Fatal("no /play card under the mp3 link")
	}
	if _, _, ok := findText(f, "(.opus can't play here)"); !ok {
		t.Fatal("unplayable format not labelled")
	}
	if a.audioOut.Len() != 0 || a.alinks.player != nil || a.alinks.loading != "" {
		t.Fatal("rendering a link must never start playback")
	}
}
