package linkprev

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

func TestParseOpenGraph(t *testing.T) {
	html := `<html><head>
<meta property="og:site_name" content="The Daily &amp; Weekly">
<meta property="og:title" content='Caf&eacute; &#8220;news&#8221; &#x2014; today'>
<meta property="og:title" content="second title loses">
<meta name="description" content="plain description">
<meta property="og:description" content="OG   description
 over	lines">
<meta property="og:image" content="/img/card.png">
<title>Fallback title</title></head></html>`
	m := Parse([]byte(html), "https://news.example/a/b?c=1")
	if m.SiteName != "The Daily & Weekly" {
		t.Errorf("site = %q", m.SiteName)
	}
	// &eacute; is not one of the entities link-preview.js decodes.
	if m.Title != `Caf&eacute; "news" -- today` {
		t.Errorf("title = %q", m.Title)
	}
	if m.Description != "OG description over lines" {
		t.Errorf("description = %q", m.Description)
	}
	if m.Image != "https://news.example/img/card.png" {
		t.Errorf("image = %q", m.Image)
	}
}

func TestParseFallbacks(t *testing.T) {
	m := Parse([]byte(`<meta name="twitter:title" content="TW title"><meta name=twitter:image content=//cdn.example/x.jpg>
<meta name="twitter:description" content="tw desc"><title>ignored</title>`), "http://site.example/")
	if m.Title != "TW title" || m.Description != "tw desc" || m.Image != "http://cdn.example/x.jpg" {
		t.Errorf("twitter = %+v", m)
	}
	m = Parse([]byte("<TITLE lang=en>\n  Just a  <b>title</b>\n</TITLE><meta name=description content=\"d\">"), "https://x.example/")
	if m.Title != "Just a <b>title</b>" || m.Description != "d" || m.Image != "" {
		t.Errorf("title fallback = %+v", m)
	}
	m = Parse([]byte(`<meta property="og:image" content="https://u.example/a.png?x=1&amp;y=2">`), "https://x.example/")
	if m.Image != "https://u.example/a.png?x=1&y=2" {
		t.Errorf("image entities not decoded: %q", m.Image)
	}
	m = Parse([]byte(`<meta property="og:image" content="javascript:alert(1)"><meta property="og:title" content="t">`), "https://x.example/")
	if m.Image != "" {
		t.Errorf("javascript: image kept: %q", m.Image)
	}
}

func TestParseGarbageAndOversize(t *testing.T) {
	for _, in := range []string{"", "\x00\xff\xfe<meta", "<meta content=>", strings.Repeat("<meta ", 5000), "<title>unclosed"} {
		m := Parse([]byte(in), "https://x.example/")
		if m.Title != "" || m.Image != "" {
			t.Errorf("%q: %+v", in[:min(len(in), 20)], m)
		}
	}
	// Tags past the parse cap are not seen.
	big := strings.Repeat(" ", ParseBytes) + `<meta property="og:title" content="late">`
	if m := Parse([]byte(big), "https://x.example/"); m.Title != "" {
		t.Errorf("title past the parse cap: %q", m.Title)
	}
	long := strings.Repeat("word ", 100)
	m := Parse([]byte(`<meta property="og:title" content="`+long+`">`), "https://x.example/")
	if len(m.Title) > titleCap || !strings.HasSuffix(m.Title, "...") {
		t.Errorf("title not capped: %d %q", len(m.Title), m.Title[len(m.Title)-5:])
	}
}

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Crème brûlée – “ok” …": `Creme brulee - "ok" ...`,
		"tab\there\r\nnew":      "tab here  new",
		"emoji 🚀 drop":          "emoji  drop",
		"Łódź Straße ©2026":     "Lodz Strasse (c)2026",
		"bad \xff byte":         "bad  byte",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
	for _, r := range Fold("日本語 Ünïcödé ½ ™") {
		if r < 0x20 || r > 0x7e {
			t.Fatalf("non-ASCII %q survived", r)
		}
	}
}

func TestPreviewURL(t *testing.T) {
	cases := map[string]string{
		"see https://example.com/story.":                                             "https://example.com/story",
		"pic https://x.org/a.png then https://news.example/b":                        "https://news.example/b",
		"https://futureland.today/chatmedia/2026-10-10/6985f2d16fe3fa4eb6f70bfb.jpg": "",
		"https://futureland.today/chatmedia/2026-10-10/6985f2d16fe3fa4eb6f70bfb.txt": "",
		"song https://x.org/a.mp3 vid https://x.org/b.webm art https://x.org/c.ans":  "",
		"two https://one.example/ and https://two.example/":                          "https://one.example/",
		// A page whose query names a file is still a page: it gets the card.
		`["Song" by X](https://futureland.today/?page=011-futureland-records.xjs&track=Song_x.mp3) -- uploaded`: "https://futureland.today/?page=011-futureland-records.xjs&track=Song_x.mp3",
		"https://x.org/a.mp3?dl=1": "",
		"no links here":            "",
		"ftp://example.com/file":   "",
	}
	for in, want := range cases {
		if got := PreviewURL(in); got != want {
			t.Errorf("PreviewURL(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakeFetcher struct {
	pages map[string]*inlineimg.Page
	errs  map[string]error
	calls int
}

func (f *fakeFetcher) FetchPage(ctx context.Context, rawURL string, maxBytes int64) (*inlineimg.Page, error) {
	f.calls++
	if maxBytes != FetchBytes {
		return nil, errors.New("wrong cap")
	}
	if err := f.errs[rawURL]; err != nil {
		return nil, err
	}
	if p := f.pages[rawURL]; p != nil {
		return p, nil
	}
	return nil, errors.New("404")
}

func TestGetAndCache(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := &Cache{Dir: t.TempDir(), now: func() time.Time { return now }}
	f := &fakeFetcher{
		pages: map[string]*inlineimg.Page{
			"https://ok.example/":     {Body: []byte(`<meta property="og:title" content="Hello">`), FinalURL: "https://ok.example/home", ContentType: "text/html; charset=utf-8"},
			"https://pic.example/raw": {FinalURL: "https://pic.example/raw/cat", ContentType: "image/jpeg"},
			"https://pdf.example/":    {Body: []byte("%PDF"), FinalURL: "https://pdf.example/", ContentType: "application/pdf"},
			"https://bare.example/":   {Body: []byte("<p>no meta</p>"), FinalURL: "https://bare.example/", ContentType: "text/html"},
		},
		errs: map[string]error{"https://lan.example/": inlineimg.ErrBlocked},
	}
	ctx := context.Background()
	m, err := Get(ctx, f, c, "https://ok.example/")
	if err != nil || m.Title != "Hello" || m.Kind != "site" || m.FinalURL != "https://ok.example/home" {
		t.Fatalf("site: %+v %v", m, err)
	}
	if m, err := Get(ctx, f, c, "https://pic.example/raw"); err != nil || m.Kind != "image" || m.Title != "cat" || m.Image != "https://pic.example/raw/cat" {
		t.Fatalf("image: %+v %v", m, err)
	}
	for _, u := range []string{"https://pdf.example/", "https://bare.example/", "https://lan.example/"} {
		if _, err := Get(ctx, f, c, u); !errors.Is(err, ErrNoCard) {
			t.Errorf("%s: err = %v, want ErrNoCard", u, err)
		}
	}
	calls := f.calls
	// Everything is cached now, failures included.
	for _, u := range []string{"https://ok.example/", "https://lan.example/"} {
		Get(ctx, f, c, u)
	}
	if f.calls != calls {
		t.Fatalf("cached lookups refetched (%d -> %d)", calls, f.calls)
	}
	// A failure expires after an hour, a success after a day.
	now = now.Add(FailTTL + time.Minute)
	Get(ctx, f, c, "https://lan.example/")
	Get(ctx, f, c, "https://ok.example/")
	if f.calls != calls+1 {
		t.Fatalf("after an hour: %d fetches, want only the failure refetched", f.calls-calls)
	}
	now = now.Add(DefaultTTL)
	Get(ctx, f, c, "https://ok.example/")
	if f.calls != calls+2 {
		t.Fatalf("after a day the success should be refetched")
	}
	// A canceled fetch says nothing about the link and is not cached.
	f.errs["https://slow.example/"] = context.Canceled
	Get(ctx, f, c, "https://slow.example/")
	if _, _, ok := c.get("https://slow.example/"); ok {
		t.Fatal("canceled fetch was cached")
	}
}

// A track page announces its stream with og:audio; the door plays that.
func TestParseOpenGraphAudio(t *testing.T) {
	html := `<head>
<meta property="og:type" content="music.song">
<meta property="og:title" content="Data Bleeds Your Name: Vektrax Records">
<meta property="og:audio" content="https://bbs.example/api/files.ssjs?call=stream-file&amp;dir=mp3s&amp;file=a.mp3">
<meta property="og:audio:type" content="Audio/MPEG">
</head>`
	m := Parse([]byte(html), "https://bbs.example/?page=records&track=a.mp3")
	if m.Audio != "https://bbs.example/api/files.ssjs?call=stream-file&dir=mp3s&file=a.mp3" {
		t.Errorf("audio = %q", m.Audio)
	}
	if m.AudioType != "audio/mpeg" {
		t.Errorf("audio type = %q", m.AudioType)
	}
	// secure_url wins; a non-http scheme is refused.
	m = Parse([]byte(`<meta property="og:audio" content="http://x.example/a.ogg"><meta property="og:audio:secure_url" content="https://x.example/a.ogg">`), "https://x.example/")
	if m.Audio != "https://x.example/a.ogg" {
		t.Errorf("secure_url: audio = %q", m.Audio)
	}
	m = Parse([]byte(`<meta property="og:audio" content="javascript:alert(1)">`), "https://x.example/")
	if m.Audio != "" {
		t.Errorf("javascript: audio = %q", m.Audio)
	}
}

// og:video counts only as media: a video/* type or a video file name. An
// embed page (text/html) is not playable and is ignored.
func TestParseOpenGraphVideo(t *testing.T) {
	cases := []struct {
		html, want string
	}{
		{`<meta property="og:video" content="/clip.mp4">`, "https://v.example/clip.mp4"},
		{`<meta property="og:video:secure_url" content="https://cdn.example/s?id=1"><meta property="og:video:type" content="video/mp4">`, "https://cdn.example/s?id=1"},
		{`<meta property="og:video" content="https://www.youtube.com/embed/x"><meta property="og:video:type" content="text/html">`, ""},
		{`<meta property="og:video" content="https://cdn.example/s?id=1">`, ""},
		{`<meta name="twitter:player:stream" content="https://cdn.example/t.mp4">`, "https://cdn.example/t.mp4"},
		{`<meta property="og:video" content="javascript:x.mp4">`, ""},
		{`<meta property="og:video" content="https://cdn.example/play?file=x.mp4">`, ""},
		{`<meta property="og:video" content="/v/clip.webm?sig=1&amp;t=2">`, "https://v.example/v/clip.webm?sig=1&t=2"},
	}
	for _, c := range cases {
		if got := Parse([]byte(c.html), "https://v.example/page").Video; got != c.want {
			t.Errorf("%s: video = %q, want %q", c.html, got, c.want)
		}
	}
}
