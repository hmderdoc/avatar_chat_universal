package ui

import (
	"context"
	"image"
	"net/url"
	"strings"

	"github.com/hmderdoc/shadecell"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/audiolink"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/linkprev"
)

// LinkPreviewConfig turns on preview cards for plain links in chat. The App
// does nothing with them while it is nil.
type LinkPreviewConfig struct {
	// Fetcher fetches pages (FetchPage) and card thumbnails (Fetch, through
	// the inline-picture cache); its guards apply to both.
	Fetcher *inlineimg.Fetcher
	Cache   *linkprev.Cache
	// NSFWKeywords veils a card whose link, line or text matches.
	NSFWKeywords []string
}

const (
	cardWorkers  = 2
	cardMaxQueue = 16
	// A card's thumbnail is at most this many cells, left of its text.
	cardThumbCols = 12
	cardThumbRows = 4
	cardTitleRows = 2
	cardDescRows  = 2
)

type cardEntry struct {
	url    string
	state  imageState
	queued bool
	meta   *linkprev.Meta
	thumb  image.Image
	nsfw   bool
	pics   map[picKey]*inlineimg.Picture
}

type cardResult struct {
	url   string
	meta  *linkprev.Meta
	thumb image.Image
	err   error
}

// cardManager owns the link-preview cards the way imageManager owns
// pictures: at most two lookups at once off the UI goroutine, results
// applied by drain on the UI goroutine.
type cardManager struct {
	cfg      LinkPreviewConfig
	q        *shadecell.Quantizer
	entries  map[string]*cardEntry
	queue    []string
	inflight int
	results  chan cardResult
	revealed bool // /nsfw
}

func newCardManager(cfg LinkPreviewConfig) *cardManager {
	return &cardManager{
		cfg:     cfg,
		q:       shadecell.New(),
		entries: map[string]*cardEntry{},
		results: make(chan cardResult, cardMaxQueue),
	}
}

// cardView is what the transcript draws for a message's link.
type cardView struct {
	pending bool
	veiled  bool
	host    string // shown on a veiled card, which hides the page's own words
	meta    *linkprev.Meta
	thumb   *inlineimg.Picture
}

// lookup returns how to show url, found in line, queueing the lookup the
// first time it is seen. ok is false when nothing should be drawn.
func (m *cardManager) lookup(rawURL, line string, truecolor bool) (cardView, bool) {
	e := m.entries[rawURL]
	if e == nil {
		e = &cardEntry{
			url:  rawURL,
			nsfw: inlineimg.LooksNSFW(rawURL, m.cfg.NSFWKeywords) || inlineimg.LooksNSFW(line, m.cfg.NSFWKeywords),
			pics: map[picKey]*inlineimg.Picture{},
		}
		m.entries[rawURL] = e
	}
	if e.state == imgPending && !e.queued {
		m.enqueue(e)
	}
	switch e.state {
	case imgPending:
		return cardView{pending: true}, true
	case imgReady:
		if e.nsfw && !m.revealed {
			return cardView{veiled: true, host: cardHost(e.meta)}, true
		}
		v := cardView{meta: e.meta}
		if e.thumb != nil {
			k := picKey{cardThumbCols, cardThumbRows, truecolor, false}
			p, ok := e.pics[k]
			if !ok {
				p = inlineimg.Render(e.thumb, m.q, inlineimg.Options{MaxCols: cardThumbCols, MaxRows: cardThumbRows, Truecolor: truecolor})
				e.pics[k] = p
			}
			v.thumb = p
		}
		return v, true
	}
	return cardView{}, false
}

// audio returns the og:audio stream of rawURL's card, once its card has
// loaded and is not veiled: the stream to fetch, the format to decode it
// as, and the track's title.
func (m *cardManager) audio(rawURL string) (stream, ext, name string, ok bool) {
	e := m.entries[rawURL]
	if e == nil || e.state != imgReady || e.meta == nil || e.meta.Audio == "" {
		return "", "", "", false
	}
	if e.nsfw && !m.revealed {
		return "", "", "", false
	}
	ext = audiolink.Ext(e.meta.Audio)
	if ext == "" {
		ext = audiolink.ExtForType(e.meta.AudioType)
	}
	if ext == "" {
		ext = "mp3" // og:audio with no type: by far the common case
	}
	name = e.meta.Title
	if name == "" {
		name = audiolink.Name(e.meta.Audio)
	}
	return e.meta.Audio, ext, name, true
}

// video returns the direct og:video stream of rawURL's card, once loaded
// and not veiled, with the track's title.
func (m *cardManager) video(rawURL string) (stream, name string, ok bool) {
	e := m.entries[rawURL]
	if e == nil || e.state != imgReady || e.meta == nil || e.meta.Video == "" {
		return "", "", false
	}
	if e.nsfw && !m.revealed {
		return "", "", false
	}
	name = e.meta.Title
	if name == "" {
		name = linkName(e.meta.Video)
	}
	return e.meta.Video, name, true
}

// title is rawURL's card title once loaded ("" otherwise).
func (m *cardManager) title(rawURL string) string {
	if e := m.entries[rawURL]; e != nil && e.state == imgReady && e.meta != nil {
		return e.meta.Title
	}
	return ""
}

// cardHost is the line a card leads with: the site's name, or its host.
func cardHost(m *linkprev.Meta) string {
	if m == nil {
		return ""
	}
	if m.SiteName != "" {
		return m.SiteName
	}
	u := m.FinalURL
	if u == "" {
		u = m.URL
	}
	if p, err := url.Parse(u); err == nil {
		return linkprev.Clean(strings.TrimPrefix(p.Hostname(), "www."), 80)
	}
	return ""
}

func (m *cardManager) enqueue(e *cardEntry) {
	if len(m.queue) >= cardMaxQueue {
		return
	}
	e.queued = true
	m.queue = append(m.queue, e.url)
	m.pump()
}

func (m *cardManager) pump() {
	for m.inflight < cardWorkers && len(m.queue) > 0 {
		rawURL := m.queue[0]
		m.queue = m.queue[1:]
		m.inflight++
		go func() {
			ctx := context.Background()
			meta, err := linkprev.Get(ctx, m.cfg.Fetcher, m.cfg.Cache, rawURL)
			var thumb image.Image
			if err == nil && meta.Image != "" {
				// A thumbnail that fails leaves a card without one.
				if body, ferr := m.cfg.Fetcher.Fetch(ctx, meta.Image); ferr == nil {
					thumb, _ = inlineimg.Decode(body)
				}
			}
			m.results <- cardResult{url: rawURL, meta: meta, thumb: thumb, err: err}
		}()
	}
}

// drain applies finished lookups; true when anything changed on screen.
func (m *cardManager) drain() bool {
	changed := false
	for {
		select {
		case r := <-m.results:
			m.inflight--
			if e := m.entries[r.url]; e != nil {
				e.queued = false
				if r.err != nil || r.meta == nil {
					e.state = imgFailed
				} else {
					e.state = imgReady
					e.meta = r.meta
					e.thumb = r.thumb
					kw := m.cfg.NSFWKeywords
					e.nsfw = e.nsfw || inlineimg.LooksNSFW(r.meta.Title, kw) ||
						inlineimg.LooksNSFW(r.meta.Description, kw) || inlineimg.LooksNSFW(r.meta.SiteName, kw) ||
						inlineimg.LooksNSFW(r.meta.FinalURL, kw) || inlineimg.LooksNSFW(r.meta.Image, kw)
				}
			}
			changed = true
		default:
			m.pump()
			return changed
		}
	}
}

// cardLayout is a card fitted to a bubble: the wrapped text lines with
// their roles, and the thumbnail, if any.
type cardLayout struct {
	lines []cardLine
	thumb *inlineimg.Picture
	rows  int
}

type cardLine struct {
	text string
	role byte // 's' site, 't' title, 'd' description, 'v' veil label
}

// layoutCard fits v into a bubble whose text is width cells wide. Column 0
// is the card's edge bar; the thumbnail, then the text, follow it.
func layoutCard(v cardView, width int) cardLayout {
	var l cardLayout
	switch {
	case v.pending:
		l.lines = []cardLine{{"[loading preview...]", 'd'}}
	case v.veiled:
		if v.host != "" {
			l.lines = append(l.lines, cardLine{v.host, 's'})
		}
		l.lines = append(l.lines, cardLine{"NSFW link - /nsfw to show", 'v'})
	default:
		textW := width - 1
		if v.thumb != nil && textW-v.thumb.Cols-1 >= 16 {
			l.thumb = v.thumb
			textW -= v.thumb.Cols + 1
		}
		if textW < 4 {
			return cardLayout{}
		}
		if h := cardHost(v.meta); h != "" {
			l.lines = append(l.lines, cardLine{clip(ansi.ToCP437(h, false), textW), 's'})
		}
		for _, s := range capLines(wrapText(ansi.ToCP437(v.meta.Title, false), textW), cardTitleRows, textW) {
			l.lines = append(l.lines, cardLine{s, 't'})
		}
		for _, s := range capLines(wrapText(ansi.ToCP437(v.meta.Description, false), textW), cardDescRows, textW) {
			l.lines = append(l.lines, cardLine{s, 'd'})
		}
	}
	l.rows = len(l.lines)
	if l.thumb != nil && l.thumb.Rows > l.rows {
		l.rows = l.thumb.Rows
	}
	return l
}

// capLines keeps the first n lines, marking a cut with "..." on the last.
func capLines(lines []string, n, width int) []string {
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	lines = lines[:n]
	last := lines[n-1]
	if len(last)+3 > width {
		last = strings.TrimRight(last[:max(0, width-3)], " ")
	}
	lines[n-1] = last + "..."
	return lines
}

func clip(s string, w int) string {
	if len(s) <= w {
		return s
	}
	if w <= 3 {
		return s[:w]
	}
	return s[:w-3] + "..."
}
