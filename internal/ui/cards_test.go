package ui

import (
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
)

// readyCards is a cardManager whose links are already looked up, so layout
// can be tested without the network. A nil meta marks a failed lookup.
func readyCards(metas map[string]*linkprev.Meta) *cardManager {
	m := newCardManager(LinkPreviewConfig{Fetcher: inlineimg.NewFetcher(""), NSFWKeywords: inlineimg.DefaultNSFWKeywords})
	for u, meta := range metas {
		e := &cardEntry{url: u, state: imgReady, meta: meta, pics: map[picKey]*inlineimg.Picture{}}
		if meta == nil {
			e.state = imgFailed
		} else {
			kw := m.cfg.NSFWKeywords
			e.nsfw = inlineimg.LooksNSFW(meta.Title, kw) || inlineimg.LooksNSFW(meta.Description, kw)
			if meta.Image != "" {
				e.thumb = testImage()
			}
		}
		m.entries[u] = e
	}
	return m
}

func TestTranscript_LinkPreviewCards(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 40)
	tr := NewTranscript(f, "alice")
	tr.Cards = readyCards(map[string]*linkprev.Meta{
		"https://news.example/story": {URL: "https://news.example/story", FinalURL: "https://news.example/story", Kind: "site",
			SiteName: "Example News", Title: "Ukraine strikes data center near Moscow",
			Description: "A precision strike hit a facility that hosts part of Yandex's infrastructure, officials said on Friday.",
			Image:       "https://news.example/card.jpg"},
		"https://adult.example/": {URL: "https://adult.example/", FinalURL: "https://adult.example/", Kind: "site",
			Title: "Hot nude photos", Description: "explicit"},
		"https://dead.example/": nil,
	})
	tr.Cards.queue = make([]string, cardMaxQueue) // full: the pending link starts no real lookup
	now := time.Now().UnixNano() / 1000000
	tr.Render([]*chat.Message{
		{Nick: &chat.Nick{Name: "bob"}, Str: "go https://news.example/story https://second.example/", Time: now},
		{Nick: &chat.Nick{Name: "carol"}, Str: "https://adult.example/", Time: now},
		{Nick: &chat.Nick{Name: "dave"}, Str: "https://dead.example/ is down", Time: now},
		{Nick: &chat.Nick{Name: "erin"}, Str: "loading https://slow.example/x", Time: now},
	})

	_, ty, ok := findText(f, "go https://news.example/story")
	if !ok {
		t.Fatal("message text not rendered")
	}
	x0 := avatarW + avatarMargin + 1
	if c := f.CellAt(x0, ty+1); c.Char != 0xDD {
		t.Fatalf("no card edge under the text: %+v", c)
	}
	if c := f.CellAt(x0+1, ty+1); c.Attr == tr.Theme.BubbleLeft && c.Char == ' ' {
		t.Fatalf("no thumbnail beside the card text: %+v", c)
	}
	sx, sy, ok := findText(f, "Example News")
	if !ok || sy != ty+1 || sx <= x0+cardThumbCols {
		t.Fatalf("site line at %d,%d ok=%v, want row %d right of the thumbnail", sx, sy, ok, ty+1)
	}
	if c := f.CellAt(sx, sy); c.Attr&0x0F != tr.Theme.CardSite&0x0F {
		t.Fatalf("site colour = %v", c.Attr)
	}
	if _, y, ok := findText(f, "Ukraine strikes data center"); !ok || y != ty+2 {
		t.Fatalf("title at row %d ok=%v", y, ok)
	}
	if _, _, ok := findText(f, "A precision strike"); !ok {
		t.Fatal("description missing")
	}
	// Only the first plain link of a message gets a card.
	if _, ok := tr.Cards.entries["https://second.example/"]; ok {
		t.Fatal("second link of the message was looked up")
	}
	// The NSFW card hides the page's words.
	if _, _, ok := findText(f, "NSFW link - /nsfw to show"); !ok {
		t.Fatal("veil missing")
	}
	if _, _, ok := findText(f, "Hot nude photos"); ok {
		t.Fatal("veiled title shown")
	}
	// A failed lookup draws nothing; a pending one a loading line.
	if _, y, _ := findText(f, "https://dead.example/ is down"); f.CellAt(x0, y+1).Char == 0xDD {
		t.Fatal("failed link got a card")
	}
	if _, _, ok := findText(f, "[loading preview...]"); !ok {
		t.Fatal("no loading line for the pending link")
	}

	tr.Cards.revealed = true
	tr.Render([]*chat.Message{{Nick: &chat.Nick{Name: "carol"}, Str: "https://adult.example/", Time: now}})
	if _, _, ok := findText(f, "Hot nude photos"); !ok {
		t.Fatal("/nsfw did not reveal the card")
	}
}

// A bubble made tall by a card is shown by paging, as pictures are.
func TestTranscript_PagingNeverSkipsCardBubble(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 12)
	tr := NewTranscript(f, "alice")
	long := "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore."
	tr.Cards = readyCards(map[string]*linkprev.Meta{
		"https://news.example/": {URL: "https://news.example/", Kind: "site", SiteName: "Site", Title: long, Description: long, Image: "https://news.example/i.png"},
	})
	now := time.Now().UnixNano() / 1000000
	msgs := []*chat.Message{
		{Nick: &chat.Nick{Name: "ann"}, Str: "oldest", Time: now},
		{Nick: &chat.Nick{Name: "bob"}, Str: "news https://news.example/ for you", Time: now},
	}
	for _, n := range []string{"cy", "di", "ed", "fay"} {
		msgs = append(msgs, &chat.Message{Nick: &chat.Nick{Name: n}, Str: "line from " + n, Time: now})
	}
	seen := map[string]bool{}
	for page := 0; page < 20; page++ {
		tr.Scroll = page * tr.PageRows()
		tr.Render(msgs)
		for _, m := range msgs {
			if _, _, ok := findText(f, m.Str); ok {
				seen[m.Str] = true
			}
		}
		if _, _, ok := findText(f, "Site"); ok {
			seen["card"] = true
		}
		if tr.Scroll < page*tr.PageRows() {
			break
		}
	}
	for _, m := range msgs {
		if !seen[m.Str] {
			t.Errorf("paging never showed %q", m.Str)
		}
	}
	if !seen["card"] {
		t.Error("paging never showed the card")
	}
}

func TestLayoutCardCaps(t *testing.T) {
	long := "word word word word word word word word word word word word word word word word word word word word"
	l := layoutCard(cardView{meta: &linkprev.Meta{URL: "https://www.host.example/x", Title: long, Description: long}}, 30)
	var site, title, desc int
	for _, ln := range l.lines {
		if len(ln.text) > 29 {
			t.Errorf("line wider than the bubble: %q", ln.text)
		}
		switch ln.role {
		case 's':
			site++
			if ln.text != "host.example" {
				t.Errorf("host line = %q", ln.text)
			}
		case 't':
			title++
		case 'd':
			desc++
		}
	}
	if site != 1 || title != cardTitleRows || desc != cardDescRows || l.rows != 5 {
		t.Fatalf("site %d title %d desc %d rows %d", site, title, desc, l.rows)
	}
	if last := l.lines[len(l.lines)-1].text; last[len(last)-3:] != "..." {
		t.Errorf("cut description not marked: %q", last)
	}
}

// A link whose card announced og:audio is a track /play can play: the
// stream is fetched, the type picks the decoder, the title names it. A
// veiled card does not offer its audio until /nsfw.
func TestCardAudioTrack(t *testing.T) {
	page := "https://bbs.example/?page=records&track=a.mp3"
	m := readyCards(map[string]*linkprev.Meta{
		page:                     {Title: "A Song", Audio: "https://bbs.example/api/stream?f=a", AudioType: "audio/mpeg"},
		"https://x.example/nude": {Title: "nude beach", Audio: "https://x.example/b.ogg"},
		"https://x.example/none": {Title: "no audio"},
	})
	stream, ext, name, ok := m.audio(page)
	if !ok || stream != "https://bbs.example/api/stream?f=a" || ext != "mp3" || name != "A Song" {
		t.Fatalf("audio(page) = %q %q %q %v", stream, ext, name, ok)
	}
	if _, _, _, ok := m.audio("https://x.example/none"); ok {
		t.Error("a card without og:audio offered a track")
	}
	if _, _, _, ok := m.audio("https://x.example/nude"); ok {
		t.Error("a veiled card offered its audio")
	}
	m.revealed = true
	if _, ext, _, ok := m.audio("https://x.example/nude"); !ok || ext != "ogg" {
		t.Errorf("revealed: ext = %q ok = %v", ext, ok)
	}
}
